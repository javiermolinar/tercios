package scenario

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type BatchGenerator interface {
	GenerateBatch(ctx context.Context) ([]model.Span, error)
}

// ChildSpec describes a single outgoing edge from a parent node together with
// the resolved source and target node metadata. It is the unit returned by
// NextChildren and consumed by both the eager generator and (later) the
// streaming exporter. ChildSpec intentionally does not carry any per-trace
// state (trace ID, parent span ID, cursor, already-emitted span IDs); those
// are resolved at span materialization time by the caller.
type ChildSpec struct {
	Edge       Edge
	SourceNode Node
	TargetNode Node
}

type Generator struct {
	definition      Definition
	outgoing        map[string][]Edge
	subtreeDuration map[string]time.Duration
	counter         atomic.Uint64
}

// NextChildren returns the outgoing edges of parentNodeID as ChildSpecs in
// definition order. The returned slice is freshly allocated on every call
// so callers may freely mutate it without affecting future calls or other
// consumers. The Edge.Repeat field is preserved on the returned spec;
// expansion of repeats is the caller's responsibility so that consumers
// can choose between eager looping and streaming scheduling.
func (g *Generator) NextChildren(parentNodeID string) []ChildSpec {
	if g == nil {
		return nil
	}
	edges := g.outgoing[parentNodeID]
	if len(edges) == 0 {
		return nil
	}
	out := make([]ChildSpec, 0, len(edges))
	for _, edge := range edges {
		out = append(out, ChildSpec{
			Edge:       edge,
			SourceNode: g.definition.Nodes[edge.From],
			TargetNode: g.definition.Nodes[edge.To],
		})
	}
	return out
}

func NewGenerator(definition Definition) *Generator {
	outgoing := make(map[string][]Edge, len(definition.Nodes))
	for _, edge := range definition.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}
	return &Generator{
		definition:      definition,
		outgoing:        outgoing,
		subtreeDuration: computeSubtreeDurations(definition.Root, outgoing),
	}
}

// stepDuration returns the scenario time one repeat of child consumes
// (the edge's own duration + the subtree under its target + a 1ms gap).
// Used by the walker to stagger sibling DueAts so each sibling fires
// only after every earlier sibling's full subtree drains.
func (g *Generator) stepDuration(child ChildSpec) time.Duration {
	d := child.Edge.Duration
	if d <= 0 {
		d = 1 * time.Millisecond
	}
	return d + g.subtreeDuration[child.Edge.To] + 1*time.Millisecond
}

// computeSubtreeDurations returns, per node, the total scenario-time
// consumed by its outgoing subtree. Same recurrence as estimateDuration.
func computeSubtreeDurations(rootID string, outgoing map[string][]Edge) map[string]time.Duration {
	out := make(map[string]time.Duration, len(outgoing))
	var walk func(id string) time.Duration
	walk = func(id string) time.Duration {
		if v, ok := out[id]; ok {
			return v
		}
		edges := outgoing[id]
		if len(edges) == 0 {
			out[id] = 0
			return 0
		}
		total := time.Duration(0)
		for _, edge := range edges {
			d := edge.Duration
			if d <= 0 {
				d = 1 * time.Millisecond
			}
			step := d + walk(edge.To) + 1*time.Millisecond
			total += time.Duration(edge.Repeat) * step
		}
		out[id] = total
		return total
	}
	walk(rootID)
	return out
}

// GenerateBatch produces all spans of one trace by constructing a walker
// and draining its heap immediately (no wall-clock pacing). The streaming
// exporter uses the same walker via NewStreamingWalker, popping one emit
// at a time and waiting until each emit's DueAt before forwarding to OTLP.
func (g *Generator) GenerateBatch(_ context.Context) ([]model.Span, error) {
	if g == nil {
		return nil, fmt.Errorf("scenario generator not configured")
	}
	if len(g.definition.Nodes) == 0 {
		return nil, fmt.Errorf("scenario definition has no nodes")
	}
	w, err := g.newWalker(time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return w.drain(), nil
}

// walker is the trace-emission engine. It owns one trace's mutable state
// (traceState) and a min-heap of pendingEmit entries keyed on each emit's
// end_time. Both the eager path (GenerateBatch) and the streaming path
// (StreamingWalker) consume the same walker; they differ only in pacing.
type walker struct {
	g     *Generator
	trace *traceState
	heap  *emitHeap
}

// newWalker constructs a walker for one trace nominally rooted at
// startedAt. Consumes one sequence number from g.counter so successive
// walkers from the same Generator emit distinct traces. Pre-allocates
// the root SpanID and records it in nodeSpans (so descendant links to
// the root resolve correctly even though the root span itself is
// materialized last) and seeds the heap with the root sentinel plus
// root's direct children, siblings staggered by stepDuration so heap-pop
// order matches the iterative walker's sequential DFS pre-order.
func (g *Generator) newWalker(startedAt time.Time) (*walker, error) {
	if _, ok := g.definition.Nodes[g.definition.Root]; !ok {
		return nil, fmt.Errorf("root node %q not found", g.definition.Root)
	}

	sequence := g.counter.Add(1)
	trace := &traceState{
		TraceID:   traceIDFromSeed(g.definition.Seed, sequence),
		StartedAt: startedAt,
		NodeSpans: make(map[string]oteltrace.SpanID),
		IDState:   newSpanIDState(g.definition.Seed, sequence),
	}

	estimated := g.subtreeDuration[g.definition.Root]
	if estimated <= 0 {
		estimated = 100 * time.Millisecond
	}

	rootSpanID := trace.IDState.next()
	trace.NodeSpans[g.definition.Root] = rootSpanID

	w := &walker{g: g, trace: trace, heap: &emitHeap{}}

	// Push root's direct children. Each child's DueAt = childBase + effDur
	// so the heap key is the child's end_time. base advances by the full
	// step (D + subtree + 1ms) * Repeat between siblings so the next
	// sibling fires only after every earlier sibling's full subtree drains.
	base := startedAt.Add(1 * time.Millisecond)
	for _, child := range g.NextChildren(g.definition.Root) {
		cd := child.Edge.Duration
		if cd <= 0 {
			cd = 1 * time.Millisecond
		}
		effDur := cd + g.subtreeDuration[child.Edge.To]
		w.heap.PushEmit(&pendingEmit{
			DueAt:            base.Add(effDur),
			Trace:            trace,
			Child:            child,
			ParentSpanID:     rootSpanID,
			RemainingRepeats: child.Edge.Repeat,
		})
		trace.InFlight++
		base = base.Add(time.Duration(child.Edge.Repeat) * g.stepDuration(child))
	}

	// Root sentinel last. Its DueAt = startedAt + subtreeDuration[root]
	// equals the largest descendant end_time; the IsRoot tiebreaker in
	// emitHeap.Less ensures root pops after every coincident descendant.
	w.heap.PushEmit(&pendingEmit{
		DueAt:  startedAt.Add(estimated),
		Trace:  trace,
		IsRoot: true,
	})
	trace.InFlight++
	return w, nil
}

// done reports whether the walker has emitted every span of its trace.
func (w *walker) done() bool { return w.heap.Len() == 0 }

// drain pops every remaining emit and returns the concatenated spans.
// Used by GenerateBatch.
func (w *walker) drain() []model.Span {
	var out []model.Span
	for !w.done() {
		out = append(out, w.popOne()...)
	}
	return out
}

// popOne pops the heap's earliest emit, materializes its span(s), and
// pushes that emit's children and (if repeats remain) self-back onto
// the heap. Returns the spans produced by this single emit.
func (w *walker) popOne() []model.Span {
	emit := w.heap.PopMin()

	if emit.IsRoot {
		rootNode := w.g.definition.Nodes[w.g.definition.Root]
		rootSpanID := w.trace.NodeSpans[w.g.definition.Root]
		duration := emit.DueAt.Sub(w.trace.StartedAt)
		rootSpan := w.g.newSpan(w.trace.TraceID, rootSpanID, oteltrace.SpanID{}, rootNode, oteltrace.SpanKindInternal, w.trace.StartedAt, duration, nil, nil, nil)
		w.trace.InFlight--
		return []model.Span{rootSpan}
	}

	// Lazy-resolve events/links on first pop; reused across repeats.
	if !emit.Resolved {
		emit.Events = resolveEvents(emit.Child.Edge.SpanEvents)
		emit.Links = resolveLinks(w.trace.TraceID, emit.Child.Edge.SpanLinks, w.trace.NodeSpans)
		emit.Resolved = true
	}

	d := emit.Child.Edge.Duration
	if d <= 0 {
		d = 1 * time.Millisecond
	}
	effDur := d + w.g.subtreeDuration[emit.Child.Edge.To]
	start := emit.DueAt.Add(-effDur)

	result := w.g.materializeChild(emit.Child, w.trace.TraceID, emit.ParentSpanID, start, w.trace.IDState, emit.Events, emit.Links)
	w.trace.NodeSpans[emit.Child.Edge.To] = result.TargetSpanID

	// Children attach to the target-side span (server/consumer/db span
	// for pair edges, the single span for Internal). With latency > 0
	// the target span starts at start + latency, so the child base is
	// target.start + 1ms = start + latency + 1ms. For Internal edges and
	// latency==0 pair edges this reduces to start + 1ms.
	childBase := start.Add(emit.Child.Edge.NetworkLatency).Add(1 * time.Millisecond)
	for _, child := range w.g.NextChildren(emit.Child.Edge.To) {
		cd := child.Edge.Duration
		if cd <= 0 {
			cd = 1 * time.Millisecond
		}
		childEffDur := cd + w.g.subtreeDuration[child.Edge.To]
		w.heap.PushEmit(&pendingEmit{
			DueAt:            childBase.Add(childEffDur),
			Trace:            w.trace,
			Child:            child,
			ParentSpanID:     result.TargetSpanID,
			RemainingRepeats: child.Edge.Repeat,
		})
		w.trace.InFlight++
		childBase = childBase.Add(time.Duration(child.Edge.Repeat) * w.g.stepDuration(child))
	}

	emit.RemainingRepeats--
	if emit.RemainingRepeats > 0 {
		emit.DueAt = emit.DueAt.Add(w.g.stepDuration(emit.Child))
		w.heap.PushEmit(emit)
	} else {
		w.trace.InFlight--
	}

	return result.Spans
}

type spanIDState struct {
	seed   uint64
	seq    uint64
	nextID atomic.Uint64
}

func newSpanIDState(seed int64, sequence uint64) *spanIDState {
	return &spanIDState{seed: uint64(seed), seq: sequence}
}

func (s *spanIDState) next() oteltrace.SpanID {
	index := s.nextID.Add(1)
	v := splitmix64(s.seed ^ s.seq ^ index)
	if v == 0 {
		v = 1
	}
	var id oteltrace.SpanID
	binary.BigEndian.PutUint64(id[:], v)
	return id
}

// materializedChild is the result of expanding one ChildSpec traversal
// into concrete spans. The walker uses TargetSpanID to populate nodeSpans
// and derives all timing from emit.DueAt + g.subtreeDuration, so this
// struct no longer carries ChildrenStart or CursorAfter fields.
type materializedChild struct {
	Spans        []model.Span
	TargetSpanID oteltrace.SpanID
}

// materializeChild produces the spans for one traversal of child without
// recursing into its descendants. It does not mutate caller state; the
// caller is responsible for appending Spans, recording TargetSpanID into
// nodeSpans, advancing the cursor, and driving recursion.
func (g *Generator) materializeChild(
	child ChildSpec,
	traceID oteltrace.TraceID,
	parentSpanID oteltrace.SpanID,
	start time.Time,
	idState *spanIDState,
	events []model.Event,
	links []model.Link,
) materializedChild {
	edge := child.Edge
	duration := edge.Duration
	if duration <= 0 {
		duration = 1 * time.Millisecond
	}

	// effDur covers own duration + subtree so the span temporally
	// contains its descendants.
	effDur := duration + g.subtreeDuration[edge.To]

	switch edge.Kind {
	case EdgeKindClientServer:
		return g.materializePair(child, traceID, parentSpanID, start, effDur, idState, events, links, oteltrace.SpanKindClient, oteltrace.SpanKindServer)
	case EdgeKindProducerConsumer:
		return g.materializePair(child, traceID, parentSpanID, start, effDur, idState, events, links, oteltrace.SpanKindProducer, oteltrace.SpanKindConsumer)
	case EdgeKindClientDatabase:
		return g.materializePair(child, traceID, parentSpanID, start, effDur, idState, events, links, oteltrace.SpanKindClient, oteltrace.SpanKindServer)
	case EdgeKindInternal:
		internalID := idState.next()
		internalSpan := g.newSpan(traceID, internalID, parentSpanID, child.TargetNode, oteltrace.SpanKindInternal, start, effDur, edge.SpanAttributes, events, links)
		return materializedChild{
			Spans:        []model.Span{internalSpan},
			TargetSpanID: internalID,
		}
	}

	// Unknown edge kinds are rejected by Config.Validate; reaching here
	// would indicate a programming error rather than user input.
	return materializedChild{TargetSpanID: parentSpanID}
}

// materializePair emits the source/target two-span pattern for non-Internal
// edges. Events and links go on the source span only. With
// edge.NetworkLatency > 0, the target span is inset by NetworkLatency on
// both sides of the source span's interval; children attach to the
// (narrower) target.
func (g *Generator) materializePair(
	child ChildSpec,
	traceID oteltrace.TraceID,
	parentSpanID oteltrace.SpanID,
	start time.Time,
	effDur time.Duration,
	idState *spanIDState,
	events []model.Event,
	links []model.Link,
	firstKind oteltrace.SpanKind,
	secondKind oteltrace.SpanKind,
) materializedChild {
	edge := child.Edge

	firstID := idState.next()
	firstSpan := g.newSpan(traceID, firstID, parentSpanID, child.SourceNode, firstKind, start, effDur, edge.SpanAttributes, events, links)
	firstSpan.Name = edgeSpanName(child.SourceNode, child.TargetNode)

	secondStart := start.Add(edge.NetworkLatency)
	secondDur := effDur - 2*edge.NetworkLatency
	secondID := idState.next()
	secondSpan := g.newSpan(traceID, secondID, firstID, child.TargetNode, secondKind, secondStart, secondDur, edge.SpanAttributes, nil, nil)

	return materializedChild{
		Spans:        []model.Span{firstSpan, secondSpan},
		TargetSpanID: secondID,
	}
}

func (g *Generator) newSpan(
	traceID oteltrace.TraceID,
	spanID oteltrace.SpanID,
	parentSpanID oteltrace.SpanID,
	node Node,
	kind oteltrace.SpanKind,
	start time.Time,
	duration time.Duration,
	edgeAttrs map[string]attribute.Value,
	events []model.Event,
	links []model.Link,
) model.Span {
	service := g.definition.Services[node.Service]
	resourceAttrs := cloneAttributeValues(service.ResourceAttributes)
	attrs := map[string]attribute.Value{}
	if serviceName, ok := resourceAttrs["service.name"]; ok {
		attrs["service.name"] = serviceName
	}
	for key, value := range edgeAttrs {
		attrs[key] = value
	}

	name := node.SpanName
	if name == "" {
		name = node.ID
	}
	if duration <= 0 {
		duration = 1 * time.Millisecond
	}

	end := start.Add(duration)
	span := model.Span{
		TraceID:            traceID,
		SpanID:             spanID,
		ParentSpanID:       parentSpanID,
		Name:               name,
		Kind:               kind,
		StartTime:          start,
		EndTime:            end,
		Attributes:         attrs,
		ResourceAttributes: resourceAttrs,
		StatusCode:         codes.Ok,
		Events:             eventsWithDefaultTime(events, start.Add(duration/2)),
		Links:              links,
	}
	return span
}

func eventsWithDefaultTime(events []model.Event, defaultTime time.Time) []model.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]model.Event, len(events))
	copy(out, events)
	for i := range out {
		if out[i].Time.IsZero() {
			out[i].Time = defaultTime
		}
	}
	return out
}

func resolveEvents(defs []EventDef) []model.Event {
	if len(defs) == 0 {
		return nil
	}
	out := make([]model.Event, len(defs))
	for i, def := range defs {
		out[i] = model.Event{
			Name:       def.Name,
			Attributes: def.Attributes,
		}
	}
	return out
}

func resolveLinks(traceID oteltrace.TraceID, defs []LinkDef, nodeSpans map[string]oteltrace.SpanID) []model.Link {
	if len(defs) == 0 {
		return nil
	}
	out := make([]model.Link, 0, len(defs))
	for _, def := range defs {
		spanID, ok := nodeSpans[def.Node]
		if !ok {
			continue
		}
		out = append(out, model.Link{
			SpanContext: oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
				TraceID:    traceID,
				SpanID:     spanID,
				TraceFlags: oteltrace.FlagsSampled,
			}),
			Attributes: def.Attributes,
		})
	}
	return out
}

func edgeSpanName(from Node, to Node) string {
	fromName := from.SpanName
	if fromName == "" {
		fromName = from.ID
	}
	toName := to.SpanName
	if toName == "" {
		toName = to.ID
	}
	return fmt.Sprintf("%s -> %s", fromName, toName)
}

func cloneAttributeValues(values map[string]attribute.Value) map[string]attribute.Value {
	if len(values) == 0 {
		return map[string]attribute.Value{}
	}
	copy := make(map[string]attribute.Value, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func estimateDuration(nodeID string, outgoing map[string][]Edge) time.Duration {
	memo := map[string]time.Duration{}
	var walk func(id string) time.Duration
	walk = func(id string) time.Duration {
		if value, ok := memo[id]; ok {
			return value
		}
		edges := outgoing[id]
		if len(edges) == 0 {
			memo[id] = 0
			return 0
		}
		total := time.Duration(0)
		for _, edge := range edges {
			duration := edge.Duration
			if duration <= 0 {
				duration = 1 * time.Millisecond
			}
			subtree := walk(edge.To)
			step := duration + subtree + 1*time.Millisecond
			total += time.Duration(edge.Repeat) * step
		}
		memo[id] = total
		return total
	}
	value := walk(nodeID)
	if value <= 0 {
		return 1 * time.Millisecond
	}
	return value
}

func traceIDFromSeed(seed int64, sequence uint64) oteltrace.TraceID {
	a := splitmix64(uint64(seed) ^ sequence)
	b := splitmix64(a ^ 0x9e3779b97f4a7c15)
	var id oteltrace.TraceID
	binary.BigEndian.PutUint64(id[0:8], a)
	binary.BigEndian.PutUint64(id[8:16], b)
	if id.IsValid() {
		return id
	}
	id[15] = 1
	return id
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

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

func (g *Generator) GenerateBatch(_ context.Context) ([]model.Span, error) {
	if g == nil {
		return nil, fmt.Errorf("scenario generator not configured")
	}
	if len(g.definition.Nodes) == 0 {
		return nil, fmt.Errorf("scenario definition has no nodes")
	}

	sequence := g.counter.Add(1)
	traceID := traceIDFromSeed(g.definition.Seed, sequence)
	idState := newSpanIDState(g.definition.Seed, sequence)
	nodeSpans := make(map[string]oteltrace.SpanID)

	estimated := estimateDuration(g.definition.Root, g.outgoing)
	if estimated <= 0 {
		estimated = 100 * time.Millisecond
	}
	base := time.Now().UTC()

	rootNode, ok := g.definition.Nodes[g.definition.Root]
	if !ok {
		return nil, fmt.Errorf("root node %q not found", g.definition.Root)
	}

	rootSpanID := idState.next()
	rootSpan := g.newSpan(traceID, rootSpanID, oteltrace.SpanID{}, rootNode, oteltrace.SpanKindInternal, base, estimated, nil, nil, nil)
	nodeSpans[g.definition.Root] = rootSpanID
	spans := []model.Span{rootSpan}

	cursor := base.Add(1 * time.Millisecond)
	spans = g.emitFromNode(spans, traceID, rootSpan.SpanID, g.definition.Root, &cursor, idState, nodeSpans)
	return spans, nil
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

// walkFrame is one pending child-edge expansion. Events/Links are
// resolved lazily on first pop and cached across repeats. A frame with
// Restore != zero is a cursor-restore sentinel: on pop the walker sets
// *cursor = Restore (no materialization) so siblings of a just-drained
// parent start at parent.CursorAfter instead of mid-subtree.
type walkFrame struct {
	Child            ChildSpec
	ParentSpanID     oteltrace.SpanID
	RemainingRepeats int
	Events           []model.Event
	Links            []model.Link
	Resolved         bool
	Restore          time.Time
}

// emitFromNode walks nodeID's descendants iteratively via a LIFO stack,
// preserving the original recursive DFS pre-order. Swapping the stack
// for a min-heap on emit-time is the seam the streaming exporter reuses.
func (g *Generator) emitFromNode(
	spans []model.Span,
	traceID oteltrace.TraceID,
	parentSpanID oteltrace.SpanID,
	nodeID string,
	cursor *time.Time,
	idState *spanIDState,
	nodeSpans map[string]oteltrace.SpanID,
) []model.Span {
	initial := g.NextChildren(nodeID)
	if len(initial) == 0 {
		return spans
	}

	// Seed: push the initial children in reverse so the leftmost sibling
	// (definition order) ends up on top and pops first.
	stack := make([]walkFrame, 0, len(initial))
	for i := len(initial) - 1; i >= 0; i-- {
		stack = append(stack, walkFrame{
			Child:            initial[i],
			ParentSpanID:     parentSpanID,
			RemainingRepeats: initial[i].Edge.Repeat,
		})
	}

	for len(stack) > 0 {
		top := len(stack) - 1
		frame := stack[top]
		stack = stack[:top]

		// Cursor-restore sentinel: the parent edge that pushed this frame
		// has now had its full subtree drained, so revert *cursor to the
		// position where the parent's NEXT sibling or repeat must start.
		if !frame.Restore.IsZero() {
			*cursor = frame.Restore
			continue
		}

		// Resolve events/links exactly once per frame, on the first pop.
		// At this point nodeSpans contains every span emitted by preceding
		// siblings' fully-drained subtrees, which matches the moment the
		// recursive code reached `links := resolveLinks(...)` for this edge.
		if !frame.Resolved {
			frame.Events = resolveEvents(frame.Child.Edge.SpanEvents)
			frame.Links = resolveLinks(traceID, frame.Child.Edge.SpanLinks, nodeSpans)
			frame.Resolved = true
		}

		result := g.materializeChild(frame.Child, traceID, frame.ParentSpanID, *cursor, idState, frame.Events, frame.Links)
		spans = append(spans, result.Spans...)
		nodeSpans[frame.Child.Edge.To] = result.TargetSpanID

		// Children of this just-materialized parent start INSIDE the
		// parent's interval at ChildrenStart, not after it. The sentinel
		// pushed below restores *cursor to CursorAfter once the children
		// (and their subtrees) have fully drained, so the next sibling or
		// repeat at the parent's level fires at parent.end + 1ms.
		*cursor = result.ChildrenStart

		frame.RemainingRepeats--

		// Push order (bottom to top of the stack): self-back for the next
		// repeat, restore sentinel, children of edge.To (leftmost on top).
		// Pop order is therefore: children drain → sentinel restores cursor
		// → self-back materializes the next repeat at parent.CursorAfter.
		if frame.RemainingRepeats > 0 {
			stack = append(stack, frame)
		}
		stack = append(stack, walkFrame{Restore: result.CursorAfter})

		nextChildren := g.NextChildren(frame.Child.Edge.To)
		for i := len(nextChildren) - 1; i >= 0; i-- {
			stack = append(stack, walkFrame{
				Child:            nextChildren[i],
				ParentSpanID:     result.TargetSpanID,
				RemainingRepeats: nextChildren[i].Edge.Repeat,
			})
		}
	}

	return spans
}

// materializedChild is the result of expanding one ChildSpec traversal.
// ChildrenStart (= span.start + 1ms) positions children inside the
// parent; CursorAfter (= span.end + 1ms) positions the next sibling at
// the same level.
type materializedChild struct {
	Spans         []model.Span
	TargetSpanID  oteltrace.SpanID // value to record in nodeSpans[edge.To]
	ChildrenStart time.Time
	CursorAfter   time.Time
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
			Spans:         []model.Span{internalSpan},
			TargetSpanID:  internalID,
			ChildrenStart: internalSpan.StartTime.Add(1 * time.Millisecond),
			CursorAfter:   internalSpan.EndTime.Add(1 * time.Millisecond),
		}
	}

	// Unknown edge kinds are rejected by Config.Validate; reaching here
	// would indicate a programming error rather than user input.
	return materializedChild{TargetSpanID: parentSpanID, ChildrenStart: start, CursorAfter: start}
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
		Spans:         []model.Span{firstSpan, secondSpan},
		TargetSpanID:  secondID,
		ChildrenStart: secondSpan.StartTime.Add(1 * time.Millisecond),
		CursorAfter:   firstSpan.EndTime.Add(1 * time.Millisecond),
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

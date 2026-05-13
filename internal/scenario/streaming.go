package scenario

import (
	"container/heap"
	"fmt"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// pendingEmit is one entry in the streaming exporter's scheduling heap.
//
// DueAt semantics: DueAt is the wall-clock moment at which the emit should
// fire = the end_time of the span(s) it produces. Waiting until
// time.Now() >= DueAt guarantees that the emitted span's end_time is in
// the past (or now), which is required by Tempo and most OTel-compatible
// backends. The span's start_time is then DueAt - edge.Duration.
//
// When IsRoot is true the emit is a sentinel for the trace's root span:
// Child, ParentSpanID, RemainingRepeats, Events, and Links are unused.
// The root SpanID is pre-allocated at NewStreamingWalker time and stored
// in trace.NodeSpans so that links from descendant spans can resolve to
// it even though the root itself is materialized last.
//
// Seq is assigned by emitHeap.PushEmit and breaks ties between emits with
// identical DueAt; the heap's Less function applies an additional rule
// that non-root emits sort before root emits at the same DueAt so that
// the root always fires after every sibling/descendant whose end_time
// coincides with the scenario's nominal end.
type pendingEmit struct {
	DueAt            time.Time
	Seq              uint64
	Trace            *traceState
	IsRoot           bool
	Child            ChildSpec
	ParentSpanID     oteltrace.SpanID
	RemainingRepeats int
	Events           []model.Event
	Links            []model.Link
	Resolved         bool
}

// traceState is the per-trace bookkeeping shared by every pendingEmit that
// belongs to one in-flight trace. The fields are the same per-trace data
// that the eager walker passes as scalar arguments (TraceID, NodeSpans,
// IDState, cursor). InFlight is a refcount of pending emits in the heap;
// when it drops to zero the trace has retired and traceState can be freed.
//
// All fields are accessed only by the streaming scheduler goroutine. No
// internal synchronization is required as long as one scheduler owns the
// trace; if a future design fans the scheduler across goroutines this
// invariant must be re-examined.
type traceState struct {
	TraceID   oteltrace.TraceID
	StartedAt time.Time
	NodeSpans map[string]oteltrace.SpanID
	IDState   *spanIDState
	InFlight  int
}

// emitHeap is a min-heap of *pendingEmit ordered by DueAt ascending, with
// Seq ascending as a stable tiebreaker. It implements heap.Interface so it
// can be driven by container/heap; callers should prefer the higher-level
// PushEmit / PopMin / PeekDueAt wrappers, which hide the Seq assignment and
// the heap.Push/Pop type assertions.
//
// emitHeap is not safe for concurrent use. The streaming scheduler owns a
// single instance and drives it from one goroutine.
type emitHeap struct {
	items []*pendingEmit
	seq   uint64
}

// Len, Less, Swap, Push, and Pop satisfy heap.Interface. They are exported
// only because the package contract requires it; do not call them directly.

func (h *emitHeap) Len() int { return len(h.items) }

func (h *emitHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if !a.DueAt.Equal(b.DueAt) {
		return a.DueAt.Before(b.DueAt)
	}
	// At equal DueAt, non-root emits pop first so the trace's root span is
	// emitted last (its end_time coincides with the last descendant's
	// end_time by construction).
	if a.IsRoot != b.IsRoot {
		return !a.IsRoot
	}
	return a.Seq < b.Seq
}

func (h *emitHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

func (h *emitHeap) Push(x any) {
	h.items = append(h.items, x.(*pendingEmit))
}

func (h *emitHeap) Pop() any {
	n := len(h.items)
	x := h.items[n-1]
	h.items[n-1] = nil // allow the popped emit to be GC'd
	h.items = h.items[:n-1]
	return x
}

// PushEmit inserts e into the heap. The caller need not set e.Seq;
// PushEmit assigns a monotonic sequence number for deterministic ordering
// among emits with identical DueAt.
func (h *emitHeap) PushEmit(e *pendingEmit) {
	h.seq++
	e.Seq = h.seq
	heap.Push(h, e)
}

// PopMin removes and returns the emit with the earliest DueAt. Panics if
// the heap is empty; callers should guard with Len or PeekDueAt.
func (h *emitHeap) PopMin() *pendingEmit {
	return heap.Pop(h).(*pendingEmit)
}

// PeekDueAt returns the DueAt of the earliest-scheduled emit without
// removing it. ok is false when the heap is empty.
func (h *emitHeap) PeekDueAt() (time.Time, bool) {
	if len(h.items) == 0 {
		return time.Time{}, false
	}
	return h.items[0].DueAt, true
}

// StreamingWalker walks one trace's DAG via emitHeap, yielding span batches
// at the wall-clock moments their spans should end. It is the single-trace
// primitive the streaming exporter composes: a future scheduler runs many
// walkers concurrently against a shared heap, waits on PeekDueAt, and
// forwards each NextEmit batch to OTLP.
//
// DueAt scheme:
//
//   - Every heap entry's DueAt equals the end_time of the span(s) it will
//     emit. The scheduler waits until time.Now() >= DueAt before popping,
//     which keeps emitted timestamps in the past (acceptable to Tempo).
//   - First repeat of a child of node N is scheduled at
//     parent.CursorAfter + child.Edge.Duration.
//   - Subsequent siblings are staggered by Generator.stepDuration(prev) *
//     prev.Edge.Repeat so each sibling fires only after its left siblings'
//     full subtree work (including all their repeats) completes.
//   - Self-back for the next repeat is scheduled at
//     emit.DueAt + Generator.stepDuration(emit.Child) so it fires after
//     the current repeat's subtree drains.
//   - The trace's root span is pushed as an IsRoot sentinel with DueAt =
//     startedAt + estimateDuration(root). By construction this is the
//     largest DueAt in the trace, so root fires last; the IsRoot field
//     also breaks ties in emitHeap.Less in case a descendant's end_time
//     coincides with the scenario's nominal end.
type StreamingWalker struct {
	g     *Generator
	trace *traceState
	heap  *emitHeap
}

// NewStreamingWalker constructs a walker for one trace whose root span is
// nominally placed at startedAt. The walker consumes one sequence number
// from g.counter so that successive walkers from the same generator emit
// distinct traces (same as GenerateBatch).
//
// Construction allocates the root SpanID immediately and records it in
// trace.NodeSpans so that descendant spans linking to the root resolve
// correctly even though the root span itself is materialized only when
// its heap sentinel pops (last).
func (g *Generator) NewStreamingWalker(startedAt time.Time) (*StreamingWalker, error) {
	if g == nil {
		return nil, fmt.Errorf("scenario generator not configured")
	}
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

	estimated := estimateDuration(g.definition.Root, g.outgoing)
	if estimated <= 0 {
		estimated = 100 * time.Millisecond
	}
	rootSpanID := trace.IDState.next()
	trace.NodeSpans[g.definition.Root] = rootSpanID

	w := &StreamingWalker{
		g:     g,
		trace: trace,
		heap:  &emitHeap{},
	}

	// Push root's children with staggered DueAts so each sibling fires
	// only after every earlier sibling's full subtree (including repeats)
	// has drained, mirroring the eager walker's sequential cursor.
	base := startedAt.Add(1 * time.Millisecond) // first child's logical start_time
	for _, child := range g.NextChildren(g.definition.Root) {
		d := child.Edge.Duration
		if d <= 0 {
			d = 1 * time.Millisecond
		}
		w.heap.PushEmit(&pendingEmit{
			DueAt:            base.Add(d), // end_time of this child's first repeat
			Trace:            trace,
			Child:            child,
			ParentSpanID:     rootSpanID,
			RemainingRepeats: child.Edge.Repeat,
		})
		trace.InFlight++
		base = base.Add(time.Duration(child.Edge.Repeat) * g.stepDuration(child))
	}

	// Push the root sentinel last. Its DueAt equals the scenario's nominal
	// end. Ties with the last descendant are broken by the IsRoot rule in
	// emitHeap.Less so root still fires after the descendant.
	w.heap.PushEmit(&pendingEmit{
		DueAt:  startedAt.Add(estimated),
		Trace:  trace,
		IsRoot: true,
	})
	trace.InFlight++
	return w, nil
}

// TraceID returns the deterministic TraceID assigned to this walker's
// trace at construction time.
func (w *StreamingWalker) TraceID() oteltrace.TraceID { return w.trace.TraceID }

// Done reports whether the walker has no more spans to emit.
func (w *StreamingWalker) Done() bool { return w.heap.Len() == 0 }

// NextDueAt returns the DueAt of the next emit without popping. ok is
// false when the walker is Done(). The scheduler uses this to decide
// how long to wait before the next NextEmit call.
func (w *StreamingWalker) NextDueAt() (time.Time, bool) {
	return w.heap.PeekDueAt()
}

// NextEmit pops the next emit from the heap, materializes its spans, and
// pushes that emit's children plus a self-repeat (if any). Returns ok=false
// once the heap is drained. Calling NextEmit after that is safe and
// continues to return ok=false.
//
// The caller is responsible for waiting until time.Now() >= dueAt before
// invoking NextEmit if wall-clock pacing is desired (see RunSingleTrace).
func (w *StreamingWalker) NextEmit() (spans []model.Span, dueAt time.Time, ok bool) {
	if w.heap.Len() == 0 {
		return nil, time.Time{}, false
	}

	emit := w.heap.PopMin()

	if emit.IsRoot {
		spans = w.materializeRoot(emit)
		w.trace.InFlight--
		return spans, emit.DueAt, true
	}

	// Resolve events/links lazily on first pop so the nodeSpans snapshot
	// reflects every span emitted earlier in the trace, matching the eager
	// walker's resolution timing.
	if !emit.Resolved {
		emit.Events = resolveEvents(emit.Child.Edge.SpanEvents)
		emit.Links = resolveLinks(w.trace.TraceID, emit.Child.Edge.SpanLinks, w.trace.NodeSpans)
		emit.Resolved = true
	}

	d := emit.Child.Edge.Duration
	if d <= 0 {
		d = 1 * time.Millisecond
	}
	start := emit.DueAt.Add(-d)
	result := w.g.materializeChild(emit.Child, w.trace.TraceID, emit.ParentSpanID, start, w.trace.IDState, emit.Events, emit.Links)
	w.trace.NodeSpans[emit.Child.Edge.To] = result.TargetSpanID

	// Push children of the just-emitted target, staggering siblings by the
	// cumulative subtree work of their predecessors so sequential cursor
	// semantics from the eager walker are preserved.
	childBase := result.CursorAfter // == emit.DueAt + 1ms, i.e. first child's logical start_time
	for _, child := range w.g.NextChildren(emit.Child.Edge.To) {
		cd := child.Edge.Duration
		if cd <= 0 {
			cd = 1 * time.Millisecond
		}
		w.heap.PushEmit(&pendingEmit{
			DueAt:            childBase.Add(cd),
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
		// Resolved stays true: events/links are reused across repeats so
		// resolution timing matches the eager walker's once-per-edge rule.
		w.heap.PushEmit(emit)
	} else {
		w.trace.InFlight--
	}

	return result.Spans, emit.DueAt, true
}

// materializeRoot constructs the root span using the SpanID pre-allocated
// at NewStreamingWalker time. Its duration equals emit.DueAt - startedAt,
// which by construction equals estimateDuration(root) and matches the
// duration the eager walker would assign.
func (w *StreamingWalker) materializeRoot(emit *pendingEmit) []model.Span {
	rootNode := w.g.definition.Nodes[w.g.definition.Root]
	rootSpanID := w.trace.NodeSpans[w.g.definition.Root]
	duration := emit.DueAt.Sub(w.trace.StartedAt)
	rootSpan := w.g.newSpan(w.trace.TraceID, rootSpanID, oteltrace.SpanID{}, rootNode, oteltrace.SpanKindInternal, w.trace.StartedAt, duration, nil, nil, nil)
	return []model.Span{rootSpan}
}

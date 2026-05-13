package scenario

import (
	"container/heap"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// pendingEmit is one entry in the streaming exporter's scheduling heap.
// It carries enough state to materialize the spans for a single ChildSpec
// traversal at DueAt, attach them under ParentSpanID, and push the
// traversal's own children onto the heap.
//
// The fields mirror the iterative walker's walkFrame plus a DueAt key and
// a Seq tiebreaker so that emits with identical DueAt pop in deterministic
// insertion order. Events and Links are resolved lazily on first pop (when
// the per-trace nodeSpans reflects every preceding span of that trace) so
// link semantics match the eager walker.
type pendingEmit struct {
	DueAt            time.Time
	Seq              uint64
	Trace            *traceState
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

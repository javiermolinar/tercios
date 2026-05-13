package scenario

import (
	"container/heap"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// pendingEmit is one entry in the trace-emission heap. DueAt is the
// (scenario-time anchored) instant the emit fires, equal to the end_time
// of the span(s) it produces. When IsRoot is true the emit is a sentinel
// for the trace's root span and Child/ParentSpanID/RemainingRepeats are
// unused; the root SpanID is preallocated at walker construction so
// descendant links resolve before the root span itself materializes.
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

// traceState is shared by every pendingEmit of one in-flight trace.
// All fields are accessed only by the single goroutine that owns the
// walker; no synchronization is required.
type traceState struct {
	TraceID   oteltrace.TraceID
	StartedAt time.Time
	NodeSpans map[string]oteltrace.SpanID
	IDState   *spanIDState
	InFlight  int
}

// emitHeap orders pendingEmit by (DueAt asc, IsRoot asc, Seq asc).
// The IsRoot tiebreaker keeps the root sentinel after any descendant
// whose end_time coincides with the scenario's nominal end. Not safe
// for concurrent use.
type emitHeap struct {
	items []*pendingEmit
	seq   uint64
}

func (h *emitHeap) Len() int { return len(h.items) }

func (h *emitHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if !a.DueAt.Equal(b.DueAt) {
		return a.DueAt.Before(b.DueAt)
	}
	if a.IsRoot != b.IsRoot {
		return !a.IsRoot
	}
	return a.Seq < b.Seq
}

func (h *emitHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *emitHeap) Push(x any) { h.items = append(h.items, x.(*pendingEmit)) }

func (h *emitHeap) Pop() any {
	n := len(h.items)
	x := h.items[n-1]
	h.items[n-1] = nil
	h.items = h.items[:n-1]
	return x
}

// PushEmit assigns a monotonic Seq and inserts e into the heap.
func (h *emitHeap) PushEmit(e *pendingEmit) {
	h.seq++
	e.Seq = h.seq
	heap.Push(h, e)
}

// PopMin removes and returns the earliest-scheduled emit. Panics on
// empty heap; callers should guard with Len or PeekDueAt.
func (h *emitHeap) PopMin() *pendingEmit { return heap.Pop(h).(*pendingEmit) }

// PeekDueAt returns the DueAt of the earliest-scheduled emit without
// removing it. ok is false when the heap is empty.
func (h *emitHeap) PeekDueAt() (time.Time, bool) {
	if len(h.items) == 0 {
		return time.Time{}, false
	}
	return h.items[0].DueAt, true
}

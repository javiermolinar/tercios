package scenario

import (
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// StreamingWalker exposes the trace-emission walker as a public type so
// the streaming exporter can drain spans one emit at a time and apply
// wall-clock pacing between pops. It is a thin facade over the same
// internal walker GenerateBatch uses.
//
// NextEmit / NextDueAt / Done are not safe for concurrent use; one
// scheduler goroutine should own one walker.
type StreamingWalker struct {
	inner *walker
}

// NewStreamingWalker constructs a walker for one trace whose root span
// is nominally placed at startedAt. Consumes one sequence number from
// the Generator's counter so successive walkers emit distinct traces.
func (g *Generator) NewStreamingWalker(startedAt time.Time) (*StreamingWalker, error) {
	w, err := g.newWalker(startedAt)
	if err != nil {
		return nil, err
	}
	return &StreamingWalker{inner: w}, nil
}

// TraceID returns the trace's deterministic ID, fixed at construction.
func (w *StreamingWalker) TraceID() oteltrace.TraceID { return w.inner.trace.TraceID }

// Done reports whether NextEmit will return ok=false on the next call.
func (w *StreamingWalker) Done() bool { return w.inner.done() }

// NextDueAt returns the DueAt of the next emit without popping. ok is
// false when the walker is Done. The scheduler uses this to compute how
// long to wait before the next NextEmit call.
func (w *StreamingWalker) NextDueAt() (time.Time, bool) {
	return w.inner.heap.PeekDueAt()
}

// NextEmit pops one emit, materializes its span(s), and pushes that
// emit's children plus a self-repeat (if any). Returns the spans, the
// DueAt the emit was scheduled at, and ok=false once the walker is
// drained. Calling NextEmit after drain remains safe.
func (w *StreamingWalker) NextEmit() (spans []model.Span, dueAt time.Time, ok bool) {
	if w.inner.done() {
		return nil, time.Time{}, false
	}
	due, _ := w.inner.heap.PeekDueAt()
	return w.inner.popOne(), due, true
}

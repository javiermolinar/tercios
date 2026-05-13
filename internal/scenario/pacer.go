package scenario

import (
	"context"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
)

// SpanSink receives one batch of spans per NextEmit call from a
// StreamingWalker. The streaming exporter wraps the eventual OTLP exporter
// in a SpanSink.
type SpanSink func(spans []model.Span)

// RunSingleTrace drives one StreamingWalker to completion with wall-clock
// pacing. The loop is the canonical streaming-exporter scheduling shape:
//
//  1. Peek the walker's next DueAt.
//  2. If it is still in the future, wait until then (or until ctx is
//     cancelled).
//  3. Pop and materialize, forward the batch to sink.
//  4. Repeat until the walker is Done().
//
// One goroutine drives one walker. Step 4 of the streaming plan will
// generalize this to one scheduler driving N walkers off a shared heap;
// the per-iteration logic stays the same.
//
// On ctx cancellation RunSingleTrace returns ctx.Err() and stops draining.
// Spans that had already been forwarded to sink are not retracted; the
// trace is left partially-emitted on the backend, which is acceptable for
// long-running traces (every backend already handles partial traces).
func RunSingleTrace(ctx context.Context, w *StreamingWalker, sink SpanSink) error {
	for !w.Done() {
		due, ok := w.NextDueAt()
		if !ok {
			return nil
		}

		if wait := time.Until(due); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}

		spans, _, ok := w.NextEmit()
		if !ok {
			return nil
		}
		if sink != nil && len(spans) > 0 {
			sink(spans)
		}
	}
	return nil
}

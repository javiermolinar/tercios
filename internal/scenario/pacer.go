package scenario

import (
	"context"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
)

// SpanSink receives one batch of spans per NextEmit. The streaming
// exporter wraps the OTLP client behind a SpanSink.
type SpanSink func(spans []model.Span)

// RunSingleTrace drives one StreamingWalker to completion with
// wall-clock pacing. The loop is the canonical streaming-exporter
// scheduling shape:
//
//  1. Peek the walker's next DueAt.
//  2. If it is still in the future, sleep until then (or until ctx is
//     cancelled).
//  3. Pop and materialize, forward the batch to sink.
//  4. Repeat until Done.
//
// Returns ctx.Err() if cancelled mid-flight; spans already forwarded
// are not retracted. One goroutine drives one walker; a future
// multi-trace scheduler will generalize this to N walkers off a shared
// heap, but the per-iteration logic stays the same.
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

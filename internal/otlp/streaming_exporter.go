package otlp

import (
	"context"
	"sort"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
)

// streamingBatchExporter wraps another BatchExporter and emits spans
// in wall-clock order: it sorts the input batch by EndTime, sleeps
// until each EndTime is reached, then forwards that emit group to the
// inner exporter. Spans sharing an EndTime are sent in one inner call.
//
// This makes long-running traces ingestible by backends that reject
// future timestamps. Pairs with chaos: when add_latency mutates an
// EndTime later, the pacer sleeps the extra duration, so the no-future
// invariant holds post-chaos too.
type streamingBatchExporter struct {
	inner model.BatchExporter
}

func NewStreamingBatchExporter(inner model.BatchExporter) model.BatchExporter {
	return &streamingBatchExporter{inner: inner}
}

func (e *streamingBatchExporter) ExportBatch(ctx context.Context, batch model.Batch) error {
	if len(batch) == 0 {
		return nil
	}

	sorted := make(model.Batch, len(batch))
	copy(sorted, batch)

	// Rebase the batch to wall-clock now. Span timestamps were set at
	// generation time; if the trace sat in the producer->exporter channel
	// or this exporter was previously busy, those times are stale and
	// would land in the past, causing the pacer to skip all sleeps and
	// flush the whole trace at once. Shift every span by (now - earliest
	// StartTime) so the trace paces from now over its full duration
	// regardless of queuing latency.
	earliest := sorted[0].StartTime
	for _, s := range sorted[1:] {
		if s.StartTime.Before(earliest) {
			earliest = s.StartTime
		}
	}
	shift := time.Since(earliest)
	if shift > 0 {
		for i := range sorted {
			sorted[i].StartTime = sorted[i].StartTime.Add(shift)
			sorted[i].EndTime = sorted[i].EndTime.Add(shift)
		}
	}

	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].EndTime.Before(sorted[j].EndTime)
	})

	for start := 0; start < len(sorted); {
		end := start + 1
		for end < len(sorted) && sorted[end].EndTime.Equal(sorted[start].EndTime) {
			end++
		}
		if wait := time.Until(sorted[start].EndTime); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if err := e.inner.ExportBatch(ctx, model.Batch(sorted[start:end])); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func (e *streamingBatchExporter) Shutdown(ctx context.Context) error {
	if e == nil || e.inner == nil {
		return nil
	}
	return e.inner.Shutdown(ctx)
}

// StreamingExporterFactory wraps another ExporterFactory so that every
// BatchExporter it produces is paced via streamingBatchExporter. The CLI
// installs this wrapper when --streaming is set.
type StreamingExporterFactory struct {
	Inner model.BatchExporterFactory
}

func NewStreamingExporterFactory(inner model.BatchExporterFactory) StreamingExporterFactory {
	return StreamingExporterFactory{Inner: inner}
}

func (f StreamingExporterFactory) NewBatchExporter(ctx context.Context) (model.BatchExporter, error) {
	inner, err := f.Inner.NewBatchExporter(ctx)
	if err != nil {
		return nil, err
	}
	return NewStreamingBatchExporter(inner), nil
}

package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/javiermolinar/tercios/internal/metrics"
	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/sdk/trace"
)

type BatchStage interface {
	name() string
	process(ctx context.Context, spans []model.Span) ([]model.Span, error)
}

type ExporterFactory interface {
	NewExporter(ctx context.Context) (trace.SpanExporter, error)
}

type Pipeline struct {
	stages  []BatchStage
	summary metrics.Summary
}

func New(stages ...BatchStage) *Pipeline {
	return &Pipeline{stages: stages}
}

func (p *Pipeline) Process(ctx context.Context, spans []model.Span) ([]model.Span, error) {
	batch := spans
	for _, stage := range p.stages {
		if stage == nil {
			continue
		}
		var err error
		batch, err = stage.process(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("stage %s: %w", stage.name(), err)
		}
	}
	return batch, nil
}

func (p *Pipeline) Run(ctx context.Context, runner *ConcurrencyRunner, factory ExporterFactory, requestInterval time.Duration, requestDuration time.Duration) error {
	if runner == nil {
		return fmt.Errorf("concurrency runner not configured")
	}
	if factory == nil {
		return fmt.Errorf("exporter factory not configured")
	}

	stats := make([]*metrics.Stats, runner.Workers())
	err := runner.Run(ctx, func(ctx context.Context, workerID int) error {
		workerStats := metrics.NewStats()
		stats[workerID] = workerStats

		var spanExporter trace.SpanExporter
		var batchExporter model.BatchExporter
		if batchFactory, ok := factory.(model.BatchExporterFactory); ok {
			exporter, err := batchFactory.NewBatchExporter(ctx)
			if err != nil {
				return err
			}
			batchExporter = metrics.NewInstrumentedBatchExporter(exporter, workerStats)
			defer batchExporter.Shutdown(ctx)
		} else {
			exporter, err := factory.NewExporter(ctx)
			if err != nil {
				return err
			}
			spanExporter = metrics.NewInstrumentedExporter(exporter, workerStats)
			defer spanExporter.Shutdown(ctx)
		}

		requests := runner.RequestsPerWorker()
		start := time.Now()
		for i := 0; ; i++ {
			if requests > 0 && i >= requests {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if requestDuration > 0 && time.Since(start) >= requestDuration {
				break
			}
			batch, err := p.Process(ctx, nil)
			if err != nil {
				return err
			}
			if len(batch) > 0 {
				if batchExporter != nil {
					if err := batchExporter.ExportBatch(ctx, model.Batch(batch)); err != nil {
						return err
					}
				} else {
					readonlyBatch, err := model.Batch(batch).ToReadOnlySpans(ctx)
					if err != nil {
						return fmt.Errorf("convert model batch to readonly spans: %w", err)
					}
					if err := spanExporter.ExportSpans(ctx, readonlyBatch); err != nil {
						return err
					}
				}
			}
			if requestInterval > 0 {
				if requests <= 0 || i < requests-1 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(requestInterval):
					}
				}
			}
		}
		return nil
	})

	p.summary = metrics.Summarize(stats)
	return err
}

func (p *Pipeline) Summary() metrics.Summary {
	if p == nil {
		return metrics.Summary{}
	}
	return p.summary
}

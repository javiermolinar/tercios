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

		exporter, err := factory.NewExporter(ctx)
		if err != nil {
			return err
		}
		defer exporter.Shutdown(ctx)

		exporter = metrics.NewInstrumentedExporter(exporter, workerStats)

		requests := runner.RequestsPerWorker()
		start := time.Now()
		for i := range requests {
			if requestDuration > 0 && time.Since(start) >= requestDuration {
				break
			}
			batch, err := p.Process(ctx, nil)
			if err != nil {
				return err
			}
			if len(batch) > 0 {
				readonlyBatch, err := model.Batch(batch).ToReadOnlySpans(ctx)
				if err != nil {
					return fmt.Errorf("convert model batch to readonly spans: %w", err)
				}
				if err := exporter.ExportSpans(ctx, readonlyBatch); err != nil {
					return err
				}
			}
			if requestInterval > 0 && i < requests-1 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(requestInterval):
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

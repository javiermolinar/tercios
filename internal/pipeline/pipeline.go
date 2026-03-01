package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/javiermolinar/tercios/internal/metrics"
	"github.com/javiermolinar/tercios/internal/model"
	"golang.org/x/sync/errgroup"
)

type BatchStage interface {
	name() string
	process(ctx context.Context, spans []model.Span) ([]model.Span, error)
}

type ExporterFactory interface {
	NewBatchExporter(ctx context.Context) (model.BatchExporter, error)
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

type exportResult struct {
	duration time.Duration
	err      error
}

func (p *Pipeline) Run(ctx context.Context, runner *ConcurrencyRunner, factory ExporterFactory, requestInterval time.Duration, requestDuration time.Duration) error {
	if runner == nil {
		return fmt.Errorf("concurrency runner not configured")
	}
	if factory == nil {
		return fmt.Errorf("exporter factory not configured")
	}
	if runner.Workers() <= 0 {
		return fmt.Errorf("workers must be > 0")
	}

	workerCount := runner.Workers()
	requestsPerWorker := runner.RequestsPerWorker()

	batchChannel := make(chan model.Batch, workerCount*2)
	summaryChannel := make(chan exportResult, workerCount*4)
	finalSummary := make(chan metrics.Summary, 1)

	group, groupCtx := errgroup.WithContext(ctx)

	var producerWG sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		producerWG.Add(1)
		group.Go(func() error {
			defer producerWG.Done()
			start := time.Now()
			for request := 0; ; request++ {
				if requestsPerWorker > 0 && request >= requestsPerWorker {
					return nil
				}
				if requestDuration > 0 && time.Since(start) >= requestDuration {
					return nil
				}
				select {
				case <-groupCtx.Done():
					return groupCtx.Err()
				default:
				}

				batch, err := p.Process(groupCtx, nil)
				if err != nil {
					return err
				}
				if len(batch) > 0 {
					select {
					case <-groupCtx.Done():
						return groupCtx.Err()
					case batchChannel <- model.Batch(batch):
					}
				}

				if requestInterval > 0 {
					if requestsPerWorker <= 0 || request < requestsPerWorker-1 {
						select {
						case <-groupCtx.Done():
							return groupCtx.Err()
						case <-time.After(requestInterval):
						}
					}
				}
			}
		})
	}

	group.Go(func() error {
		producerWG.Wait()
		close(batchChannel)
		return nil
	})

	var exporterWG sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		exporterWG.Add(1)
		group.Go(func() error {
			defer exporterWG.Done()
			exporter, err := factory.NewBatchExporter(groupCtx)
			if err != nil {
				return err
			}
			defer exporter.Shutdown(groupCtx)

			for {
				select {
				case <-groupCtx.Done():
					return groupCtx.Err()
				case batch, ok := <-batchChannel:
					if !ok {
						return nil
					}
					start := time.Now()
					err := exporter.ExportBatch(groupCtx, batch)
					result := exportResult{duration: time.Since(start), err: err}
					select {
					case <-groupCtx.Done():
						return groupCtx.Err()
					case summaryChannel <- result:
					}
					if err != nil {
						return err
					}
				}
			}
		})
	}

	group.Go(func() error {
		exporterWG.Wait()
		close(summaryChannel)
		return nil
	})

	group.Go(func() error {
		stats := metrics.NewStats()
		for result := range summaryChannel {
			stats.Record(result.duration, result.err)
		}
		finalSummary <- stats.Summary()
		close(finalSummary)
		return nil
	})

	err := group.Wait()
	if summary, ok := <-finalSummary; ok {
		p.summary = summary
	} else {
		p.summary = metrics.Summary{}
	}

	return err
}

func (p *Pipeline) Summary() metrics.Summary {
	if p == nil {
		return metrics.Summary{}
	}
	return p.summary
}

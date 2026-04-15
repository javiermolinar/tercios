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
	traceIDs []string
	spans    int
}

func (p *Pipeline) Run(ctx context.Context, runner *ConcurrencyRunner, factory ExporterFactory, requestInterval time.Duration, requestDuration time.Duration, rampUpDuration time.Duration, exportTimeout time.Duration, traceIDSampleLimit int) error {
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

	startTime := time.Now()

	var producerWG sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workerID := i
		producerWG.Add(1)
		group.Go(func() error {
			defer producerWG.Done()

			if delay := rampUpDelay(workerID, workerCount, rampUpDuration); delay > 0 {
				select {
				case <-groupCtx.Done():
					return groupCtx.Err()
				case <-time.After(delay):
				}
			}

			for request := 0; ; request++ {
				if requestsPerWorker > 0 && request >= requestsPerWorker {
					return nil
				}
				if requestDuration > 0 && time.Since(startTime) >= requestDuration {
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
		workerID := i
		exporterWG.Add(1)
		group.Go(func() error {
			defer exporterWG.Done()
			exporter, err := factory.NewBatchExporter(groupCtx)
			if err != nil {
				return fmt.Errorf("export worker=%d init: %w", workerID, err)
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

					exportCtx := groupCtx
					cancel := func() {}
					if exportTimeout > 0 {
						exportCtx, cancel = context.WithTimeout(groupCtx, exportTimeout)
					}
					traceIDs := sampleTraceIDs(batch, traceIDSampleLimit)
					start := time.Now()
					err := exporter.ExportBatch(exportCtx, batch)
					cancel()
					if err != nil {
						err = fmt.Errorf("export worker=%d: %w", workerID, err)
					}
					result := exportResult{duration: time.Since(start), err: err, traceIDs: traceIDs, spans: len(batch)}
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
		stats := metrics.NewStatsWithTraceIDSampleLimit(traceIDSampleLimit)
		for result := range summaryChannel {
			stats.RecordBatchWithTraceIDs(result.duration, result.err, result.traceIDs, result.spans)
		}
		finalSummary <- stats.SummaryWithElapsed(time.Since(startTime))
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

func rampUpDelay(workerID int, workerCount int, rampUpDuration time.Duration) time.Duration {
	if rampUpDuration <= 0 || workerCount <= 1 || workerID <= 0 {
		return 0
	}
	fraction := float64(workerID) / float64(workerCount-1)
	return time.Duration(float64(rampUpDuration) * fraction)
}

func sampleTraceIDs(batch model.Batch, limit int) []string {
	if limit <= 0 || len(batch) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(batch))
	traceIDs := make([]string, 0, limit)
	for _, span := range batch {
		traceID := span.TraceID.String()
		if _, exists := seen[traceID]; exists {
			continue
		}
		seen[traceID] = struct{}{}
		traceIDs = append(traceIDs, traceID)
		if len(traceIDs) >= limit {
			break
		}
	}
	return traceIDs
}

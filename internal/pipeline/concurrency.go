package pipeline

import (
	"context"

	"golang.org/x/sync/errgroup"
)

type ConcurrencyRunner struct {
	workers           int
	requestsPerWorker int
}

func NewConcurrencyRunner(workers, requestsPerWorker int) *ConcurrencyRunner {
	return &ConcurrencyRunner{
		workers:           workers,
		requestsPerWorker: requestsPerWorker,
	}
}

func (r *ConcurrencyRunner) Workers() int {
	return r.workers
}

func (r *ConcurrencyRunner) RequestsPerWorker() int {
	return r.requestsPerWorker
}

func (r *ConcurrencyRunner) Run(ctx context.Context, fn func(ctx context.Context, workerID int) error) error {
	group, ctx := errgroup.WithContext(ctx)
	for i := 0; i < r.workers; i++ {
		workerID := i
		group.Go(func() error {
			return fn(ctx, workerID)
		})
	}
	return group.Wait()
}

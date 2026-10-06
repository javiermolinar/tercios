package scenario

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/javiermolinar/tercios/internal/model"
)

type MultiGenerator struct {
	generators []BatchGenerator
	strategy   SelectionStrategy
	seed       uint64
	counter    atomic.Uint64
}

func NewMultiGenerator(definitions []Definition, strategy SelectionStrategy, seed int64) (*MultiGenerator, error) {
	if len(definitions) == 0 {
		return nil, fmt.Errorf("at least one scenario definition is required")
	}
	if strategy != SelectionStrategyRoundRobin && strategy != SelectionStrategyRandom {
		return nil, fmt.Errorf("unsupported selection strategy %q", strategy)
	}

	generators := make([]BatchGenerator, 0, len(definitions))
	for _, definition := range definitions {
		generators = append(generators, NewGenerator(definition))
	}

	return &MultiGenerator{
		generators: generators,
		strategy:   strategy,
		seed:       uint64(seed),
	}, nil
}

func (g *MultiGenerator) GenerateBatch(ctx context.Context) ([]model.Span, error) {
	if g == nil {
		return nil, fmt.Errorf("scenario generator not configured")
	}
	if len(g.generators) == 0 {
		return nil, fmt.Errorf("no scenario generators configured")
	}

	index := g.nextIndex()
	return g.generators[index].GenerateBatch(ctx)
}

func (g *MultiGenerator) nextIndex() int {
	count := len(g.generators)
	if count == 1 {
		return 0
	}
	sequence := g.counter.Add(1)

	switch g.strategy {
	case SelectionStrategyRandom:
		value := splitmix64(g.seed ^ sequence)
		return int(value % uint64(count))
	default:
		return int((sequence - 1) % uint64(count))
	}
}

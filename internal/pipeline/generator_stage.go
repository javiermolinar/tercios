package pipeline

import (
	"context"
	"fmt"

	"github.com/javiermolinar/tercios/internal/model"
	"github.com/javiermolinar/tercios/internal/tracegen"
)

type generatorStage struct {
	generator *tracegen.Generator
}

func NewGeneratorStage(generator *tracegen.Generator) BatchStage {
	return generatorStage{generator: generator}
}

func (s generatorStage) name() string {
	return "generator"
}

func (s generatorStage) process(ctx context.Context, _ []model.Span) ([]model.Span, error) {
	if s.generator == nil {
		return nil, fmt.Errorf("trace generator not configured")
	}
	spans, err := s.generator.GenerateBatch(ctx)
	if err != nil {
		return nil, err
	}
	return model.FromReadOnlySpans(spans), nil
}

package pipeline

import (
	"context"
	"fmt"

	"github.com/javiermolinar/tercios/internal/tracegen"
	"go.opentelemetry.io/otel/sdk/trace"
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

func (s generatorStage) process(ctx context.Context, _ []trace.ReadOnlySpan) ([]trace.ReadOnlySpan, error) {
	if s.generator == nil {
		return nil, fmt.Errorf("trace generator not configured")
	}
	return s.generator.GenerateBatch(ctx)
}

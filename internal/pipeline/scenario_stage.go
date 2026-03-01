package pipeline

import (
	"context"
	"fmt"

	"github.com/javiermolinar/tercios/internal/model"
	"github.com/javiermolinar/tercios/internal/scenario"
)

type scenarioStage struct {
	generator *scenario.Generator
}

func NewScenarioStage(generator *scenario.Generator) BatchStage {
	return scenarioStage{generator: generator}
}

func (s scenarioStage) name() string {
	return "scenario"
}

func (s scenarioStage) process(ctx context.Context, _ []model.Span) ([]model.Span, error) {
	if s.generator == nil {
		return nil, fmt.Errorf("scenario generator not configured")
	}
	return s.generator.GenerateBatch(ctx)
}

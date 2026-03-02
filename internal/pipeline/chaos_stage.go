package pipeline

import (
	"context"
	"fmt"

	"github.com/javiermolinar/tercios/internal/chaos"
	"github.com/javiermolinar/tercios/internal/model"
)

type chaosStage struct {
	engine      *chaos.Engine
	shouldApply chaos.ShouldApplyFunc
}

func NewChaosStage(engine *chaos.Engine, shouldApply chaos.ShouldApplyFunc) BatchStage {
	return &chaosStage{engine: engine, shouldApply: shouldApply}
}

func (s *chaosStage) name() string {
	return "chaos"
}

func (s *chaosStage) process(_ context.Context, spans []model.Span) ([]model.Span, error) {
	if s == nil || s.engine == nil {
		return nil, fmt.Errorf("chaos engine not configured")
	}
	return s.engine.Apply(spans, s.shouldApply), nil
}

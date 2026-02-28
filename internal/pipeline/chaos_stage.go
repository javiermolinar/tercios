package pipeline

import (
	"context"
	"fmt"
	"sync"

	"github.com/javiermolinar/tercios/internal/chaos"
	"github.com/javiermolinar/tercios/internal/model"
)

type chaosStage struct {
	engine      *chaos.Engine
	shouldApply chaos.ShouldApplyFunc
	mu          sync.Mutex
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
	return s.engine.Apply(spans, s.threadSafeShouldApply), nil
}

func (s *chaosStage) threadSafeShouldApply(probability float64) bool {
	if s == nil || s.shouldApply == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shouldApply(probability)
}

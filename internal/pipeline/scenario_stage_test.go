package pipeline

import (
	"context"
	"testing"

	"github.com/javiermolinar/tercios/internal/scenario"
)

func TestScenarioStageEmitsSpans(t *testing.T) {
	cfg := scenario.Config{
		Name: "test",
		Seed: 1,
		Services: map[string]scenario.ServiceConfig{
			"svc": {Resource: map[string]scenario.TypedValue{"service.name": {Type: scenario.ValueTypeString, Value: "svc"}}},
		},
		Nodes: map[string]scenario.NodeConfig{
			"a": {Service: "svc", SpanName: "root"},
			"b": {Service: "svc", SpanName: "child"},
		},
		Root: "a",
		Edges: []scenario.EdgeConfig{
			{From: "a", To: "b", Kind: scenario.EdgeKindInternal, Repeat: 1, DurationMs: 10},
		},
	}
	def, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	stage := NewScenarioStage(scenario.NewGenerator(def))

	batch, err := stage.process(context.Background(), nil)
	if err != nil {
		t.Fatalf("process() error = %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(batch))
	}
}

package pipeline

import (
	"context"
	"testing"

	"github.com/javiermolinar/tercios/internal/chaos"
	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestChaosStageAppliesPolicy(t *testing.T) {
	engine, err := chaos.NewEngine(chaos.Config{
		Policies: []chaos.Policy{
			{
				Name:        "error-post-service",
				Probability: 1,
				Match: chaos.Match{
					ServiceName: "post-service",
				},
				Actions: []chaos.Action{{Type: "set_status", Code: "error", Message: "simulated"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	stage := NewChaosStage(engine, chaos.NewSeededShouldApply(42))
	input := []model.Span{{
		Name:               "POST /posts",
		Kind:               oteltrace.SpanKindServer,
		Attributes:         map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		ResourceAttributes: map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		StatusCode:         codes.Ok,
	}}

	out, err := stage.process(context.Background(), input)
	if err != nil {
		t.Fatalf("process() error = %v", err)
	}
	if out[0].StatusCode != codes.Error {
		t.Fatalf("expected status error, got %s", out[0].StatusCode)
	}
	if out[0].StatusDescription != "simulated" {
		t.Fatalf("expected status message simulated, got %q", out[0].StatusDescription)
	}
}

func TestChaosStageRequiresEngine(t *testing.T) {
	stage := NewChaosStage(nil, nil)
	_, err := stage.process(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error when chaos engine is nil")
	}
}

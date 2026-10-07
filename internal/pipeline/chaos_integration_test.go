package pipeline

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/chaos"
	"github.com/javiermolinar/tercios/internal/model"
	"github.com/javiermolinar/tercios/internal/scenario"
	"github.com/javiermolinar/tercios/internal/typedvalue"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type capturedExporterFactory struct {
	mu    sync.Mutex
	spans []model.Span
}

func (f *capturedExporterFactory) NewBatchExporter(_ context.Context) (model.BatchExporter, error) {
	return &capturedBatchExporter{factory: f}, nil
}

type capturedBatchExporter struct {
	factory *capturedExporterFactory
}

func (e *capturedBatchExporter) ExportBatch(_ context.Context, batch model.Batch) error {
	e.factory.mu.Lock()
	defer e.factory.mu.Unlock()
	e.factory.spans = append(e.factory.spans, batch...)
	return nil
}

func (e *capturedBatchExporter) Shutdown(_ context.Context) error {
	return nil
}

func TestPipelineAppliesChaosPolicy(t *testing.T) {
	cfg := chaos.Config{
		Seed:       42,
		PolicyMode: chaos.PolicyModeAll,
		Policies: []chaos.Policy{{
			Name:        "inject-errors",
			Probability: 1,
			Match:       chaos.Match{ServiceName: "post-service"},
			Actions: []chaos.Action{
				{
					Type:    "set_status",
					Code:    "error",
					Message: "simulated failure",
				},
				{
					Type:  "set_attribute",
					Scope: "span",
					Name:  "http.response.status_code",
					Value: typedvalue.TypedValue{Type: "int", Value: int64(500)},
				},
				{
					Type:    "add_latency",
					DeltaMs: 120,
				},
			},
		}},
	}

	engine, err := chaos.NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	cfgScenario, err := scenario.DecodeJSON(strings.NewReader(`{"name":"chaos","seed":42,
		"services":{"svc":{"resource":{"service.name":{"type":"string","value":"post-service"},"service.version":{"type":"string","value":"2.10.0"}}},"other":{}},
		"nodes":{"a":{"service":"svc","span_name":"POST /posts","kind":"SERVER","duration_ms":10,"span_attributes":{"http.route":{"type":"string","value":"/posts"},"http.response.status_code":{"type":"int","value":200}}},
		"b":{"service":"other","span_name":"untouched","parent":null}},"edges":[{"from":"a","to":"b"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	definition, err := cfgScenario.Build()
	if err != nil {
		t.Fatal(err)
	}
	input, err := scenario.NewGenerator(definition).GenerateBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	runner := NewConcurrencyRunner(1, 1)
	factory := &capturedExporterFactory{}
	pipe := New(
		NewScenarioStage(scenario.NewGenerator(definition)),
		NewChaosStage(engine, chaos.NewSeededShouldApply(cfg.Seed)),
	)

	if err := pipe.Run(context.Background(), runner, factory, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}

	factory.mu.Lock()
	exported := append([]model.Span{}, factory.spans...)
	factory.mu.Unlock()

	if len(exported) != 2 || pipe.Summary().Total != 1 || pipe.Summary().SuccessfulSpans != 2 || pipe.Summary().Failures != 0 {
		t.Fatalf("export count/summary differs: %+v", pipe.Summary())
	}
	for i, got := range exported {
		want := input[i]
		want.StartTime, want.EndTime = got.StartTime, got.StartTime.Add(want.EndTime.Sub(want.StartTime))
		if i == 0 {
			want.Attributes["http.response.status_code"] = attribute.Int64Value(500)
			want.StatusCode, want.StatusDescription = codes.Error, "simulated failure"
			want.EndTime = want.EndTime.Add(120 * time.Millisecond)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chaos changed unexpected fields or topology: got=%+v want=%+v", got, want)
		}
	}

	span := exported[0]
	if span.StatusCode != codes.Error {
		t.Fatalf("expected error status, got %s", span.StatusCode)
	}
	if status, ok := span.Attributes["http.response.status_code"]; !ok || status.Emit() != "500" {
		t.Fatalf("expected http.response.status_code=500, got %s", status.Emit())
	}
}

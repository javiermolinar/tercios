package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/chaos"
	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type fixedStage struct {
	batch []model.Span
}

func (s fixedStage) name() string {
	return "fixed"
}

func (s fixedStage) process(_ context.Context, _ []model.Span) ([]model.Span, error) {
	return s.batch, nil
}

type capturedExporterFactory struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (f *capturedExporterFactory) NewExporter(_ context.Context) (sdktrace.SpanExporter, error) {
	return &capturedExporter{factory: f}, nil
}

type capturedExporter struct {
	factory *capturedExporterFactory
}

func (e *capturedExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.factory.mu.Lock()
	defer e.factory.mu.Unlock()
	e.factory.spans = append(e.factory.spans, spans...)
	return nil
}

func (e *capturedExporter) Shutdown(_ context.Context) error {
	return nil
}

func TestPipelineAppliesExampleChaosPolicies(t *testing.T) {
	cfg, err := loadExampleChaosConfig(t)
	if err != nil {
		t.Fatalf("load example chaos config: %v", err)
	}

	// Keep the test deterministic: always apply first policy.
	if len(cfg.Policies) == 0 {
		t.Fatalf("expected policies in example config")
	}
	cfg.Policies[0].Probability = 1
	cfg.Seed = 42

	engine, err := chaos.NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	start := time.Date(2026, time.January, 27, 12, 0, 0, 0, time.UTC)
	input := []model.Span{{
		Name:      "POST /posts",
		Kind:      oteltrace.SpanKindServer,
		StartTime: start,
		EndTime:   start.Add(10 * time.Millisecond),
		Attributes: map[string]attribute.Value{
			"service.name":              attribute.StringValue("post-service"),
			"http.route":                attribute.StringValue("/posts"),
			"http.response.status_code": attribute.Int64Value(200),
		},
		ResourceAttributes: map[string]attribute.Value{
			"service.name":    attribute.StringValue("post-service"),
			"service.version": attribute.StringValue("2.10.0"),
		},
		StatusCode: codes.Ok,
	}}

	runner := NewConcurrencyRunner(1, 1)
	factory := &capturedExporterFactory{}
	pipe := New(
		fixedStage{batch: input},
		NewChaosStage(engine, chaos.NewSeededShouldApply(cfg.Seed)),
	)

	if err := pipe.Run(context.Background(), runner, factory, 0, 0); err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}

	factory.mu.Lock()
	exported := append([]sdktrace.ReadOnlySpan{}, factory.spans...)
	factory.mu.Unlock()

	if len(exported) == 0 {
		t.Fatalf("expected exported spans, got none")
	}

	span := exported[0]
	if span.Status().Code != codes.Error {
		t.Fatalf("expected error status, got %s", span.Status().Code)
	}
	status := attributeValue(span.Attributes(), "http.response.status_code")
	if status != "500" {
		t.Fatalf("expected http.response.status_code=500, got %s", status)
	}
}

func loadExampleChaosConfig(t *testing.T) (chaos.Config, error) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return chaos.Config{}, fmt.Errorf("runtime caller not available")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return chaos.LoadFromJSON(filepath.Join(root, "examples", "chaos-policies.json"))
}

func attributeValue(attributes []attribute.KeyValue, key string) string {
	for _, kv := range attributes {
		if string(kv.Key) == key {
			return kv.Value.Emit()
		}
	}
	return ""
}

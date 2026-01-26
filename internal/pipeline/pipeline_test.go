package pipeline

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/javiermolinar/tercios/internal/config"
	"github.com/javiermolinar/tercios/internal/tracegen"
	"go.opentelemetry.io/otel/sdk/trace"
)

type testExporterFactory struct {
	count *int64
}

func (f testExporterFactory) NewExporter(_ context.Context) (trace.SpanExporter, error) {
	return &countingExporter{count: f.count}, nil
}

type countingExporter struct {
	count *int64
}

func (e *countingExporter) ExportSpans(_ context.Context, spans []trace.ReadOnlySpan) error {
	atomic.AddInt64(e.count, int64(len(spans)))
	return nil
}

func (e *countingExporter) Shutdown(_ context.Context) error {
	return nil
}

func TestPipelineRunsWithConcurrencyAndGenerator(t *testing.T) {
	var count int64
	cfg := config.Config{
		Endpoint: config.EndpointConfig{
			Address:  "localhost:4317",
			Protocol: config.ProtocolGRPC,
		},
		Concurrency: config.ConcurrencyConfig{
			Exporters: 3,
		},
		Requests: config.RequestConfig{
			PerExporter: 5,
		},
	}

	runner := NewConcurrencyRunner(cfg.Concurrency.Exporters, cfg.Requests.PerExporter)
	generator := &tracegen.Generator{ServiceName: "test", SpanName: "span", Services: 1, MaxDepth: 1, MaxSpans: 1}
	pipe := New(NewGeneratorStage(generator))
	factory := testExporterFactory{count: &count}

	if err := pipe.Run(context.Background(), runner, factory, 0, 0); err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}

	if got := atomic.LoadInt64(&count); got != 15 {
		t.Fatalf("expected 15 spans, got %d", got)
	}
}

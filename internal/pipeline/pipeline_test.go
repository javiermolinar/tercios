package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"github.com/javiermolinar/tercios/internal/tracegen"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestPipelineRunsWithConcurrencyAndGenerator(t *testing.T) {
	var calls int64
	var spans int64

	runner := NewConcurrencyRunner(3, 5)
	generator := &tracegen.Generator{ServiceName: "test", SpanName: "span", Services: 1, MaxDepth: 1, MaxSpans: 1}
	pipe := New(NewGeneratorStage(generator))
	factory := testBatchExporterFactory{calls: &calls, spans: &spans}

	if err := pipe.Run(context.Background(), runner, factory, 0, 0); err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}

	if got := atomic.LoadInt64(&calls); got != 15 {
		t.Fatalf("expected 15 export calls, got %d", got)
	}
	if got := atomic.LoadInt64(&spans); got != 15 {
		t.Fatalf("expected 15 spans, got %d", got)
	}
}

type fixedModelStage struct{}

func (fixedModelStage) name() string {
	return "fixed-model"
}

func (fixedModelStage) process(_ context.Context, _ []model.Span) ([]model.Span, error) {
	start := time.Date(2026, time.January, 27, 12, 0, 0, 0, time.UTC)
	return []model.Span{{
		TraceID:    oteltrace.TraceID{0x01},
		SpanID:     oteltrace.SpanID{0x02},
		Name:       "fixed",
		Kind:       oteltrace.SpanKindInternal,
		StartTime:  start,
		EndTime:    start.Add(10 * time.Millisecond),
		Attributes: map[string]attribute.Value{"k": attribute.StringValue("v")},
		ResourceAttributes: map[string]attribute.Value{
			"service.name": attribute.StringValue("svc"),
		},
		StatusCode: codes.Ok,
	}}, nil
}

type testBatchExporterFactory struct {
	calls *int64
	spans *int64
}

func (f testBatchExporterFactory) NewBatchExporter(_ context.Context) (model.BatchExporter, error) {
	return &countingBatchExporter{calls: f.calls, spans: f.spans}, nil
}

type countingBatchExporter struct {
	calls *int64
	spans *int64
}

func (e *countingBatchExporter) ExportBatch(_ context.Context, batch model.Batch) error {
	atomic.AddInt64(e.calls, 1)
	atomic.AddInt64(e.spans, int64(len(batch)))
	return nil
}

func (e *countingBatchExporter) Shutdown(_ context.Context) error {
	return nil
}

func TestPipelineUsesModelBatchExporterWhenAvailable(t *testing.T) {
	var calls int64
	var spans int64
	runner := NewConcurrencyRunner(2, 3)
	pipe := New(fixedModelStage{})
	factory := testBatchExporterFactory{calls: &calls, spans: &spans}

	if err := pipe.Run(context.Background(), runner, factory, 0, 0); err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}

	if got := atomic.LoadInt64(&calls); got != 6 {
		t.Fatalf("expected 6 batch export calls, got %d", got)
	}
	if got := atomic.LoadInt64(&spans); got != 6 {
		t.Fatalf("expected 6 exported spans, got %d", got)
	}
}

func TestPipelineUnlimitedRequestsStopsOnContextCancel(t *testing.T) {
	var calls int64
	var spans int64
	runner := NewConcurrencyRunner(1, 0)
	pipe := New(fixedModelStage{})
	factory := testBatchExporterFactory{calls: &calls, spans: &spans}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := pipe.Run(ctx, runner, factory, 0, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if got := atomic.LoadInt64(&calls); got == 0 {
		t.Fatalf("expected at least one export call before cancel")
	}
}

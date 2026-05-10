package pipeline

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"github.com/javiermolinar/tercios/internal/scenario"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestPipelineRunsWithConcurrencyAndGenerator(t *testing.T) {
	var calls int64
	var spans int64

	runner := NewConcurrencyRunner(3, 5)
	defaultGen, err := scenario.DefaultGenerator(0)
	if err != nil {
		t.Fatalf("DefaultGenerator() error = %v", err)
	}
	pipe := New(NewScenarioStage(defaultGen))
	factory := testBatchExporterFactory{calls: &calls, spans: &spans}

	if err := pipe.Run(context.Background(), runner, factory, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}

	if got := atomic.LoadInt64(&calls); got != 15 {
		t.Fatalf("expected 15 export calls (3 exporters × 5 requests), got %d", got)
	}
	if got := atomic.LoadInt64(&spans); got == 0 {
		t.Fatalf("expected spans, got 0")
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

type traceSampleStage struct{}

func (traceSampleStage) name() string {
	return "trace-samples"
}

func (traceSampleStage) process(_ context.Context, _ []model.Span) ([]model.Span, error) {
	start := time.Date(2026, time.January, 27, 12, 0, 0, 0, time.UTC)
	traceA := oteltrace.TraceID{0x0a}
	traceB := oteltrace.TraceID{0x0b}
	return []model.Span{
		{TraceID: traceA, SpanID: oteltrace.SpanID{0x01}, Name: "a1", Kind: oteltrace.SpanKindInternal, StartTime: start, EndTime: start.Add(1 * time.Millisecond), StatusCode: codes.Ok},
		{TraceID: traceA, SpanID: oteltrace.SpanID{0x02}, Name: "a2", Kind: oteltrace.SpanKindInternal, StartTime: start, EndTime: start.Add(1 * time.Millisecond), StatusCode: codes.Ok},
		{TraceID: traceB, SpanID: oteltrace.SpanID{0x03}, Name: "b1", Kind: oteltrace.SpanKindInternal, StartTime: start, EndTime: start.Add(1 * time.Millisecond), StatusCode: codes.Ok},
	}, nil
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
	if e.calls != nil {
		atomic.AddInt64(e.calls, 1)
	}
	if e.spans != nil {
		atomic.AddInt64(e.spans, int64(len(batch)))
	}
	return nil
}

func (e *countingBatchExporter) Shutdown(_ context.Context) error {
	return nil
}

type noopBatchExporterFactory struct{}

func (noopBatchExporterFactory) NewBatchExporter(_ context.Context) (model.BatchExporter, error) {
	return noopBatchExporter{}, nil
}

type noopBatchExporter struct{}

func (noopBatchExporter) ExportBatch(_ context.Context, _ model.Batch) error {
	return nil
}

func (noopBatchExporter) Shutdown(_ context.Context) error {
	return nil
}

type blockingBatchExporterFactory struct{}

func (blockingBatchExporterFactory) NewBatchExporter(_ context.Context) (model.BatchExporter, error) {
	return blockingBatchExporter{}, nil
}

type blockingBatchExporter struct{}

func (blockingBatchExporter) ExportBatch(ctx context.Context, _ model.Batch) error {
	<-ctx.Done()
	return ctx.Err()
}

func (blockingBatchExporter) Shutdown(_ context.Context) error {
	return nil
}

func TestPipelineUsesModelBatchExporterWhenAvailable(t *testing.T) {
	var calls int64
	var spans int64
	runner := NewConcurrencyRunner(2, 3)
	pipe := New(fixedModelStage{})
	factory := testBatchExporterFactory{calls: &calls, spans: &spans}

	if err := pipe.Run(context.Background(), runner, factory, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}

	if got := atomic.LoadInt64(&calls); got != 6 {
		t.Fatalf("expected 6 batch export calls, got %d", got)
	}
	if got := atomic.LoadInt64(&spans); got != 6 {
		t.Fatalf("expected 6 exported spans, got %d", got)
	}
}

func TestPipelineCollectsTraceIDSamplesWhenEnabled(t *testing.T) {
	runner := NewConcurrencyRunner(1, 1)
	pipe := New(traceSampleStage{})
	factory := noopBatchExporterFactory{}

	if err := pipe.Run(context.Background(), runner, factory, 0, 0, 0, 0, 1); err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}

	summary := pipe.Summary()
	if got := len(summary.TraceIDSamples); got != 1 {
		t.Fatalf("expected 1 trace id sample due to limit, got %d", got)
	}
	if summary.TraceIDSamples[0] == "" {
		t.Fatalf("expected non-empty trace id sample")
	}
}

func TestPipelineRampUpDelaysLateWorkers(t *testing.T) {
	var calls int64
	runner := NewConcurrencyRunner(2, 1)
	pipe := New(fixedModelStage{})
	factory := testBatchExporterFactory{calls: &calls}

	if err := pipe.Run(context.Background(), runner, factory, 0, 100*time.Millisecond, 200*time.Millisecond, 0, 0); err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("expected only first worker to export before duration cutoff, got %d", got)
	}
}

func TestPipelineAppliesPerExportTimeout(t *testing.T) {
	runner := NewConcurrencyRunner(1, 1)
	pipe := New(fixedModelStage{})
	factory := blockingBatchExporterFactory{}

	err := pipe.Run(context.Background(), runner, factory, 0, 0, 0, 10*time.Millisecond, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "export worker=") {
		t.Fatalf("expected exporter worker context in error, got %q", got)
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

	err := pipe.Run(ctx, runner, factory, 0, 0, 0, 0, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if got := atomic.LoadInt64(&calls); got == 0 {
		t.Fatalf("expected at least one export call before cancel")
	}
}

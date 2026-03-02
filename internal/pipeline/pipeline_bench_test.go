package pipeline

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type benchmarkFixedStage struct {
	batch []model.Span
}

func (s benchmarkFixedStage) name() string {
	return "benchmark-fixed"
}

func (s benchmarkFixedStage) process(_ context.Context, _ []model.Span) ([]model.Span, error) {
	return s.batch, nil
}

type benchmarkNoopExporterFactory struct{}

func (benchmarkNoopExporterFactory) NewBatchExporter(_ context.Context) (model.BatchExporter, error) {
	return benchmarkNoopExporter{}, nil
}

type benchmarkNoopExporter struct{}

func (benchmarkNoopExporter) ExportBatch(_ context.Context, _ model.Batch) error {
	return nil
}

func (benchmarkNoopExporter) Shutdown(_ context.Context) error {
	return nil
}

func BenchmarkPipelineRun(b *testing.B) {
	for _, workers := range []int{1, 4, 16} {
		for _, spanCount := range []int{10, 100} {
			workers := workers
			spanCount := spanCount
			name := fmt.Sprintf("workers_%d_spans_%d", workers, spanCount)
			b.Run(name, func(b *testing.B) {
				pipeline := New(benchmarkFixedStage{batch: makePipelineBenchBatch(spanCount)})
				runner := NewConcurrencyRunner(workers, 10)
				factory := benchmarkNoopExporterFactory{}

				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := pipeline.Run(context.Background(), runner, factory, 0, 0, 0, 0); err != nil {
						b.Fatalf("Run() error = %v", err)
					}
				}
			})
		}
	}
}

func makePipelineBenchBatch(count int) []model.Span {
	if count <= 0 {
		return nil
	}
	start := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	traceID := oteltrace.TraceID{0x44, 0x22, 0x11, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80}

	batch := make([]model.Span, 0, count)
	for i := 0; i < count; i++ {
		spanID := benchmarkSpanID(i + 1)
		parentID := oteltrace.SpanID{}
		if i > 0 {
			parentID = benchmarkSpanID(i)
		}
		batch = append(batch, model.Span{
			TraceID:      traceID,
			SpanID:       spanID,
			ParentSpanID: parentID,
			Name:         fmt.Sprintf("span-%d", i),
			Kind:         oteltrace.SpanKindInternal,
			StartTime:    start.Add(time.Duration(i) * time.Millisecond),
			EndTime:      start.Add(time.Duration(i+5) * time.Millisecond),
			Attributes: map[string]attribute.Value{
				"http.method":               attribute.StringValue("GET"),
				"http.response.status_code": attribute.IntValue(200),
			},
			ResourceAttributes: map[string]attribute.Value{
				"service.name": attribute.StringValue("bench-service"),
			},
			StatusCode: codes.Ok,
		})
	}
	return batch
}

func benchmarkSpanID(value int) oteltrace.SpanID {
	var id oteltrace.SpanID
	binary.BigEndian.PutUint64(id[:], uint64(value))
	return id
}

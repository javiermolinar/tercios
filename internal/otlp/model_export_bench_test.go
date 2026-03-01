package otlp

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

func BenchmarkModelExportPath_ToReadOnlySpans(b *testing.B) {
	benchmarkModelBatchSizes(b, func(b *testing.B, batch model.Batch) {
		ctx := context.Background()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			spans, err := batch.ToReadOnlySpans(ctx)
			if err != nil {
				b.Fatalf("ToReadOnlySpans() error = %v", err)
			}
			if len(spans) != len(batch) {
				b.Fatalf("expected %d spans, got %d", len(batch), len(spans))
			}
		}
	})
}

func BenchmarkModelExportPath_ToProto(b *testing.B) {
	benchmarkModelBatchSizes(b, func(b *testing.B, batch model.Batch) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resourceSpans := modelBatchToProto(batch)
			if len(resourceSpans) == 0 {
				b.Fatalf("expected non-empty resource spans")
			}
		}
	})
}

func benchmarkModelBatchSizes(b *testing.B, run func(b *testing.B, batch model.Batch)) {
	for _, size := range []int{10, 100, 500} {
		size := size
		b.Run(fmt.Sprintf("spans_%d", size), func(b *testing.B) {
			b.ReportAllocs()
			batch := makeBenchmarkBatch(size, 4)
			run(b, batch)
		})
	}
}

func makeBenchmarkBatch(spanCount int, resourceGroups int) model.Batch {
	if resourceGroups <= 0 {
		resourceGroups = 1
	}

	start := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	traceID := oteltrace.TraceID{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a}

	resourceAttrs := make([]map[string]attribute.Value, resourceGroups)
	for i := 0; i < resourceGroups; i++ {
		resourceAttrs[i] = map[string]attribute.Value{
			"service.namespace": attribute.StringValue("bench"),
			"service.name":      attribute.StringValue(fmt.Sprintf("svc-%d", i)),
			"service.version":   attribute.StringValue("1.0.0"),
		}
	}

	batch := make(model.Batch, 0, spanCount)
	for i := 0; i < spanCount; i++ {
		spanID := spanIDFromInt(i + 1)
		parentID := oteltrace.SpanID{}
		if i > 0 {
			parentID = spanIDFromInt(i)
		}

		serviceIndex := i % resourceGroups
		spanAttrs := map[string]attribute.Value{
			"http.method":               attribute.StringValue("GET"),
			"http.route":                attribute.StringValue("/bench"),
			"http.response.status_code": attribute.IntValue(200),
			"span.index":                attribute.IntValue(i),
			"feature.enabled":           attribute.BoolValue(true),
		}

		batch = append(batch, model.Span{
			TraceID:            traceID,
			SpanID:             spanID,
			ParentSpanID:       parentID,
			Name:               fmt.Sprintf("span-%d", i),
			Kind:               oteltrace.SpanKindInternal,
			StartTime:          start.Add(time.Duration(i) * time.Millisecond),
			EndTime:            start.Add(time.Duration(i+5) * time.Millisecond),
			Attributes:         spanAttrs,
			ResourceAttributes: resourceAttrs[serviceIndex],
			StatusCode:         codes.Ok,
		})
	}

	return batch
}

func spanIDFromInt(value int) oteltrace.SpanID {
	var id oteltrace.SpanID
	binary.BigEndian.PutUint64(id[:], uint64(value))
	return id
}

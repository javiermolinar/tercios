package otlp

import (
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestModelBatchToProto(t *testing.T) {
	traceID := oteltrace.TraceID{0x01}
	spanID := oteltrace.SpanID{0x02}
	start := time.Date(2026, time.January, 27, 12, 0, 0, 0, time.UTC)
	end := start.Add(25 * time.Millisecond)

	batch := model.Batch{{
		TraceID:    traceID,
		SpanID:     spanID,
		Name:       "root",
		Kind:       oteltrace.SpanKindServer,
		StartTime:  start,
		EndTime:    end,
		Attributes: map[string]attribute.Value{"http.response.status_code": attribute.Int64Value(200)},
		ResourceAttributes: map[string]attribute.Value{
			"service.name": attribute.StringValue("test-service"),
		},
		StatusCode: codes.Ok,
	}}

	resourceSpans := modelBatchToProto(batch)
	if len(resourceSpans) != 1 {
		t.Fatalf("expected 1 resource spans, got %d", len(resourceSpans))
	}
	scopeSpans := resourceSpans[0].GetScopeSpans()
	if len(scopeSpans) != 1 {
		t.Fatalf("expected 1 scope spans, got %d", len(scopeSpans))
	}
	spans := scopeSpans[0].GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	pbSpan := spans[0]
	if pbSpan.Name != "root" {
		t.Fatalf("expected span name root, got %q", pbSpan.Name)
	}
	if pbSpan.Kind != tracepb.Span_SPAN_KIND_SERVER {
		t.Fatalf("expected server span kind, got %s", pbSpan.Kind)
	}
	if pbSpan.Status.GetCode() != tracepb.Status_STATUS_CODE_OK {
		t.Fatalf("expected status OK, got %s", pbSpan.Status.GetCode())
	}
}

package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"github.com/javiermolinar/tercios/internal/tracegen"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestParseDryRunOutput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    DryRunOutput
		wantErr bool
	}{
		{name: "default", input: "", want: DryRunOutputSummary},
		{name: "summary", input: "summary", want: DryRunOutputSummary},
		{name: "json", input: "json", want: DryRunOutputJSON},
		{name: "invalid", input: "xml", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDryRunOutput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDryRunOutput() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseDryRunOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDryRunJSONExporterWritesBatch(t *testing.T) {
	spans, err := tracegen.Generator{
		ServiceName: "svc",
		SpanName:    "span",
		Services:    1,
		MaxDepth:    1,
		MaxSpans:    1,
	}.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch() error = %v", err)
	}

	var out bytes.Buffer
	factory := NewDryRunExporterFactory(DryRunOutputJSON, &out)
	exporter, err := factory.NewExporter(context.Background())
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}

	if err := exporter.ExportSpans(context.Background(), spans); err != nil {
		t.Fatalf("ExportSpans() error = %v", err)
	}

	var batch jsonBatch
	if err := json.Unmarshal(out.Bytes(), &batch); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}
	if len(batch.Spans) != 1 {
		t.Fatalf("expected 1 span in output, got %d", len(batch.Spans))
	}
	if batch.Spans[0].TraceID == "" {
		t.Fatalf("expected trace_id in json output")
	}
}

func TestDryRunJSONBatchExporterWritesBatch(t *testing.T) {
	start := time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)
	batch := model.Batch{{
		TraceID:      oteltrace.TraceID{0x01},
		SpanID:       oteltrace.SpanID{0x02},
		ParentSpanID: oteltrace.SpanID{0x03},
		Name:         "payment",
		Kind:         oteltrace.SpanKindClient,
		StartTime:    start,
		EndTime:      start.Add(42 * time.Millisecond),
		Attributes: map[string]attribute.Value{
			"http.response.status_code": attribute.IntValue(504),
		},
		ResourceAttributes: map[string]attribute.Value{
			"service.name": attribute.StringValue("payment-service"),
		},
		StatusCode:        codes.Error,
		StatusDescription: "timeout",
	}}

	var out bytes.Buffer
	factory := NewDryRunExporterFactory(DryRunOutputJSON, &out)
	exporter, err := factory.NewBatchExporter(context.Background())
	if err != nil {
		t.Fatalf("NewBatchExporter() error = %v", err)
	}

	if err := exporter.ExportBatch(context.Background(), batch); err != nil {
		t.Fatalf("ExportBatch() error = %v", err)
	}

	var payload jsonBatch
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}
	if len(payload.Spans) != 1 {
		t.Fatalf("expected 1 span in output, got %d", len(payload.Spans))
	}
	span := payload.Spans[0]
	if span.TraceID == "" {
		t.Fatalf("expected trace_id in json output")
	}
	if span.Kind != "client" {
		t.Fatalf("expected kind=client, got %q", span.Kind)
	}
	if span.DurationMs != 42 {
		t.Fatalf("expected duration_ms=42, got %d", span.DurationMs)
	}
	if got := span.Resource["service.name"]; got != "payment-service" {
		t.Fatalf("expected resource service.name=payment-service, got %#v", got)
	}
	if got := span.Status.Code; got != "Error" {
		t.Fatalf("expected status code Error, got %q", got)
	}
}

func TestDryRunSummaryExporterDoesNotWrite(t *testing.T) {
	spans, err := tracegen.Generator{
		ServiceName: "svc",
		SpanName:    "span",
		Services:    1,
		MaxDepth:    1,
		MaxSpans:    1,
	}.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch() error = %v", err)
	}

	var out bytes.Buffer
	factory := NewDryRunExporterFactory(DryRunOutputSummary, &out)
	exporter, err := factory.NewExporter(context.Background())
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}

	if err := exporter.ExportSpans(context.Background(), spans); err != nil {
		t.Fatalf("ExportSpans() error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output for summary exporter, got %q", out.String())
	}
}

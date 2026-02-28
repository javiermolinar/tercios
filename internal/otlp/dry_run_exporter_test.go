package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/javiermolinar/tercios/internal/tracegen"
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

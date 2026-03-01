package otlp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type DryRunOutput string

const (
	DryRunOutputSummary DryRunOutput = "summary"
	DryRunOutputJSON    DryRunOutput = "json"
)

func ParseDryRunOutput(value string) (DryRunOutput, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(DryRunOutputSummary):
		return DryRunOutputSummary, nil
	case string(DryRunOutputJSON):
		return DryRunOutputJSON, nil
	default:
		return "", fmt.Errorf("unsupported output format %q (supported: summary, json)", value)
	}
}

type DryRunExporterFactory struct {
	Output DryRunOutput
	Writer io.Writer

	lock *sync.Mutex
}

func NewDryRunExporterFactory(output DryRunOutput, writer io.Writer) DryRunExporterFactory {
	if writer == nil {
		writer = os.Stdout
	}
	return DryRunExporterFactory{
		Output: output,
		Writer: writer,
		lock:   &sync.Mutex{},
	}
}

func (f DryRunExporterFactory) NewBatchExporter(_ context.Context) (model.BatchExporter, error) {
	switch f.Output {
	case DryRunOutputJSON:
		return &jsonBatchExporter{writer: f.Writer, lock: f.lock}, nil
	case DryRunOutputSummary:
		fallthrough
	default:
		return noopBatchExporter{}, nil
	}
}

func (f DryRunExporterFactory) NewExporter(_ context.Context) (sdktrace.SpanExporter, error) {
	switch f.Output {
	case DryRunOutputJSON:
		return &jsonExporter{writer: f.Writer, lock: f.lock}, nil
	case DryRunOutputSummary:
		fallthrough
	default:
		return noopExporter{}, nil
	}
}

type noopBatchExporter struct{}

func (noopBatchExporter) ExportBatch(_ context.Context, _ model.Batch) error {
	return nil
}

func (noopBatchExporter) Shutdown(_ context.Context) error {
	return nil
}

type noopExporter struct{}

func (noopExporter) ExportSpans(_ context.Context, _ []sdktrace.ReadOnlySpan) error {
	return nil
}

func (noopExporter) Shutdown(_ context.Context) error {
	return nil
}

type jsonBatchExporter struct {
	writer io.Writer
	lock   *sync.Mutex
}

func (e *jsonBatchExporter) ExportBatch(_ context.Context, batch model.Batch) error {
	if len(batch) == 0 {
		return nil
	}

	payload := jsonBatch{Spans: make([]jsonSpan, 0, len(batch))}
	for _, span := range batch {
		payload.Spans = append(payload.Spans, toJSONSpanFromModel(span))
	}

	return writeJSONBatch(e.writer, e.lock, payload)
}

func (e *jsonBatchExporter) Shutdown(_ context.Context) error {
	return nil
}

type jsonExporter struct {
	writer io.Writer
	lock   *sync.Mutex
}

func (e *jsonExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}

	payload := jsonBatch{Spans: make([]jsonSpan, 0, len(spans))}
	for _, span := range spans {
		payload.Spans = append(payload.Spans, toJSONSpan(span))
	}

	return writeJSONBatch(e.writer, e.lock, payload)
}

func (e *jsonExporter) Shutdown(_ context.Context) error {
	return nil
}

func writeJSONBatch(writer io.Writer, lock *sync.Mutex, payload jsonBatch) error {
	lock.Lock()
	defer lock.Unlock()

	encoder := json.NewEncoder(writer)
	return encoder.Encode(payload)
}

type jsonBatch struct {
	Spans []jsonSpan `json:"spans"`
}

type jsonSpan struct {
	TraceID      string         `json:"trace_id"`
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	StartTime    string         `json:"start_time"`
	EndTime      string         `json:"end_time"`
	DurationMs   int64          `json:"duration_ms"`
	Attributes   map[string]any `json:"attributes,omitempty"`
	Resource     map[string]any `json:"resource,omitempty"`
	Status       jsonStatus     `json:"status"`
}

type jsonStatus struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func toJSONSpan(span sdktrace.ReadOnlySpan) jsonSpan {
	parentSpanID := ""
	if parent := span.Parent(); parent.IsValid() {
		parentSpanID = parent.SpanID().String()
	}

	resourceAttributes := map[string]any(nil)
	if res := span.Resource(); res != nil {
		resourceAttributes = attributeSliceToMap(res.Attributes())
	}

	status := span.Status()
	return jsonSpan{
		TraceID:      span.SpanContext().TraceID().String(),
		SpanID:       span.SpanContext().SpanID().String(),
		ParentSpanID: parentSpanID,
		Name:         span.Name(),
		Kind:         span.SpanKind().String(),
		StartTime:    span.StartTime().UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
		EndTime:      span.EndTime().UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
		DurationMs:   span.EndTime().Sub(span.StartTime()).Milliseconds(),
		Attributes:   attributeSliceToMap(span.Attributes()),
		Resource:     resourceAttributes,
		Status: jsonStatus{
			Code:    status.Code.String(),
			Message: status.Description,
		},
	}
}

func toJSONSpanFromModel(span model.Span) jsonSpan {
	parentSpanID := ""
	if span.ParentSpanID.IsValid() {
		parentSpanID = span.ParentSpanID.String()
	}

	return jsonSpan{
		TraceID:      span.TraceID.String(),
		SpanID:       span.SpanID.String(),
		ParentSpanID: parentSpanID,
		Name:         span.Name,
		Kind:         span.Kind.String(),
		StartTime:    span.StartTime.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
		EndTime:      span.EndTime.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
		DurationMs:   span.EndTime.Sub(span.StartTime).Milliseconds(),
		Attributes:   attributeMapToAnyMap(span.Attributes),
		Resource:     attributeMapToAnyMap(span.ResourceAttributes),
		Status: jsonStatus{
			Code:    span.StatusCode.String(),
			Message: span.StatusDescription,
		},
	}
}

func attributeSliceToMap(attributes []attribute.KeyValue) map[string]any {
	if len(attributes) == 0 {
		return nil
	}
	out := make(map[string]any, len(attributes))
	for _, kv := range attributes {
		out[string(kv.Key)] = kv.Value.AsInterface()
	}
	return out
}

func attributeMapToAnyMap(attributes map[string]attribute.Value) map[string]any {
	if len(attributes) == 0 {
		return nil
	}
	out := make(map[string]any, len(attributes))
	for key, value := range attributes {
		out[key] = value.AsInterface()
	}
	return out
}

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

type noopBatchExporter struct{}

func (noopBatchExporter) ExportBatch(_ context.Context, _ model.Batch) error {
	return nil
}

func (noopBatchExporter) Shutdown(_ context.Context) error {
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
	Events       []jsonEvent    `json:"events,omitempty"`
	Links        []jsonLink     `json:"links,omitempty"`
	Status       jsonStatus     `json:"status"`
}

type jsonEvent struct {
	Name       string         `json:"name"`
	Time       string         `json:"time,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type jsonLink struct {
	TraceID    string         `json:"trace_id"`
	SpanID     string         `json:"span_id"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type jsonStatus struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
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
		Events:       eventsToJSON(span.Events),
		Links:        linksToJSON(span.Links),
		Status: jsonStatus{
			Code:    span.StatusCode.String(),
			Message: span.StatusDescription,
		},
	}
}

func eventsToJSON(events []model.Event) []jsonEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]jsonEvent, len(events))
	for i, e := range events {
		var t string
		if !e.Time.IsZero() {
			t = e.Time.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
		}
		out[i] = jsonEvent{
			Name:       e.Name,
			Time:       t,
			Attributes: keyValuesToAnyMap(e.Attributes),
		}
	}
	return out
}

func linksToJSON(links []model.Link) []jsonLink {
	if len(links) == 0 {
		return nil
	}
	out := make([]jsonLink, len(links))
	for i, l := range links {
		out[i] = jsonLink{
			TraceID:    l.SpanContext.TraceID().String(),
			SpanID:     l.SpanContext.SpanID().String(),
			Attributes: keyValuesToAnyMap(l.Attributes),
		}
	}
	return out
}

func keyValuesToAnyMap(kvs []attribute.KeyValue) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
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

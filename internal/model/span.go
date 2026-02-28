package model

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Span struct {
	TraceID      oteltrace.TraceID
	SpanID       oteltrace.SpanID
	ParentSpanID oteltrace.SpanID

	Name      string
	Kind      oteltrace.SpanKind
	StartTime time.Time
	EndTime   time.Time

	Attributes         map[string]attribute.Value
	ResourceAttributes map[string]attribute.Value

	Links  []sdktrace.Link
	Events []sdktrace.Event

	StatusCode        codes.Code
	StatusDescription string
}

type Batch []Span

func (s Span) ToReadOnlySpan(ctx context.Context) (sdktrace.ReadOnlySpan, error) {
	spans, err := Batch{s}.ToReadOnlySpans(ctx)
	if err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		return nil, fmt.Errorf("span conversion returned empty batch")
	}
	return spans[0], nil
}

func (b Batch) ToReadOnlySpans(ctx context.Context) ([]sdktrace.ReadOnlySpan, error) {
	return toReadOnlySpans(ctx, []Span(b))
}

func AttributesToMap(attributes []attribute.KeyValue) map[string]attribute.Value {
	if len(attributes) == 0 {
		return map[string]attribute.Value{}
	}
	out := make(map[string]attribute.Value, len(attributes))
	for _, kv := range attributes {
		out[string(kv.Key)] = kv.Value
	}
	return out
}

func AttributesFromMap(attributes map[string]attribute.Value) []attribute.KeyValue {
	if len(attributes) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]attribute.KeyValue, 0, len(keys))
	for _, key := range keys {
		out = append(out, attribute.KeyValue{Key: attribute.Key(key), Value: attributes[key]})
	}
	return out
}

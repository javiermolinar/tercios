package otlp

import (
	"sort"
	"strings"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func modelBatchToProto(batch model.Batch) []*tracepb.ResourceSpans {
	if len(batch) == 0 {
		return nil
	}

	type grouped struct {
		resourceAttrs map[string]attribute.Value
		spans         []*tracepb.Span
	}

	groups := map[string]*grouped{}
	order := make([]string, 0)
	for _, span := range batch {
		resourceKey := resourceAttributesKey(span.ResourceAttributes)
		entry, exists := groups[resourceKey]
		if !exists {
			entry = &grouped{resourceAttrs: span.ResourceAttributes, spans: make([]*tracepb.Span, 0)}
			groups[resourceKey] = entry
			order = append(order, resourceKey)
		}
		entry.spans = append(entry.spans, modelSpanToProto(span))
	}

	out := make([]*tracepb.ResourceSpans, 0, len(order))
	for _, key := range order {
		group := groups[key]
		out = append(out, &tracepb.ResourceSpans{
			Resource: &resourcepb.Resource{Attributes: attrsFromValueMap(group.resourceAttrs)},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Scope: &commonpb.InstrumentationScope{Name: "tercios"},
				Spans: group.spans,
			}},
		})
	}
	return out
}

func modelSpanToProto(span model.Span) *tracepb.Span {
	pb := &tracepb.Span{
		TraceId:           append([]byte(nil), span.TraceID[:]...),
		SpanId:            append([]byte(nil), span.SpanID[:]...),
		ParentSpanId:      spanIDBytes(span.ParentSpanID),
		Name:              span.Name,
		Kind:              spanKindToProto(span.Kind),
		StartTimeUnixNano: uint64(span.StartTime.UnixNano()),
		EndTimeUnixNano:   uint64(span.EndTime.UnixNano()),
		Attributes:        attrsFromValueMap(span.Attributes),
		Events:            eventsToProto(span.Events),
		Links:             linksToProto(span.Links),
	}
	if span.StatusCode != codes.Unset || span.StatusDescription != "" {
		pb.Status = &tracepb.Status{
			Code:    statusCodeToProto(span.StatusCode),
			Message: span.StatusDescription,
		}
	}
	return pb
}

func linksToProto(links []sdktrace.Link) []*tracepb.Span_Link {
	if len(links) == 0 {
		return nil
	}
	out := make([]*tracepb.Span_Link, 0, len(links))
	for _, link := range links {
		sc := link.SpanContext
		traceID := sc.TraceID()
		spanID := sc.SpanID()
		out = append(out, &tracepb.Span_Link{
			TraceId:                append([]byte(nil), traceID[:]...),
			SpanId:                 append([]byte(nil), spanID[:]...),
			TraceState:             sc.TraceState().String(),
			Attributes:             keyValuesToProto(link.Attributes),
			DroppedAttributesCount: uint32(link.DroppedAttributeCount),
		})
	}
	return out
}

func eventsToProto(events []sdktrace.Event) []*tracepb.Span_Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]*tracepb.Span_Event, 0, len(events))
	for _, event := range events {
		out = append(out, &tracepb.Span_Event{
			TimeUnixNano:           uint64(event.Time.UnixNano()),
			Name:                   event.Name,
			Attributes:             keyValuesToProto(event.Attributes),
			DroppedAttributesCount: uint32(event.DroppedAttributeCount),
		})
	}
	return out
}

func keyValuesToProto(attributes []attribute.KeyValue) []*commonpb.KeyValue {
	if len(attributes) == 0 {
		return nil
	}
	out := make([]*commonpb.KeyValue, 0, len(attributes))
	for _, kv := range attributes {
		out = append(out, &commonpb.KeyValue{Key: string(kv.Key), Value: anyValueFromAttributeValue(kv.Value)})
	}
	return out
}

func attrsFromValueMap(attributes map[string]attribute.Value) []*commonpb.KeyValue {
	if len(attributes) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]*commonpb.KeyValue, 0, len(keys))
	for _, key := range keys {
		out = append(out, &commonpb.KeyValue{Key: key, Value: anyValueFromAttributeValue(attributes[key])})
	}
	return out
}

func anyValueFromAttributeValue(value attribute.Value) *commonpb.AnyValue {
	switch value.Type() {
	case attribute.BOOL:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: value.AsBool()}}
	case attribute.INT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value.AsInt64()}}
	case attribute.FLOAT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: value.AsFloat64()}}
	case attribute.STRING:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value.AsString()}}
	case attribute.BOOLSLICE:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: anyArrayFromBools(value.AsBoolSlice())}}
	case attribute.INT64SLICE:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: anyArrayFromInt64s(value.AsInt64Slice())}}
	case attribute.FLOAT64SLICE:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: anyArrayFromFloats(value.AsFloat64Slice())}}
	case attribute.STRINGSLICE:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: anyArrayFromStrings(value.AsStringSlice())}}
	default:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value.Emit()}}
	}
}

func anyArrayFromBools(values []bool) *commonpb.ArrayValue {
	out := &commonpb.ArrayValue{Values: make([]*commonpb.AnyValue, 0, len(values))}
	for _, value := range values {
		out.Values = append(out.Values, &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: value}})
	}
	return out
}

func anyArrayFromInt64s(values []int64) *commonpb.ArrayValue {
	out := &commonpb.ArrayValue{Values: make([]*commonpb.AnyValue, 0, len(values))}
	for _, value := range values {
		out.Values = append(out.Values, &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value}})
	}
	return out
}

func anyArrayFromFloats(values []float64) *commonpb.ArrayValue {
	out := &commonpb.ArrayValue{Values: make([]*commonpb.AnyValue, 0, len(values))}
	for _, value := range values {
		out.Values = append(out.Values, &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: value}})
	}
	return out
}

func anyArrayFromStrings(values []string) *commonpb.ArrayValue {
	out := &commonpb.ArrayValue{Values: make([]*commonpb.AnyValue, 0, len(values))}
	for _, value := range values {
		out.Values = append(out.Values, &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}})
	}
	return out
}

func spanKindToProto(kind oteltrace.SpanKind) tracepb.Span_SpanKind {
	switch kind {
	case oteltrace.SpanKindServer:
		return tracepb.Span_SPAN_KIND_SERVER
	case oteltrace.SpanKindClient:
		return tracepb.Span_SPAN_KIND_CLIENT
	case oteltrace.SpanKindProducer:
		return tracepb.Span_SPAN_KIND_PRODUCER
	case oteltrace.SpanKindConsumer:
		return tracepb.Span_SPAN_KIND_CONSUMER
	case oteltrace.SpanKindInternal:
		return tracepb.Span_SPAN_KIND_INTERNAL
	default:
		return tracepb.Span_SPAN_KIND_UNSPECIFIED
	}
}

func statusCodeToProto(code codes.Code) tracepb.Status_StatusCode {
	switch code {
	case codes.Ok:
		return tracepb.Status_STATUS_CODE_OK
	case codes.Error:
		return tracepb.Status_STATUS_CODE_ERROR
	default:
		return tracepb.Status_STATUS_CODE_UNSET
	}
}

func resourceAttributesKey(attributes map[string]attribute.Value) string {
	if len(attributes) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := attributes[key]
		parts = append(parts, key+"="+value.Emit()+"|"+value.Type().String())
	}
	return strings.Join(parts, ";")
}

func spanIDBytes(spanID oteltrace.SpanID) []byte {
	if !spanID.IsValid() {
		return nil
	}
	return append([]byte(nil), spanID[:]...)
}

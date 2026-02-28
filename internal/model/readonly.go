package model

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func FromReadOnlySpans(spans []sdktrace.ReadOnlySpan) []Span {
	if len(spans) == 0 {
		return nil
	}
	out := make([]Span, 0, len(spans))
	for _, span := range spans {
		if span == nil {
			continue
		}
		parentSpanID := oteltrace.SpanID{}
		if parent := span.Parent(); parent.IsValid() {
			parentSpanID = parent.SpanID()
		}

		resourceAttributes := map[string]attribute.Value{}
		if res := span.Resource(); res != nil {
			resourceAttributes = AttributesToMap(res.Attributes())
		}

		status := span.Status()
		out = append(out, Span{
			TraceID:            span.SpanContext().TraceID(),
			SpanID:             span.SpanContext().SpanID(),
			ParentSpanID:       parentSpanID,
			Name:               span.Name(),
			Kind:               span.SpanKind(),
			StartTime:          span.StartTime(),
			EndTime:            span.EndTime(),
			Attributes:         AttributesToMap(span.Attributes()),
			ResourceAttributes: resourceAttributes,
			Links:              cloneLinks(span.Links()),
			Events:             cloneEvents(span.Events()),
			StatusCode:         status.Code,
			StatusDescription:  status.Description,
		})
	}
	return out
}

func toReadOnlySpans(ctx context.Context, spans []Span) ([]sdktrace.ReadOnlySpan, error) {
	if len(spans) == 0 {
		return nil, nil
	}

	collector := &spanCollector{}
	providers := map[string]*sdktrace.TracerProvider{}
	tracers := map[string]oteltrace.Tracer{}

	shutdownAll := func() error {
		var firstErr error
		for _, provider := range providers {
			if provider == nil {
				continue
			}
			if err := provider.Shutdown(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	ordered := orderForRebuild(spans)
	createdSpanContexts := make(map[oteltrace.SpanID]oteltrace.SpanContext, len(ordered))

	for _, span := range ordered {
		resourceKey := buildResourceKey(span.ResourceAttributes)
		tracer, err := tracerForResource(ctx, resourceKey, span.ResourceAttributes, collector, providers, tracers)
		if err != nil {
			_ = shutdownAll()
			return nil, err
		}

		parentCtx := parentContextForSpan(span, createdSpanContexts)
		startOptions := []oteltrace.SpanStartOption{
			oteltrace.WithSpanKind(span.Kind),
			oteltrace.WithTimestamp(span.StartTime),
		}
		_, otSpan := tracer.Start(parentCtx, span.Name, startOptions...)

		if attributes := AttributesFromMap(span.Attributes); len(attributes) > 0 {
			otSpan.SetAttributes(attributes...)
		}
		for _, link := range span.Links {
			otSpan.AddLink(oteltrace.Link{SpanContext: link.SpanContext, Attributes: link.Attributes})
		}
		for _, event := range span.Events {
			otSpan.AddEvent(event.Name, oteltrace.WithTimestamp(event.Time), oteltrace.WithAttributes(event.Attributes...))
		}
		if span.StatusCode != codes.Unset || span.StatusDescription != "" {
			otSpan.SetStatus(span.StatusCode, span.StatusDescription)
		}

		otSpan.End(oteltrace.WithTimestamp(span.EndTime))
		if span.SpanID.IsValid() {
			createdSpanContexts[span.SpanID] = otSpan.SpanContext()
		}
	}

	if err := shutdownAll(); err != nil {
		return nil, err
	}
	return collector.spans, nil
}

type spanCollector struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (c *spanCollector) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spans = append(c.spans, spans...)
	return nil
}

func (c *spanCollector) Shutdown(_ context.Context) error {
	return nil
}

func tracerForResource(
	ctx context.Context,
	resourceKey string,
	resourceAttributes map[string]attribute.Value,
	collector sdktrace.SpanExporter,
	providers map[string]*sdktrace.TracerProvider,
	tracers map[string]oteltrace.Tracer,
) (oteltrace.Tracer, error) {
	if tracer, ok := tracers[resourceKey]; ok {
		return tracer, nil
	}

	res, err := resource.New(ctx, resource.WithAttributes(AttributesFromMap(resourceAttributes)...))
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(collector)),
		sdktrace.WithResource(res),
	)
	tracer := provider.Tracer("tercios/model")
	providers[resourceKey] = provider
	tracers[resourceKey] = tracer
	return tracer, nil
}

func parentContextForSpan(span Span, created map[oteltrace.SpanID]oteltrace.SpanContext) context.Context {
	if span.ParentSpanID.IsValid() {
		if parent, ok := created[span.ParentSpanID]; ok {
			return oteltrace.ContextWithSpanContext(context.Background(), parent)
		}
		return oteltrace.ContextWithSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
			TraceID:    span.TraceID,
			SpanID:     span.ParentSpanID,
			TraceFlags: oteltrace.FlagsSampled,
			Remote:     true,
		}))
	}
	if span.TraceID.IsValid() {
		return oteltrace.ContextWithSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
			TraceID:    span.TraceID,
			TraceFlags: oteltrace.FlagsSampled,
		}))
	}
	return context.Background()
}

func buildResourceKey(attributes map[string]attribute.Value) string {
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

func orderForRebuild(spans []Span) []Span {
	ordered := make([]Span, len(spans))
	copy(ordered, spans)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartTime.Equal(ordered[j].StartTime) {
			return ordered[i].SpanID.String() < ordered[j].SpanID.String()
		}
		return ordered[i].StartTime.Before(ordered[j].StartTime)
	})

	exists := make(map[oteltrace.SpanID]struct{}, len(ordered))
	for _, span := range ordered {
		if span.SpanID.IsValid() {
			exists[span.SpanID] = struct{}{}
		}
	}

	added := make(map[oteltrace.SpanID]bool, len(ordered))
	result := make([]Span, 0, len(ordered))

	for len(result) < len(ordered) {
		progress := false
		for _, span := range ordered {
			if span.SpanID.IsValid() && added[span.SpanID] {
				continue
			}
			parentUnknown := false
			parentAdded := true
			if span.ParentSpanID.IsValid() {
				_, parentExists := exists[span.ParentSpanID]
				parentUnknown = !parentExists
				parentAdded = added[span.ParentSpanID]
			}
			if !parentUnknown && span.ParentSpanID.IsValid() && !parentAdded {
				continue
			}

			result = append(result, span)
			if span.SpanID.IsValid() {
				added[span.SpanID] = true
			}
			progress = true
		}
		if progress {
			continue
		}
		for _, span := range ordered {
			if !span.SpanID.IsValid() || !added[span.SpanID] {
				result = append(result, span)
				if span.SpanID.IsValid() {
					added[span.SpanID] = true
				}
			}
		}
	}
	return result
}

func cloneLinks(links []sdktrace.Link) []sdktrace.Link {
	if len(links) == 0 {
		return nil
	}
	out := make([]sdktrace.Link, len(links))
	for i := range links {
		out[i] = links[i]
		if len(links[i].Attributes) > 0 {
			out[i].Attributes = append([]attribute.KeyValue{}, links[i].Attributes...)
		}
	}
	return out
}

func cloneEvents(events []sdktrace.Event) []sdktrace.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]sdktrace.Event, len(events))
	for i := range events {
		out[i] = events[i]
		if len(events[i].Attributes) > 0 {
			out[i].Attributes = append([]attribute.KeyValue{}, events[i].Attributes...)
		}
	}
	return out
}

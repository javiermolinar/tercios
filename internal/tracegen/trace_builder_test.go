package tracegen

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestTraceBuilderPrintsTree(t *testing.T) {
	ctx := context.Background()
	collector := &batchCollector{}
	processor := sdktrace.NewSimpleSpanProcessor(collector)
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
	tracer := provider.Tracer("tercios/tracegen/test")

	traceID := oteltrace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	rootCtx := oteltrace.ContextWithSpanContext(ctx, oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		TraceFlags: oteltrace.FlagsSampled,
	}))

	base := time.Date(2026, time.January, 27, 12, 0, 0, 0, time.UTC)
	builder := NewTraceBuilder(tracer, rootCtx)

	root := builder.AddSpan(SpanSpec{
		Name:       "root",
		Kind:       oteltrace.SpanKindInternal,
		Start:      base,
		End:        base.Add(10 * time.Second),
		Attributes: serviceAttributes("root-service"),
	})

	childA := root.AddChildSpan(SpanSpec{
		Name:       "child-a",
		Kind:       oteltrace.SpanKindClient,
		Start:      base.Add(1 * time.Second),
		End:        base.Add(4 * time.Second),
		Attributes: serviceAttributes("service-a"),
	})

	_ = childA.AddChildSpan(SpanSpec{
		Name:       "grandchild-a1",
		Kind:       oteltrace.SpanKindServer,
		Start:      base.Add(2 * time.Second),
		End:        base.Add(3 * time.Second),
		Attributes: serviceAttributes("service-a1"),
	})

	childB := root.AddChildSpan(SpanSpec{
		Name:       "child-b",
		Kind:       oteltrace.SpanKindProducer,
		Start:      base.Add(5 * time.Second),
		End:        base.Add(8 * time.Second),
		Attributes: serviceAttributes("service-b"),
	})

	childC := root.AddChildSpan(SpanSpec{
		Name:       "child-c",
		Kind:       oteltrace.SpanKindConsumer,
		Start:      base.Add(6 * time.Second),
		End:        base.Add(9 * time.Second),
		Attributes: serviceAttributes("service-c"),
	})

	childA.AddChild(childC)
	_ = childB

	builder.EndAll()
	if err := provider.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	if len(collector.spans) == 0 {
		t.Fatalf("expected spans, got none")
	}

	logSpanTree(t, collector.spans)
}

type spanView struct {
	span   sdktrace.ReadOnlySpan
	id     oteltrace.SpanID
	parent oteltrace.SpanID
	links  []oteltrace.SpanID
}

func logSpanTree(t *testing.T, spans []sdktrace.ReadOnlySpan) {
	views := make([]spanView, 0, len(spans))
	children := make(map[oteltrace.SpanID][]spanView)
	roots := make([]spanView, 0, len(spans))

	for _, span := range spans {
		view := spanView{
			span: span,
			id:   span.SpanContext().SpanID(),
		}
		parent := span.Parent()
		if parent.IsValid() {
			view.parent = parent.SpanID()
		}
		for _, link := range span.Links() {
			view.links = append(view.links, link.SpanContext.SpanID())
		}
		views = append(views, view)
	}

	sort.Slice(views, func(i, j int) bool {
		return views[i].span.StartTime().Before(views[j].span.StartTime())
	})

	for _, view := range views {
		if view.parent.IsValid() {
			children[view.parent] = append(children[view.parent], view)
		} else {
			roots = append(roots, view)
		}
	}

	for _, root := range roots {
		logSpanTreeLine(t, root, 0)
		logSpanTreeChildren(t, children, root, 1)
	}
}

func logSpanTreeChildren(t *testing.T, children map[oteltrace.SpanID][]spanView, parent spanView, indent int) {
	kids := children[parent.id]
	sort.Slice(kids, func(i, j int) bool {
		return kids[i].span.StartTime().Before(kids[j].span.StartTime())
	})
	for _, child := range kids {
		logSpanTreeLine(t, child, indent)
		logSpanTreeChildren(t, children, child, indent+1)
	}
}

func logSpanTreeLine(t *testing.T, view spanView, indent int) {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}
	start := view.span.StartTime().Format("15:04:05.000")
	end := view.span.EndTime().Format("15:04:05.000")
	links := ""
	if len(view.links) > 0 {
		links = " links=" + spanIDs(view.links)
	}
	t.Logf("%s%s [%s - %s] kind=%s%s", prefix, view.span.Name(), start, end, view.span.SpanKind(), links)
}

func spanIDs(ids []oteltrace.SpanID) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, id.String())
	}
	sort.Strings(parts)
	return "[" + strings.Join(parts, ",") + "]"
}

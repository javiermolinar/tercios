package tracegen

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.17.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Generator struct {
	ServiceName string
	SpanName    string
	Services    int
	MaxDepth    int
	MaxSpans    int
}

type batchCollector struct {
	spans []sdktrace.ReadOnlySpan
}

func (c *batchCollector) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	c.spans = append(c.spans, spans...)
	return nil
}

func (c *batchCollector) Shutdown(_ context.Context) error {
	return nil
}

func (g Generator) GenerateBatch(ctx context.Context) ([]sdktrace.ReadOnlySpan, error) {
	rng := rand.Reader
	serviceNames := buildServiceNames(g.Services, rng, g.ServiceName)
	if g.ServiceName == "" {
		if len(serviceNames) > 0 {
			g.ServiceName = serviceNames[0]
		} else {
			g.ServiceName = randomLabel(rng, "service")
		}
	}
	if g.SpanName == "" {
		g.SpanName = randomLabel(rng, "span")
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(g.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}
	collector := &batchCollector{}
	processor := sdktrace.NewSimpleSpanProcessor(collector)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(processor),
		sdktrace.WithResource(res),
	)
	tracer := provider.Tracer("tercios/tracegen")

	if err := g.emitTrace(ctx, tracer, serviceNames, rng); err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}
	if err := provider.Shutdown(ctx); err != nil {
		return nil, fmt.Errorf("shutdown tracer provider: %w", err)
	}
	return collector.spans, nil
}

type spanNode struct {
	ctx   context.Context
	span  oteltrace.Span
	depth int
	start time.Time
	end   time.Time
}

func (g Generator) emitTrace(ctx context.Context, tracer oteltrace.Tracer, serviceNames []string, rng io.Reader) error {
	spanCount, err := randomSpanCount(rng, g.MaxSpans)
	if err != nil {
		return fmt.Errorf("random span count: %w", err)
	}
	if spanCount < 1 {
		spanCount = 1
	}
	traceID, err := randomTraceID(rng)
	if err != nil {
		return fmt.Errorf("random trace id: %w", err)
	}
	rootCtx := oteltrace.ContextWithSpanContext(ctx, oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		TraceFlags: oteltrace.FlagsSampled,
	}))
	traceEnd := time.Now()
	rootDuration, err := randomSpanDuration(rng)
	if err != nil {
		return fmt.Errorf("random span duration: %w", err)
	}
	rootStart := traceEnd.Add(-rootDuration)
	rootCtx, rootSpan := tracer.Start(
		rootCtx,
		g.spanName(serviceNames, rng),
		oteltrace.WithSpanKind(randomSpanKind(rng)),
		oteltrace.WithTimestamp(rootStart),
	)
	setServiceAttributes(rootSpan, g.serviceName(serviceNames, rng))
	nodes := []spanNode{{ctx: rootCtx, span: rootSpan, depth: 1, start: rootStart, end: traceEnd}}

	spansRemaining := spanCount - 1
	for spansRemaining > 0 {
		parentIndex := pickParentIndex(nodes, g.MaxDepth, rng)
		parent := nodes[parentIndex]
		if parent.depth >= g.MaxDepth {
			break
		}

		choice, err := randomIndex(rng, 3)
		if err != nil {
			return fmt.Errorf("random edge choice: %w", err)
		}

		switch choice {
		case 0:
			// Client -> Server pair.
			if spansRemaining < 2 {
				continue
			}
			clientNode, serverNode, err := g.emitPairedSpan(tracer, parent, serviceNames, rng, oteltrace.SpanKindClient, oteltrace.SpanKindServer)
			if err != nil {
				return err
			}
			nodes = append(nodes, clientNode, serverNode)
			spansRemaining -= 2
		case 1:
			// Producer -> Consumer pair.
			if spansRemaining < 2 {
				continue
			}
			producerNode, consumerNode, err := g.emitPairedSpan(tracer, parent, serviceNames, rng, oteltrace.SpanKindProducer, oteltrace.SpanKindConsumer)
			if err != nil {
				return err
			}
			nodes = append(nodes, producerNode, consumerNode)
			spansRemaining -= 2
		default:
			// DB request: client span with db attributes.
			childNode, err := g.emitChildSpan(tracer, parent, serviceNames, rng, oteltrace.SpanKindClient, addDBAttributes)
			if err != nil {
				return err
			}
			nodes = append(nodes, childNode)
			spansRemaining--
		}
	}

	for i := len(nodes) - 1; i >= 0; i-- {
		nodes[i].span.End(oteltrace.WithTimestamp(nodes[i].end))
	}
	return nil
}

func pickParentIndex(nodes []spanNode, maxDepth int, rng io.Reader) int {
	if maxDepth <= 1 {
		return 0
	}
	for attempts := 0; attempts < 10; attempts++ {
		index, err := randomIndex(rng, len(nodes))
		if err != nil {
			break
		}
		if nodes[index].depth < maxDepth {
			return index
		}
	}
	for i, node := range nodes {
		if node.depth < maxDepth {
			return i
		}
	}
	return 0
}

func (g Generator) spanName(serviceNames []string, rng io.Reader) string {
	service := g.serviceName(serviceNames, rng)
	if g.SpanName == "" {
		return service
	}
	return fmt.Sprintf("%s:%s", service, g.SpanName)
}

func (g Generator) serviceName(serviceNames []string, rng io.Reader) string {
	if len(serviceNames) == 0 {
		return g.ServiceName
	}
	index, err := randomIndex(rng, len(serviceNames))
	if err != nil {
		return serviceNames[0]
	}
	return serviceNames[index]
}

func setServiceAttributes(span oteltrace.Span, serviceName string) {
	span.SetAttributes(
		semconv.ServiceNameKey.String(serviceName),
		attribute.String("service.name", serviceName),
	)
}

func randomSpanKind(rng io.Reader) oteltrace.SpanKind {
	kinds := []oteltrace.SpanKind{
		oteltrace.SpanKindClient,
		oteltrace.SpanKindServer,
		oteltrace.SpanKindProducer,
		oteltrace.SpanKindConsumer,
		oteltrace.SpanKindInternal,
	}
	index, err := randomIndex(rng, len(kinds))
	if err != nil {
		return oteltrace.SpanKindInternal
	}
	return kinds[index]
}

func randomIndex(rng io.Reader, max int) (int, error) {
	if max <= 0 {
		return 0, nil
	}
	n, err := rand.Int(rng, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()), nil
}

func randomLabel(rng io.Reader, prefix string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	const length = 8
	buf := make([]byte, length)
	for i := 0; i < length; i++ {
		idx, err := randomIndex(rng, len(alphabet))
		if err != nil {
			buf[i] = alphabet[0]
			continue
		}
		buf[i] = alphabet[idx]
	}
	return fmt.Sprintf("%s-%s", prefix, string(buf))
}

func randomSpanCount(rng io.Reader, max int) (int, error) {
	if max <= 1 {
		return 1, nil
	}
	n, err := rand.Int(rng, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()) + 1, nil
}

func randomSpanDuration(rng io.Reader) (time.Duration, error) {
	buckets := []struct {
		min time.Duration
		max time.Duration
	}{
		{min: 1 * time.Millisecond, max: 80 * time.Millisecond},
		{min: 80 * time.Millisecond, max: 900 * time.Millisecond},
		{min: 900 * time.Millisecond, max: 8 * time.Second},
		{min: 8 * time.Second, max: 2 * time.Minute},
	}
	bucketIndex, err := randomIndex(rng, len(buckets))
	if err != nil {
		return 0, err
	}
	bucket := buckets[bucketIndex]
	return randomDurationRange(rng, bucket.min, bucket.max)
}

func randomDurationRange(rng io.Reader, min, max time.Duration) (time.Duration, error) {
	if max <= min {
		return min, nil
	}
	delta := max - min
	n, err := rand.Int(rng, big.NewInt(delta.Nanoseconds()+1))
	if err != nil {
		return 0, err
	}
	return min + time.Duration(n.Int64()), nil
}

func randomChildWindow(rng io.Reader, parentStart, parentEnd time.Time, duration time.Duration) (time.Time, time.Time, error) {
	if duration <= 0 {
		return parentStart, parentStart, nil
	}
	if parentEnd.Before(parentStart) {
		return parentStart, parentEnd, nil
	}
	latestStart := parentEnd.Add(-duration)
	if latestStart.Before(parentStart) {
		latestStart = parentStart
	}
	offsetRange := latestStart.Sub(parentStart)
	offset, err := randomDurationRange(rng, 0, offsetRange)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start := parentStart.Add(offset)
	return start, start.Add(duration), nil
}

func (g Generator) emitChildSpan(
	tracer oteltrace.Tracer,
	parent spanNode,
	serviceNames []string,
	rng io.Reader,
	kind oteltrace.SpanKind,
	attrsFn func(span oteltrace.Span),
) (spanNode, error) {
	childDuration, err := randomSpanDuration(rng)
	if err != nil {
		return spanNode{}, fmt.Errorf("random span duration: %w", err)
	}
	parentWindow := parent.end.Sub(parent.start)
	if childDuration > parentWindow {
		childDuration = parentWindow
	}
	childStart, childEnd, err := randomChildWindow(rng, parent.start, parent.end, childDuration)
	if err != nil {
		return spanNode{}, fmt.Errorf("random child window: %w", err)
	}
	childCtx, childSpan := tracer.Start(
		parent.ctx,
		g.spanName(serviceNames, rng),
		oteltrace.WithSpanKind(kind),
		oteltrace.WithTimestamp(childStart),
	)
	setServiceAttributes(childSpan, g.serviceName(serviceNames, rng))
	if attrsFn != nil {
		attrsFn(childSpan)
	}
	return spanNode{ctx: childCtx, span: childSpan, depth: parent.depth + 1, start: childStart, end: childEnd}, nil
}

func (g Generator) emitPairedSpan(
	tracer oteltrace.Tracer,
	parent spanNode,
	serviceNames []string,
	rng io.Reader,
	parentKind oteltrace.SpanKind,
	childKind oteltrace.SpanKind,
) (spanNode, spanNode, error) {
	firstNode, err := g.emitChildSpan(tracer, parent, serviceNames, rng, parentKind, nil)
	if err != nil {
		return spanNode{}, spanNode{}, err
	}
	secondNode, err := g.emitChildSpan(tracer, firstNode, serviceNames, rng, childKind, nil)
	if err != nil {
		return spanNode{}, spanNode{}, err
	}
	return firstNode, secondNode, nil
}

func addDBAttributes(span oteltrace.Span) {
	systems := []string{"postgresql", "mysql", "redis", "mongodb"}
	idx := time.Now().UnixNano() % int64(len(systems))
	span.SetAttributes(
		attribute.String("db.system", systems[idx]),
		attribute.String("db.name", "example"),
	)
}

func randomTraceID(rng io.Reader) (oteltrace.TraceID, error) {
	var id oteltrace.TraceID
	if _, err := rand.Read(id[:]); err != nil {
		return oteltrace.TraceID{}, err
	}
	return id, nil
}

func buildServiceNames(count int, rng io.Reader, baseName string) []string {
	if count <= 0 {
		return nil
	}
	if baseName != "" {
		if count == 1 {
			return []string{baseName}
		}
		names := make([]string, 0, count)
		for i := 1; i <= count; i++ {
			names = append(names, fmt.Sprintf("%s-%d", baseName, i))
		}
		return names
	}
	fruit := []string{
		"apple",
		"apricot",
		"banana",
		"blackberry",
		"blueberry",
		"cherry",
		"coconut",
		"fig",
		"grape",
		"kiwi",
		"lemon",
		"lime",
		"mango",
		"melon",
		"nectarine",
		"orange",
		"papaya",
		"peach",
		"pear",
		"pineapple",
		"plum",
		"pomegranate",
		"raspberry",
		"strawberry",
		"watermelon",
	}
	for i := len(fruit) - 1; i > 0; i-- {
		j, err := randomIndex(rng, i+1)
		if err != nil {
			break
		}
		fruit[i], fruit[j] = fruit[j], fruit[i]
	}
	if count <= len(fruit) {
		return fruit[:count]
	}
	names := append([]string{}, fruit...)
	for i := len(fruit) + 1; i <= count; i++ {
		names = append(names, fmt.Sprintf("fruit-%d", i))
	}
	return names
}

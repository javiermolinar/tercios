package tracegen

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type SpanSpec struct {
	Name       string
	Kind       oteltrace.SpanKind
	Start      time.Time
	End        time.Time
	Attributes []attribute.KeyValue
}

type TraceBuilder struct {
	tracer  oteltrace.Tracer
	rootCtx context.Context
	nodes   []*SpanBuilder
}

type SpanBuilder struct {
	builder  *TraceBuilder
	ctx      context.Context
	span     oteltrace.Span
	start    time.Time
	end      time.Time
	depth    int
	parents  []*SpanBuilder
	children []*SpanBuilder
}

func NewTraceBuilder(tracer oteltrace.Tracer, rootCtx context.Context) *TraceBuilder {
	return &TraceBuilder{
		tracer:  tracer,
		rootCtx: rootCtx,
	}
}

func (b *TraceBuilder) AddSpan(spec SpanSpec) *SpanBuilder {
	return b.newSpan(spec, nil)
}

func (b *TraceBuilder) EndAll() {
	for i := len(b.nodes) - 1; i >= 0; i-- {
		node := b.nodes[i]
		node.span.End(oteltrace.WithTimestamp(node.end))
	}
}

func (b *TraceBuilder) newSpan(spec SpanSpec, parent *SpanBuilder) *SpanBuilder {
	ctx := b.rootCtx
	if parent != nil {
		ctx = parent.ctx
	}
	ctx, span := b.tracer.Start(
		ctx,
		spec.Name,
		oteltrace.WithSpanKind(spec.Kind),
		oteltrace.WithTimestamp(spec.Start),
	)
	if len(spec.Attributes) > 0 {
		span.SetAttributes(spec.Attributes...)
	}

	node := &SpanBuilder{
		builder: b,
		ctx:     ctx,
		span:    span,
		start:   spec.Start,
		end:     spec.End,
		depth:   1,
	}
	b.nodes = append(b.nodes, node)

	if parent != nil {
		parent.addChild(node)
	}
	return node
}

func (s *SpanBuilder) AddChildSpan(spec SpanSpec) *SpanBuilder {
	if s == nil {
		return nil
	}
	return s.builder.newSpan(spec, s)
}

func (s *SpanBuilder) AddChild(child *SpanBuilder) *SpanBuilder {
	if s == nil || child == nil {
		return child
	}
	if s.builder != child.builder {
		return child
	}
	if child == s || s.hasAncestor(child) || s.hasChild(child) {
		return child
	}

	s.addChild(child)
	child.span.AddLink(oteltrace.Link{SpanContext: s.span.SpanContext()})
	newDepth := s.depth + 1
	if newDepth > child.depth {
		child.bumpDepth(newDepth)
	}
	return child
}

func (s *SpanBuilder) addChild(child *SpanBuilder) {
	if child == nil || s == nil {
		return
	}
	s.children = append(s.children, child)
	child.parents = append(child.parents, s)
	child.depth = s.depth + 1
}

func (s *SpanBuilder) hasChild(child *SpanBuilder) bool {
	for _, existing := range s.children {
		if existing == child {
			return true
		}
	}
	return false
}

func (s *SpanBuilder) hasAncestor(target *SpanBuilder) bool {
	if s == nil || target == nil {
		return false
	}
	for _, parent := range s.parents {
		if parent == target || parent.hasAncestor(target) {
			return true
		}
	}
	return false
}

func (s *SpanBuilder) bumpDepth(depth int) {
	if depth <= s.depth {
		return
	}
	s.depth = depth
	for _, child := range s.children {
		child.bumpDepth(depth + 1)
	}
}

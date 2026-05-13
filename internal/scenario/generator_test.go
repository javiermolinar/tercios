package scenario

import (
	"context"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestGeneratorEmitsExpectedShape(t *testing.T) {
	definition := testDefinition(t)
	generator := NewGenerator(definition)

	spans, err := generator.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch() error = %v", err)
	}

	// root + (a->b)x2 where each repeat emits client+server + (b->c)x1 emits client+server
	// = 1 + 2*(2+2) = 9
	if len(spans) != 9 {
		t.Fatalf("expected 9 spans, got %d", len(spans))
	}

	if spans[0].Kind != oteltrace.SpanKindInternal {
		t.Fatalf("expected root internal span, got %s", spans[0].Kind)
	}

	foundDBServer := false
	for _, span := range spans {
		if span.EndTime.Sub(span.StartTime) <= 0 {
			t.Fatalf("expected positive span duration, got %s", span.EndTime.Sub(span.StartTime))
		}
		if got := span.ResourceAttributes["service.name"]; got.Type() == attribute.STRING && got.AsString() == "postgres" {
			if span.Kind == oteltrace.SpanKindServer {
				foundDBServer = true
			}
		}
	}
	if !foundDBServer {
		t.Fatalf("expected at least one database server span")
	}
}

func TestGeneratorDeterministicTraceIDForFirstBatch(t *testing.T) {
	definition := testDefinition(t)

	g1 := NewGenerator(definition)
	g2 := NewGenerator(definition)

	batch1, err := g1.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("first generator GenerateBatch() error = %v", err)
	}
	batch2, err := g2.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("second generator GenerateBatch() error = %v", err)
	}

	if len(batch1) == 0 || len(batch2) == 0 {
		t.Fatalf("expected non-empty batches")
	}
	if batch1[0].TraceID != batch2[0].TraceID {
		t.Fatalf("expected deterministic first trace ID, got %s vs %s", batch1[0].TraceID, batch2[0].TraceID)
	}
}

func testDefinition(t testing.TB) Definition {
	t.Helper()
	cfg := Config{
		Name: "test",
		Seed: 42,
		Services: map[string]ServiceConfig{
			"frontend": {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "frontend"}}},
			"post":     {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "post-service"}}},
			"db":       {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "postgres"}}},
		},
		Nodes: map[string]NodeConfig{
			"a": {Service: "frontend", SpanName: "GET /posts"},
			"b": {Service: "post", SpanName: "POST /posts"},
			"c": {Service: "db", SpanName: "SELECT posts"},
		},
		Root: "a",
		Edges: []EdgeConfig{
			{From: "a", To: "b", Kind: EdgeKindClientServer, Repeat: 2, DurationMs: 10},
			{From: "b", To: "c", Kind: EdgeKindClientDatabase, Repeat: 1, DurationMs: 5},
		},
	}
	definition, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return definition
}

func TestGeneratorEmitsEventsAndLinks(t *testing.T) {
	cfg := Config{
		Name: "events-links",
		Seed: 42,
		Services: map[string]ServiceConfig{
			"svc": {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "svc"}}},
		},
		Nodes: map[string]NodeConfig{
			"a": {Service: "svc", SpanName: "A"},
			"b": {Service: "svc", SpanName: "B"},
		},
		Root: "a",
		Edges: []EdgeConfig{
			{
				From: "a", To: "b", Kind: EdgeKindInternal, Repeat: 1, DurationMs: 10,
				SpanEvents: []EventConfig{
					{Name: "cache.miss", Attributes: map[string]TypedValue{
						"cache.key": {Type: ValueTypeString, Value: "items:list"},
					}},
				},
				SpanLinks: []LinkConfig{
					{Node: "a", Attributes: map[string]TypedValue{
						"link.type": {Type: ValueTypeString, Value: "follows_from"},
					}},
				},
			},
		},
	}
	definition, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	generator := NewGenerator(definition)

	spans, err := generator.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch() error = %v", err)
	}

	// Find the span with events (the internal span from edge a->b)
	foundEvent := false
	foundLink := false
	for _, span := range spans {
		for _, event := range span.Events {
			if event.Name == "cache.miss" {
				foundEvent = true
				if len(event.Attributes) == 0 {
					t.Fatalf("expected event attributes")
				}
				if event.Time.Before(span.StartTime) || event.Time.After(span.EndTime) {
					t.Fatalf("expected event time inside span duration, got event=%s start=%s end=%s", event.Time, span.StartTime, span.EndTime)
				}
			}
		}
		for _, link := range span.Links {
			if link.SpanContext.IsValid() {
				foundLink = true
				if len(link.Attributes) == 0 {
					t.Fatalf("expected link attributes")
				}
			}
		}
	}
	if !foundEvent {
		t.Fatalf("expected at least one span with cache.miss event")
	}
	if !foundLink {
		t.Fatalf("expected at least one span with a link to node a")
	}
}

// TestGeneratorParentContainsChild asserts that every emitted span's
// [StartTime, EndTime] interval is fully contained within its parent's
// interval, matching real OTel SDK semantics. This is the property that
// made the iterative-walker rewrite worthwhile: a backend rendering this
// trace will draw children temporally inside their parents instead of
// after them.
func TestGeneratorParentContainsChild(t *testing.T) {
	definition := testDefinition(t)
	g := NewGenerator(definition)
	spans, err := g.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch: %v", err)
	}

	byID := make(map[oteltrace.SpanID]model.Span, len(spans))
	for _, s := range spans {
		byID[s.SpanID] = s
	}

	for _, span := range spans {
		if !span.ParentSpanID.IsValid() {
			continue // root has no parent to contain it
		}
		parent, ok := byID[span.ParentSpanID]
		if !ok {
			t.Fatalf("span %s parent %s not in output", span.SpanID, span.ParentSpanID)
		}
		if span.StartTime.Before(parent.StartTime) {
			t.Fatalf("span %s starts %s before parent %s (parent starts %s)", span.SpanID, span.StartTime, parent.SpanID, parent.StartTime)
		}
		if span.EndTime.After(parent.EndTime) {
			t.Fatalf("span %s ends %s after parent %s (parent ends %s)", span.SpanID, span.EndTime, parent.SpanID, parent.EndTime)
		}
	}
}

// TestGeneratorRootCoversWholeTrace asserts that the root span spans the
// entire trace timeline: its StartTime is the earliest StartTime in the
// trace and its EndTime is the latest EndTime. Real traces show the root
// as the outermost bar in any timeline view; we must not produce a trace
// where some descendant outlasts the root.
func TestGeneratorRootCoversWholeTrace(t *testing.T) {
	definition := testDefinition(t)
	g := NewGenerator(definition)
	spans, err := g.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch: %v", err)
	}

	var root *model.Span
	for i := range spans {
		if !spans[i].ParentSpanID.IsValid() {
			root = &spans[i]
			break
		}
	}
	if root == nil {
		t.Fatalf("no root span found")
	}

	for _, span := range spans {
		if span.StartTime.Before(root.StartTime) {
			t.Fatalf("span %s starts %s before root start %s", span.SpanID, span.StartTime, root.StartTime)
		}
		if span.EndTime.After(root.EndTime) {
			t.Fatalf("span %s ends %s after root end %s", span.SpanID, span.EndTime, root.EndTime)
		}
	}
}

// TestGeneratorSiblingsDoNotOverlap asserts that two spans sharing the
// same parent occupy disjoint intervals (sequential sibling semantics).
// Without this, a service appearing twice as a sibling under the same
// parent would look like a single overlapping pair of concurrent calls,
// which the model deliberately does not produce.
//
// Pair edges (ClientServer, ProducerConsumer, ClientDatabase) produce a
// source span and a target span sharing start/end — the target is parented
// at the source, so they don't count as siblings for this test.
func TestGeneratorSiblingsDoNotOverlap(t *testing.T) {
	definition := testDefinition(t)
	g := NewGenerator(definition)
	spans, err := g.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch: %v", err)
	}

	byParent := make(map[oteltrace.SpanID][]model.Span, len(spans))
	for _, s := range spans {
		if !s.ParentSpanID.IsValid() {
			continue
		}
		byParent[s.ParentSpanID] = append(byParent[s.ParentSpanID], s)
	}

	for parentID, siblings := range byParent {
		if len(siblings) < 2 {
			continue
		}
		for i := 0; i < len(siblings); i++ {
			for j := i + 1; j < len(siblings); j++ {
				a, b := siblings[i], siblings[j]
				// Two intervals overlap iff a.Start < b.End && b.Start < a.End.
				if a.StartTime.Before(b.EndTime) && b.StartTime.Before(a.EndTime) {
					t.Fatalf("siblings %s [%s, %s] and %s [%s, %s] under parent %s overlap", a.SpanID, a.StartTime, a.EndTime, b.SpanID, b.StartTime, b.EndTime, parentID)
				}
			}
		}
	}
}

// TestGeneratorChildStartsAfterParentStart asserts that every non-root
// span begins strictly after its parent's StartTime. Without this, a
// child appearing at exactly the same instant as its parent would be
// indistinguishable from the parent in trace timestamp ordering, which
// some backends use for sorting.
//
// Pair edges intentionally produce two spans sharing start/end (the
// target is parented at the source); those are excluded since the
// equality is by design.
func TestGeneratorChildStartsAfterParentStart(t *testing.T) {
	definition := testDefinition(t)
	g := NewGenerator(definition)
	spans, err := g.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch: %v", err)
	}

	byID := make(map[oteltrace.SpanID]model.Span, len(spans))
	for _, s := range spans {
		byID[s.SpanID] = s
	}

	for _, span := range spans {
		if !span.ParentSpanID.IsValid() {
			continue
		}
		parent, ok := byID[span.ParentSpanID]
		if !ok {
			continue
		}
		// Pair edges legitimately produce same-start spans.
		if span.StartTime.Equal(parent.StartTime) && span.EndTime.Equal(parent.EndTime) {
			continue
		}
		if !span.StartTime.After(parent.StartTime) {
			t.Fatalf("span %s starts %s but parent %s starts %s (expected strictly after)", span.SpanID, span.StartTime, parent.SpanID, parent.StartTime)
		}
	}
}

func TestEstimateDurationPositive(t *testing.T) {
	outgoing := map[string][]Edge{
		"a": {{From: "a", To: "b", Repeat: 2, Duration: 10 * time.Millisecond}},
		"b": {{From: "b", To: "c", Repeat: 1, Duration: 5 * time.Millisecond}},
	}
	d := estimateDuration("a", outgoing)
	if d <= 0 {
		t.Fatalf("expected positive estimate, got %s", d)
	}
}

// BenchmarkGenerateBatch measures the cost of producing one trace from the
// shared test definition (9 spans). It exercises the iterative walker,
// span materialization, and per-span allocations, but excludes any OTLP
// encoding or export. ReportAllocs is enabled so the heap pressure of the
// walker (stack frames, per-pop NextChildren slices, events/links) is
// visible alongside ns/op.
func BenchmarkGenerateBatch(b *testing.B) {
	definition := testDefinition(b)
	generator := NewGenerator(definition)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spans, err := generator.GenerateBatch(ctx)
		if err != nil {
			b.Fatalf("GenerateBatch: %v", err)
		}
		if len(spans) == 0 {
			b.Fatalf("GenerateBatch returned no spans")
		}
	}
}

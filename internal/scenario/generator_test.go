package scenario

import (
	"context"
	"testing"
	"time"

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

func testDefinition(t *testing.T) Definition {
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

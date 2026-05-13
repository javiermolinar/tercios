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

// TestPairEdgeNetworkLatencyInsetsTargetSpan verifies that a pair edge
// with NetworkLatency > 0 produces a target-side span whose interval is
// inset by latency on both sides of the source-side span's interval.
// This is the realistic shape backends expect: the server records
// receipt and response after request travel and before response travel,
// so its interval is strictly inside the client's.
func TestPairEdgeNetworkLatencyInsetsTargetSpan(t *testing.T) {
	cfg := Config{
		Name: "latency-inset",
		Seed: 42,
		Services: map[string]ServiceConfig{
			"client": {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "client"}}},
			"server": {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "server"}}},
		},
		Nodes: map[string]NodeConfig{
			"a": {Service: "client", SpanName: "caller"},
			"b": {Service: "server", SpanName: "callee"},
		},
		Root: "a",
		Edges: []EdgeConfig{
			{From: "a", To: "b", Kind: EdgeKindClientServer, Repeat: 1, DurationMs: 20, NetworkLatencyMs: 3},
		},
	}
	definition, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := NewGenerator(definition)
	spans, err := g.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch: %v", err)
	}

	var client, server model.Span
	for _, s := range spans {
		switch s.Kind {
		case oteltrace.SpanKindClient:
			client = s
		case oteltrace.SpanKindServer:
			server = s
		}
	}
	if client.SpanID == (oteltrace.SpanID{}) {
		t.Fatalf("client span not found")
	}
	if server.SpanID == (oteltrace.SpanID{}) {
		t.Fatalf("server span not found")
	}

	latency := 3 * time.Millisecond
	wantServerStart := client.StartTime.Add(latency)
	wantServerEnd := client.EndTime.Add(-latency)
	if !server.StartTime.Equal(wantServerStart) {
		t.Fatalf("server.start = %s, want client.start + %s = %s", server.StartTime, latency, wantServerStart)
	}
	if !server.EndTime.Equal(wantServerEnd) {
		t.Fatalf("server.end = %s, want client.end - %s = %s", server.EndTime, latency, wantServerEnd)
	}
	if !server.StartTime.After(client.StartTime) {
		t.Fatalf("server.start (%s) must be strictly after client.start (%s)", server.StartTime, client.StartTime)
	}
	if !server.EndTime.Before(client.EndTime) {
		t.Fatalf("server.end (%s) must be strictly before client.end (%s)", server.EndTime, client.EndTime)
	}
	if server.ParentSpanID != client.SpanID {
		t.Fatalf("server parent %s, want client.SpanID %s", server.ParentSpanID, client.SpanID)
	}
}

// TestPairEdgeZeroLatencyPreservesSharedInterval verifies that the
// default (NetworkLatencyMs == 0) keeps the pre-existing behavior where
// both spans of a pair share start_time and end_time. This guards
// against accidental regressions for scenarios that don't opt in.
func TestPairEdgeZeroLatencyPreservesSharedInterval(t *testing.T) {
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

	// For every server/consumer span whose parent is a client/producer
	// span (i.e., the pair-edge target side), intervals must match
	// exactly when the source scenario has no network_latency_ms.
	for _, s := range spans {
		isTarget := s.Kind == oteltrace.SpanKindServer || s.Kind == oteltrace.SpanKindConsumer
		if !isTarget {
			continue
		}
		parent, ok := byID[s.ParentSpanID]
		if !ok {
			continue
		}
		isPairSource := parent.Kind == oteltrace.SpanKindClient || parent.Kind == oteltrace.SpanKindProducer
		if !isPairSource {
			continue
		}
		if !s.StartTime.Equal(parent.StartTime) {
			t.Fatalf("target %s start %s != source %s start %s (expected equal at latency=0)", s.SpanID, s.StartTime, parent.SpanID, parent.StartTime)
		}
		if !s.EndTime.Equal(parent.EndTime) {
			t.Fatalf("target %s end %s != source %s end %s (expected equal at latency=0)", s.SpanID, s.EndTime, parent.SpanID, parent.EndTime)
		}
	}
}

// TestPairEdgeLatencyChildrenFitInsideTarget verifies the
// subtree-positioning invariant: when a pair edge with latency has a
// subtree under its target node, the children attach to the (narrower)
// target span and fit strictly inside its interval, not the client's.
func TestPairEdgeLatencyChildrenFitInsideTarget(t *testing.T) {
	cfg := Config{
		Name: "latency-subtree",
		Seed: 42,
		Services: map[string]ServiceConfig{
			"app": {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "app"}}},
			"api": {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "api"}}},
			"db":  {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "db"}}},
		},
		Nodes: map[string]NodeConfig{
			"app": {Service: "app", SpanName: "front"},
			"api": {Service: "api", SpanName: "backend"},
			"db":  {Service: "db", SpanName: "query"},
		},
		Root: "app",
		Edges: []EdgeConfig{
			{From: "app", To: "api", Kind: EdgeKindClientServer, Repeat: 1, DurationMs: 30, NetworkLatencyMs: 4},
			{From: "api", To: "db", Kind: EdgeKindClientDatabase, Repeat: 1, DurationMs: 10, NetworkLatencyMs: 2},
		},
	}
	definition, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := NewGenerator(definition)
	spans, err := g.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch: %v", err)
	}

	byID := make(map[oteltrace.SpanID]model.Span, len(spans))
	for _, s := range spans {
		byID[s.SpanID] = s
	}

	// Every non-root span must be temporally contained in its parent,
	// even with non-zero latencies at every pair edge.
	for _, s := range spans {
		if !s.ParentSpanID.IsValid() {
			continue
		}
		parent, ok := byID[s.ParentSpanID]
		if !ok {
			t.Fatalf("span %s parent %s not in output", s.SpanID, s.ParentSpanID)
		}
		if s.StartTime.Before(parent.StartTime) {
			t.Fatalf("span %s starts %s before parent %s (parent starts %s)", s.SpanID, s.StartTime, parent.SpanID, parent.StartTime)
		}
		if s.EndTime.After(parent.EndTime) {
			t.Fatalf("span %s ends %s after parent %s (parent ends %s)", s.SpanID, s.EndTime, parent.SpanID, parent.EndTime)
		}
	}

	// Specifically: the api server is the parent of the db client, and
	// the db client (with its own latency) must fit inside the api server.
	var apiServer, dbClient model.Span
	for _, s := range spans {
		switch s.Name {
		case "backend":
			if s.Kind == oteltrace.SpanKindServer {
				apiServer = s
			}
		case "front -> backend":
			// Skip the outer client span.
		default:
			if s.Kind == oteltrace.SpanKindClient && s.ParentSpanID == apiServer.SpanID {
				dbClient = s
			}
		}
	}
	if apiServer.SpanID == (oteltrace.SpanID{}) {
		t.Fatalf("api server span not found")
	}
	if dbClient.SpanID == (oteltrace.SpanID{}) {
		t.Fatalf("db client span not found")
	}
	if !dbClient.StartTime.After(apiServer.StartTime) {
		t.Fatalf("db client must start strictly after api server (db.start %s, api.start %s)", dbClient.StartTime, apiServer.StartTime)
	}
	if !dbClient.EndTime.Before(apiServer.EndTime) {
		t.Fatalf("db client must end strictly before api server (db.end %s, api.end %s)", dbClient.EndTime, apiServer.EndTime)
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

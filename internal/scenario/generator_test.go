package scenario

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
	if root.Kind != oteltrace.SpanKindInternal {
		t.Fatalf("expected root internal span, got %s", root.Kind)
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

func directScenarioJSON(root string) string {
	rootField := ""
	if root != "" {
		rootField = `"root":"` + root + `",`
	}
	return `{"name":"direct","services":{"svc":{}},` + rootField + `"nodes":{
		"a":{"service":"svc","kind":"SERVER","duration_ms":20,"status":{"code":"ERROR"}},
		"b":{"service":"svc","kind":"CLIENT","start_offset_ms":2,"duration_ms":5}},
		"edges":[{"from":"a","to":"b"}]}`
}

func TestGeneratorDirectBatch(t *testing.T) {
	for _, root := range []string{"a", ""} {
		t.Run("root="+root, func(t *testing.T) {
			cfg, err := DecodeJSON(strings.NewReader(directScenarioJSON(root)))
			if err != nil {
				t.Fatalf("direct definitions must remain decodable: %v", err)
			}
			definition, err := cfg.Build()
			if err != nil {
				t.Fatalf("direct definitions must remain buildable: %v", err)
			}
			g := NewGenerator(definition)
			spans, err := g.GenerateBatch(context.Background())
			if err != nil || len(spans) != 2 {
				t.Fatalf("expected exact direct batch, got spans=%+v error=%v", spans, err)
			}
			if spans[0].Name != "a" || spans[1].Name != "b" || spans[0].ParentSpanID.IsValid() || spans[1].ParentSpanID != spans[0].SpanID {
				t.Fatalf("unexpected direct topology: %+v", spans)
			}
			if spans[0].Kind != oteltrace.SpanKindServer || spans[0].StatusCode != codes.Error || spans[0].EndTime.Sub(spans[0].StartTime) != 20*time.Millisecond ||
				spans[1].Kind != oteltrace.SpanKindClient || spans[1].StatusCode != codes.Unset || spans[1].EndTime.Sub(spans[1].StartTime) != 5*time.Millisecond ||
				!spans[1].StartTime.Equal(spans[0].StartTime.Add(2*time.Millisecond)) {
				t.Fatalf("public direct batch native fields/timing differ: %+v", spans)
			}
			walker, err := g.NewStreamingWalker(time.Now())
			if err == nil || !strings.Contains(err.Error(), "batch streaming exporter") || walker != nil {
				t.Fatalf("expected unsupported-generation error and no walker, got walker=%v error=%v", walker, err)
			}
			if g.counter.Load() != 1 {
				t.Fatal("rejected streaming generation consumed a trace sequence")
			}
		})
	}
}

func TestGeneratorRootOnlyDefinitionStillSupported(t *testing.T) {
	// A manually constructed root-only definition is not a compiled direct
	// scenario; empty edges alone must not identify the unsupported mode.
	definition := testDefinition(t)
	definition.Edges = nil
	spans, err := NewGenerator(definition).GenerateBatch(context.Background())
	if err != nil || len(spans) != 1 {
		t.Fatalf("root-only generation changed: spans=%+v error=%v", spans, err)
	}
}

func TestGeneratorDeterministicIDs(t *testing.T) {
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
	for sequence := 0; sequence < 2; sequence++ {
		if len(batch1) != len(batch2) {
			t.Fatalf("sequence %d: span counts differ", sequence)
		}
		for i := range batch1 {
			a, b := batch1[i], batch2[i]
			if !a.TraceID.IsValid() || !a.SpanID.IsValid() {
				t.Fatalf("sequence %d span %d: invalid IDs", sequence, i)
			}
			if a.TraceID != b.TraceID || a.SpanID != b.SpanID || a.ParentSpanID != b.ParentSpanID {
				t.Fatalf("sequence %d span %d: IDs differ across fresh generators", sequence, i)
			}
		}
		if sequence == 0 {
			firstTraceID := batch1[0].TraceID
			batch1, err = g1.GenerateBatch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			batch2, err = g2.GenerateBatch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(batch1) == 0 || len(batch2) == 0 {
				t.Fatal("expected non-empty second batches")
			}
			if batch1[0].TraceID == firstTraceID {
				t.Fatal("successive batches reused a trace ID")
			}
		}
	}
}

func TestGeneratorOutputMapsDoNotMutateDefinition(t *testing.T) {
	definition := testDefinition(t)
	definition.Edges[0].SpanAttributes = map[string]attribute.Value{"operation": attribute.StringValue("original")}
	generator := NewGenerator(definition)
	batch, err := generator.GenerateBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) == 0 {
		t.Fatal("expected non-empty batch")
	}
	for _, span := range batch {
		span.ResourceAttributes["service.name"] = attribute.StringValue("mutated")
		span.Attributes["operation"] = attribute.StringValue("mutated")
	}
	for _, next := range []*Generator{generator, NewGenerator(definition)} {
		batch, err := next.GenerateBatch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		foundOperation := false
		for _, span := range batch {
			if span.ResourceAttributes["service.name"].AsString() == "mutated" {
				t.Fatal("generated resource map mutation leaked into later traces")
			}
			if value, ok := span.Attributes["operation"]; ok {
				foundOperation = true
				if value.AsString() != "original" {
					t.Fatal("generated attribute map mutation leaked into later traces")
				}
			}
		}
		if !foundOperation {
			t.Fatal("expected configured operation attribute")
		}
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
				SpanAttributes: map[string]TypedValue{
					"service.name": {Type: ValueTypeInt, Value: 7},
					"span.kind":    {Type: ValueTypeString, Value: "SERVER"},
				},
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

	if len(spans) != 2 {
		t.Fatalf("legacy event/link span count=%d, want 2", len(spans))
	}
	var rootID oteltrace.SpanID
	for _, span := range spans {
		if span.Name == "A" {
			rootID = span.SpanID
			if span.ParentSpanID.IsValid() || len(span.Events) != 0 || len(span.Links) != 0 || span.Attributes["service.name"] != attribute.StringValue("svc") {
				t.Fatalf("legacy root fields differ: %+v", span)
			}
		}
	}
	if !rootID.IsValid() {
		t.Fatal("legacy root missing")
	}
	// Find the span with events (the internal span from edge a->b).
	foundEvent := false
	foundLink := false
	for _, span := range spans {
		if span.Kind != oteltrace.SpanKindInternal || span.StatusCode != codes.Ok || span.StatusDescription != "" || !reflect.DeepEqual(span.ResourceAttributes, map[string]attribute.Value{"service.name": attribute.StringValue("svc")}) {
			t.Fatalf("legacy native/resource fields differ: %+v", span)
		}
		if span.Name == "B" {
			if span.ParentSpanID != rootID || len(span.Events) != 1 || len(span.Links) != 1 || !reflect.DeepEqual(span.Attributes, map[string]attribute.Value{"service.name": attribute.Int64Value(7), "span.kind": attribute.StringValue("SERVER")}) {
				t.Fatalf("legacy child attribute precedence/events/links differ: %+v", span)
			}
		}
		for _, event := range span.Events {
			if event.Name == "cache.miss" {
				foundEvent = true
				if !reflect.DeepEqual(event.Attributes, []attribute.KeyValue{attribute.String("cache.key", "items:list")}) || !event.Time.Equal(span.StartTime.Add(5*time.Millisecond)) {
					t.Fatalf("legacy event attributes/midpoint differ: %+v", event)
				}
				if event.Time.Before(span.StartTime) || event.Time.After(span.EndTime) {
					t.Fatalf("expected event time inside span duration, got event=%s start=%s end=%s", event.Time, span.StartTime, span.EndTime)
				}
			}
		}
		for _, link := range span.Links {
			if link.SpanContext.IsValid() {
				foundLink = true
				if link.SpanContext.TraceID() != span.TraceID || link.SpanContext.SpanID() != rootID || link.SpanContext.TraceFlags() != oteltrace.FlagsSampled || !reflect.DeepEqual(link.Attributes, []attribute.KeyValue{attribute.String("link.type", "follows_from")}) {
					t.Fatalf("legacy link reference/attributes differ: %+v", link)
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

// TestGeneratorParentContainsChild: every span's [Start, End] is
// contained in its parent's, matching real OTel semantics.
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

// TestGeneratorRootCoversWholeTrace: root's [Start, End] contains every
// other span. The root is the outermost bar in a timeline view.
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

// TestGeneratorSiblingsDoNotOverlap: siblings under the same parent are
// disjoint in time (sequential semantics). Pair-edge source/target spans
// aren't siblings (target's parent is source).
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

// TestGeneratorChildStartsAfterParentStart: child.Start > parent.Start
// strictly. Pair-edge source/target spans that share start/end by design
// are excluded.
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

// TestPairEdgeNetworkLatencyInsetsTargetSpan: target span = [client.start+lat, client.end-lat].
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

// TestPairEdgeZeroLatencyPreservesSharedInterval: latency=0 ⇒ target
// and source share intervals (regression guard for existing scenarios).
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

// TestPairEdgeLatencyChildrenFitInsideTarget: with latency, descendants
// still fit inside the (narrower) target span at every depth.
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

func batchAt(t testing.TB, g *Generator, anchor time.Time) []model.Span {
	t.Helper()
	spans, err := g.generateBatchAt(anchor)
	if err != nil {
		t.Fatal(err)
	}
	return spans
}

func TestGeneratorLegacyLiteralCharacterization(t *testing.T) {
	anchor := time.Unix(1700000000, 0).UTC()
	for _, kind := range []EdgeKind{EdgeKindClientServer, EdgeKindClientDatabase, EdgeKindProducerConsumer, EdgeKindInternal} {
		t.Run(string(kind), func(t *testing.T) {
			d := testDefinition(t)
			d.Seed, d.Edges = 0, d.Edges[:1]
			d.Edges[0].Kind, d.Edges[0].Repeat = kind, 1
			g := NewGenerator(d)
			for sequence, ids := range [][]string{
				{"1d0b14e4db018fed", "975835de1c9756ce", "e220a8397b1dcdaf"},
				{"e220a8397b1dcdaf", "910a2dec89025cc1", "1d0b14e4db018fed"},
			} {
				names := []string{"GET /posts -> POST /posts", "POST /posts", "GET /posts"}
				services := []string{"frontend", "post-service", "frontend"}
				kinds := []oteltrace.SpanKind{oteltrace.SpanKindClient, oteltrace.SpanKindServer, oteltrace.SpanKindInternal}
				parents := []string{ids[2], ids[0], "0000000000000000"}
				if kind == EdgeKindProducerConsumer {
					kinds[0], kinds[1] = oteltrace.SpanKindProducer, oteltrace.SpanKindConsumer
				}
				if kind == EdgeKindInternal {
					ids, names, services, kinds, parents = []string{ids[0], ids[2]}, names[1:], services[1:], kinds[1:], []string{ids[2], "0000000000000000"}
					kinds[0] = oteltrace.SpanKindInternal
				}
				spans := batchAt(t, g, anchor)
				if len(spans) != len(ids) {
					t.Fatalf("count=%d, want %d", len(spans), len(ids))
				}
				for i, span := range spans {
					start := anchor.Add(time.Millisecond)
					if i == len(spans)-1 {
						start = anchor
					}
					if span.SpanID.String() != ids[i] || span.ParentSpanID.String() != parents[i] || span.Name != names[i] || span.Kind != kinds[i] ||
						span.ResourceAttributes["service.name"].AsString() != services[i] || span.StatusCode != codes.Ok || span.StatusDescription != "" ||
						span.TraceID.String()[:16] != []string{"910a2dec89025cc1", "975835de1c9756ce"}[sequence] || span.TraceID != spans[0].TraceID ||
						!span.StartTime.Equal(start) || !span.EndTime.Equal(anchor.Add(11*time.Millisecond)) {
						t.Fatalf("sequence %d span %d changed: %+v", sequence+1, i, span)
					}
				}
			}
		})
	}
}

type directTopologyNode struct {
	name, service string
	parent        int // Index in sorted node-ID order; -1 is a root.
}

func buildDirectTopology(t testing.TB, nodes, edges string) Definition {
	t.Helper()
	cfg, err := DecodeJSON(strings.NewReader(`{"name":"topology","seed":42,"services":{
		"frontend":{"resource":{"service.name":{"type":"string","value":"frontend"}}},
		"db":{"resource":{"service.name":{"type":"string","value":"postgres"}}}},"nodes":` + nodes + `,"edges":` + edges + `}`))
	if err != nil {
		t.Fatal(err)
	}
	d, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func assertDirectTopology(t testing.TB, spans []model.Span, want []directTopologyNode, roots, depth, branching int) {
	t.Helper()
	if len(spans) != len(want) || len(spans) == 0 {
		t.Fatalf("count=%d, want %d nonempty spans", len(spans), len(want))
	}
	seen := map[oteltrace.SpanID]bool{}
	children := make([][]int, len(spans))
	queue, depths := []int{}, make([]int, len(spans))
	for i, span := range spans {
		parent := oteltrace.SpanID{}
		if want[i].parent >= 0 {
			parent = spans[want[i].parent].SpanID
			children[want[i].parent] = append(children[want[i].parent], i)
		} else {
			queue = append(queue, i)
		}
		if !span.TraceID.IsValid() || span.TraceID != spans[0].TraceID || !span.SpanID.IsValid() || seen[span.SpanID] ||
			span.ParentSpanID != parent || span.Name != want[i].name || span.ResourceAttributes["service.name"].AsString() != want[i].service {
			t.Fatalf("node %d: topology/name/service/ID differs: %+v", i, span)
		}
		seen[span.SpanID] = true
	}
	gotRoots, gotDepth, gotBranching := len(queue), 0, 0
	for head := 0; head < len(queue); head++ {
		i := queue[head]
		gotDepth, gotBranching = max(gotDepth, depths[i]), max(gotBranching, len(children[i]))
		for _, child := range children[i] {
			depths[child] = depths[i] + 1
			queue = append(queue, child)
		}
	}
	if len(queue) != len(spans) || gotRoots != roots || gotDepth != depth || gotBranching != branching {
		t.Fatalf("roots/depth/branching=%d/%d/%d, want %d/%d/%d", gotRoots, gotDepth, gotBranching, roots, depth, branching)
	}
}

func TestGeneratorDirectTopology(t *testing.T) {
	fixtures := []struct {
		name, nodes, edges      string
		want                    []directTopologyNode
		roots, depth, branching int
	}{
		{"singleton", `{"a":{"service":"frontend","span_name":"request"}}`, `[{"from":"a"}]`, []directTopologyNode{{"request", "frontend", -1}}, 1, 0, 0},
		{"request-query", `{"a":{"service":"frontend","span_name":"request"},"b":{"service":"db","span_name":"query"}}`, `[{"from":"a","to":"b"}]`, []directTopologyNode{{"request", "frontend", -1}, {"query", "postgres", 0}}, 1, 1, 1},
		{"null-forest", `{"a":{"service":"frontend"},"b":{"service":"db","parent":null},"c":{"service":"db"}}`, `[{"from":"a","to":"b"},{"from":"b","to":"c"}]`, []directTopologyNode{{"a", "frontend", -1}, {"b", "postgres", -1}, {"c", "postgres", 1}}, 2, 1, 1},
		{"forward-parent", `{"a":{"service":"db","parent":"z"},"b":{"service":"frontend"},"z":{"service":"frontend"}}`, `[{"from":"b","to":"a"},{"from":"z"}]`, []directTopologyNode{{"a", "postgres", 2}, {"b", "frontend", -1}, {"z", "frontend", -1}}, 2, 1, 1},
		{"shared-caller", `{"a":{"service":"frontend"},"b":{"service":"db"},"c":{"service":"db"}}`, `[{"from":"a","to":"b"},{"from":"a","to":"c"},{"from":"a","to":"b"}]`, []directTopologyNode{{"a", "frontend", -1}, {"b", "postgres", 0}, {"c", "postgres", 0}}, 1, 1, 2},
		{"repeated-siblings", `{"a":{"service":"frontend","span_name":"same"},"b":{"service":"frontend","span_name":"same"},"c":{"service":"frontend","span_name":"same"}}`, `[{"from":"a","to":"b"},{"from":"a","to":"c"}]`, []directTopologyNode{{"same", "frontend", -1}, {"same", "frontend", 0}, {"same", "frontend", 0}}, 1, 1, 2},
		{"same-service-chain", `{"a":{"service":"frontend"},"b":{"service":"frontend"},"c":{"service":"frontend"}}`, `[{"from":"a","to":"b"},{"from":"b","to":"c"}]`, []directTopologyNode{{"a", "frontend", -1}, {"b", "frontend", 0}, {"c", "frontend", 1}}, 1, 2, 1},
	}
	anchor := time.Unix(1700000000, 123456789).UTC()
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			d := buildDirectTopology(t, fixture.nodes, fixture.edges)
			g, replay := NewGenerator(d), NewGenerator(d)
			for sequence := 0; sequence < 2; sequence++ {
				spans := batchAt(t, g, anchor)
				assertDirectTopology(t, spans, fixture.want, fixture.roots, fixture.depth, fixture.branching)
				if !reflect.DeepEqual(spans, batchAt(t, replay, anchor)) {
					t.Fatal("anchored regeneration differs")
				}
			}
		})
	}
}

func TestGeneratorDirectDeepAndWide(t *testing.T) {
	const count = 10000
	for _, shape := range []string{"deep", "wide"} {
		t.Run(shape, func(t *testing.T) {
			cfg := Config{Name: shape, Services: map[string]ServiceConfig{"svc": {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "svc"}}}}, Nodes: map[string]NodeConfig{}}
			want := make([]directTopologyNode, count)
			for i := range want {
				id, parent := fmt.Sprintf("n%05d", i), -1
				cfg.Nodes[id] = NodeConfig{Service: "svc", SpanName: "same"}
				if i > 0 {
					parent = 0
					if shape == "deep" {
						parent = i - 1
					}
					cfg.Edges = append(cfg.Edges, EdgeConfig{From: fmt.Sprintf("n%05d", parent), To: id})
				}
				want[i] = directTopologyNode{"same", "svc", parent}
			}
			d, err := cfg.Build()
			if err != nil {
				t.Fatal(err)
			}
			depth, branching := 1, count-1
			if shape == "deep" {
				depth, branching = count-1, 1
			}
			assertDirectTopology(t, batchAt(t, NewGenerator(d), time.Unix(1700000000, 0)), want, 1, depth, branching)
		})
	}
}

func TestGeneratorDirectConcurrentIDs(t *testing.T) {
	d := buildDirectTopology(t, `{"z":{"service":"frontend"},"a":{"service":"db"},"b":{"service":"db"}}`, `[{"from":"z","to":"b"},{"from":"z","to":"a"}]`)
	reordered := buildDirectTopology(t, `{"b":{"service":"db"},"a":{"service":"db"},"z":{"service":"frontend"}}`, `[{"from":"z","to":"a"},{"from":"z","to":"b"}]`)
	anchor := time.Unix(1700000000, 0).UTC()
	const requests = 128
	g, replay := NewGenerator(d), NewGenerator(reordered)
	batches, errors := make([][]model.Span, requests), make([]error, requests)
	var wg sync.WaitGroup
	for i := range batches {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			batches[i], errors[i] = g.generateBatchAt(anchor)
		}(i)
	}
	wg.Wait()
	want, seen := map[oteltrace.TraceID][]model.Span{}, map[oteltrace.SpanID]bool{}
	for range requests {
		batch := batchAt(t, replay, anchor)
		if _, duplicate := want[batch[0].TraceID]; duplicate {
			t.Fatal("sequential trace ID reused")
		}
		want[batch[0].TraceID] = batch
		for _, span := range batch {
			if !span.SpanID.IsValid() || seen[span.SpanID] {
				t.Fatal("sequential span ID invalid/reused")
			}
			seen[span.SpanID] = true
		}
	}
	for i, batch := range batches {
		if errors[i] != nil || len(batch) != 3 || !reflect.DeepEqual(batch, want[batch[0].TraceID]) {
			t.Fatalf("concurrent request %d differs: %v", i, errors[i])
		}
		delete(want, batch[0].TraceID)
	}
	changed := d
	changed.Seed++
	if len(want) != 0 || g.counter.Load() != requests || batchAt(t, NewGenerator(d), anchor)[0].SpanID == batchAt(t, NewGenerator(changed), anchor)[0].SpanID {
		t.Fatal("missing sequences or seed ignored")
	}
}

func TestDirectSpanIDRetriesOccupiedCandidates(t *testing.T) {
	used := map[oteltrace.SpanID]struct{}{}
	first, second := directSpanID(42, 1, 1, used), directSpanID(42, 1, 1, used)
	if !first.IsValid() || !second.IsValid() || first == second || len(used) != 2 || directSpanID(42, 1, 1, map[oteltrace.SpanID]struct{}{first: {}}) != second {
		t.Fatal("ID retry is not unique and reproducible")
	}
}

func TestGeneratorDirectNativeFields(t *testing.T) {
	anchor := time.Unix(1700000000, 123456789).UTC()
	for _, kind := range []string{"", "UNSPECIFIED", "INTERNAL", "SERVER", "CLIENT", "PRODUCER", "CONSUMER"} {
		for _, status := range []string{"", "UNSET", "OK", "ERROR"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				fields, description := "", ""
				if kind != "" {
					fields += fmt.Sprintf(`,"kind":%q`, kind)
				}
				if status != "" {
					if status == "ERROR" {
						description = " RAW Error: 原\nnot normalized "
					}
					fields += fmt.Sprintf(`,"status":{"code":%q,"description":%q}`, status, description)
				}
				d := buildDirectTopology(t, `{"a":{"service":"frontend","span_name":" Root /原 "`+fields+`,"span_attributes":{"span.kind":{"type":"string","value":"SERVER"},"http.method":{"type":"string","value":"pOsT"}}},"b":{"service":"db","span_name":"Child /RAW"`+fields+`}}`, `[{"from":"a","to":"b"}]`)
				spans := batchAt(t, NewGenerator(d), anchor)
				wantKind := map[string]oteltrace.SpanKind{"": oteltrace.SpanKindUnspecified, "UNSPECIFIED": oteltrace.SpanKindUnspecified, "INTERNAL": oteltrace.SpanKindInternal, "SERVER": oteltrace.SpanKindServer, "CLIENT": oteltrace.SpanKindClient, "PRODUCER": oteltrace.SpanKindProducer, "CONSUMER": oteltrace.SpanKindConsumer}[kind]
				wantStatus := map[string]codes.Code{"": codes.Unset, "UNSET": codes.Unset, "OK": codes.Ok, "ERROR": codes.Error}[status]
				for i, span := range spans {
					if span.Name != []string{" Root /原 ", "Child /RAW"}[i] || span.Kind != wantKind || span.StatusCode != wantStatus || span.StatusDescription != description ||
						!span.StartTime.Equal(anchor) || !span.EndTime.Equal(anchor.Add(time.Millisecond)) {
						t.Fatalf("native fields/defaults differ: %+v", span)
					}
				}
				if spans[0].Attributes["span.kind"] != attribute.StringValue("SERVER") || spans[0].Attributes["http.method"] != attribute.StringValue("pOsT") {
					t.Fatal("root protocol attributes normalized or lost")
				}
			})
		}
	}
}

func TestGeneratorDirectTypedFieldsAndIsolation(t *testing.T) {
	values := []struct {
		typeName ValueType
		value    any
		want     attribute.Value
	}{
		{ValueTypeString, " gEt /原 ", attribute.StringValue(" gEt /原 ")},
		{ValueTypeInt, 503, attribute.Int64Value(503)},
		{ValueTypeFloat, -1.25, attribute.Float64Value(-1.25)},
		{ValueTypeBool, false, attribute.BoolValue(false)},
		{ValueTypeStringArray, []any{" A ", "原", ""}, attribute.StringSliceValue([]string{" A ", "原", ""})},
		{ValueTypeIntArray, []any{0, -7}, attribute.Int64SliceValue([]int64{0, -7})},
		{ValueTypeFloatArray, []any{0.0, -1.25}, attribute.Float64SliceValue([]float64{0, -1.25})},
		{ValueTypeBoolArray, []any{false, true}, attribute.BoolSliceValue([]bool{false, true})},
		{ValueTypeStringArray, []any{}, attribute.StringSliceValue([]string{})},
		{ValueTypeIntArray, []any{}, attribute.Int64SliceValue([]int64{})},
		{ValueTypeFloatArray, []any{}, attribute.Float64SliceValue([]float64{})},
		{ValueTypeBoolArray, []any{}, attribute.BoolSliceValue([]bool{})},
	}
	input, want := map[string]TypedValue{}, map[string]attribute.Value{}
	for i, value := range values {
		key := fmt.Sprintf("v%02d", i)
		input[key], want[key] = TypedValue{Type: value.typeName, Value: value.value}, value.want
	}
	input["service.name"], want["service.name"] = TypedValue{Type: ValueTypeInt, Value: 77}, attribute.Int64Value(77)
	offset, duration, childDuration := int64(5), int64(7), int64(21)
	cfg := Config{Name: "fields", Seed: 42, Services: map[string]ServiceConfig{"svc": {Resource: input}}, Nodes: map[string]NodeConfig{
		"a": {Service: "svc", StartOffsetMs: &offset, DurationMs: &duration, SpanAttributes: input, SpanEvents: []EventConfig{{Name: " First /原 ", Attributes: input}, {Name: "second", Attributes: input}}, SpanLinks: []LinkConfig{{Node: "z", Attributes: input}, {Node: "b", Attributes: input}, {Node: "a", Attributes: input}, {Node: "z", Attributes: input}}},
		"b": {Service: "svc", DurationMs: &childDuration, SpanAttributes: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: "override"}}, SpanEvents: []EventConfig{{Name: "child"}}},
		"z": {Service: "svc", SpanLinks: []LinkConfig{{Node: "b", Attributes: input}}},
	}, Edges: []EdgeConfig{{From: "a", To: "b"}, {From: "z"}}}
	d, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	pristine, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Unix(1700000000, 123456789).UTC()
	g, replay := NewGenerator(d), NewGenerator(pristine)
	spans := batchAt(t, g, anchor)
	if !reflect.DeepEqual(spans, batchAt(t, replay, anchor)) || !reflect.DeepEqual(spans[0].Attributes, want) ||
		!spans[0].StartTime.Equal(anchor.Add(5*time.Millisecond)) || !spans[0].EndTime.Equal(anchor.Add(12*time.Millisecond)) ||
		!spans[1].StartTime.Equal(anchor) || !spans[1].EndTime.Equal(anchor.Add(21*time.Millisecond)) || spans[1].Attributes["service.name"] != attribute.StringValue("override") {
		t.Fatal("typed fields, precedence, exact timing or regeneration differs")
	}
	for _, span := range spans {
		if !reflect.DeepEqual(span.ResourceAttributes, want) {
			t.Fatal("typed resource changed")
		}
		for _, event := range span.Events {
			if !event.Time.Equal(span.StartTime.Add(span.EndTime.Sub(span.StartTime) / 2)) {
				t.Fatal("event midpoint changed")
			}
		}
	}
	for i, name := range []string{" First /原 ", "second"} {
		if spans[0].Events[i].Name != name || !reflect.DeepEqual(model.AttributesToMap(spans[0].Events[i].Attributes), want) {
			t.Fatal("typed event/order changed")
		}
	}
	for source, targets := range map[int][]int{0: {2, 1, 0, 2}, 2: {1}} {
		if len(spans[source].Links) != len(targets) {
			t.Fatal("link count changed")
		}
		for i, target := range targets {
			link := spans[source].Links[i]
			if link.SpanContext.TraceID() != spans[target].TraceID || link.SpanContext.SpanID() != spans[target].SpanID || link.SpanContext.TraceFlags() != oteltrace.FlagsSampled || !reflect.DeepEqual(model.AttributesToMap(link.Attributes), want) {
				t.Fatal("typed forward/self/cross-root link changed")
			}
		}
	}
	spans[0].Attributes["service.name"] = attribute.StringValue("mutated")
	spans[0].ResourceAttributes["service.name"] = attribute.StringValue("mutated")
	spans[0].Events[0].Name, spans[0].Events[0].Attributes[0] = "mutated", attribute.String("mutated", "event")
	spans[0].Links[0].SpanContext, spans[0].Links[0].Attributes[0] = oteltrace.SpanContext{}, attribute.String("mutated", "link")
	if !reflect.DeepEqual(model.AttributesToMap(spans[0].Events[1].Attributes), want) || !reflect.DeepEqual(model.AttributesToMap(spans[0].Links[3].Attributes), want) ||
		!reflect.DeepEqual(spans[1].ResourceAttributes, want) || !reflect.DeepEqual(d, pristine) || !reflect.DeepEqual(batchAt(t, g, anchor), batchAt(t, replay, anchor)) {
		t.Fatal("mutation leaked into siblings, definitions or subsequent output")
	}
}

func TestGeneratorDirectTiming(t *testing.T) {
	const maxMs int64 = math.MaxInt64 / int64(time.Millisecond)
	fixtures := []struct {
		name                     string
		offset, duration         *int64
		wantOffset, wantDuration int64
		invalid                  bool
	}{
		{"defaults", nil, nil, 0, 1, false}, {"offset-only", new(int64(8)), nil, 8, 1, false}, {"duration-only", nil, new(int64(13)), 0, 13, false},
		{"zero", new(int64(0)), new(int64(0)), 0, 0, false}, {"max-duration", nil, new(maxMs), 0, maxMs, false},
		{"max-offset", new(maxMs), new(int64(0)), maxMs, 0, false}, {"max-sum", new(maxMs - 1), new(int64(1)), maxMs - 1, 1, false},
		{"negative-offset", new(int64(-1)), nil, 0, 0, true}, {"negative-duration", nil, new(int64(-1)), 0, 0, true},
		{"offset-overflow", new(maxMs + 1), nil, 0, 0, true}, {"duration-overflow", nil, new(maxMs + 1), 0, 0, true},
		{"sum-overflow", new(maxMs), new(int64(1)), 0, 0, true}, {"int64-limit", nil, new(int64(math.MaxInt64)), 0, 0, true},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			cfg := Config{Name: "timing", Services: map[string]ServiceConfig{"svc": {}}, Nodes: map[string]NodeConfig{"a": {Service: "svc", StartOffsetMs: fixture.offset, DurationMs: fixture.duration, SpanEvents: []EventConfig{{Name: "midpoint"}}}}, Edges: []EdgeConfig{{From: "a"}}}
			d, err := cfg.Build()
			if fixture.invalid {
				if err == nil {
					t.Fatal("invalid timing accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			anchor := time.Unix(1700000000, 123456789).UTC()
			span := batchAt(t, NewGenerator(d), anchor)[0]
			start, duration := anchor.Add(time.Duration(fixture.wantOffset)*time.Millisecond), time.Duration(fixture.wantDuration)*time.Millisecond
			if !span.StartTime.Equal(start) || !span.EndTime.Equal(start.Add(duration)) || !span.Events[0].Time.Equal(start.Add(duration/2)) {
				t.Fatalf("timing boundary/default changed: %+v", span)
			}
		})
	}
}

func TestLegacySpanDefaults(t *testing.T) {
	for _, duration := range []time.Duration{0, -time.Millisecond, 3 * time.Millisecond} {
		fields := legacySpanFields(oteltrace.SpanKindClient, time.Time{}, duration, nil, nil, nil)
		if fields.StatusCode != codes.Ok || fields.Kind != oteltrace.SpanKindClient || fields.Duration != max(duration, time.Millisecond) {
			t.Fatal("legacy defaults changed")
		}
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

package scenario

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type spanKey struct {
	Name string
	Kind oteltrace.SpanKind
}

func tallySpans(spans []model.Span) map[spanKey]int {
	out := make(map[spanKey]int)
	for _, s := range spans {
		out[spanKey{Name: s.Name, Kind: s.Kind}]++
	}
	return out
}

func drainStreamingWalker(t *testing.T, w *StreamingWalker) []model.Span {
	t.Helper()
	var out []model.Span
	for {
		spans, _, ok := w.NextEmit()
		if !ok {
			return out
		}
		out = append(out, spans...)
	}
}

// TestStreamingWalkerMatchesGenerateBatch: the public walker produces
// the same set of spans as GenerateBatch (which uses the same internal
// walker). Compared by count + (name, kind) multiset.
func TestStreamingWalkerMatchesGenerateBatch(t *testing.T) {
	definition := testDefinition(t)

	gEager := NewGenerator(definition)
	eager, err := gEager.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch: %v", err)
	}

	gStream := NewGenerator(definition)
	w, err := gStream.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	streaming := drainStreamingWalker(t, w)

	if len(streaming) != len(eager) {
		t.Fatalf("count: eager=%d streaming=%d", len(eager), len(streaming))
	}
	if !reflect.DeepEqual(tallySpans(eager), tallySpans(streaming)) {
		t.Fatalf("multiset differs:\n eager: %v\n streaming: %v", tallySpans(eager), tallySpans(streaming))
	}
}

// TestStreamingWalkerWellFormedTrace: exactly one root, unique SpanIDs,
// every non-root parent is present, single TraceID.
func TestStreamingWalkerWellFormedTrace(t *testing.T) {
	g := NewGenerator(testDefinition(t))
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	spans := drainStreamingWalker(t, w)
	if len(spans) == 0 {
		t.Fatalf("no spans")
	}

	seen := make(map[oteltrace.SpanID]bool, len(spans))
	var rootCount int
	for i, s := range spans {
		if seen[s.SpanID] {
			t.Fatalf("duplicate SpanID at %d", i)
		}
		seen[s.SpanID] = true
		if !s.ParentSpanID.IsValid() {
			rootCount++
		}
		if s.TraceID != w.TraceID() {
			t.Fatalf("TraceID mismatch at %d", i)
		}
	}
	if rootCount != 1 {
		t.Fatalf("expected 1 root, got %d", rootCount)
	}
	for _, s := range spans {
		if s.ParentSpanID.IsValid() && !seen[s.ParentSpanID] {
			t.Fatalf("span %s has parent %s not in output", s.SpanID, s.ParentSpanID)
		}
	}
}

// TestStreamingWalkerEventsAndLinks: lazy event/link resolution still
// works; the link to the (last-emitted) root resolves at first pop.
func TestStreamingWalkerEventsAndLinks(t *testing.T) {
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
	def, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := NewGenerator(def)
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	spans := drainStreamingWalker(t, w)

	var foundEvent, foundLink bool
	for _, s := range spans {
		for _, e := range s.Events {
			if e.Name == "cache.miss" {
				foundEvent = true
			}
		}
		for _, l := range s.Links {
			if l.SpanContext.IsValid() {
				foundLink = true
			}
		}
	}
	if !foundEvent {
		t.Fatalf("cache.miss event missing")
	}
	if !foundLink {
		t.Fatalf("link to root missing")
	}
}

// TestStreamingWalkerDoneFlag: Done() flips at drain; post-drain
// NextEmit remains safe.
func TestStreamingWalkerDoneFlag(t *testing.T) {
	g := NewGenerator(testDefinition(t))
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	if w.Done() {
		t.Fatalf("walker should not be Done() at construction")
	}
	for {
		if _, _, ok := w.NextEmit(); !ok {
			break
		}
	}
	if !w.Done() {
		t.Fatalf("walker should be Done() after drain")
	}
	if _, _, ok := w.NextEmit(); ok {
		t.Fatalf("post-drain NextEmit returned ok=true")
	}
}

// TestStreamingWalkerRootEmitsLast: with the heap-based walker, root
// pops after every descendant.
func TestStreamingWalkerRootEmitsLast(t *testing.T) {
	g := NewGenerator(testDefinition(t))
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}

	var batches [][]model.Span
	for {
		spans, _, ok := w.NextEmit()
		if !ok {
			break
		}
		batches = append(batches, spans)
	}
	if len(batches) == 0 {
		t.Fatalf("no batches")
	}
	last := batches[len(batches)-1]
	if len(last) != 1 || last[0].ParentSpanID.IsValid() {
		t.Fatalf("expected final batch to be the root span (1 parentless span); got %d spans, first parent=%v", len(last), last[0].ParentSpanID)
	}
	// And no earlier batch should contain a parentless span.
	for i, b := range batches[:len(batches)-1] {
		for j, s := range b {
			if !s.ParentSpanID.IsValid() {
				t.Fatalf("batch %d span %d is parentless but not the final batch", i, j)
			}
		}
	}
}

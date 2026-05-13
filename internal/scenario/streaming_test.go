package scenario

import (
	"context"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestEmitHeapOrdersByDueAt(t *testing.T) {
	h := &emitHeap{}
	base := time.Now()

	h.PushEmit(&pendingEmit{DueAt: base.Add(3 * time.Second)})
	h.PushEmit(&pendingEmit{DueAt: base.Add(1 * time.Second)})
	h.PushEmit(&pendingEmit{DueAt: base.Add(2 * time.Second)})

	want := []time.Duration{1 * time.Second, 2 * time.Second, 3 * time.Second}
	for i, w := range want {
		got := h.PopMin()
		if !got.DueAt.Equal(base.Add(w)) {
			t.Fatalf("pop %d: expected DueAt=base+%s, got base+%s", i, w, got.DueAt.Sub(base))
		}
	}
	if h.Len() != 0 {
		t.Fatalf("expected empty heap, got len=%d", h.Len())
	}
}

func TestEmitHeapStableForEqualDueAt(t *testing.T) {
	h := &emitHeap{}
	base := time.Now()

	a := &pendingEmit{DueAt: base}
	b := &pendingEmit{DueAt: base}
	c := &pendingEmit{DueAt: base}
	h.PushEmit(a)
	h.PushEmit(b)
	h.PushEmit(c)

	if got := h.PopMin(); got != a {
		t.Fatalf("expected first pushed (a) to pop first, got different pointer")
	}
	if got := h.PopMin(); got != b {
		t.Fatalf("expected second pushed (b) to pop second")
	}
	if got := h.PopMin(); got != c {
		t.Fatalf("expected third pushed (c) to pop third")
	}
}

func TestEmitHeapStress(t *testing.T) {
	h := &emitHeap{}
	base := time.Now()
	rng := rand.New(rand.NewSource(42))

	const n = 1000
	for i := 0; i < n; i++ {
		offset := time.Duration(rng.Int63n(int64(time.Hour)))
		h.PushEmit(&pendingEmit{DueAt: base.Add(offset)})
	}

	var prev time.Time
	for i := 0; i < n; i++ {
		got := h.PopMin()
		if i > 0 && got.DueAt.Before(prev) {
			t.Fatalf("non-monotonic pop at i=%d: %s came after %s", i, got.DueAt, prev)
		}
		prev = got.DueAt
	}
	if h.Len() != 0 {
		t.Fatalf("expected empty heap after draining, got len=%d", h.Len())
	}
}

func TestEmitHeapPeekDueAt(t *testing.T) {
	h := &emitHeap{}

	if _, ok := h.PeekDueAt(); ok {
		t.Fatalf("expected ok=false on empty heap")
	}

	base := time.Now()
	h.PushEmit(&pendingEmit{DueAt: base.Add(2 * time.Second)})
	h.PushEmit(&pendingEmit{DueAt: base.Add(1 * time.Second)})

	due, ok := h.PeekDueAt()
	if !ok {
		t.Fatalf("expected ok=true on non-empty heap")
	}
	if !due.Equal(base.Add(1 * time.Second)) {
		t.Fatalf("expected peek DueAt=base+1s, got base+%s", due.Sub(base))
	}
	if h.Len() != 2 {
		t.Fatalf("expected len=2 after peek (peek must not pop), got %d", h.Len())
	}
}

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

// TestStreamingWalkerStructuralEquivalence verifies that the streaming
// walker produces the same span count and the same multiset of
// (name, kind) as GenerateBatch for the shared test scenario. SpanIDs and
// timestamps are not compared because the streaming walker pops in
// (DueAt, Seq) order rather than DFS-preorder.
func TestStreamingWalkerStructuralEquivalence(t *testing.T) {
	definition := testDefinition(t)

	gEager := NewGenerator(definition)
	eagerSpans, err := gEager.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("eager GenerateBatch: %v", err)
	}

	gStreaming := NewGenerator(definition)
	walker, err := gStreaming.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	streamingSpans := drainStreamingWalker(t, walker)

	if len(streamingSpans) != len(eagerSpans) {
		t.Fatalf("span count differs: eager=%d streaming=%d", len(eagerSpans), len(streamingSpans))
	}

	eagerTally := tallySpans(eagerSpans)
	streamingTally := tallySpans(streamingSpans)
	if !reflect.DeepEqual(eagerTally, streamingTally) {
		t.Fatalf("(name, kind) multiset differs:\n  eager:     %v\n  streaming: %v", eagerTally, streamingTally)
	}
}

// TestStreamingWalkerParentChildStructure verifies that the trace produced
// by the streaming walker is well-formed: exactly one root, every non-root
// span's parent is present in the output, all SpanIDs unique, and all
// spans share one TraceID.
func TestStreamingWalkerParentChildStructure(t *testing.T) {
	definition := testDefinition(t)
	g := NewGenerator(definition)
	walker, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	spans := drainStreamingWalker(t, walker)
	if len(spans) == 0 {
		t.Fatalf("streaming walker produced no spans")
	}

	seen := make(map[oteltrace.SpanID]bool, len(spans))
	var rootCount int
	traceID := spans[0].TraceID
	if traceID != walker.TraceID() {
		t.Fatalf("first span TraceID %s differs from walker.TraceID() %s", traceID, walker.TraceID())
	}
	for i, span := range spans {
		if seen[span.SpanID] {
			t.Fatalf("duplicate SpanID at index %d: %s", i, span.SpanID)
		}
		seen[span.SpanID] = true
		if !span.ParentSpanID.IsValid() {
			rootCount++
		}
		if span.TraceID != traceID {
			t.Fatalf("span %d TraceID mismatch: %s vs %s", i, span.TraceID, traceID)
		}
	}
	if rootCount != 1 {
		t.Fatalf("expected exactly 1 root span, got %d", rootCount)
	}
	for _, span := range spans {
		if !span.ParentSpanID.IsValid() {
			continue
		}
		if !seen[span.ParentSpanID] {
			t.Fatalf("span %s has parent %s which is not in output", span.SpanID, span.ParentSpanID)
		}
	}
}

// TestStreamingWalkerEmitsEventsAndLinks mirrors
// TestGeneratorEmitsEventsAndLinks but drives the streaming walker. It
// verifies that lazy event/link resolution still emits events on the
// expected span and produces a valid link to a previously-emitted span
// (the root).
func TestStreamingWalkerEmitsEventsAndLinks(t *testing.T) {
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
		t.Fatalf("Build: %v", err)
	}
	g := NewGenerator(definition)
	walker, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	spans := drainStreamingWalker(t, walker)

	var foundEvent, foundLink bool
	for _, span := range spans {
		for _, event := range span.Events {
			if event.Name == "cache.miss" {
				foundEvent = true
				if len(event.Attributes) == 0 {
					t.Fatalf("cache.miss event missing attributes")
				}
			}
		}
		for _, link := range span.Links {
			if link.SpanContext.IsValid() {
				foundLink = true
				if len(link.Attributes) == 0 {
					t.Fatalf("link missing attributes")
				}
			}
		}
	}
	if !foundEvent {
		t.Fatalf("expected cache.miss event")
	}
	if !foundLink {
		t.Fatalf("expected at least one span with a valid link")
	}
}

// TestStreamingWalkerDoneFlag verifies Done() transitions to true after
// the heap drains and stays consistent with NextEmit returning ok=false.
func TestStreamingWalkerDoneFlag(t *testing.T) {
	definition := testDefinition(t)
	g := NewGenerator(definition)
	walker, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	if walker.Done() {
		t.Fatalf("walker should not be done before any emits")
	}
	for {
		_, _, ok := walker.NextEmit()
		if !ok {
			break
		}
	}
	if !walker.Done() {
		t.Fatalf("walker should be done after NextEmit returns ok=false")
	}
	// Calling NextEmit again must remain safe and still return ok=false.
	if _, _, ok := walker.NextEmit(); ok {
		t.Fatalf("NextEmit after drain returned ok=true")
	}
}

func TestEmitHeapInterleavedPushPop(t *testing.T) {
	// Verify the invariant survives interleaved pushes and pops, not just
	// load-then-drain. This is closer to how the streaming scheduler will
	// actually use the heap.
	h := &emitHeap{}
	base := time.Now()

	h.PushEmit(&pendingEmit{DueAt: base.Add(10 * time.Millisecond)})
	h.PushEmit(&pendingEmit{DueAt: base.Add(5 * time.Millisecond)})

	first := h.PopMin()
	if !first.DueAt.Equal(base.Add(5 * time.Millisecond)) {
		t.Fatalf("expected first pop at base+5ms, got base+%s", first.DueAt.Sub(base))
	}

	// Push something earlier than the remaining item; it should pop next.
	h.PushEmit(&pendingEmit{DueAt: base.Add(7 * time.Millisecond)})
	h.PushEmit(&pendingEmit{DueAt: base.Add(20 * time.Millisecond)})

	second := h.PopMin()
	if !second.DueAt.Equal(base.Add(7 * time.Millisecond)) {
		t.Fatalf("expected second pop at base+7ms, got base+%s", second.DueAt.Sub(base))
	}
	third := h.PopMin()
	if !third.DueAt.Equal(base.Add(10 * time.Millisecond)) {
		t.Fatalf("expected third pop at base+10ms, got base+%s", third.DueAt.Sub(base))
	}
	fourth := h.PopMin()
	if !fourth.DueAt.Equal(base.Add(20 * time.Millisecond)) {
		t.Fatalf("expected fourth pop at base+20ms, got base+%s", fourth.DueAt.Sub(base))
	}
}

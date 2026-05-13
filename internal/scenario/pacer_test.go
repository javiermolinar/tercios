package scenario

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// pacedRun is a thin test helper that collects every batch a pacer emits
// along with the wall-clock instant at which the sink received it. The
// sink-time information is what proves the wall-clock pacing actually
// blocked — without it, we can only assert on logical timestamps.
type pacedBatch struct {
	Spans   []model.Span
	SinkAt  time.Time
}

func pacedRun(t *testing.T, ctx context.Context, w *StreamingWalker) ([]pacedBatch, error) {
	t.Helper()
	var (
		mu      sync.Mutex
		batches []pacedBatch
	)
	sink := func(spans []model.Span) {
		mu.Lock()
		defer mu.Unlock()
		// Copy the slice so the walker can reuse its backing array safely.
		out := make([]model.Span, len(spans))
		copy(out, spans)
		batches = append(batches, pacedBatch{Spans: out, SinkAt: time.Now()})
	}
	err := RunSingleTrace(ctx, w, sink)
	return batches, err
}

// TestRunSingleTraceWallClockDuration verifies that wall-clock pacing
// actually blocks for the scenario's nominal duration. The test
// definition has an estimated duration of ~34ms; the pacer must take at
// least most of that (a loose lower bound survives CI noise).
func TestRunSingleTraceWallClockDuration(t *testing.T) {
	definition := testDefinition(t)
	g := NewGenerator(definition)
	startedAt := time.Now()
	w, err := g.NewStreamingWalker(startedAt)
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}

	batches, err := pacedRun(t, context.Background(), w)
	if err != nil {
		t.Fatalf("RunSingleTrace: %v", err)
	}
	elapsed := time.Since(startedAt)

	if len(batches) == 0 {
		t.Fatalf("pacer produced no batches")
	}

	// Scenario nominal duration is 34ms (see TestEstimateDurationPositive
	// and the test definition). Allow a 50% lower margin for scheduler
	// jitter while still catching a regression that would skip pacing.
	const minExpected = 17 * time.Millisecond
	if elapsed < minExpected {
		t.Fatalf("pacer drained in %s, expected >= %s (scenario nominal is 34ms)", elapsed, minExpected)
	}
}

// TestRunSingleTraceNoFutureTimestamps verifies the core invariant the
// streaming exporter exists to satisfy: every emitted span's end_time is
// at most the wall-clock instant at which the sink received it (modulo a
// small tolerance for unmaterialized waits). Without this Tempo (and most
// OTel-compatible backends) would reject the spans.
func TestRunSingleTraceNoFutureTimestamps(t *testing.T) {
	definition := testDefinition(t)
	g := NewGenerator(definition)
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}

	batches, err := pacedRun(t, context.Background(), w)
	if err != nil {
		t.Fatalf("RunSingleTrace: %v", err)
	}

	// Allow 2ms of slack to absorb scheduler imprecision in time.NewTimer.
	const slack = 2 * time.Millisecond
	for i, batch := range batches {
		for j, span := range batch.Spans {
			if span.EndTime.After(batch.SinkAt.Add(slack)) {
				t.Fatalf("batch %d span %d has end_time %s which is more than %s after sink time %s", i, j, span.EndTime, slack, batch.SinkAt)
			}
			if span.StartTime.After(span.EndTime) {
				t.Fatalf("batch %d span %d has start_time %s after end_time %s", i, j, span.StartTime, span.EndTime)
			}
		}
	}
}

// TestRunSingleTraceRootEmitsLast verifies that the root span (the span
// without a valid parent) is the final batch returned by the pacer. This
// is the property that makes long-running traces work against Tempo:
// the root's end_time is the moment the trace logically completed, not
// a precomputed future timestamp.
func TestRunSingleTraceRootEmitsLast(t *testing.T) {
	definition := testDefinition(t)
	g := NewGenerator(definition)
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}

	batches, err := pacedRun(t, context.Background(), w)
	if err != nil {
		t.Fatalf("RunSingleTrace: %v", err)
	}
	if len(batches) == 0 {
		t.Fatalf("no batches emitted")
	}

	last := batches[len(batches)-1]
	if len(last.Spans) != 1 {
		t.Fatalf("expected final batch to contain exactly the root span (1 span), got %d", len(last.Spans))
	}
	if last.Spans[0].ParentSpanID.IsValid() {
		t.Fatalf("final batch's span has a parent (%s), expected the root (no parent)", last.Spans[0].ParentSpanID)
	}
	if last.Spans[0].SpanID == (oteltrace.SpanID{}) {
		t.Fatalf("root span has zero SpanID")
	}
	if last.Spans[0].TraceID != w.TraceID() {
		t.Fatalf("root span TraceID %s != walker TraceID %s", last.Spans[0].TraceID, w.TraceID())
	}

	// Sanity: no earlier batch contains a parentless span.
	for i, batch := range batches[:len(batches)-1] {
		for j, span := range batch.Spans {
			if !span.ParentSpanID.IsValid() {
				t.Fatalf("batch %d span %d is parentless but not the final batch", i, j)
			}
		}
	}
}

// TestRunSingleTraceCancellation verifies the pacer honors ctx
// cancellation: it must return ctx.Err() promptly even when sleeping
// inside a long inter-emit wait. Uses a slow scenario (each span ~50ms)
// so the pacer is guaranteed to be mid-sleep when cancellation fires.
func TestRunSingleTraceCancellation(t *testing.T) {
	cfg := Config{
		Name: "cancel",
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
			{From: "a", To: "b", Kind: EdgeKindInternal, Repeat: 5, DurationMs: 50},
		},
	}
	definition, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := NewGenerator(definition)
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- RunSingleTrace(ctx, w, func(_ []model.Span) {})
	}()

	// Let one or two emits land, then cancel.
	time.Sleep(75 * time.Millisecond)
	cancel()

	select {
	case err := <-doneCh:
		if err == nil {
			t.Fatalf("expected ctx.Err() from RunSingleTrace, got nil")
		}
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("RunSingleTrace did not return within 100ms of cancellation")
	}
}

// TestRunSingleTracePacedStructuralEquivalence verifies that the pacer
// produces the same trace shape (count, name+kind multiset) as
// GenerateBatch, just stretched in wall-clock time. This guards against
// regressions where pacing logic inadvertently drops or duplicates emits.
func TestRunSingleTracePacedStructuralEquivalence(t *testing.T) {
	definition := testDefinition(t)

	gEager := NewGenerator(definition)
	eagerSpans, err := gEager.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("eager: %v", err)
	}

	gPaced := NewGenerator(definition)
	w, err := gPaced.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	batches, err := pacedRun(t, context.Background(), w)
	if err != nil {
		t.Fatalf("RunSingleTrace: %v", err)
	}

	var paced []model.Span
	for _, batch := range batches {
		paced = append(paced, batch.Spans...)
	}

	if len(paced) != len(eagerSpans) {
		t.Fatalf("span count: eager=%d paced=%d", len(eagerSpans), len(paced))
	}
	if !reflect.DeepEqual(tallySpans(eagerSpans), tallySpans(paced)) {
		t.Fatalf("(name, kind) multiset differs:\n  eager: %v\n  paced: %v", tallySpans(eagerSpans), tallySpans(paced))
	}
}

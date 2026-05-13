package scenario

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
)

type pacedBatch struct {
	Spans  []model.Span
	SinkAt time.Time
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
		out := make([]model.Span, len(spans))
		copy(out, spans)
		batches = append(batches, pacedBatch{Spans: out, SinkAt: time.Now()})
	}
	err := RunSingleTrace(ctx, w, sink)
	return batches, err
}

// TestRunSingleTraceWallClockDuration: the pacer waits for real
// wall-clock time. The test scenario has ~34ms nominal duration; the
// drain takes at least most of that even on noisy CI.
func TestRunSingleTraceWallClockDuration(t *testing.T) {
	g := NewGenerator(testDefinition(t))
	startedAt := time.Now()
	w, err := g.NewStreamingWalker(startedAt)
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	if _, err := pacedRun(t, context.Background(), w); err != nil {
		t.Fatalf("RunSingleTrace: %v", err)
	}
	elapsed := time.Since(startedAt)
	const minExpected = 17 * time.Millisecond
	if elapsed < minExpected {
		t.Fatalf("pacer drained in %s, expected >= %s (scenario nominal is 34ms)", elapsed, minExpected)
	}
}

// TestRunSingleTraceNoFutureTimestamps: every emitted span's end_time
// is at most the wall-clock instant the sink received it (+ small slack
// for time.NewTimer imprecision). This is the invariant Tempo requires.
func TestRunSingleTraceNoFutureTimestamps(t *testing.T) {
	g := NewGenerator(testDefinition(t))
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	batches, err := pacedRun(t, context.Background(), w)
	if err != nil {
		t.Fatalf("RunSingleTrace: %v", err)
	}
	const slack = 2 * time.Millisecond
	for i, b := range batches {
		for j, s := range b.Spans {
			if s.EndTime.After(b.SinkAt.Add(slack)) {
				t.Fatalf("batch %d span %d ends %s, sink at %s (slack %s exceeded)", i, j, s.EndTime, b.SinkAt, slack)
			}
		}
	}
}

// TestRunSingleTraceRootEmitsLast: the root span is the final batch
// the pacer delivers — its end_time aligns with the moment the trace
// is logically complete.
func TestRunSingleTraceRootEmitsLast(t *testing.T) {
	g := NewGenerator(testDefinition(t))
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}
	batches, err := pacedRun(t, context.Background(), w)
	if err != nil {
		t.Fatalf("RunSingleTrace: %v", err)
	}
	if len(batches) == 0 {
		t.Fatalf("no batches")
	}
	last := batches[len(batches)-1]
	if len(last.Spans) != 1 || last.Spans[0].ParentSpanID.IsValid() {
		t.Fatalf("expected final batch to be the root span")
	}
}

// TestRunSingleTraceCancellation: ctx cancellation returns
// context.Canceled within ~100ms even when the pacer is mid-sleep.
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
	def, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := NewGenerator(def)
	w, err := g.NewStreamingWalker(time.Now())
	if err != nil {
		t.Fatalf("NewStreamingWalker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- RunSingleTrace(ctx, w, func(_ []model.Span) {})
	}()
	time.Sleep(75 * time.Millisecond)
	cancel()

	select {
	case err := <-doneCh:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("RunSingleTrace did not return within 100ms of cancellation")
	}
}

package otlp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type recordedEmit struct {
	at    time.Time
	spans model.Batch
}

type fakeBatchExporter struct {
	mu        sync.Mutex
	emits     []recordedEmit
	exportErr error
	shutdown  int
}

func (f *fakeBatchExporter) ExportBatch(_ context.Context, batch model.Batch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.exportErr != nil {
		return f.exportErr
	}
	copyBatch := make(model.Batch, len(batch))
	copy(copyBatch, batch)
	f.emits = append(f.emits, recordedEmit{at: time.Now(), spans: copyBatch})
	return nil
}

func (f *fakeBatchExporter) Shutdown(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdown++
	return nil
}

func (f *fakeBatchExporter) snapshot() []recordedEmit {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedEmit, len(f.emits))
	copy(out, f.emits)
	return out
}

func makeSpan(name string, endOffset time.Duration, base time.Time) model.Span {
	return model.Span{
		TraceID:   oteltrace.TraceID{0x01},
		SpanID:    oteltrace.SpanID{byte(len(name))},
		Name:      name,
		StartTime: base,
		EndTime:   base.Add(endOffset),
	}
}

func TestStreamingBatchExporterEmptyBatchIsNoop(t *testing.T) {
	inner := &fakeBatchExporter{}
	exp := NewStreamingBatchExporter(inner)

	if err := exp.ExportBatch(context.Background(), nil); err != nil {
		t.Fatalf("ExportBatch(nil) err = %v", err)
	}
	if got := len(inner.snapshot()); got != 0 {
		t.Fatalf("expected no inner emits, got %d", got)
	}
}

func TestStreamingBatchExporterEmitsInEndTimeOrder(t *testing.T) {
	inner := &fakeBatchExporter{}
	exp := NewStreamingBatchExporter(inner)

	base := time.Now()
	batch := model.Batch{
		makeSpan("late", 30*time.Millisecond, base),
		makeSpan("early", 10*time.Millisecond, base),
		makeSpan("mid", 20*time.Millisecond, base),
	}

	if err := exp.ExportBatch(context.Background(), batch); err != nil {
		t.Fatalf("ExportBatch err = %v", err)
	}

	emits := inner.snapshot()
	if len(emits) != 3 {
		t.Fatalf("expected 3 emits, got %d", len(emits))
	}
	want := []string{"early", "mid", "late"}
	for i, name := range want {
		if got := emits[i].spans[0].Name; got != name {
			t.Fatalf("emit %d: want %q, got %q", i, name, got)
		}
	}
}

func TestStreamingBatchExporterGroupsByEndTime(t *testing.T) {
	inner := &fakeBatchExporter{}
	exp := NewStreamingBatchExporter(inner)

	base := time.Now()
	batch := model.Batch{
		makeSpan("a", 10*time.Millisecond, base),
		makeSpan("b", 10*time.Millisecond, base),
		makeSpan("c", 20*time.Millisecond, base),
	}

	if err := exp.ExportBatch(context.Background(), batch); err != nil {
		t.Fatalf("ExportBatch err = %v", err)
	}

	emits := inner.snapshot()
	if len(emits) != 2 {
		t.Fatalf("expected 2 emits (grouped by EndTime), got %d", len(emits))
	}
	if len(emits[0].spans) != 2 {
		t.Fatalf("first emit: expected 2 spans, got %d", len(emits[0].spans))
	}
	if len(emits[1].spans) != 1 {
		t.Fatalf("second emit: expected 1 span, got %d", len(emits[1].spans))
	}
}

func TestStreamingBatchExporterSleepsUntilEndTime(t *testing.T) {
	inner := &fakeBatchExporter{}
	exp := NewStreamingBatchExporter(inner)

	base := time.Now()
	batch := model.Batch{
		makeSpan("a", 30*time.Millisecond, base),
	}

	start := time.Now()
	if err := exp.ExportBatch(context.Background(), batch); err != nil {
		t.Fatalf("ExportBatch err = %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 25*time.Millisecond {
		t.Fatalf("expected pacer to sleep ~30ms before emit, took %s", elapsed)
	}

	emits := inner.snapshot()
	if len(emits) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(emits))
	}
	if !emits[0].at.After(base.Add(25 * time.Millisecond)) {
		t.Fatalf("emit landed at %s, expected after %s", emits[0].at, base.Add(25*time.Millisecond))
	}
}

func TestStreamingBatchExporterRebasesStaleTimestamps(t *testing.T) {
	// Spans generated an hour ago should still pace over their nominal
	// duration when exported, not flush instantly. This guards against
	// the channel-queuing case where a trace sits buffered before the
	// exporter pulls it.
	inner := &fakeBatchExporter{}
	exp := NewStreamingBatchExporter(inner)

	base := time.Now().Add(-time.Hour)
	batch := model.Batch{
		{Name: "a", StartTime: base, EndTime: base.Add(10 * time.Millisecond)},
		{Name: "b", StartTime: base.Add(5 * time.Millisecond), EndTime: base.Add(30 * time.Millisecond)},
	}

	start := time.Now()
	if err := exp.ExportBatch(context.Background(), batch); err != nil {
		t.Fatalf("ExportBatch err = %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 25*time.Millisecond {
		t.Fatalf("expected pacer to rebase and sleep ~30ms, took %s", elapsed)
	}
	if got := len(inner.snapshot()); got != 2 {
		t.Fatalf("expected 2 emits after rebasing, got %d", got)
	}
	// The exported spans should have timestamps anchored at-or-after the
	// moment ExportBatch was called, not an hour ago.
	for _, e := range inner.snapshot() {
		for _, s := range e.spans {
			if s.EndTime.Before(start) {
				t.Fatalf("span %q EndTime %s is before ExportBatch start %s (rebase did not run)", s.Name, s.EndTime, start)
			}
		}
	}
}

func TestStreamingBatchExporterCancellationReturnsCtxErr(t *testing.T) {
	inner := &fakeBatchExporter{}
	exp := NewStreamingBatchExporter(inner)

	base := time.Now()
	batch := model.Batch{
		makeSpan("far", time.Hour, base),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- exp.ExportBatch(ctx, batch) }()

	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("ExportBatch did not return within 100ms of cancel")
	}
	if got := len(inner.snapshot()); got != 0 {
		t.Fatalf("expected inner to receive no emits, got %d", got)
	}
}

func TestStreamingBatchExporterPropagatesInnerError(t *testing.T) {
	wantErr := errors.New("boom")
	inner := &fakeBatchExporter{exportErr: wantErr}
	exp := NewStreamingBatchExporter(inner)

	base := time.Now().Add(-time.Second) // past, no sleep
	batch := model.Batch{
		makeSpan("a", 10*time.Millisecond, base),
		makeSpan("b", 20*time.Millisecond, base),
	}

	err := exp.ExportBatch(context.Background(), batch)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected inner error to propagate, got %v", err)
	}
}

func TestStreamingBatchExporterShutdownForwarded(t *testing.T) {
	inner := &fakeBatchExporter{}
	exp := NewStreamingBatchExporter(inner)

	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown err = %v", err)
	}
	if inner.shutdown != 1 {
		t.Fatalf("expected inner Shutdown called once, got %d", inner.shutdown)
	}
}

func TestStreamingExporterFactoryWrapsInner(t *testing.T) {
	innerFactory := fakeFactory{inner: &fakeBatchExporter{}}
	f := NewStreamingExporterFactory(innerFactory)

	exp, err := f.NewBatchExporter(context.Background())
	if err != nil {
		t.Fatalf("NewBatchExporter err = %v", err)
	}
	if _, ok := exp.(*streamingBatchExporter); !ok {
		t.Fatalf("expected *streamingBatchExporter, got %T", exp)
	}
}

type fakeFactory struct {
	inner model.BatchExporter
}

func (f fakeFactory) NewBatchExporter(_ context.Context) (model.BatchExporter, error) {
	return f.inner, nil
}

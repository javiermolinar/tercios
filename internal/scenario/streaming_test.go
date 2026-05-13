package scenario

import (
	"math/rand"
	"testing"
	"time"
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

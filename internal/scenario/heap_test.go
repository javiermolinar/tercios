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
			t.Fatalf("pop %d: expected base+%s, got base+%s", i, w, got.DueAt.Sub(base))
		}
	}
	if h.Len() != 0 {
		t.Fatalf("expected empty heap, got len=%d", h.Len())
	}
}

func TestEmitHeapStableForEqualDueAt(t *testing.T) {
	h := &emitHeap{}
	base := time.Now()
	a, b, c := &pendingEmit{DueAt: base}, &pendingEmit{DueAt: base}, &pendingEmit{DueAt: base}
	h.PushEmit(a)
	h.PushEmit(b)
	h.PushEmit(c)
	if h.PopMin() != a || h.PopMin() != b || h.PopMin() != c {
		t.Fatalf("expected push order to determine pop order at equal DueAt")
	}
}

func TestEmitHeapRootBreaksTieAfterNonRoot(t *testing.T) {
	h := &emitHeap{}
	base := time.Now()
	root := &pendingEmit{DueAt: base, IsRoot: true}
	leaf := &pendingEmit{DueAt: base}
	// Push root first; if Seq alone broke ties, root would pop first.
	h.PushEmit(root)
	h.PushEmit(leaf)
	if first := h.PopMin(); first != leaf {
		t.Fatalf("expected non-root to pop before root at equal DueAt")
	}
	if second := h.PopMin(); second != root {
		t.Fatalf("expected root to pop second")
	}
}

func TestEmitHeapStress(t *testing.T) {
	h := &emitHeap{}
	base := time.Now()
	rng := rand.New(rand.NewSource(42))
	const n = 1000
	for i := 0; i < n; i++ {
		h.PushEmit(&pendingEmit{DueAt: base.Add(time.Duration(rng.Int63n(int64(time.Hour))))})
	}
	var prev time.Time
	for i := 0; i < n; i++ {
		got := h.PopMin()
		if i > 0 && got.DueAt.Before(prev) {
			t.Fatalf("non-monotonic pop at i=%d: %s came after %s", i, got.DueAt, prev)
		}
		prev = got.DueAt
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
	if !ok || !due.Equal(base.Add(1*time.Second)) {
		t.Fatalf("peek returned (%s, %v); expected base+1s, true", due, ok)
	}
	if h.Len() != 2 {
		t.Fatalf("peek must not pop")
	}
}

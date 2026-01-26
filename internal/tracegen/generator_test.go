package tracegen

import (
	"context"
	"testing"
)

func TestGeneratorEmitsExpectedSpanCount(t *testing.T) {
	total := 0
	gen := Generator{
		ServiceName: "test-service",
		SpanName:    "test-span",
		Services:    1,
		MaxDepth:    1,
		MaxSpans:    1,
	}

	for i := 0; i < 5; i++ {
		spans, err := gen.GenerateBatch(context.Background())
		if err != nil {
			t.Fatalf("GenerateBatch() error = %v", err)
		}
		total += len(spans)
	}

	if total != 5 {
		t.Fatalf("expected 5 spans, got %d", total)
	}
}

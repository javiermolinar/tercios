package tracegen

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/codes"
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

func TestGeneratorSetsSpanStatus(t *testing.T) {
	gen := Generator{
		ServiceName: "test-service",
		SpanName:    "test-span",
		Services:    1,
		MaxDepth:    2,
		MaxSpans:    5,
	}

	for i := 0; i < 10; i++ {
		spans, err := gen.GenerateBatch(context.Background())
		if err != nil {
			t.Fatalf("GenerateBatch() error = %v", err)
		}
		for _, span := range spans {
			if span.Status().Code == codes.Unset {
				t.Fatalf("expected span status to be set, got unset for span %q", span.Name())
			}
		}
	}
}

func TestGeneratorErrorRateExtremes(t *testing.T) {
	tests := []struct {
		name       string
		errorRate  float64
		wantStatus codes.Code
	}{
		{name: "all ok", errorRate: 0, wantStatus: codes.Ok},
		{name: "all error", errorRate: 1, wantStatus: codes.Error},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gen := Generator{
				ServiceName: "test-service",
				SpanName:    "test-span",
				Services:    1,
				MaxDepth:    2,
				MaxSpans:    8,
				ErrorRate:   tt.errorRate,
			}

			spans, err := gen.GenerateBatch(context.Background())
			if err != nil {
				t.Fatalf("GenerateBatch() error = %v", err)
			}
			for _, span := range spans {
				if span.Status().Code != tt.wantStatus {
					t.Fatalf("expected status %s, got %s", tt.wantStatus, span.Status().Code)
				}
			}
		})
	}
}

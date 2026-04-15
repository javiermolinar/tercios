package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStatsSummaryIncludesFailureBreakdown(t *testing.T) {
	stats := NewStats()
	stats.Record(5*time.Millisecond, nil)
	stats.Record(7*time.Millisecond, context.DeadlineExceeded)
	stats.Record(9*time.Millisecond, errors.New("dial tcp 127.0.0.1:4317: connect: connection refused"))

	summary := stats.Summary()
	if summary.Successes != 1 {
		t.Fatalf("expected 1 success, got %d", summary.Successes)
	}
	if summary.Failures != 2 {
		t.Fatalf("expected 2 failures, got %d", summary.Failures)
	}
	if got := summary.FailureBreakdown["timeout"]; got != 1 {
		t.Fatalf("expected timeout breakdown=1, got %d", got)
	}
	if got := summary.FailureBreakdown["connection_refused"]; got != 1 {
		t.Fatalf("expected connection_refused breakdown=1, got %d", got)
	}
	if len(summary.FailureSamples["timeout"]) == 0 {
		t.Fatalf("expected timeout sample")
	}
}

func TestFormatSummaryPrintsFailureBreakdown(t *testing.T) {
	summary := Summary{
		Total:      3,
		Successes:  1,
		Failures:   2,
		AvgLatency: 6 * time.Millisecond,
		P95Latency: 9 * time.Millisecond,
		FailureBreakdown: map[string]int{
			"timeout":            1,
			"connection_refused": 1,
		},
		FailureSamples: map[string][]string{
			"timeout": {"context deadline exceeded"},
		},
	}

	formatted := FormatSummary(summary)
	if !strings.Contains(formatted, "Failure breakdown:") {
		t.Fatalf("expected failure breakdown section, got %q", formatted)
	}
	if !strings.Contains(formatted, "- timeout: 1") {
		t.Fatalf("expected timeout breakdown line, got %q", formatted)
	}
	if !strings.Contains(formatted, "sample: context deadline exceeded") {
		t.Fatalf("expected timeout sample line, got %q", formatted)
	}
}

func TestStatsCollectsTraceIDSamples(t *testing.T) {
	stats := NewStatsWithTraceIDSampleLimit(2)
	stats.RecordWithTraceIDs(5*time.Millisecond, nil, []string{"trace-1", "trace-2", "trace-1", "trace-3"})
	stats.RecordWithTraceIDs(6*time.Millisecond, errors.New("boom"), []string{"trace-2", "trace-4"})

	summary := stats.Summary()
	if got := len(summary.TraceIDSamples); got != 2 {
		t.Fatalf("expected trace id sample limit to apply, got %d", got)
	}
	if got := len(summary.FailedTraceIDSamples); got != 2 {
		t.Fatalf("expected failed trace id sample limit to apply, got %d", got)
	}
	if summary.FailedTraceIDSamples[0] != "trace-2" {
		t.Fatalf("expected first failed trace id sample to be trace-2, got %q", summary.FailedTraceIDSamples[0])
	}
}

func TestFormatSummaryPrintsTraceIDSamples(t *testing.T) {
	summary := Summary{
		Total:                2,
		Successes:            1,
		Failures:             1,
		TraceIDSamples:       []string{"trace-1", "trace-2"},
		FailedTraceIDSamples: []string{"trace-2"},
	}

	formatted := FormatSummary(summary)
	if !strings.Contains(formatted, "Trace ID samples (2):") {
		t.Fatalf("expected trace id samples section, got %q", formatted)
	}
	if !strings.Contains(formatted, "Failed trace ID samples (1):") {
		t.Fatalf("expected failed trace id samples section, got %q", formatted)
	}
}

func TestStatsSummaryWithElapsedIncludesSenderMetrics(t *testing.T) {
	stats := NewStats()
	stats.RecordBatchWithTraceIDs(5*time.Millisecond, nil, nil, 10)
	stats.RecordBatchWithTraceIDs(7*time.Millisecond, errors.New("boom"), nil, 5)

	summary := stats.SummaryWithElapsed(2 * time.Second)
	if summary.Total != 2 {
		t.Fatalf("expected 2 total requests, got %d", summary.Total)
	}
	if summary.TotalSpans != 15 {
		t.Fatalf("expected 15 attempted spans, got %d", summary.TotalSpans)
	}
	if summary.SuccessfulSpans != 10 {
		t.Fatalf("expected 10 successful spans, got %d", summary.SuccessfulSpans)
	}
	if summary.FailedSpans != 5 {
		t.Fatalf("expected 5 failed spans, got %d", summary.FailedSpans)
	}
	if summary.RequestsPerSecond != 1.0 {
		t.Fatalf("expected 1 req/s, got %v", summary.RequestsPerSecond)
	}
	if summary.SpansPerSecond != 7.5 {
		t.Fatalf("expected 7.5 spans/s, got %v", summary.SpansPerSecond)
	}
	if summary.AverageSpansPerRequest != 7.5 {
		t.Fatalf("expected 7.5 spans/request, got %v", summary.AverageSpansPerRequest)
	}
}

func TestFormatSummaryPrintsSenderMetrics(t *testing.T) {
	summary := Summary{
		Total:                       2,
		Successes:                   2,
		Failures:                    0,
		WallTime:                    2 * time.Second,
		RequestsPerSecond:           1.0,
		SuccessfulRequestsPerSecond: 1.0,
		TotalSpans:                  20,
		SuccessfulSpans:             20,
		SpansPerSecond:              10.0,
		SuccessfulSpansPerSecond:    10.0,
		AverageSpansPerRequest:      10.0,
	}

	formatted := FormatSummary(summary)
	for _, needle := range []string{
		"Wall time:",
		"Request rate:",
		"Attempted spans:",
		"Span rate:",
		"Avg spans/request:",
	} {
		if !strings.Contains(formatted, needle) {
			t.Fatalf("expected %q in summary, got %q", needle, formatted)
		}
	}
	for _, needle := range []string{"Attempted payload:", "Payload rate:", "Avg payload/request:"} {
		if strings.Contains(formatted, needle) {
			t.Fatalf("did not expect %q in summary, got %q", needle, formatted)
		}
	}
}

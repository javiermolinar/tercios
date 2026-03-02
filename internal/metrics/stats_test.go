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

package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/sdk/trace"
)

type Stats struct {
	durations []time.Duration
	successes int
	failures  int
}

func NewStats() *Stats {
	return &Stats{}
}

func (s *Stats) Record(duration time.Duration, err error) {
	s.durations = append(s.durations, duration)
	if err != nil {
		s.failures++
	} else {
		s.successes++
	}
}

type Summary struct {
	Total      int
	Successes  int
	Failures   int
	AvgLatency time.Duration
	P95Latency time.Duration
}

func (s *Stats) Summary() Summary {
	total := len(s.durations)
	if total == 0 {
		return Summary{Total: 0, Successes: s.successes, Failures: s.failures}
	}

	durations := make([]time.Duration, total)
	copy(durations, s.durations)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	avg := time.Duration(int64(sum) / int64(total))
	p95Index := int(float64(total-1) * 0.95)
	p95 := durations[p95Index]

	return Summary{
		Total:      total,
		Successes:  s.successes,
		Failures:   s.failures,
		AvgLatency: avg,
		P95Latency: p95,
	}
}

func Summarize(stats []*Stats) Summary {
	var total int
	var successes int
	var failures int
	for _, stat := range stats {
		if stat == nil {
			continue
		}
		total += len(stat.durations)
		successes += stat.successes
		failures += stat.failures
	}

	if total == 0 {
		return Summary{Total: 0, Successes: successes, Failures: failures}
	}

	durations := make([]time.Duration, 0, total)
	for _, stat := range stats {
		if stat == nil {
			continue
		}
		durations = append(durations, stat.durations...)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	avg := time.Duration(int64(sum) / int64(total))
	p95Index := int(float64(total-1) * 0.95)
	p95 := durations[p95Index]

	return Summary{
		Total:      total,
		Successes:  successes,
		Failures:   failures,
		AvgLatency: avg,
		P95Latency: p95,
	}
}

type InstrumentedExporter struct {
	inner trace.SpanExporter
	stats *Stats
}

func NewInstrumentedExporter(inner trace.SpanExporter, stats *Stats) *InstrumentedExporter {
	return &InstrumentedExporter{inner: inner, stats: stats}
}

func (e *InstrumentedExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	start := time.Now()
	err := e.inner.ExportSpans(ctx, spans)
	if e.stats != nil {
		e.stats.Record(time.Since(start), err)
	}
	return err
}

func (e *InstrumentedExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

func FormatSummary(summary Summary) string {
	lines := []string{
		fmt.Sprintf("Sent %s requests", formatCount(summary.Total)),
		fmt.Sprintf("Success: %s", formatCount(summary.Successes)),
		fmt.Sprintf("Failures: %s", formatCount(summary.Failures)),
		fmt.Sprintf("Avg latency: %s", formatLatency(summary.AvgLatency)),
		fmt.Sprintf("P95 latency: %s", formatLatency(summary.P95Latency)),
	}
	return strings.Join(lines, "\n")
}

func FormatProgress(summary Summary, expected int) string {
	expectedText := formatCount(expected)
	if expected <= 0 {
		expectedText = "?"
	}
	return fmt.Sprintf(
		"Progress: %s/%s sent | Success: %s | Failures: %s | Avg: %s | P95: %s",
		formatCount(summary.Total),
		expectedText,
		formatCount(summary.Successes),
		formatCount(summary.Failures),
		formatLatency(summary.AvgLatency),
		formatLatency(summary.P95Latency),
	)
}

func formatCount(count int) string {
	if count >= 1000 {
		value := float64(count) / 1000.0
		if count%1000 == 0 {
			return fmt.Sprintf("%dk", count/1000)
		}
		return fmt.Sprintf("%.1fk", value)
	}
	return fmt.Sprintf("%d", count)
}

func formatLatency(duration time.Duration) string {
	if duration <= 0 {
		return "0ms"
	}
	return fmt.Sprintf("%dms", duration.Milliseconds())
}

package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/sdk/trace"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const maxFailureSamplesPerClass = 3

type Stats struct {
	durations            []time.Duration
	successes            int
	failures             int
	attemptedSpans       int
	successfulSpans      int
	failedSpans          int
	failureBreakdown     map[string]int
	failureSamples       map[string][]string
	traceIDSampleLimit   int
	traceIDSamples       []string
	failedTraceIDSamples []string
	seenTraceIDs         map[string]struct{}
	seenFailedTraceIDs   map[string]struct{}
}

func NewStats() *Stats {
	return NewStatsWithTraceIDSampleLimit(0)
}

func NewStatsWithTraceIDSampleLimit(limit int) *Stats {
	if limit < 0 {
		limit = 0
	}
	return &Stats{
		failureBreakdown:   make(map[string]int),
		failureSamples:     make(map[string][]string),
		traceIDSampleLimit: limit,
		seenTraceIDs:       make(map[string]struct{}),
		seenFailedTraceIDs: make(map[string]struct{}),
	}
}

func (s *Stats) Record(duration time.Duration, err error) {
	s.RecordBatchWithTraceIDs(duration, err, nil, 0)
}

func (s *Stats) RecordWithTraceIDs(duration time.Duration, err error, traceIDs []string) {
	s.RecordBatchWithTraceIDs(duration, err, traceIDs, 0)
}

func (s *Stats) RecordBatchWithTraceIDs(duration time.Duration, err error, traceIDs []string, spans int) {
	if spans < 0 {
		spans = 0
	}

	s.durations = append(s.durations, duration)
	s.attemptedSpans += spans
	if err != nil {
		s.failures++
		s.failedSpans += spans
		class := classifyError(err)
		s.failureBreakdown[class]++
		s.recordFailureSample(class, err)
	} else {
		s.successes++
		s.successfulSpans += spans
	}
	s.recordTraceIDSamples(traceIDs, err != nil)
}

func (s *Stats) recordFailureSample(class string, err error) {
	if err == nil {
		return
	}
	if s.failureSamples == nil {
		s.failureSamples = make(map[string][]string)
	}
	normalized := normalizeErrorMessage(err.Error())
	if normalized == "" {
		return
	}
	samples := s.failureSamples[class]
	for _, sample := range samples {
		if sample == normalized {
			return
		}
	}
	if len(samples) >= maxFailureSamplesPerClass {
		return
	}
	s.failureSamples[class] = append(samples, normalized)
}

func (s *Stats) recordTraceIDSamples(traceIDs []string, failed bool) {
	if s.traceIDSampleLimit <= 0 || len(traceIDs) == 0 {
		return
	}
	if s.seenTraceIDs == nil {
		s.seenTraceIDs = make(map[string]struct{})
	}
	if s.seenFailedTraceIDs == nil {
		s.seenFailedTraceIDs = make(map[string]struct{})
	}
	for _, traceID := range traceIDs {
		traceID = strings.TrimSpace(traceID)
		if traceID == "" {
			continue
		}
		if len(s.traceIDSamples) < s.traceIDSampleLimit {
			if _, exists := s.seenTraceIDs[traceID]; !exists {
				s.seenTraceIDs[traceID] = struct{}{}
				s.traceIDSamples = append(s.traceIDSamples, traceID)
			}
		}
		if failed && len(s.failedTraceIDSamples) < s.traceIDSampleLimit {
			if _, exists := s.seenFailedTraceIDs[traceID]; !exists {
				s.seenFailedTraceIDs[traceID] = struct{}{}
				s.failedTraceIDSamples = append(s.failedTraceIDSamples, traceID)
			}
		}
	}
}

type Summary struct {
	Total                       int
	Successes                   int
	Failures                    int
	WallTime                    time.Duration
	RequestsPerSecond           float64
	SuccessfulRequestsPerSecond float64
	TotalSpans                  int
	SuccessfulSpans             int
	FailedSpans                 int
	SpansPerSecond              float64
	SuccessfulSpansPerSecond    float64
	AverageSpansPerRequest      float64
	AvgLatency                  time.Duration
	P95Latency                  time.Duration
	FailureBreakdown            map[string]int
	FailureSamples              map[string][]string
	TraceIDSamples              []string
	FailedTraceIDSamples        []string
}

func (s *Stats) Summary() Summary {
	totalRequests := len(s.durations)
	if totalRequests == 0 {
		summary := Summary{
			Total:                0,
			Successes:            s.successes,
			Failures:             s.failures,
			TotalSpans:           s.attemptedSpans,
			SuccessfulSpans:      s.successfulSpans,
			FailedSpans:          s.failedSpans,
			FailureBreakdown:     cloneBreakdown(s.failureBreakdown),
			FailureSamples:       cloneSamples(s.failureSamples),
			TraceIDSamples:       cloneStrings(s.traceIDSamples),
			FailedTraceIDSamples: cloneStrings(s.failedTraceIDSamples),
		}
		populateDerivedSummary(&summary)
		return summary
	}

	durations := make([]time.Duration, len(s.durations))
	copy(durations, s.durations)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	avg := time.Duration(int64(sum) / int64(len(durations)))
	p95Index := int(float64(len(durations)-1) * 0.95)
	p95 := durations[p95Index]

	summary := Summary{
		Total:                totalRequests,
		Successes:            s.successes,
		Failures:             s.failures,
		TotalSpans:           s.attemptedSpans,
		SuccessfulSpans:      s.successfulSpans,
		FailedSpans:          s.failedSpans,
		AvgLatency:           avg,
		P95Latency:           p95,
		FailureBreakdown:     cloneBreakdown(s.failureBreakdown),
		FailureSamples:       cloneSamples(s.failureSamples),
		TraceIDSamples:       cloneStrings(s.traceIDSamples),
		FailedTraceIDSamples: cloneStrings(s.failedTraceIDSamples),
	}
	populateDerivedSummary(&summary)
	return summary
}

func (s *Stats) SummaryWithElapsed(elapsed time.Duration) Summary {
	summary := s.Summary()
	summary.WallTime = elapsed
	populateDerivedSummary(&summary)
	return summary
}

func Summarize(stats []*Stats) Summary {
	var total int
	var successes int
	var failures int
	var totalSpans int
	var successfulSpans int
	var failedSpans int
	failureBreakdown := make(map[string]int)
	failureSamples := make(map[string][]string)
	traceIDLimit := 0
	traceIDSamples := make([]string, 0)
	failedTraceIDSamples := make([]string, 0)

	for _, stat := range stats {
		if stat == nil {
			continue
		}
		total += len(stat.durations)
		successes += stat.successes
		failures += stat.failures
		totalSpans += stat.attemptedSpans
		successfulSpans += stat.successfulSpans
		failedSpans += stat.failedSpans
		mergeBreakdown(failureBreakdown, stat.failureBreakdown)
		mergeSamples(failureSamples, stat.failureSamples)
		if stat.traceIDSampleLimit > traceIDLimit {
			traceIDLimit = stat.traceIDSampleLimit
		}
	}
	for _, stat := range stats {
		if stat == nil {
			continue
		}
		traceIDSamples = mergeStringSamples(traceIDSamples, stat.traceIDSamples, traceIDLimit)
		failedTraceIDSamples = mergeStringSamples(failedTraceIDSamples, stat.failedTraceIDSamples, traceIDLimit)
	}

	summary := Summary{
		Total:                total,
		Successes:            successes,
		Failures:             failures,
		TotalSpans:           totalSpans,
		SuccessfulSpans:      successfulSpans,
		FailedSpans:          failedSpans,
		FailureBreakdown:     failureBreakdown,
		FailureSamples:       failureSamples,
		TraceIDSamples:       traceIDSamples,
		FailedTraceIDSamples: failedTraceIDSamples,
	}

	durations := make([]time.Duration, 0, total)
	for _, stat := range stats {
		if stat == nil {
			continue
		}
		durations = append(durations, stat.durations...)
	}
	if len(durations) == 0 {
		populateDerivedSummary(&summary)
		return summary
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	summary.AvgLatency = time.Duration(int64(sum) / int64(len(durations)))
	summary.P95Latency = durations[int(float64(len(durations)-1)*0.95)]
	populateDerivedSummary(&summary)
	return summary
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

type InstrumentedBatchExporter struct {
	inner model.BatchExporter
	stats *Stats
}

func NewInstrumentedBatchExporter(inner model.BatchExporter, stats *Stats) *InstrumentedBatchExporter {
	return &InstrumentedBatchExporter{inner: inner, stats: stats}
}

func (e *InstrumentedBatchExporter) ExportBatch(ctx context.Context, batch model.Batch) error {
	start := time.Now()
	err := e.inner.ExportBatch(ctx, batch)
	if e.stats != nil {
		e.stats.RecordBatchWithTraceIDs(time.Since(start), err, nil, len(batch))
	}
	return err
}

func (e *InstrumentedBatchExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

func populateDerivedSummary(summary *Summary) {
	if summary == nil {
		return
	}
	if summary.Total > 0 {
		summary.AverageSpansPerRequest = float64(summary.TotalSpans) / float64(summary.Total)
	}
	if summary.WallTime > 0 {
		seconds := summary.WallTime.Seconds()
		if seconds > 0 {
			summary.RequestsPerSecond = float64(summary.Total) / seconds
			summary.SuccessfulRequestsPerSecond = float64(summary.Successes) / seconds
			summary.SpansPerSecond = float64(summary.TotalSpans) / seconds
			summary.SuccessfulSpansPerSecond = float64(summary.SuccessfulSpans) / seconds
		}
	}
}

func FormatSummary(summary Summary) string {
	lines := []string{
		fmt.Sprintf("Sent %s requests", formatCount(summary.Total)),
		fmt.Sprintf("Success: %s", formatCount(summary.Successes)),
		fmt.Sprintf("Failures: %s", formatCount(summary.Failures)),
	}
	if summary.WallTime > 0 {
		lines = append(lines,
			fmt.Sprintf("Wall time: %s", formatWallTime(summary.WallTime)),
			fmt.Sprintf("Request rate: %s req/s", formatRate(summary.RequestsPerSecond)),
			fmt.Sprintf("Successful request rate: %s req/s", formatRate(summary.SuccessfulRequestsPerSecond)),
		)
	}
	if summary.TotalSpans > 0 || summary.SuccessfulSpans > 0 || summary.FailedSpans > 0 {
		lines = append(lines,
			fmt.Sprintf("Attempted spans: %s", formatCount(summary.TotalSpans)),
			fmt.Sprintf("Successful spans: %s", formatCount(summary.SuccessfulSpans)),
		)
		if summary.FailedSpans > 0 {
			lines = append(lines, fmt.Sprintf("Failed spans: %s", formatCount(summary.FailedSpans)))
		}
		if summary.WallTime > 0 {
			lines = append(lines,
				fmt.Sprintf("Span rate: %s spans/s", formatRate(summary.SpansPerSecond)),
				fmt.Sprintf("Successful span rate: %s spans/s", formatRate(summary.SuccessfulSpansPerSecond)),
			)
		}
		if summary.AverageSpansPerRequest > 0 {
			lines = append(lines, fmt.Sprintf("Avg spans/request: %.1f", summary.AverageSpansPerRequest))
		}
	}
	lines = append(lines,
		fmt.Sprintf("Avg latency: %s", formatLatency(summary.AvgLatency)),
		fmt.Sprintf("P95 latency: %s", formatLatency(summary.P95Latency)),
	)

	if summary.Failures > 0 && len(summary.FailureBreakdown) > 0 {
		keys := make([]string, 0, len(summary.FailureBreakdown))
		for key := range summary.FailureBreakdown {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		lines = append(lines, "Failure breakdown:")
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("  - %s: %s", key, formatCount(summary.FailureBreakdown[key])))
			samples := summary.FailureSamples[key]
			for _, sample := range samples {
				lines = append(lines, fmt.Sprintf("    sample: %s", sample))
			}
		}
	}

	if len(summary.TraceIDSamples) > 0 {
		lines = append(lines, fmt.Sprintf("Trace ID samples (%d):", len(summary.TraceIDSamples)))
		for _, traceID := range summary.TraceIDSamples {
			lines = append(lines, fmt.Sprintf("  - %s", traceID))
		}
	}
	if len(summary.FailedTraceIDSamples) > 0 {
		lines = append(lines, fmt.Sprintf("Failed trace ID samples (%d):", len(summary.FailedTraceIDSamples)))
		for _, traceID := range summary.FailedTraceIDSamples {
			lines = append(lines, fmt.Sprintf("  - %s", traceID))
		}
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
	if count >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(count)/1_000_000)
	}
	if count >= 1_000 {
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

func formatRate(value float64) string {
	if value >= 1_000_000 {
		return fmt.Sprintf("%.1fM", value/1_000_000)
	}
	if value >= 1_000 {
		return fmt.Sprintf("%.1fk", value/1_000)
	}
	return fmt.Sprintf("%.1f", value)
}

func formatWallTime(duration time.Duration) string {
	if duration <= 0 {
		return "0s"
	}
	return fmt.Sprintf("%.3fs", duration.Seconds())
}

func cloneBreakdown(in map[string]int) map[string]int {
	if len(in) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneSamples(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(in))
	for key, samples := range in {
		out[key] = append([]string(nil), samples...)
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func mergeBreakdown(dst map[string]int, src map[string]int) {
	for key, value := range src {
		dst[key] += value
	}
}

func mergeSamples(dst map[string][]string, src map[string][]string) {
	for key, samples := range src {
		for _, sample := range samples {
			if len(dst[key]) >= maxFailureSamplesPerClass {
				break
			}
			exists := false
			for _, existing := range dst[key] {
				if existing == sample {
					exists = true
					break
				}
			}
			if !exists {
				dst[key] = append(dst[key], sample)
			}
		}
	}
}

func mergeStringSamples(dst []string, src []string, limit int) []string {
	for _, sample := range src {
		if limit > 0 && len(dst) >= limit {
			break
		}
		exists := false
		for _, existing := range dst {
			if existing == sample {
				exists = true
				break
			}
		}
		if !exists {
			dst = append(dst, sample)
		}
	}
	return dst
}

func normalizeErrorMessage(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\n", " | ")
	return strings.Join(strings.Fields(trimmed), " ")
}

func classifyError(err error) string {
	if err == nil {
		return "other"
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}

	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case grpccodes.DeadlineExceeded:
			return "timeout"
		case grpccodes.Canceled:
			return "canceled"
		case grpccodes.Unavailable:
			if strings.Contains(strings.ToLower(st.Message()), "connection refused") {
				return "connection_refused"
			}
			return "unavailable"
		case grpccodes.Unauthenticated:
			return "unauthenticated"
		case grpccodes.PermissionDenied:
			return "permission_denied"
		}
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "timed out"), strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "connection refused"):
		return "connection_refused"
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "name resolution"):
		return "dns"
	case strings.Contains(msg, "x509"), strings.Contains(msg, "tls"), strings.Contains(msg, "certificate"):
		return "tls"
	case strings.Contains(msg, "unauthenticated"), strings.Contains(msg, "unauthorized"), strings.Contains(msg, "401"):
		return "unauthenticated"
	case strings.Contains(msg, "permission denied"), strings.Contains(msg, "403"):
		return "permission_denied"
	case strings.Contains(msg, "unavailable"):
		return "unavailable"
	default:
		return "other"
	}
}

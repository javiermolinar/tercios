package otlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func otlpJSONTestBatch(t *testing.T) model.Batch {
	t.Helper()
	start := time.Date(2026, time.March, 1, 10, 0, 0, 123456789, time.UTC)
	traceState, err := oteltrace.ParseTraceState("vendor=value")
	if err != nil {
		t.Fatal(err)
	}
	root := model.Span{
		TraceID:   oteltrace.TraceID{0x01, 0xab},
		SpanID:    oteltrace.SpanID{0x02, 0xcd},
		Name:      "request",
		Kind:      oteltrace.SpanKindServer,
		StartTime: start,
		EndTime:   start.Add(42 * time.Millisecond),
		Attributes: map[string]attribute.Value{
			"large":   attribute.Int64Value(9007199254740993),
			"zero":    attribute.Int64Value(0),
			"false":   attribute.BoolValue(false),
			"empty":   attribute.StringValue(""),
			"double":  attribute.Float64Value(1.5),
			"ints":    attribute.Int64SliceValue([]int64{0, 9007199254740993}),
			"bools":   attribute.BoolSliceValue([]bool{true, false}),
			"strings": attribute.StringSliceValue([]string{"", "value"}),
			"doubles": attribute.Float64SliceValue([]float64{0, 1.5}),
		},
		ResourceAttributes: map[string]attribute.Value{"service.name": attribute.StringValue("frontend")},
		Events: []model.Event{{
			Name: "exception", Time: start.Add(time.Millisecond),
			Attributes: []attribute.KeyValue{attribute.String("exception.message", "timeout")},
		}},
		Links: []model.Link{{
			SpanContext: oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
				TraceID: oteltrace.TraceID{0x03, 0xef}, SpanID: oteltrace.SpanID{0x04, 0xab}, TraceState: traceState,
			}),
			Attributes: []attribute.KeyValue{attribute.Int64("attempt", 9007199254740993)},
		}},
		StatusCode: codes.Error, StatusDescription: "timeout",
	}
	child := model.Span{
		TraceID: root.TraceID, SpanID: oteltrace.SpanID{0x05}, ParentSpanID: root.SpanID,
		Name: "query", Kind: oteltrace.SpanKindClient, StartTime: start, EndTime: start,
		ResourceAttributes: map[string]attribute.Value{"service.name": attribute.StringValue("backend")},
	}
	return model.Batch{root, child}
}

func decodeOTLPJSONRequest(t *testing.T, data []byte) *collectortracepb.ExportTraceServiceRequest {
	t.Helper()
	request := ptraceotlp.NewExportRequest()
	if err := request.UnmarshalJSON(data); err != nil {
		t.Fatalf("decode OTLP JSON: %v", err)
	}
	wire, err := request.MarshalProto()
	if err != nil {
		t.Fatal(err)
	}
	decoded := &collectortracepb.ExportTraceServiceRequest{}
	if err := proto.Unmarshal(wire, decoded); err != nil {
		t.Fatal(err)
	}
	// pdata represents an unset status as an empty message rather than nil.
	// Normalize that presence-only difference before comparing live payloads.
	for _, resource := range decoded.ResourceSpans {
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				if span.GetStatus().GetCode() == 0 && span.GetStatus().GetMessage() == "" {
					span.Status = nil
				}
			}
		}
	}
	return decoded
}

func TestDryRunOTLPJSONEncoding(t *testing.T) {
	batch := otlpJSONTestBatch(t)
	var out bytes.Buffer
	exporter, err := NewDryRunExporterFactory(DryRunOutputOTLPJSON, &out).NewBatchExporter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := exporter.ExportBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(out.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("expected one compact JSON line, got %q", out.String())
	}

	// Compare against the same protobuf payload used by the live exporter.
	want := &collectortracepb.ExportTraceServiceRequest{ResourceSpans: modelBatchToProto(batch)}
	if got := decodeOTLPJSONRequest(t, out.Bytes()); !proto.Equal(got, want) {
		t.Fatalf("OTLP JSON round-trip differs:\ngot: %v\nwant: %v", got, want)
	}

	// Independently inspect the JSON representation; a protobuf JSON encoder
	// could round-trip while violating OTLP's hex-ID and numeric-enum rules.
	var payload struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Scope struct{ Name string }
				Spans []struct {
					TraceID      string `json:"traceId"`
					SpanID       string `json:"spanId"`
					ParentSpanID string `json:"parentSpanId"`
					Kind         int
					StartTime    string `json:"startTimeUnixNano"`
					EndTime      string `json:"endTimeUnixNano"`
					Attributes   []struct {
						Key   string
						Value struct{ IntValue string }
					}
					Events []struct {
						Time string `json:"timeUnixNano"`
					}
					Links []struct {
						TraceID string `json:"traceId"`
						SpanID  string `json:"spanId"`
					}
					Status struct {
						Code    int
						Message string
					}
				}
			}
		}
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.ResourceSpans) != 2 || len(payload.ResourceSpans[0].ScopeSpans) != 1 || len(payload.ResourceSpans[1].ScopeSpans) != 1 {
		t.Fatalf("unexpected resource/scope grouping: %s", out.String())
	}
	scope := payload.ResourceSpans[0].ScopeSpans[0]
	if scope.Scope.Name != "tercios" || len(scope.Spans) != 1 || len(payload.ResourceSpans[1].ScopeSpans[0].Spans) != 1 {
		t.Fatal("unexpected scope or span count")
	}
	root := scope.Spans[0]
	child := payload.ResourceSpans[1].ScopeSpans[0].Spans[0]
	if root.TraceID != batch[0].TraceID.String() || root.SpanID != batch[0].SpanID.String() || root.ParentSpanID != "" || child.ParentSpanID != root.SpanID {
		t.Fatal("span IDs must be hex strings and preserve parent relationships")
	}
	if root.Kind != 2 || child.Kind != 3 || root.Status.Code != 2 || root.Status.Message != "timeout" {
		t.Fatal("kind/status must use OTLP numeric enums")
	}
	if root.StartTime != fmt.Sprint(batch[0].StartTime.UnixNano()) || root.EndTime != fmt.Sprint(batch[0].EndTime.UnixNano()) {
		t.Fatal("nanosecond timestamps must be decimal strings")
	}
	foundLarge := false
	for _, attr := range root.Attributes {
		if attr.Key == "large" {
			foundLarge = true
			if attr.Value.IntValue != "9007199254740993" {
				t.Fatal("int64 attribute lost precision or was not a decimal string")
			}
		}
	}
	if !foundLarge || len(root.Events) != 1 || root.Events[0].Time != fmt.Sprint(batch[0].Events[0].Time.UnixNano()) || len(root.Links) != 1 {
		t.Fatal("missing attributes, events, or links")
	}
	if root.Links[0].TraceID != batch[0].Links[0].SpanContext.TraceID().String() || root.Links[0].SpanID != batch[0].Links[0].SpanContext.SpanID().String() {
		t.Fatal("link IDs must be hex strings")
	}
}

func TestDryRunOTLPJSONConcurrentBatches(t *testing.T) {
	batch := otlpJSONTestBatch(t)
	var out bytes.Buffer
	factory := NewDryRunExporterFactory(DryRunOutputOTLPJSON, &out)
	const workers, requests = 8, 10
	results := make(chan error, workers)
	for range workers {
		exporter, err := factory.NewBatchExporter(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for range requests {
				if err := exporter.ExportBatch(context.Background(), batch); err != nil {
					results <- err
					return
				}
			}
			results <- exporter.Shutdown(context.Background())
		}()
	}
	for range workers {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	want := &collectortracepb.ExportTraceServiceRequest{ResourceSpans: modelBatchToProto(batch)}
	scanner := bufio.NewScanner(&out)
	count := 0
	for scanner.Scan() {
		if got := decodeOTLPJSONRequest(t, scanner.Bytes()); !proto.Equal(got, want) {
			t.Fatal("concurrent output corrupted a batch")
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != workers*requests {
		t.Fatalf("got %d lines, want %d", count, workers*requests)
	}
}

type otlpJSONTestWriter struct{ err error }

func (w otlpJSONTestWriter) Write(_ []byte) (int, error) { return 0, w.err }

func TestDryRunOTLPJSONEmptyAndWriteErrors(t *testing.T) {
	writeErr := errors.New("write failed")
	for _, want := range []error{writeErr, io.ErrShortWrite} {
		t.Run(want.Error(), func(t *testing.T) {
			var writerErr error
			if want == writeErr {
				writerErr = writeErr
			}
			exporter, err := NewDryRunExporterFactory(DryRunOutputOTLPJSON, otlpJSONTestWriter{err: writerErr}).NewBatchExporter(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := exporter.ExportBatch(context.Background(), nil); err != nil {
				t.Fatalf("empty batch should not write: %v", err)
			}
			if err := exporter.ExportBatch(context.Background(), otlpJSONTestBatch(t)); !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			if err := exporter.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

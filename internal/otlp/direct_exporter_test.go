package otlp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/config"
	"github.com/javiermolinar/tercios/internal/model"
	"github.com/javiermolinar/tercios/internal/scenario"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

type fakeOTLPClient struct {
	uploadErr error
	stopErr   error
	uploaded  []*tracepb.ResourceSpans
}

func (f *fakeOTLPClient) Start(_ context.Context) error {
	return nil
}

func (f *fakeOTLPClient) Stop(_ context.Context) error {
	return f.stopErr
}

func (f *fakeOTLPClient) UploadTraces(_ context.Context, spans []*tracepb.ResourceSpans) error {
	f.uploaded = spans
	return f.uploadErr
}

func TestDirectBatchExporterExactFields(t *testing.T) {
	cfg, err := scenario.DecodeJSON(strings.NewReader(`{"name":"otlp","services":{"svc":{"resource":{"service.name":{"type":"string","value":"svc"},"r":{"type":"int_array","value":[1,2]}}}},
		"nodes":{"a":{"service":"svc","kind":"SERVER","duration_ms":2,"status":{"code":"ERROR","description":" RAW error "},"span_attributes":{"f":{"type":"float","value":1.5}},
		"span_events":[{"name":"event","attributes":{"ok":{"type":"bool","value":false}}}],"span_links":[{"node":"z","attributes":{"attempt":{"type":"int","value":7}}}]},
		"b":{"service":"svc","kind":"CLIENT","duration_ms":0,"status":{"code":"OK"}},"z":{"service":"svc","parent":null}},
		"edges":[{"from":"a","to":"b"},{"from":"z"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	d, err := cfg.Build()
	if err != nil {
		t.Fatal(err)
	}
	batch, err := scenario.NewGenerator(d).GenerateBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []config.Protocol{config.ProtocolGRPC, config.ProtocolHTTP} {
		t.Run(string(protocol), func(t *testing.T) {
			client := &fakeOTLPClient{}
			exporter := &directBatchExporter{client: client, protocol: protocol}
			if err := exporter.ExportBatch(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
			if len(client.uploaded) != 1 || len(client.uploaded[0].ScopeSpans[0].Spans) != 3 {
				t.Fatal("OTLP count differs")
			}
			resource := client.uploaded[0].Resource.Attributes
			if resource[0].Key != "r" || resource[0].Value.GetArrayValue().Values[1].GetIntValue() != 2 || resource[1].Value.GetStringValue() != "svc" {
				t.Fatal("typed resource differs")
			}
			spans := client.uploaded[0].ScopeSpans[0].Spans
			for i, span := range spans {
				if !bytes.Equal(span.TraceId, batch[i].TraceID[:]) || !bytes.Equal(span.SpanId, batch[i].SpanID[:]) || span.Name != batch[i].Name ||
					span.StartTimeUnixNano != uint64(batch[i].StartTime.UnixNano()) || span.EndTimeUnixNano != uint64(batch[i].EndTime.UnixNano()) {
					t.Fatal("OTLP identity/timing differs")
				}
			}
			a, b, z := spans[0], spans[1], spans[2]
			if a.Kind != tracepb.Span_SPAN_KIND_SERVER || a.Status.Code != tracepb.Status_STATUS_CODE_ERROR || a.Status.Message != " RAW error " || a.Attributes[0].Value.GetDoubleValue() != 1.5 ||
				b.Kind != tracepb.Span_SPAN_KIND_CLIENT || b.Status.Code != tracepb.Status_STATUS_CODE_OK || b.StartTimeUnixNano != b.EndTimeUnixNano || !bytes.Equal(b.ParentSpanId, a.SpanId) ||
				z.Kind != tracepb.Span_SPAN_KIND_UNSPECIFIED || z.Status != nil || len(z.ParentSpanId) != 0 {
				t.Fatal("OTLP native fields/topology differs")
			}
			if len(a.Events) != 1 || a.Events[0].Name != "event" || a.Events[0].TimeUnixNano != a.StartTimeUnixNano+1000000 ||
				a.Events[0].Attributes[0].Value.GetBoolValue() || len(a.Links) != 1 || !bytes.Equal(a.Links[0].SpanId, z.SpanId) ||
				!bytes.Equal(a.Links[0].TraceId, z.TraceId) || a.Links[0].Attributes[0].Value.GetIntValue() != 7 {
				t.Fatal("OTLP event/link differs")
			}
			if _, ok := a.Events[0].Attributes[0].Value.Value.(*commonpb.AnyValue_BoolValue); !ok {
				t.Fatal("false event attribute lost its boolean type")
			}
		})
	}
}

func TestDirectBatchExporterWrapsUploadErrorWithDiagnostics(t *testing.T) {
	client := &fakeOTLPClient{uploadErr: context.DeadlineExceeded}
	exporter := &directBatchExporter{
		client:   client,
		protocol: config.ProtocolHTTP,
		endpoint: "http://localhost:4318/v1/traces",
	}

	now := time.Now()
	batch := model.Batch{{
		TraceID:    oteltrace.TraceID{0x01},
		SpanID:     oteltrace.SpanID{0x02},
		Name:       "span",
		Kind:       oteltrace.SpanKindInternal,
		StartTime:  now,
		EndTime:    now.Add(5 * time.Millisecond),
		StatusCode: codes.Ok,
	}}

	err := exporter.ExportBatch(context.Background(), batch)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	message := err.Error()
	if !strings.Contains(message, "protocol=http") {
		t.Fatalf("expected protocol in error message, got %q", message)
	}
	if !strings.Contains(message, "endpoint=http://localhost:4318/v1/traces") {
		t.Fatalf("expected endpoint in error message, got %q", message)
	}
}

func TestDirectBatchExporterShutdownWrapsErrorWithDiagnostics(t *testing.T) {
	exporter := &directBatchExporter{
		client:   &fakeOTLPClient{stopErr: errors.New("shutdown failed")},
		protocol: config.ProtocolGRPC,
		endpoint: "localhost:4317",
	}

	err := exporter.Shutdown(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	message := err.Error()
	if !strings.Contains(message, "protocol=grpc") {
		t.Fatalf("expected protocol in error message, got %q", message)
	}
	if !strings.Contains(message, "endpoint=localhost:4317") {
		t.Fatalf("expected endpoint in error message, got %q", message)
	}
}

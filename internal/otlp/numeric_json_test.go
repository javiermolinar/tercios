package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/javiermolinar/tercios/internal/config"
	"github.com/javiermolinar/tercios/internal/scenario"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Distinct placeholders let rejection tests isolate each attribute location.
func numericScenarioTemplate(mode string) string {
	fields := `"span_attributes":SPAN,"span_events":[{"name":"event","attributes":EVENT}],"span_links":[{"node":"a","attributes":LINK}]`
	prefix := `{"name":"numbers","services":{"svc":{"resource":RESOURCE}},`
	if mode == "exact" {
		return prefix + `"nodes":{"a":{"service":"svc","span_name":"target",` + fields + `}},"edges":[{"from":"a"}]}`
	}
	return prefix + `"root":"a","nodes":{"a":{"service":"svc"},"b":{"service":"svc","span_name":"target"}},"edges":[{"from":"a","to":"b","kind":"internal","repeat":1,"duration_ms":1,` + fields + `}]}`
}

func TestJSONIntegerFidelityThroughExporters(t *testing.T) {
	attrs := `{"precision":{"type":"int","value":9007199254740993},"max":{"type":"int","value":9223372036854775807},"min":{"type":"int","value":-9223372036854775808},"array":{"type":"int_array","value":[9007199254740993,9223372036854775807,-9223372036854775808]}}`
	want := map[string]int64{"precision": 9007199254740993, "max": math.MaxInt64, "min": math.MinInt64}
	wantArray := []int64{9007199254740993, math.MaxInt64, math.MinInt64}
	checkProto := func(t *testing.T, location string, attributes []*commonpb.KeyValue) {
		t.Helper()
		values := make(map[string]*commonpb.AnyValue)
		for _, kv := range attributes {
			values[kv.Key] = kv.Value
		}
		for key, expected := range want {
			if got := values[key].GetIntValue(); got != expected {
				t.Fatalf("%s %s=%d, want %d", location, key, got, expected)
			}
		}
		array := values["array"].GetArrayValue().GetValues()
		if len(array) != len(wantArray) {
			t.Fatalf("%s array length=%d", location, len(array))
		}
		for i, expected := range wantArray {
			if got := array[i].GetIntValue(); got != expected {
				t.Fatalf("%s array[%d]=%d, want %d", location, i, got, expected)
			}
		}
	}
	for _, mode := range []string{"exact", "call-expansion"} {
		t.Run(mode, func(t *testing.T) {
			input := strings.NewReplacer("RESOURCE", attrs, "SPAN", attrs, "EVENT", attrs, "LINK", attrs).Replace(numericScenarioTemplate(mode))
			cfg, err := scenario.DecodeJSON(strings.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			definition, err := cfg.Build()
			if err != nil {
				t.Fatal(err)
			}
			batch, err := scenario.NewGenerator(definition).GenerateBatch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, protocol := range []config.Protocol{config.ProtocolGRPC, config.ProtocolHTTP} {
				client := &fakeOTLPClient{}
				exporter := &directBatchExporter{client: client, protocol: protocol}
				if err := exporter.ExportBatch(context.Background(), batch); err != nil {
					t.Fatal(err)
				}
				if len(client.uploaded) != 1 {
					t.Fatalf("resource groups=%d", len(client.uploaded))
				}
				checkProto(t, "resource", client.uploaded[0].Resource.Attributes)
				var target *tracepb.Span
				for _, span := range client.uploaded[0].ScopeSpans[0].Spans {
					if span.Name == "target" {
						target = span
					}
				}
				if target == nil || len(target.Events) != 1 || len(target.Links) != 1 {
					t.Fatal("missing target, event or link")
				}
				checkProto(t, "span", target.Attributes)
				checkProto(t, "event", target.Events[0].Attributes)
				checkProto(t, "link", target.Links[0].Attributes)
			}
			var output bytes.Buffer
			exporter, err := NewDryRunExporterFactory(DryRunOutputJSON, &output).NewBatchExporter(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := exporter.ExportBatch(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(&output)
			decoder.UseNumber() // Do not introduce rounding in the assertion itself.
			var payload jsonBatch
			if err := decoder.Decode(&payload); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, span := range payload.Spans {
				if span.Name != "target" {
					continue
				}
				found = true
				if len(span.Events) != 1 || len(span.Links) != 1 {
					t.Fatal("missing JSON event or link")
				}
				for _, values := range []map[string]any{span.Resource, span.Attributes, span.Events[0].Attributes, span.Links[0].Attributes} {
					for key, expected := range want {
						if got, ok := values[key].(json.Number); !ok || got.String() != fmt.Sprint(expected) {
							t.Fatalf("JSON %s=%v, want %d", key, values[key], expected)
						}
					}
					array, ok := values["array"].([]any)
					if !ok || len(array) != len(wantArray) {
						t.Fatal("missing JSON integer array")
					}
					for i, expected := range wantArray {
						if got, ok := array[i].(json.Number); !ok || got.String() != fmt.Sprint(expected) {
							t.Fatalf("JSON array[%d]=%v, want %d", i, array[i], expected)
						}
					}
				}
			}
			if !found {
				t.Fatal("missing JSON target span")
			}
		})
	}
}

func TestJSONIntegerAttributesRejectInvalidValues(t *testing.T) {
	for _, mode := range []string{"exact", "call-expansion"} {
		for _, location := range []string{"RESOURCE", "SPAN", "EVENT", "LINK"} {
			for _, kind := range []string{"int", "int_array"} {
				for _, raw := range []string{"9223372036854775808", "-9223372036854775809", "9007199254740993.0001"} {
					t.Run(mode+"/"+location+"/"+kind+"/"+raw, func(t *testing.T) {
						if kind == "int_array" {
							raw = "[1," + raw + "]"
						}
						attrs := fmt.Sprintf(`{"bad":{"type":%q,"value":%s}}`, kind, raw)
						input := strings.ReplaceAll(numericScenarioTemplate(mode), location, attrs)
						input = strings.NewReplacer("RESOURCE", "{}", "SPAN", "{}", "EVENT", "{}", "LINK", "{}").Replace(input)
						if _, err := scenario.DecodeJSON(strings.NewReader(input)); err == nil {
							t.Fatal("accepted invalid integer attribute")
						}
					})
				}
			}
		}
	}
}

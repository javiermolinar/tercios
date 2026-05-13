package scenario

import (
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

func TestDecodeJSONAndBuildValidScenario(t *testing.T) {
	input := `{
  "name": "post-service-graph",
  "seed": 42,
  "services": {
    "frontend": {
      "resource": {
        "service.name": { "type": "string", "value": "frontend" }
      }
    },
    "post": {
      "resource": {
        "service.name": { "type": "string", "value": "post-service" }
      }
    },
    "db": {
      "resource": {
        "service.name": { "type": "string", "value": "postgres" }
      }
    }
  },
  "nodes": {
    "a": { "service": "frontend", "span_name": "GET /posts" },
    "b": { "service": "post", "span_name": "POST /posts" },
    "c": { "service": "db", "span_name": "SELECT posts" }
  },
  "root": "a",
  "edges": [
    {
      "from": "a",
      "to": "b",
      "kind": "client_server",
      "repeat": 50,
      "duration_ms": 120,
      "span_attributes": {
        "http.response.status_code": { "type": "int", "value": 200 }
      }
    },
    {
      "from": "b",
      "to": "c",
      "kind": "client_database",
      "repeat": 1,
      "duration_ms": 20
    }
  ]
}`

	cfg, err := DecodeJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}

	definition, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if definition.Name != "post-service-graph" {
		t.Fatalf("expected name post-service-graph, got %q", definition.Name)
	}
	if len(definition.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(definition.Edges))
	}
	if definition.Edges[0].Duration != 120*time.Millisecond {
		t.Fatalf("expected first edge duration 120ms, got %s", definition.Edges[0].Duration)
	}
	status := definition.Edges[0].SpanAttributes["http.response.status_code"]
	if status.Type() != attribute.INT64 || status.AsInt64() != 200 {
		t.Fatalf("expected http.response.status_code int64=200, got %s/%s", status.Type(), status.Emit())
	}
}

func TestDecodeJSONRejectsCycle(t *testing.T) {
	input := `{
  "name": "cycle",
  "services": {
    "a": { "resource": { "service.name": { "type": "string", "value": "a" } } },
    "b": { "resource": { "service.name": { "type": "string", "value": "b" } } }
  },
  "nodes": {
    "n1": { "service": "a", "span_name": "n1" },
    "n2": { "service": "b", "span_name": "n2" }
  },
  "root": "n1",
  "edges": [
    { "from": "n1", "to": "n2", "kind": "client_server", "repeat": 1, "duration_ms": 10 },
    { "from": "n2", "to": "n1", "kind": "client_server", "repeat": 1, "duration_ms": 10 }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected error for cycle, got nil")
	}
}

func TestDecodeJSONWithArrayAttributes(t *testing.T) {
	input := `{
  "name": "array-test",
  "seed": 1,
  "services": {
    "svc": { "resource": { "service.name": { "type": "string", "value": "svc" } } }
  },
  "nodes": {
    "a": { "service": "svc", "span_name": "A" },
    "b": { "service": "svc", "span_name": "B" }
  },
  "root": "a",
  "edges": [
    {
      "from": "a",
      "to": "b",
      "kind": "internal",
      "repeat": 1,
      "duration_ms": 10,
      "span_attributes": {
        "http.methods": { "type": "string_array", "value": ["GET", "POST"] },
        "retry.codes":  { "type": "int_array",    "value": [200, 503] },
        "scores":       { "type": "float_array",  "value": [1.1, 2.2] },
        "flags":        { "type": "bool_array",   "value": [true, false] }
      }
    }
  ]
}`

	cfg, err := DecodeJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}

	definition, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	attrs := definition.Edges[0].SpanAttributes

	methods := attrs["http.methods"]
	if methods.Type() != attribute.STRINGSLICE {
		t.Fatalf("expected STRINGSLICE, got %s", methods.Type())
	}
	if got := methods.AsStringSlice(); len(got) != 2 || got[0] != "GET" || got[1] != "POST" {
		t.Fatalf("unexpected string_array value: %v", got)
	}

	codes := attrs["retry.codes"]
	if codes.Type() != attribute.INT64SLICE {
		t.Fatalf("expected INT64SLICE, got %s", codes.Type())
	}
	if got := codes.AsInt64Slice(); len(got) != 2 || got[0] != 200 || got[1] != 503 {
		t.Fatalf("unexpected int_array value: %v", got)
	}

	scores := attrs["scores"]
	if scores.Type() != attribute.FLOAT64SLICE {
		t.Fatalf("expected FLOAT64SLICE, got %s", scores.Type())
	}
	if got := scores.AsFloat64Slice(); len(got) != 2 || got[0] != 1.1 || got[1] != 2.2 {
		t.Fatalf("unexpected float_array value: %v", got)
	}

	flags := attrs["flags"]
	if flags.Type() != attribute.BOOLSLICE {
		t.Fatalf("expected BOOLSLICE, got %s", flags.Type())
	}
	if got := flags.AsBoolSlice(); len(got) != 2 || got[0] != true || got[1] != false {
		t.Fatalf("unexpected bool_array value: %v", got)
	}
}

func TestDecodeJSONWithStringSizeAttribute(t *testing.T) {
	input := `{
  "name": "blob-test",
  "seed": 1,
  "services": {
    "svc": { "resource": { "service.name": { "type": "string", "value": "svc" } } }
  },
  "nodes": {
    "a": { "service": "svc", "span_name": "A" },
    "b": { "service": "svc", "span_name": "B" }
  },
  "root": "a",
  "edges": [
    {
      "from": "a",
      "to": "b",
      "kind": "internal",
      "repeat": 1,
      "duration_ms": 10,
      "span_attributes": {
        "http.request.body": { "type": "string", "size": 2048 }
      }
    }
  ]
}`

	cfg, err := DecodeJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}

	definition, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	body := definition.Edges[0].SpanAttributes["http.request.body"]
	if body.Type() != attribute.STRING {
		t.Fatalf("expected STRING, got %s", body.Type())
	}
	if len(body.AsString()) != 2048 {
		t.Fatalf("expected 2048 bytes, got %d", len(body.AsString()))
	}
}

func TestDecodeJSONWithEventsAndLinks(t *testing.T) {
	input := `{
  "name": "events-links-test",
  "seed": 1,
  "services": {
    "svc": { "resource": { "service.name": { "type": "string", "value": "svc" } } }
  },
  "nodes": {
    "a": { "service": "svc", "span_name": "A" },
    "b": { "service": "svc", "span_name": "B" },
    "c": { "service": "svc", "span_name": "C" }
  },
  "root": "a",
  "edges": [
    {
      "from": "a",
      "to": "b",
      "kind": "internal",
      "repeat": 1,
      "duration_ms": 10,
      "span_events": [
        {
          "name": "cache.miss",
          "attributes": {
            "cache.key": { "type": "string", "value": "items:list" }
          }
        }
      ],
      "span_links": [
        {
          "node": "a",
          "attributes": {
            "link.type": { "type": "string", "value": "follows_from" }
          }
        }
      ]
    },
    {
      "from": "b",
      "to": "c",
      "kind": "internal",
      "repeat": 1,
      "duration_ms": 5
    }
  ]
}`

	cfg, err := DecodeJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}

	definition, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if len(definition.Edges[0].SpanEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(definition.Edges[0].SpanEvents))
	}
	if definition.Edges[0].SpanEvents[0].Name != "cache.miss" {
		t.Fatalf("expected event name cache.miss, got %q", definition.Edges[0].SpanEvents[0].Name)
	}
	if len(definition.Edges[0].SpanLinks) != 1 {
		t.Fatalf("expected 1 link, got %d", len(definition.Edges[0].SpanLinks))
	}
	if definition.Edges[0].SpanLinks[0].Node != "a" {
		t.Fatalf("expected link node a, got %q", definition.Edges[0].SpanLinks[0].Node)
	}
}

func TestDecodeJSONRejectsLinkToUnknownNode(t *testing.T) {
	input := `{
  "name": "bad-link",
  "seed": 1,
  "services": {
    "svc": { "resource": { "service.name": { "type": "string", "value": "svc" } } }
  },
  "nodes": {
    "a": { "service": "svc", "span_name": "A" },
    "b": { "service": "svc", "span_name": "B" }
  },
  "root": "a",
  "edges": [
    {
      "from": "a",
      "to": "b",
      "kind": "internal",
      "repeat": 1,
      "duration_ms": 10,
      "span_links": [{ "node": "nonexistent" }]
    }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected error for link to unknown node")
	}
}

func TestDecodeJSONRejectsEventWithoutName(t *testing.T) {
	input := `{
  "name": "bad-event",
  "seed": 1,
  "services": {
    "svc": { "resource": { "service.name": { "type": "string", "value": "svc" } } }
  },
  "nodes": {
    "a": { "service": "svc", "span_name": "A" },
    "b": { "service": "svc", "span_name": "B" }
  },
  "root": "a",
  "edges": [
    {
      "from": "a",
      "to": "b",
      "kind": "internal",
      "repeat": 1,
      "duration_ms": 10,
      "span_events": [{ "name": "" }]
    }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected error for event without name")
	}
}

func TestDecodeJSONRejectsUnknownNodeService(t *testing.T) {
	input := `{
  "name": "unknown-service",
  "services": {
    "frontend": { "resource": { "service.name": { "type": "string", "value": "frontend" } } }
  },
  "nodes": {
    "a": { "service": "frontend", "span_name": "A" },
    "b": { "service": "post", "span_name": "B" }
  },
  "root": "a",
  "edges": [
    { "from": "a", "to": "b", "kind": "client_server", "repeat": 1, "duration_ms": 10 }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected error for unknown node service, got nil")
	}
}

func TestDecodeJSONRejectsRootWithIncomingEdges(t *testing.T) {
	// gateway is declared as root, but the api -> gateway edge gives it
	// an incoming edge. The walker would silently ignore that edge and
	// api would become orphaned. validateDAG must reject.
	input := `{
  "name": "root-with-incoming",
  "services": { "s": { "resource": { "service.name": { "type": "string", "value": "s" } } } },
  "nodes": {
    "gateway": { "service": "s", "span_name": "G" },
    "api":     { "service": "s", "span_name": "A" }
  },
  "root": "gateway",
  "edges": [
    { "from": "gateway", "to": "api", "kind": "internal", "repeat": 1, "duration_ms": 10 },
    { "from": "api", "to": "gateway", "kind": "internal", "repeat": 1, "duration_ms": 10 }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected error for root with incoming edge, got nil")
	}
	if !strings.Contains(err.Error(), "must have no incoming edges") {
		t.Fatalf("expected error mentioning incoming edges, got %v", err)
	}
}

func TestDecodeJSONRejectsUnreachableNodes(t *testing.T) {
	// 'orphan' is defined but never referenced as an edge target from root.
	// The walker never visits it, producing a smaller trace than the user
	// expects. validateDAG must reject and name the unreachable nodes so
	// the user can fix the typo.
	input := `{
  "name": "with-orphan",
  "services": { "s": { "resource": { "service.name": { "type": "string", "value": "s" } } } },
  "nodes": {
    "root":   { "service": "s", "span_name": "R" },
    "child":  { "service": "s", "span_name": "C" },
    "orphan": { "service": "s", "span_name": "O" }
  },
  "root": "root",
  "edges": [
    { "from": "root", "to": "child", "kind": "internal", "repeat": 1, "duration_ms": 10 }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected error for unreachable node, got nil")
	}
	if !strings.Contains(err.Error(), "not reachable from root") {
		t.Fatalf("expected error mentioning unreachable nodes, got %v", err)
	}
	if !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("expected error naming the orphan node, got %v", err)
	}
}

func TestDecodeJSONRejectsUnreachableSubgraph(t *testing.T) {
	// Two unrelated nodes 'x' and 'y' connected to each other but not to
	// root. validateDAG must report BOTH as unreachable, sorted.
	input := `{
  "name": "split-graph",
  "services": { "s": { "resource": { "service.name": { "type": "string", "value": "s" } } } },
  "nodes": {
    "root": { "service": "s", "span_name": "R" },
    "a":    { "service": "s", "span_name": "A" },
    "x":    { "service": "s", "span_name": "X" },
    "y":    { "service": "s", "span_name": "Y" }
  },
  "root": "root",
  "edges": [
    { "from": "root", "to": "a", "kind": "internal", "repeat": 1, "duration_ms": 10 },
    { "from": "x", "to": "y", "kind": "internal", "repeat": 1, "duration_ms": 10 }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected error for unreachable subgraph, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x") || !strings.Contains(msg, "y") {
		t.Fatalf("expected error naming both unreachable nodes x and y, got %v", err)
	}
	// Orphans should be sorted for stable output.
	xIdx := strings.Index(msg, "x")
	yIdx := strings.Index(msg, "y")
	if xIdx == -1 || yIdx == -1 || xIdx > yIdx {
		t.Fatalf("expected x to precede y in orphans list, got %v", err)
	}
}

func TestDecodeJSONLatencyErrorIncludesSubtreeContext(t *testing.T) {
	// When the latency check fails, the error message must explain the
	// consequence in terms the user can act on: effective duration,
	// subtree under the target, and resulting server interval.
	input := `{
  "name": "latency-error-context",
  "services": { "s": { "resource": { "service.name": { "type": "string", "value": "s" } } } },
  "nodes": {
    "a": { "service": "s", "span_name": "A" },
    "b": { "service": "s", "span_name": "B" },
    "c": { "service": "s", "span_name": "C" }
  },
  "root": "a",
  "edges": [
    { "from": "a", "to": "b", "kind": "client_server", "repeat": 1, "duration_ms": 10, "network_latency_ms": 5 },
    { "from": "b", "to": "c", "kind": "internal", "repeat": 1, "duration_ms": 5 }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected timing validation error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"effective span duration",
		"server interval",
		`subtree("b")`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}

func TestDecodeJSONNetworkLatencyValidation(t *testing.T) {
	base := func(kind, extra string) string {
		return `{
  "name": "latency-test",
  "services": { "s": { "resource": { "service.name": { "type": "string", "value": "s" } } } },
  "nodes": {
    "a": { "service": "s", "span_name": "A" },
    "b": { "service": "s", "span_name": "B" }
  },
  "root": "a",
  "edges": [
    { "from": "a", "to": "b", "kind": "` + kind + `", "repeat": 1, "duration_ms": 10` + extra + ` }
  ]
}`
	}

	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantMsg string
	}{
		{
			name:    "valid pair edge with small latency",
			input:   base("client_server", `, "network_latency_ms": 2`),
			wantErr: false,
		},
		{
			name:    "zero latency on pair edge is fine",
			input:   base("client_server", `, "network_latency_ms": 0`),
			wantErr: false,
		},
		{
			name:    "latency unset on pair edge is fine",
			input:   base("client_server", ``),
			wantErr: false,
		},
		{
			name:    "negative latency rejected",
			input:   base("client_server", `, "network_latency_ms": -1`),
			wantErr: true,
			wantMsg: "network_latency_ms must be >= 0",
		},
		{
			name:    "latency on internal edge rejected",
			input:   base("internal", `, "network_latency_ms": 1`),
			wantErr: true,
			wantMsg: "network_latency_ms is not supported on internal edges",
		},
		{
			name:    "latency too large rejected",
			input:   base("client_server", `, "network_latency_ms": 5`),
			wantErr: true,
			wantMsg: "duration_ms > 2*network_latency_ms",
		},
		{
			name:    "latency exactly half duration rejected (boundary)",
			input:   base("client_server", `, "network_latency_ms": 5`),
			wantErr: true,
			wantMsg: "duration_ms > 2*network_latency_ms",
		},
		{
			name:    "latency on producer_consumer accepted",
			input:   base("producer_consumer", `, "network_latency_ms": 3`),
			wantErr: false,
		},
		{
			name:    "latency on client_database accepted",
			input:   base("client_database", `, "network_latency_ms": 2`),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeJSON(strings.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
					t.Fatalf("expected error containing %q, got %v", tt.wantMsg, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

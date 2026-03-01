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

# Scenarios

Tercios generates deterministic traces from scenario topology definitions. When no `--scenario-file` is provided, a built-in default scenario is used (5-service web app: gateway → api → cache + db + async worker). See the [embedded default](../internal/scenario/default_scenario.json) for the full definition.

Use `--scenario-file` (or `-s`) to provide custom scenarios.

## Quick start

```bash
# Uses embedded default scenario
go run ./cmd/tercios --dry-run -o json 2>/dev/null

# Uses custom scenario
go run ./cmd/tercios \
  --scenario-file=my-scenario.json \
  --dry-run -o json \
  --exporters=1 \
  --max-requests=1 \
  2>/dev/null
```

## CLI flags

| Flag | Description |
|---|---|
| `--scenario-file`, `-s` | Path to scenario JSON file (repeatable for multiple scenarios) |
| `--scenario-strategy` | Selection strategy when multiple files are provided: `round-robin` (default) or `random` |
| `--scenario-run-seed` | Trace/span ID namespace (`0` = auto-random per process, non-zero = reproducible across runs) |

All execution knobs still apply: `--exporters`, `--max-requests`, `--for`, `--request-interval`, `--ramp-up`.

Chaos can be composed on top of scenarios with `--chaos-policies-file` (see [chaos.md](chaos.md)).

## Scenario config format

```json
{
  "name": "my-scenario",
  "seed": 42,
  "services": { ... },
  "nodes": { ... },
  "root": "node-id",
  "edges": [ ... ]
}
```

### Top-level fields

| Field | Type | Description |
|---|---|---|
| `name` | string | **Required.** Scenario identifier |
| `seed` | int | Random seed for deterministic trace/span ID generation |
| `services` | map | **Required.** Service definitions keyed by service ID |
| `nodes` | map | **Required.** Node (span) definitions keyed by node ID |
| `root` | string | **Required.** ID of the root node |
| `edges` | array | **Required.** At least one edge connecting nodes |

### Services

Each service defines resource attributes attached to all spans from that service.

```json
{
  "services": {
    "frontend": {
      "resource": {
        "service.name": {"type": "string", "value": "frontend"},
        "service.version": {"type": "string", "value": "2.10.0"}
      }
    }
  }
}
```

Resource attribute values use [typed values](typed-values.md).

### Nodes

Each node represents a span template within a service.

```json
{
  "nodes": {
    "a": {"service": "frontend", "span_name": "GET /posts"},
    "b": {"service": "post", "span_name": "POST /posts"}
  }
}
```

| Field | Type | Description |
|---|---|---|
| `service` | string | **Required.** References a service ID |
| `span_name` | string | Span name (defaults to the node ID if empty) |

### Edges

Edges define the call graph between nodes.

```json
{
  "edges": [
    {
      "from": "a",
      "to": "b",
      "kind": "client_server",
      "repeat": 1,
      "duration_ms": 100,
      "span_attributes": {
        "http.method": {"type": "string", "value": "POST"},
        "http.response.status_code": {"type": "int", "value": 200}
      }
    }
  ]
}
```

| Field | Type | Description |
|---|---|---|
| `from` | string | **Required.** Source node ID |
| `to` | string | **Required.** Target node ID |
| `kind` | string | **Required.** Edge kind (see below) |
| `repeat` | int | **Required.** Number of times to repeat this call (must be > 0) |
| `duration_ms` | int | **Required.** Span duration in milliseconds (must be > 0) |
| `span_attributes` | map | Optional span attributes using [typed values](typed-values.md) |
| `span_events` | array | Optional span events (see below) |
| `span_links` | array | Optional span links (see below) |

### Edge kinds

| Kind | Spans generated |
|---|---|
| `client_server` | Client span (caller) + Server span (callee) |
| `producer_consumer` | Producer span + Consumer span |
| `client_database` | Client span + Server span (database) |
| `internal` | Single internal span on the target node |

### Span events

Events are things that happened during a span's lifetime.

```json
"span_events": [
  {
    "name": "cache.miss",
    "attributes": {
      "cache.key": {"type": "string", "value": "items:list"}
    }
  }
]
```

| Field | Type | Description |
|---|---|---|
| `name` | string | **Required.** Event name |
| `attributes` | map | Optional event attributes using [typed values](typed-values.md) |

### Span links

Links reference spans from other nodes in the same trace. The linked node must have been visited earlier in the DAG traversal.

```json
"span_links": [
  {
    "node": "gateway",
    "attributes": {
      "link.type": {"type": "string", "value": "follows_from"}
    }
  }
]
```

| Field | Type | Description |
|---|---|---|
| `node` | string | **Required.** Node ID to link to (must exist in `nodes`) |
| `attributes` | map | Optional link attributes using [typed values](typed-values.md) |

### Topology constraints

The node graph must be a **DAG** (directed acyclic graph). Cycles are rejected at validation time.

## Multiple scenarios

Provide multiple `--scenario-file` flags to mix scenarios:

```bash
go run ./cmd/tercios \
  -s scenario-a.json \
  -s scenario-b.json \
  --scenario-strategy=round-robin \
  --dry-run -o json \
  --exporters=1 --max-requests=4 \
  2>/dev/null
```

- `round-robin`: cycles through scenarios in order.
- `random`: picks a random scenario per batch (deterministic when `--scenario-run-seed` is set).

## Minimal example

```json
{
  "name": "simple",
  "seed": 1,
  "services": {
    "svc": {
      "resource": {
        "service.name": {"type": "string", "value": "my-service"}
      }
    }
  },
  "nodes": {
    "root": {"service": "svc", "span_name": "handle-request"},
    "db":   {"service": "svc", "span_name": "query-db"}
  },
  "root": "root",
  "edges": [
    {
      "from": "root",
      "to": "db",
      "kind": "client_database",
      "repeat": 1,
      "duration_ms": 25
    }
  ]
}
```

See also: the [embedded default scenario](../internal/scenario/default_scenario.json) for a complete example.

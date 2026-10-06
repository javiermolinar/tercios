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

Native span fields live on `nodes`. The decoder supports direct node construction,
but **direct span generation is not implemented yet**. Scenario-file setup and
batch/streaming generation reject direct definitions with a clear error until
that support lands. Existing call-expansion examples remain unchanged.

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
| `root` | string | **Required when edges use `kind`.** Otherwise optional; if supplied, must identify a node with no effective parent. It does not add a span or connect other roots. |
| `edges` | array | **Required.** At least one entry; an entry may declare a single node using only `from`. |

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
| `span_name` | string or null | Optional span name; omitted, empty or null values fall back to the node ID. |

The following fields are optional and only valid when all edges omit `kind`.
In that mode each node identifies exactly one span, rather than a reusable
operation template.

| Field | Type | Default / meaning |
|---|---|---|
| `kind` | string | `UNSPECIFIED`. Native span kind: `UNSPECIFIED`, `INTERNAL`, `SERVER`, `CLIENT`, `PRODUCER` or `CONSUMER`; not an edge kind. |
| `parent` | string or null | Omitted: derive the parent from connections. Null: make this node a root. Node ID: override its parent. |
| `status` | object | Optional `code` (`UNSET`, `OK` or `ERROR`, default `UNSET`) and `description` (default empty). A nonempty description requires `ERROR`. |
| `start_offset_ms` | int | 0. Nonnegative offset from the trace start. |
| `duration_ms` | int | 1. Nonnegative total span duration; explicit 0 is preserved. |
| `span_attributes` | map | Empty. Uses [typed values](typed-values.md); resource attributes come from the node's service. |
| `span_events` | array | Empty. Uses the event fields below; event timestamps default to the span midpoint. |
| `span_links` | array | Empty. Uses the link fields below; forward references are allowed. |

For example, direct construction places span fields on the nodes and only
connections on the edges:

```json
{
  "name": "direct",
  "services": {"app": {}},
  "nodes": {
    "a": {"service": "app", "kind": "SERVER", "duration_ms": 20},
    "b": {"service": "app", "kind": "CLIENT", "start_offset_ms": 2, "duration_ms": 5}
  },
  "edges": [{"from": "a", "to": "b"}]
}
```

Each node is configured once; repeated connections do not duplicate spans or
merge definitions. Explicit parent overrides are authoritative. An attribute
named `span.kind` does not set the native `kind` field.

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
| `from` | string | **Required.** References an existing source node. |
| `to` | string | **Required with `kind`.** Otherwise optional; omit it to declare a single node. |
| `kind` | string | Required for call expansion (see below). Omit it to construct nodes directly, without additional caller/root spans. Entries with and without `kind` cannot be mixed. |
| `repeat` | int | **Required with `kind`; must be > 0.** Without `kind`, optional and must be 1 if supplied. |
| `duration_ms` | int | **Required with `kind`; must be > 0.** Edge own-work duration; generated spans also contain the target subtree. Without `kind`, specify total duration on the node. |
| `network_latency_ms` | int | **Optional.** Default 0. Positive values require a paired edge and `2 * network_latency_ms < duration_ms`. Without `kind`, only 0 is allowed. |
| `span_attributes` | map | **Optional with `kind`.** Typed span attributes; otherwise place them on the node. |
| `span_events` | array | **Optional with `kind`.** Span events; otherwise place them on the node. |
| `span_links` | array | **Optional with `kind`.** Span links; otherwise place them on the node. |

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

Links reference spans from nodes in the same trace. For edge-level links, the
linked node must have been visited earlier in the traversal. Node-level links
in direct construction may reference any node constructed in that trace.

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

With edge `kind`, the graph must be a rooted **DAG**: every node is reachable from
`root`, and `root` has no incoming edges. Without `kind`, resolved parent
relationships must form an acyclic forest. Missing parent references and
self-parenting are invalid.

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

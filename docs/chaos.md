# Chaos Testing

Chaos testing in Tercios means mutating generated trace data using policies — status codes, attributes, resource values, and latency — to test downstream behavior and analysis.

This is **not** infrastructure chaos (no pods/nodes/network failures). It is telemetry/trace-shape behavior testing.

## Quick start

```bash
go run ./cmd/tercios \
  --dry-run -o json \
  --chaos-policies-file=my-chaos.json \
  --chaos-seed=42 \
  --exporters=1 \
  --max-requests=10 \
  2>/dev/null
```

## CLI flags

| Flag | Description |
|---|---|
| `--chaos-policies-file` | Path to chaos policy JSON file |
| `--chaos-seed` | Override policy seed for deterministic probability decisions (`0` uses config/default) |

Tips:
- Use `--dry-run -o json` to inspect mutated spans locally before sending to a collector.
- Chaos composes on top of both the embedded default scenario and custom scenarios.
- Match on `service_name` values from your scenario definition (e.g., `"api-service"` for the embedded default).

## Policy config format

```json
{
  "seed": 42,
  "policy_mode": "all",
  "policies": [
    {
      "name": "policy-name",
      "probability": 0.3,
      "match": { ... },
      "actions": [ ... ]
    }
  ]
}
```

### Top-level fields

| Field | Type | Description |
|---|---|---|
| `seed` | int | Random seed for deterministic probability decisions |
| `policy_mode` | string | `"all"` (apply every matching policy) or `"first_match"` (stop after first match) |
| `policies` | array | List of policy definitions |

### Policy fields

| Field | Type | Description |
|---|---|---|
| `name` | string | **Required.** Policy identifier |
| `probability` | float | Probability of applying this policy (`0.0` – `1.0`) |
| `match` | object | Span matching criteria |
| `actions` | array | **Required.** At least one action |

### Match criteria

All match fields are optional. A span must satisfy all specified criteria.

| Field | Type | Description |
|---|---|---|
| `service_name` | string | Match spans from this service |
| `span_name` | string | Match spans with this name |
| `span_kinds` | string array | Match spans of these kinds |
| `attributes` | map | Match spans with these attribute values (uses [typed values](typed-values.md)) |

### Actions

#### `set_status`

Set the span status code.

```json
{
  "type": "set_status",
  "code": "error",
  "message": "simulated failure"
}
```

| Field | Values |
|---|---|
| `code` | `"ok"`, `"error"`, `"unset"` |
| `message` | Optional status description |

#### `set_attribute`

Set a span or resource attribute.

```json
{
  "type": "set_attribute",
  "scope": "span",
  "name": "http.response.status_code",
  "value": {"type": "int", "value": 500}
}
```

| Field | Description |
|---|---|
| `scope` | `"span"` or `"resource"` |
| `name` | Attribute key |
| `value` | [Typed value](typed-values.md) |

#### `add_latency`

Shift span duration by a delta.

```json
{
  "type": "add_latency",
  "delta_ms": 120
}
```

| Field | Description |
|---|---|
| `delta_ms` | Milliseconds to add (positive) or subtract (negative). Zero is a valid no-op |

Latency safety: if the delta would produce a non-positive duration, the span is clamped to `1ms`.

## Full example

```json
{
  "seed": 42,
  "policy_mode": "all",
  "policies": [
    {
      "name": "post-service-errors",
      "probability": 0.3,
      "match": {
        "service_name": "post-service"
      },
      "actions": [
        {
          "type": "set_status",
          "code": "error",
          "message": "simulated failure"
        },
        {
          "type": "set_attribute",
          "scope": "span",
          "name": "http.response.status_code",
          "value": {"type": "int", "value": 500}
        },
        {
          "type": "add_latency",
          "delta_ms": 120
        }
      ]
    },
    {
      "name": "post-service-version-shift",
      "probability": 0.1,
      "match": {
        "service_name": "post-service"
      },
      "actions": [
        {
          "type": "set_attribute",
          "scope": "resource",
          "name": "service.name",
          "value": {"type": "string", "value": "post-service-v2"}
        }
      ]
    }
  ]
}
```

The embedded default scenario uses these service names: `api-gateway`, `api-service`, `redis-cache`, `postgres`, `background-worker`.

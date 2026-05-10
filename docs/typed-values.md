# Typed Values

Tercios uses **typed values** wherever configuration needs an attribute value — scenario span/resource attributes and chaos policy actions. A typed value is a JSON object with a `type` field and a `value` field.

## Scalar types

| Type | `value` field | OTel attribute type | Example |
|---|---|---|---|
| `string` | JSON string | `STRING` | `{"type": "string", "value": "GET"}` |
| `int` | JSON number (integer) | `INT64` | `{"type": "int", "value": 200}` |
| `float` | JSON number | `FLOAT64` | `{"type": "float", "value": 3.14}` |
| `bool` | JSON boolean | `BOOL` | `{"type": "bool", "value": true}` |

## Array types

Array types use a native JSON array in the `value` field. Each element must match the declared element type.

| Type | Element type | OTel attribute type | Example |
|---|---|---|---|
| `string_array` | string | `STRINGSLICE` | `{"type": "string_array", "value": ["GET", "POST"]}` |
| `int_array` | integer | `INT64SLICE` | `{"type": "int_array", "value": [200, 503]}` |
| `float_array` | number | `FLOAT64SLICE` | `{"type": "float_array", "value": [1.1, 2.2]}` |
| `bool_array` | boolean | `BOOLSLICE` | `{"type": "bool_array", "value": [true, false]}` |

Empty arrays are valid: `{"type": "string_array", "value": []}`.

## Generated strings (size)

The `string` type supports an optional `size` field (in bytes) to generate large attribute values for load testing. When `size` is set, the value is produced by tiling a seed string to the exact byte count.

| Fields | Behavior |
|---|---|
| `size` only | Tiles a built-in JSON seed (~880 bytes) to the target size |
| `size` + `value` | Tiles the provided `value` string to the target size |
| `value` only | Literal string (current behavior) |

`size` must be a positive integer.

Examples:

```json
{"type": "string", "size": 4096}
```

Generates a 4 KB string by repeating a realistic JSON log line.

```json
{"type": "string", "value": "ABC", "size": 12}
```

Produces `"ABCABCABCABC"` (tiles `"ABC"` to 12 bytes).

```json
{"type": "string", "value": "hello"}
```

Literal `"hello"` — no generation, same as before.

## Where typed values are used

### Scenario configs (`--scenario-file`)

**Service resource attributes:**

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

**Edge span attributes:**

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
        "http.response.status_code": {"type": "int", "value": 200},
        "http.request.body": {"type": "string", "size": 4096},
        "feature.flags": {"type": "string_array", "value": ["canary", "dark-launch"]}
      }
    }
  ]
}
```

### Chaos policy configs (`--chaos-policies-file`)

**Match attributes:**

```json
{
  "match": {
    "service_name": "post-service",
    "attributes": {
      "http.response.status_code": {"type": "int", "value": 200}
    }
  }
}
```

**`set_attribute` action values:**

```json
{
  "type": "set_attribute",
  "scope": "span",
  "name": "http.response.status_code",
  "value": {"type": "int", "value": 500}
}
```

## Validation

All typed values are validated at config load time:

- `type` is required and must be one of the supported types.
- `value` is required for all types except `string` with `size`.
- Array elements are individually type-checked (e.g., `int_array` rejects `["not_a_number"]`).
- `size` must be > 0 when present.
- Type names are case-insensitive (`"String"` and `"string"` both work).

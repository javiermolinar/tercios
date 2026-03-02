# Tercios

Tercios is a CLI tool to generate OTLP traces for testing collectors and tracing pipelines.

## Build

```bash
make build
```

---

## 1) First test (minimal)

If you just want to verify Tercios works, run it in dry-run mode (no collector needed):

```bash
go run ./cmd/tercios --dry-run
```

What this does (with defaults):
- generates 1 request
- with 1 exporter worker
- prints a summary

If you want to see the generated spans as JSON:

```bash
go run ./cmd/tercios --dry-run -o json 2>/dev/null
```

If you want to send traces to a local OpenTelemetry Collector with environment variables instead of flags:

```bash
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=localhost:4317
export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_TRACES_INSECURE=true

go run ./cmd/tercios \
  --scenario-file=./examples/scenario-diff-checkout-baseline.json \
  --exporters=1 \
  --max-requests=1
```

Notes:
- `OTEL_EXPORTER_OTLP_TRACES_*` takes precedence over `OTEL_EXPORTER_OTLP_*`.
- CLI flags still take precedence over environment variables.
- `localhost:4317` is the common OTLP gRPC endpoint for an OTEL Collector.

---

## 2) Stress testing an OpenTelemetry Collector

In this context, **stress testing** means sending high-volume traces to an OTEL collector to measure:
- throughput capacity
- error/failure rate
- latency under load
- behavior under sustained traffic

Example (HTTP collector):

```bash
go run ./cmd/tercios \
  --protocol=http \
  --endpoint=http://localhost:4318/v1/traces \
  --exporters=50 \
  --max-requests=1000 \
  --request-interval=0 \
  --services=20 \
  --max-depth=5 \
  --max-spans=30 \
  --error-rate=0.05
```

Key options for stress tests:
- `--endpoint`, `--protocol`: collector target
- `--exporters`: parallel workers/connections
- `--max-requests`: total work per exporter
- `--request-interval`: pacing (`0` = max speed)
- `--for`: duration-based runs
- `--header`: auth/custom headers

Before any non-dry-run load generation, Tercios runs an automatic exporter preflight check (a small connectivity probe) and exits early if it cannot reach the collector. This probe performs an empty OTLP export request (no spans).

Duration-based run example:

```bash
go run ./cmd/tercios \
  --endpoint=localhost:4317 \
  --exporters=20 \
  --max-requests=0 \
  --for=60 \
  --request-interval=0
```

Long-running mode (send forever, stop with Ctrl+C):

```bash
go run ./cmd/tercios \
  --endpoint=localhost:4317 \
  --exporters=20 \
  --max-requests=0 \
  --request-interval=0
```

---

## 3) Chaos testing (trace behavior mutation)

In this context, **chaos testing** means mutating generated trace data using policies (for example status, attributes, resource values, and latency) to test downstream behavior and analysis.

This is **not** infrastructure chaos (no pods/nodes/network failures). It is telemetry/trace-shape behavior testing.

Policy file example: `examples/chaos-policies.json`

```bash
go run ./cmd/tercios \
  --dry-run -o json \
  --chaos-policies-file=./examples/chaos-policies.json \
  --chaos-seed=42 \
  --exporters=1 \
  --max-requests=10 \
  --services=1 \
  --service-name=post-service \
  --error-rate=0 \
  2>/dev/null
```

Key chaos options:
- `--chaos-policies-file`: JSON policy definitions
- `--chaos-seed`: deterministic probability decisions
- `--dry-run -o json`: inspect mutated spans locally
- `--service-name`: useful to guarantee policy selectors match generated spans
- `--error-rate`: set to `0` when you want policy-driven errors only

Policy actions currently supported:
- `set_status`
- `set_attribute`
- `add_latency` (uses `delta_ms`, supports positive and negative values)

Latency safety:
- if `add_latency` would make a non-positive duration, Tercios clamps span duration to `1ms`.

Policy JSON uses typed values:
- `string`
- `int`
- `float`
- `bool`

---

## 4) Scenario mode (deterministic topology)

Use `--scenario-file` (or `-s`) to generate deterministic traces from a scenario definition.
You can repeat the flag to mix multiple scenarios.

Scenario example: `examples/scenario.json`

```bash
go run ./cmd/tercios \
  --scenario-file=./examples/scenario.json \
  --dry-run -o json \
  --exporters=1 \
  --max-requests=1 \
  2>/dev/null
```

Notes:
- Scenario mode replaces random topology generation.
- You can provide multiple scenario files (repeat `--scenario-file` / `-s`).
- `--scenario-strategy` controls selection when multiple scenarios are configured (`round-robin` or `random`).
- Execution knobs still apply (`--exporters`, `--max-requests`, `--for`, `--request-interval`).
- Chaos can be composed on top of scenarios with `--chaos-policies-file`.

Example with two scenarios mixed in round-robin:

```bash
go run ./cmd/tercios \
  -s ./examples/scenario.json \
  -s ./examples/scenario-diff-cache-after-travel.json \
  --scenario-strategy=round-robin \
  --dry-run -o json \
  --exporters=1 --max-requests=4 \
  2>/dev/null
```

---

## 5) Scenario example (representative)

Representative scenario file:
- `examples/scenario-diff-cache-after-travel.json`

This scenario models a travel search flow with a cache layer in front of pricing data reads.

Run locally (dry-run JSON):

```bash
go run ./cmd/tercios \
  --scenario-file=./examples/scenario-diff-cache-after-travel.json \
  --dry-run -o json \
  --exporters=1 --max-requests=1 \
  2>/dev/null
```

Send to Tempo:

```bash
go run ./cmd/tercios \
  --scenario-file=./examples/scenario-diff-cache-after-travel.json \
  --exporters=1 --max-requests=1
```

---

## CLI options (reference)

- `--endpoint` OTLP endpoint (gRPC: `host:port`, HTTP: `http(s)://host:port/v1/traces`)
- `--protocol` `grpc` or `http`
- `--insecure` disable TLS
- `--header` repeatable headers (`Key=Value` or `Key: Value`)
- `--exporters` concurrent exporters
- `--max-requests` requests per exporter (`0` for no request limit)
- `--request-interval` seconds between requests
- `--for` duration in seconds
- `--export-timeout` per-export timeout in seconds (`0` disables per-export timeout)
- `--services` number of distinct services
- `--max-depth` max span depth
- `--max-spans` max spans per trace
- `--error-rate` probability (`0..1`) of generated error spans
- `--service-name` base service name
- `--span-name` base span name
- `--scenario-file`, `-s` path to deterministic scenario JSON (repeatable)
- `--scenario-strategy` scenario selection strategy for multiple scenario files: `round-robin` or `random`
- `--chaos-policies-file` path to chaos policy JSON
- `--chaos-seed` override policy seed (`0` uses config/default)
- `--dry-run` do not export, generate locally
- `-o, --output` `summary` or `json` (json requires `--dry-run`)
- `--summary-trace-ids` include sampled trace IDs in summary output
- `--summary-trace-ids-limit` maximum sampled trace IDs in summary output

---

## JSON config example

`examples/config.json` shows the nested config shape used by `config.DecodeJSON`.

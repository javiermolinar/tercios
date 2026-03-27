# Tercios

[![Build](https://img.shields.io/github/actions/workflow/status/javiermolinar/tercios/build.yml?branch=main&label=build)](https://github.com/javiermolinar/tercios/actions/workflows/build.yml) [![Test](https://img.shields.io/github/actions/workflow/status/javiermolinar/tercios/test.yml?branch=main&label=test)](https://github.com/javiermolinar/tercios/actions/workflows/test.yml) [![Release](https://img.shields.io/github/v/release/javiermolinar/tercios?display_name=tag)](https://github.com/javiermolinar/tercios/releases) [![License](https://img.shields.io/github/license/javiermolinar/tercios)](./LICENSE)

Tercios is a Swiss-army-knife CLI tool for generating OTLP traces to test collectors and tracing pipelines. It can be used to stress-test your tracing backend, generate complex scenarios, and introduce chaos.

<img width="796" height="960" alt="capitan-al-frente-de-su-compania-en-un-tercio" src="https://github.com/user-attachments/assets/de5e2cf7-9652-4ecb-b451-343d45a4cfea" />

## Installation

Install with Go:

```bash
go install github.com/javiermolinar/tercios/cmd/tercios@latest
```

Or download a prebuilt binary from [GitHub Releases](https://github.com/javiermolinar/tercios/releases/latest):

- `tercios_linux_amd64.tar.gz`
- `tercios_linux_arm64.tar.gz`
- `tercios_darwin_amd64.tar.gz`
- `tercios_darwin_arm64.tar.gz`
- `tercios_windows_amd64.zip`

Each release also includes a `checksums.txt` file.

## Docker (CI / automation)

Build a local image:

```bash
make docker-build
```

Run a quick smoke test in container:

```bash
make docker-run
```

Build with buildx:

```bash
make docker-buildx
```

## How Tercios works

Tercios is a composable pipeline with three main pieces:

1. **Generator (load testing)**
   - Default mode when you do not pass `--scenario-file`.
   - Produces random traces to generate sustained load against your collector/backend.

2. **Scenarios (deterministic topology)**
   - Enabled with `--scenario-file` (repeatable).
   - Replays deterministic service graphs while namespacing trace/span IDs per process by default.

3. **Chaos (optional mutations)**
   - Enabled with `--chaos-policies-file`.
   - Mutates generated/scenario traces (status, attributes, latency, etc.) to test resilience and analysis behavior.

In short: **generator or scenario source → optional chaos → export**.

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

## TLS / secure OTLP endpoints

Tercios now exposes TLS settings as first-class CLI flags and JSON config fields.
Use these when your collector uses HTTPS/TLS with an internal CA or self-signed chain.

CLI flags:
- `--tls-ca-cert <path>`: PEM CA certificate bundle used to verify the collector certificate
- `--tls-skip-verify`: skip TLS certificate verification (**testing only**)

Notes:
- Both flags are ignored when `--insecure` is set.
- They work with both OTLP/gRPC and OTLP/HTTP.
- They also work with `--slow-response-delay` on the HTTP exporter path.
- Standard OpenTelemetry OTLP TLS env vars are still supported for advanced setups such as mTLS.

Examples:

HTTP with an internal CA:

```bash
go run ./cmd/tercios \
  --protocol=http \
  --endpoint=https://collector.internal:4318/v1/traces \
  --tls-ca-cert=/path/to/internal-ca.pem \
  --exporters=5 \
  --max-requests=100
```

gRPC with certificate verification disabled (testing only):

```bash
go run ./cmd/tercios \
  --protocol=grpc \
  --endpoint=collector.internal:4317 \
  --tls-skip-verify \
  --exporters=5 \
  --max-requests=100
```

JSON config shape:

```json
{
  "endpoint": {
    "address": "collector.internal:4317",
    "protocol": "grpc",
    "insecure": false,
    "tls_ca_cert": "/path/to/internal-ca.pem",
    "tls_skip_verify": false
  }
}
```

Advanced note:
- If you already rely on standard OTEL env vars such as `OTEL_EXPORTER_OTLP_CERTIFICATE`, `OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE`, or `OTEL_EXPORTER_OTLP_CLIENT_KEY`, they continue to work.
- Signal-specific env vars such as `OTEL_EXPORTER_OTLP_TRACES_*` still take precedence over the generic `OTEL_EXPORTER_OTLP_*` equivalents.

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
- `--ramp-up`: linearly ramp exporter workers over time (for gentler load warm-up)
- `--header`: auth/custom headers

Before any non-dry-run load generation, Tercios runs an automatic exporter preflight check (a small connectivity probe) and exits early if it cannot reach the collector. This probe performs an empty OTLP export request (no spans).

Duration-based run example:

```bash
go run ./cmd/tercios \
  --endpoint=localhost:4317 \
  --exporters=20 \
  --max-requests=0 \
  --for=60 \
  --ramp-up=30 \
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
- `--scenario-run-seed` controls trace/span ID namespacing (`0` = auto-random per process, non-zero = reproducible IDs across runs).
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
- `--tls-ca-cert` PEM CA certificate bundle used to verify the collector certificate
- `--tls-skip-verify` skip TLS certificate verification (testing only)
- `--header` repeatable headers (`Key=Value` or `Key: Value`)
- `--exporters` concurrent exporters
- `--max-requests` requests per exporter (`0` for no request limit)
- `--request-interval` seconds between requests
- `--for` duration in seconds
- `--ramp-up` ramp-up duration in seconds (linearly ramps exporter workers)
- `--export-timeout` per-export timeout in seconds (`0` disables per-export timeout)
- `--services` number of distinct services
- `--max-depth` max span depth
- `--max-spans` max spans per trace
- `--error-rate` probability (`0..1`) of generated error spans
- `--service-name` base service name
- `--span-name` base span name
- `--scenario-file`, `-s` path to deterministic scenario JSON (repeatable)
- `--scenario-strategy` scenario selection strategy for multiple scenario files: `round-robin` or `random`
- `--scenario-run-seed` trace/span ID namespace for scenario mode (`0` auto-random per process)
- `--chaos-policies-file` path to chaos policy JSON
- `--chaos-seed` override policy seed (`0` uses config/default)
- `--dry-run` do not export, generate locally
- `-o, --output` `summary` or `json` (json requires `--dry-run`)
- `--summary-trace-ids` include sampled trace IDs in summary output
- `--summary-trace-ids-limit` maximum sampled trace IDs in summary output

---

## JSON config example

`examples/config.json` shows the nested config shape used by `config.DecodeJSON`.
The endpoint object also supports `tls_ca_cert` and `tls_skip_verify` for secure collector setups.

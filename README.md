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

## Documentation

- [Scenarios](docs/scenarios.md) — deterministic topology configs
- [Chaos](docs/chaos.md) — trace mutation policies
- [TLS](docs/tls.md) — secure endpoints, CA certs, mTLS
- [Typed Values](docs/typed-values.md) — attribute value types, arrays, generated strings

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

Tercios supports TLS with CA certs, skip-verify, and standard OTEL mTLS env vars. See [docs/tls.md](docs/tls.md) for flags, JSON config, and examples.

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

## 3) Chaos testing

Mutate generated traces with policies — inject errors, shift attributes, add latency. See [docs/chaos.md](docs/chaos.md) for the full policy format and actions reference.

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

---

## 4) Scenario mode

Generate deterministic traces from topology definitions. See [docs/scenarios.md](docs/scenarios.md) for the full config format, edge kinds, and multi-scenario selection.

```bash
go run ./cmd/tercios \
  --scenario-file=./examples/scenario.json \
  --dry-run -o json \
  --exporters=1 \
  --max-requests=1 \
  2>/dev/null
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
See [docs/tls.md](docs/tls.md) for the TLS-related endpoint fields.

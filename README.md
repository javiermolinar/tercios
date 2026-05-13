# Tercios

[![Build](https://img.shields.io/github/actions/workflow/status/javiermolinar/tercios/build.yml?branch=main&label=build)](https://github.com/javiermolinar/tercios/actions/workflows/build.yml) [![Test](https://img.shields.io/github/actions/workflow/status/javiermolinar/tercios/test.yml?branch=main&label=test)](https://github.com/javiermolinar/tercios/actions/workflows/test.yml) [![Lint](https://img.shields.io/github/actions/workflow/status/javiermolinar/tercios/lint.yml?branch=main&label=lint)](https://github.com/javiermolinar/tercios/actions/workflows/lint.yml) [![Release](https://img.shields.io/github/v/release/javiermolinar/tercios?display_name=tag)](https://github.com/javiermolinar/tercios/releases) [![License](https://img.shields.io/github/license/javiermolinar/tercios)](./LICENSE)

Tercios is a Swiss-army-knife CLI tool for generating OTLP traces to test collectors and tracing pipelines. It can be used to stress-test your tracing backend, generate complex scenarios, and introduce chaos.

<img width="1024" height="819" alt="Unknown" src="https://github.com/user-attachments/assets/b75377c6-d646-4d5b-b92c-a814feff3345" />


## Installation

Install with Go:

```bash
go install github.com/javiermolinar/tercios/cmd/tercios@latest
```

Or download prebuilt binaries and checksums from [GitHub Releases](https://github.com/javiermolinar/tercios/releases/latest).

## Docker image

Published multi-architecture images are available on Docker Hub. Use version tags for CI and other programmatic use; `latest` tracks the newest published image.

- `javimolinar/tercios:v0.7.0`
- `javimolinar/tercios:latest`


## How Tercios works

Tercios generates OTLP traces and sends them at configurable concurrency and rate to stress-test collectors, backends, and tracing pipelines. The pipeline has three composable pieces:

1. **Scenarios (trace topology)**
   - A built-in default scenario (5-service web app) is used out of the box — no config needed.
   - Use `--scenario-file` for custom topology definitions (repeatable).
   - Deterministic traces with namespaced trace/span IDs per process.

2. **Chaos (optional mutations)**
   - Enabled with `--chaos-policies-file`.
   - Mutates generated traces (status, attributes, latency, etc.) to test resilience and analysis behavior.

3. **Emission mode**
   - Default: eager. Each generated trace is exported in a single OTLP request.
   - `--streaming` paces each trace's spans across wall-clock time according to their `EndTime`. Use this for long-running traces against backends that reject future timestamps (e.g. Tempo). See [CHANGELOG](CHANGELOG.md) v0.7.0 for details.

Scale with `--exporters` (parallel connections), `--max-requests` (volume), `--for` (duration), and `--ramp-up` (gradual warm-up).

In short: **scenario source → optional chaos → export (eager or streaming) at scale**.

## Documentation

- [Scenarios](docs/scenarios.md) — deterministic topology configs
- [Chaos](docs/chaos.md) — trace mutation policies
- [TLS](docs/tls.md) — secure endpoints, CA certs, mTLS
- [Typed Values](docs/typed-values.md) — attribute value types, arrays, generated strings

---

## 1) First test (minimal)

If you just want to verify Tercios works, run it in dry-run mode (no collector needed):

```bash
tercios --dry-run
```

What this does (with defaults):
- generates 1 request
- with 1 exporter worker
- prints a summary

If you want to see the generated spans as JSON:

```bash
tercios --dry-run -o json 2>/dev/null
```

If you want to send traces to a local OpenTelemetry Collector with environment variables instead of flags:

```bash
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=localhost:4317
export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_TRACES_INSECURE=true

tercios --exporters=1 --max-requests=1
```

Notes:
- Without `--scenario-file`, Tercios uses a built-in default scenario (5-service web app).
- `OTEL_EXPORTER_OTLP_TRACES_*` takes precedence over `OTEL_EXPORTER_OTLP_*`.
- CLI flags still take precedence over environment variables.
- `localhost:4317` is the common OTLP gRPC endpoint for an OTEL Collector.

## TLS / secure OTLP endpoints

Tercios supports TLS with CA certs, skip-verify, and standard OTEL mTLS env vars. `https://` and `grpcs://` endpoints enable TLS by default; host-only endpoints need `--insecure=false`. See [docs/tls.md](docs/tls.md) for flags, JSON config, and examples.

---

## 2) Stress testing an OpenTelemetry Collector

In this context, **stress testing** means sending high-volume traces to an OTEL collector to measure:
- throughput capacity
- error/failure rate
- latency under load
- behavior under sustained traffic

Example (HTTP collector):

```bash
tercios --protocol=http \
  --endpoint=http://localhost:4318/v1/traces \
  --exporters=50 \
  --max-requests=1000 \
  --request-interval=0
```

Key options for stress tests:
- `--endpoint`, `--protocol`: collector target
- `--exporters`: parallel workers/connections
- `--max-requests`: total work per exporter
- `--request-interval`: pacing (`0` = max speed)
- `--for`: duration-based runs
- `--ramp-up`: linearly ramp exporter workers over time (for gentler load warm-up)
- `--header`: auth/custom headers
- `--scenario-file`: custom trace topology (optional, uses embedded default otherwise)

Before any non-dry-run load generation, Tercios runs an automatic exporter preflight check (a small connectivity probe) and exits early if it cannot reach the collector. This probe performs an empty OTLP export request (no spans).

Duration-based run example:

```bash
tercios --endpoint=localhost:4317 \
  --exporters=20 \
  --max-requests=0 \
  --for=60 \
  --ramp-up=30 \
  --request-interval=0
```

Long-running mode (send forever, stop with Ctrl+C):

```bash
tercios --endpoint=localhost:4317 \
  --exporters=20 \
  --max-requests=0 \
  --request-interval=0
```

---

## 3) Chaos testing

Mutate generated traces with policies — inject errors, shift attributes, add latency. See [docs/chaos.md](docs/chaos.md) for the full policy format and actions reference.

```bash
tercios --dry-run -o json \
  --chaos-policies-file=my-chaos.json \
  --chaos-seed=42 \
  --exporters=1 \
  --max-requests=10 \
  2>/dev/null
```

---

## 4) Custom scenarios

Generate deterministic traces from custom topology definitions. Without `--scenario-file`, the embedded default scenario is used. See [docs/scenarios.md](docs/scenarios.md) for the full config format, edge kinds, and multi-scenario selection.

```bash
tercios --scenario-file=my-scenario.json \
  --dry-run -o json \
  --exporters=1 \
  --max-requests=1 \
  2>/dev/null
```

---

## CLI options (reference)

- `--endpoint` OTLP endpoint (gRPC: `host:port`, HTTP: `http(s)://host:port/v1/traces`)
- `--protocol` `grpc` or `http`
- `--insecure` use plaintext/insecure transport instead of TLS (`https://` and `grpcs://` endpoints default to TLS)
- `--tls-ca-cert` PEM CA certificate bundle used to verify the collector certificate (requires TLS)
- `--tls-skip-verify` skip TLS certificate verification (testing only; requires TLS)
- `--header` repeatable headers (`Key=Value` or `Key: Value`)
- `--exporters` concurrent exporters
- `--max-requests` requests per exporter (`0` for no request limit)
- `--request-interval` seconds between requests
- `--for` duration in seconds
- `--ramp-up` ramp-up duration in seconds (linearly ramps exporter workers)
- `--export-timeout` per-export timeout in seconds, applied to both the pipeline context and the OTLP SDK client (`0` disables the pipeline timeout and leaves the SDK default of 10s in place; raise this when running with many exporters so burst phases are not aborted by the SDK). In streaming mode the pipeline-level wrapper is bypassed and this value applies per inner OTLP request instead.
- `--streaming` pace each trace's spans by `EndTime` before sending to OTLP (default off). Required for long-running traces (e.g. >10s) against backends that reject future timestamps. In streaming mode, `--exporters` becomes the in-flight cap (one paced trace per exporter worker) and `add_latency` chaos is honored by the pacer.
- `--scenario-file`, `-s` path to scenario JSON (repeatable; uses embedded default if omitted)
- `--scenario-strategy` scenario selection strategy for multiple scenario files: `round-robin` or `random`
- `--scenario-run-seed` trace/span ID namespace for scenario mode (`0` auto-random per process)
- `--chaos-policies-file` path to chaos policy JSON
- `--chaos-seed` override policy seed (`0` uses config/default)
- `--dry-run` do not export, generate locally
- `-o, --output` `summary` or `json` (json requires `--dry-run`)
- `--summary-trace-ids` include sampled trace IDs in summary output
- `--summary-trace-ids-limit` maximum sampled trace IDs in summary output

---

## Embedded default scenario

When no `--scenario-file` is provided, Tercios uses a built-in 5-service web app scenario:
`gateway → api → cache (redis) + db (postgres) + worker (kafka → db)`.
See the [source](internal/scenario/default_scenario.json) for the full definition.

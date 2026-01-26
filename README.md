# Tercios

Tercios is a CLI tool for load testing OTLP-compatible endpoints by emitting synthetic traces with configurable concurrency, timing, and realistic service graphs.

## Build & Run

```bash
make build
./tercios --endpoint=localhost:4317
```

## Options

- `--endpoint` OTLP endpoint (gRPC default: `host:port`, HTTP: `http(s)://host:port/v1/traces`).
- `--protocol` `grpc` or `http`.
- `--insecure` Disable TLS for the exporter (default: true).
- `--header` HTTP/gRPC headers (`Key=Value` or `Key: Value`), repeatable.
- `--exporters` Number of concurrent exporters (connections).
- `--max-requests` Number of traces per exporter.
- `--request-interval` Seconds between traces per exporter (`0` for no delay).
- `--for` Seconds to send traces per exporter (`0` for no duration limit).
- `--services` Number of distinct service names (fruit-based when `--service-name` is empty).
- `--max-depth` Maximum span depth per trace.
- `--max-spans` Maximum spans per trace.
- `--service-name` Base service name (optional; random if empty).
- `--span-name` Base span name (optional; random if empty).

## Example

```bash
go run ./cmd/tercios \
  --protocol=http \
  --endpoint=http://localhost:4318/v1/traces \
  --header='Authorization=Basic ...' \
  --exporters=3 \
  --max-requests=10 \
  --request-interval=0.5 \
  --services=5 \
  --max-depth=4 \
  --max-spans=25
```

## JSON Config Example

`examples/config.json` shows the nested config shape used by `config.DecodeJSON`:

```json
{
  "endpoint": {
    "address": "localhost:4317",
    "protocol": "grpc",
    "insecure": true,
    "headers": {
      "Authorization": "Basic ..."
    }
  },
  "concurrency": {
    "exporters": 3
  },
  "requests": {
    "per_exporter": 10,
    "interval": "500ms",
    "for": "0s"
  },
  "generator": {
    "services": 5,
    "max_depth": 4,
    "max_spans": 25,
    "service_name": "tercios",
    "span_name": "load-test-span"
  }
}
```

Example summary output:

```
Sent 10k requests
Success: 9.8k
Failures: 200
Avg latency: 120ms
P95 latency: 250ms
```

## Architecture

Tercios uses a composable pipeline:

- `ConcurrencyRunner` manages parallel exporters and per-exporter request counts.
- The pipeline stages operate on trace batches, starting with a generator stage and then middleware transforms before export.

Key implementation areas:

- `cmd/tercios/` CLI flags and pipeline wiring.
- `internal/tracegen/` trace shape generation and span timing.
- `internal/otlp/` exporter selection and endpoint/header handling.
- `internal/metrics/` summary stats.

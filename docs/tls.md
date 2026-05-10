# TLS / Secure OTLP Endpoints

Tercios exposes TLS settings as first-class CLI flags and JSON config fields. Use these when your collector uses HTTPS/TLS with an internal CA or self-signed chain.

## CLI flags

| Flag | Description |
|---|---|
| `--tls-ca-cert <path>` | PEM CA certificate bundle used to verify the collector certificate |
| `--tls-skip-verify` | Skip TLS certificate verification (**testing only**) |

Both flags are ignored when `--insecure` is set. They work with both OTLP/gRPC and OTLP/HTTP, and with `--slow-response-delay` on the HTTP exporter path.

## Examples

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

## JSON config

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

## OpenTelemetry environment variables

Standard OTEL TLS env vars are supported for advanced setups such as mTLS:

- `OTEL_EXPORTER_OTLP_CERTIFICATE` — CA certificate
- `OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE` — client certificate (mTLS)
- `OTEL_EXPORTER_OTLP_CLIENT_KEY` — client key (mTLS)

Signal-specific variants (`OTEL_EXPORTER_OTLP_TRACES_*`) take precedence over the generic `OTEL_EXPORTER_OTLP_*` equivalents. CLI flags take precedence over all environment variables.

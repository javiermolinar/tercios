# Repository Guidelines

## Project Structure & Module Organization

This is a Go CLI for OTLP load testing (traces). Key locations:

- `cmd/tercios/` entrypoint and CLI flag wiring.
- `internal/config/` configuration types and validation.
- `internal/pipeline/` composable pipeline stages (concurrency, generator).
- `internal/tracegen/` trace generator implementation.
- `internal/otlp/` OTLP exporter factory (gRPC/HTTP, headers, endpoint parsing).
- `tools/` Go tools module (golangci-lint).
- `vendor/` vendored dependencies (OTLP exporter and friends).

Add new pipeline features as stages under `internal/pipeline/` and register them in the CLI in `cmd/tercios/main.go`.

## Build, Test, and Development Commands

Use the Makefile for common tasks:

- `make build` builds the CLI binary.
- `make run` runs the CLI with flags (add `--endpoint`, `--protocol`, etc.).
- `make test` runs all tests via `go test ./...`.
- `make lint` runs `golangci-lint` using the tools module.
- `make vendor` tidies and vendors dependencies.
- `make tidy` runs `go mod tidy`.

Example run:

```bash
go run ./cmd/tercios --protocol=http --endpoint=http://localhost:4318/v1/traces \
  --exporters=3 --max-requests=10 --request-interval=0.5 --header='Authorization=Basic ...'
```

## Coding Style & Naming Conventions

- Format with `gofmt` (Go defaults).
- Lint with `golangci-lint` (`make lint`).
- Keep names explicit (`RequestsPerExporter`, `TraceGeneratorStage`) and avoid abbreviations unless standard.

## Testing Guidelines

- Tests live alongside code using Go’s `*_test.go` convention.
- Run with `make test` (or `go test ./...`).
- Focus on deterministic unit tests; the pipeline and generator tests should not rely on networked OTLP endpoints.

## Commit & Pull Request Guidelines

No enforced convention yet. Prefer concise, imperative commit subjects (for example, `feat: add rate limiter stage`). PRs should include:

- A short description and the expected behavior.
- CLI flag changes (if any) and a sample command.
- Tests run (for example, `make test`, `make lint`).

## Agent-Specific Instructions

If using an automated agent, keep changes scoped to the requested feature and update this guide when new tooling, directories, or pipeline stages are introduced.

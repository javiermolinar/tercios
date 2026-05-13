# Changelog

All notable changes are recorded here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
version numbers follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [v0.6.0] — 2026-05-13

This release unifies the eager and streaming trace generators behind a single
heap-driven walker. The eager `GenerateBatch` API is unchanged; internally
it now drains the same heap the future streaming exporter uses, with no
wall-clock pacing.

### Added

- **Public `StreamingWalker` + `NewStreamingWalker`** in `internal/scenario`.
  Drains a single trace one emit at a time; the caller controls pacing.
  Methods: `NextEmit`, `NextDueAt`, `Done`, `TraceID`.
- **`RunSingleTrace(ctx, walker, sink)`** helper that drives a walker to
  completion with wall-clock pacing (`time.NewTimer` per pop), respecting
  `ctx.Done()`. The minimal scheduling primitive a future streaming
  exporter will compose.
- **`pendingEmit` / `traceState` / `emitHeap` types** as the foundation
  shared by both modes. The heap orders by `(DueAt asc, IsRoot asc, Seq
  asc)` so the root sentinel always pops after coincident descendants.
- **`Generator.stepDuration(child)`**: scenario time one repeat of an
  edge consumes (`edge.Duration + subtreeDuration[edge.To] + 1ms`).
  Used to stagger sibling DueAts so the heap-pop order matches
  sequential DFS pre-order.

### Changed

- **Eager walker replaced** with the heap-driven walker. `emitFromNode`,
  `walkFrame`, and the cursor-restore sentinel are gone. `materializedChild`
  no longer carries `ChildrenStart` or `CursorAfter` — the heap walker
  derives those positions directly from `DueAt` and `subtreeDuration`.
- **Root span moves from first to last** in `GenerateBatch`'s returned
  slice. The trace is semantically identical (same span IDs, same
  parent/child links, same timestamps); only the order in the output
  blob differs. This matches real OTel SDK emission order, where parents
  call `Span.End()` after their children complete.

  **User-visible effect**: anything that read `batch[0]` and assumed it
  was the root needs to find the root by `ParentSpanID.IsValid() ==
  false`. The internal test helpers in this repo (`rootSpanName`) show
  the pattern.
- **Span ID allocation order unchanged** from v0.5.0: the root SpanID
  is still `idState.next()` first, then descendants in DFS pre-order.
  Only the emit order in the returned slice differs.

### Internal

- `walker` is the internal trace-emission engine; `GenerateBatch` calls
  `drain()`, `StreamingWalker` calls `popOne()`. One codebase, two
  pacing modes.
- Children of a pair edge with `network_latency_ms > 0` now correctly
  attach inside the (narrower) target-side span instead of starting
  before it. Caught by `TestPairEdgeLatencyChildrenFitInsideTarget`
  failing on a two-level latency scenario during the unification.

## [v0.5.0] — 2026-05-13

This release focuses on producing **realistic distributed-trace shapes**: spans
now properly contain their children in time, pair edges can model network
latency between client and server, and the scenario validator catches several
classes of silent misconfigurations.

### Added

- **`network_latency_ms` field on pair edges** (`client_server`,
  `producer_consumer`, `client_database`). When non-zero, the target-side
  span (server / consumer / database) is inset by `network_latency_ms` on both
  sides of the source-side span's interval — modeling request-travel and
  response-travel time. Defaults to `0` (preserves prior behavior). The
  default scenario now uses small realistic values (1ms HTTP, 2ms Postgres,
  1ms Kafka).
- **Reachability validation**: scenarios with nodes not reachable from `root`
  are rejected with the offending IDs named in the error. Previously these
  nodes were silently ignored.
- **Root-incoming-edge validation**: the node declared as `root` must not be
  the target of any edge. Previously the walker silently dropped the
  incoming edge and orphaned the parent.
- **Richer timing-validation errors**: when `2 * network_latency_ms >=
  duration_ms`, the error now reports the computed effective span duration,
  the subtree work under the target, and the resulting (infeasible) server
  interval.
- **`--export-timeout` CLI flag** forwarded to the OTLP SDK client.
- **`BenchmarkGenerateBatch`** for tracking per-trace generation cost.

### Changed

- **Span timestamps**: every parent span now temporally contains every
  descendant span (`parent.start ≤ child.start && child.end ≤ parent.end`).
  Intermediate spans previously ended at `start + edge.Duration` and their
  children began after the parent ended — a known oddity inherited from
  the original recursive walker. Span IDs, names, parent links, attributes,
  and root timing are unchanged.

  **User-visible effect**: any test asserting on exact intermediate-span
  timestamps will need updates. Golden output comparisons against pre-0.5.0
  span dumps will not match.

- **Iterative walker**: the scenario generator no longer uses Go-runtime
  recursion. The traversal is now an explicit stack of `walkFrame` entries.
  Output is structurally equivalent to before; the change is internal and
  unlocks the future streaming exporter without affecting the eager path.

### Internal refactors (no behavior change)

- `NextChildren` / `ChildSpec` expose the outgoing-edge lookup as a
  first-class operation on `Generator`.
- `materializeChild` / `materializePair` factor the per-edge-kind span
  construction out of `emitFromNode`. The three two-span edge kinds
  (`ClientServer`, `ProducerConsumer`, `ClientDatabase`) now share one
  helper parameterized by `SpanKind`.
- Iterative walker uses a cursor-restore sentinel on the stack so siblings
  of a just-drained parent start at the correct timestamp.
- Validation logic is unified behind `Config.Validate` with a shared
  outgoing-edges index reused by both DAG and timing checks.

### Performance

- `BenchmarkGenerateBatch` on the test scenario: ~5.5 µs per 9-span trace,
  62 allocations, 21 KB. Roughly 20% slower than v0.4.0's recursive walker
  due to the cursor-restore sentinel pushes; well below OTLP encoding and
  network costs in real workloads.

### Documentation

- README gained a Docker-image usage example (already shipped in v0.4.0 docs
  rev; included here for the unified history).

---

## Prior to v0.5.0

Earlier releases (`v0.1.0` through `v0.4.0`) predate this changelog. See the
git log for details:

```sh
git log --oneline v0.4.0
```

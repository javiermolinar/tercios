package scenario

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/javiermolinar/tercios/internal/model"
)

// rootSpanName returns the Name of the span with no parent in batch, or
// the empty string if none is found. Used by tests that previously
// relied on batch[0].Name when the root was emitted first; under the
// heap-driven walker the root is emitted last.
func rootSpanName(batch []model.Span) string {
	for _, s := range batch {
		if !s.ParentSpanID.IsValid() {
			return s.Name
		}
	}
	return ""
}

func TestNewBatchGeneratorFromFilesSingle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(path, []byte(minimalScenarioJSON("single", 1, "root-single")), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	generator, err := NewBatchGeneratorFromFiles([]string{path}, SelectionStrategyRoundRobin)
	if err != nil {
		t.Fatalf("NewBatchGeneratorFromFiles() error = %v", err)
	}

	batch, err := generator.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch() error = %v", err)
	}
	if len(batch) == 0 {
		t.Fatalf("expected non-empty batch")
	}
	if name := rootSpanName(batch); name != "root-single" {
		t.Fatalf("expected root-single, got %q", name)
	}
}

func TestNewBatchGeneratorFromFilesMultiple(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "scenario-a.json")
	pathB := filepath.Join(dir, "scenario-b.json")
	if err := os.WriteFile(pathA, []byte(minimalScenarioJSON("a", 1, "root-a")), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(pathB, []byte(minimalScenarioJSON("b", 2, "root-b")), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	generator, err := NewBatchGeneratorFromFiles([]string{pathA, pathB}, SelectionStrategyRoundRobin)
	if err != nil {
		t.Fatalf("NewBatchGeneratorFromFiles() error = %v", err)
	}

	first, err := generator.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch() error = %v", err)
	}
	second, err := generator.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("GenerateBatch() error = %v", err)
	}
	if a, b := rootSpanName(first), rootSpanName(second); a != "root-a" || b != "root-b" {
		t.Fatalf("expected round robin root-a/root-b, got %q/%q", a, b)
	}
}

func TestNewBatchGeneratorFromFilesRejectsEmpty(t *testing.T) {
	_, err := NewBatchGeneratorFromFiles(nil, SelectionStrategyRoundRobin)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestNewBatchGeneratorFromFilesWithRunSeedNamespacesRepeatedScenarios(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(path, []byte(minimalScenarioJSON("same", 41, "root")), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	generator, err := NewBatchGeneratorFromFilesWithRunSeed([]string{path, path}, SelectionStrategyRoundRobin, 123)
	if err != nil {
		t.Fatalf("NewBatchGeneratorFromFilesWithRunSeed() error = %v", err)
	}

	first, err := generator.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("first GenerateBatch() error = %v", err)
	}
	second, err := generator.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("second GenerateBatch() error = %v", err)
	}
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("expected non-empty batches")
	}
	if first[0].TraceID == second[0].TraceID {
		t.Fatalf("expected repeated scenarios to use different trace ID namespace, got %s", first[0].TraceID)
	}
}

func TestNewBatchGeneratorFromFilesWithRunSeedIsReproducibleWhenFixed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(path, []byte(minimalScenarioJSON("single", 52, "root")), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	g1, err := NewBatchGeneratorFromFilesWithRunSeed([]string{path}, SelectionStrategyRoundRobin, 77)
	if err != nil {
		t.Fatalf("g1 NewBatchGeneratorFromFilesWithRunSeed() error = %v", err)
	}
	g2, err := NewBatchGeneratorFromFilesWithRunSeed([]string{path}, SelectionStrategyRoundRobin, 77)
	if err != nil {
		t.Fatalf("g2 NewBatchGeneratorFromFilesWithRunSeed() error = %v", err)
	}

	b1, err := g1.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("g1 GenerateBatch() error = %v", err)
	}
	b2, err := g2.GenerateBatch(context.Background())
	if err != nil {
		t.Fatalf("g2 GenerateBatch() error = %v", err)
	}
	if len(b1) == 0 || len(b2) == 0 {
		t.Fatalf("expected non-empty batches")
	}
	if b1[0].TraceID != b2[0].TraceID {
		t.Fatalf("expected fixed run seed to be reproducible, got %s vs %s", b1[0].TraceID, b2[0].TraceID)
	}
}

func minimalScenarioJSON(name string, seed int64, rootSpanName string) string {
	return `{
  "name": "` + name + `",
  "seed": ` + strconv.FormatInt(seed, 10) + `,
  "services": {
    "svc": {
      "resource": {
        "service.name": { "type": "string", "value": "` + name + `" }
      }
    }
  },
  "nodes": {
    "root": { "service": "svc", "span_name": "` + rootSpanName + `" },
    "child": { "service": "svc", "span_name": "child" }
  },
  "root": "root",
  "edges": [
    { "from": "root", "to": "child", "kind": "internal", "repeat": 1, "duration_ms": 10 }
  ]
}`
}

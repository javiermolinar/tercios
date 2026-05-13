package scenario

import (
	"context"
	"testing"
)

func TestMultiGeneratorRoundRobin(t *testing.T) {
	defA := testSimpleDefinition(t, "scenario-a", 1, "root-a")
	defB := testSimpleDefinition(t, "scenario-b", 2, "root-b")

	g, err := NewMultiGenerator([]Definition{defA, defB}, SelectionStrategyRoundRobin, 7)
	if err != nil {
		t.Fatalf("NewMultiGenerator() error = %v", err)
	}

	want := []string{"root-a", "root-b", "root-a", "root-b"}
	for i := range want {
		batch, err := g.GenerateBatch(context.Background())
		if err != nil {
			t.Fatalf("GenerateBatch() error = %v", err)
		}
		if len(batch) == 0 {
			t.Fatalf("expected non-empty batch")
		}
		if name := rootSpanName(batch); name != want[i] {
			t.Fatalf("iteration %d: expected %q, got %q", i, want[i], name)
		}
	}
}

func TestMultiGeneratorRandomDeterministicSelection(t *testing.T) {
	defs := []Definition{
		testSimpleDefinition(t, "scenario-a", 1, "root-a"),
		testSimpleDefinition(t, "scenario-b", 2, "root-b"),
		testSimpleDefinition(t, "scenario-c", 3, "root-c"),
	}

	g1, err := NewMultiGenerator(defs, SelectionStrategyRandom, 42)
	if err != nil {
		t.Fatalf("NewMultiGenerator() error = %v", err)
	}
	g2, err := NewMultiGenerator(defs, SelectionStrategyRandom, 42)
	if err != nil {
		t.Fatalf("NewMultiGenerator() error = %v", err)
	}

	for i := 0; i < 10; i++ {
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
		if n1, n2 := rootSpanName(b1), rootSpanName(b2); n1 != n2 {
			t.Fatalf("iteration %d: expected same selected scenario, got %q vs %q", i, n1, n2)
		}
	}
}

func testSimpleDefinition(t *testing.T, scenarioName string, seed int64, rootSpanName string) Definition {
	t.Helper()

	cfg := Config{
		Name: scenarioName,
		Seed: seed,
		Services: map[string]ServiceConfig{
			"svc": {Resource: map[string]TypedValue{"service.name": {Type: ValueTypeString, Value: scenarioName}}},
		},
		Nodes: map[string]NodeConfig{
			"root":  {Service: "svc", SpanName: rootSpanName},
			"child": {Service: "svc", SpanName: "child"},
		},
		Root: "root",
		Edges: []EdgeConfig{
			{From: "root", To: "child", Kind: EdgeKindInternal, Repeat: 1, DurationMs: 5},
		},
	}
	def, err := cfg.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return def
}

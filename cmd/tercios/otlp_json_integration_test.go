package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func TestCLIDryRunOTLPJSON(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		args      []string
		requests  int
		streaming bool
		wantError string
	}{
		{name: "short flag", args: []string{"--dry-run", "-o", "otlp-json", "--exporters=2", "--max-requests=2"}, requests: 4},
		{name: "long flag", args: []string{"--dry-run", "--output=otlp-json", "--exporters=1", "--max-requests=1"}, requests: 1},
		{name: "streaming", args: []string{"--dry-run", "-o=otlp-json", "--streaming", "--exporters=1", "--max-requests=1"}, requests: 1, streaming: true},
		{name: "requires dry-run", args: []string{"-o=otlp-json"}, wantError: "requires --dry-run"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			args := append([]string{"-test.run=^TestExactCLIProcess$", "--", "--request-interval=0", "--scenario-run-seed=77"}, fixture.args...)
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			for _, env := range os.Environ() {
				if !strings.HasPrefix(env, "OTEL_") && !strings.HasPrefix(env, "GORACE=") && !strings.HasPrefix(env, "TERCIOS_CLI_TEST=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Env = append(cmd.Env, "TERCIOS_CLI_TEST=1", "GORACE=atexit_sleep_ms=0")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if fixture.wantError != "" {
				if err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), fixture.wantError) {
					t.Fatalf("expected rejection before output, got %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CLI failed: %v\n%s", err, &stderr)
			}
			lines, spans := 0, 0
			traceIDs := map[string]bool{}
			scanner := bufio.NewScanner(&stdout)
			for scanner.Scan() {
				request := ptraceotlp.NewExportRequest()
				if err := request.UnmarshalJSON(scanner.Bytes()); err != nil {
					t.Fatalf("stdout must contain only OTLP JSON lines: %v\n%s", err, &stdout)
				}
				resources := request.Traces().ResourceSpans()
				if resources.Len() == 0 {
					t.Fatal("empty OTLP request")
				}
				for i := 0; i < resources.Len(); i++ {
					scopes := resources.At(i).ScopeSpans()
					for j := 0; j < scopes.Len(); j++ {
						if scopes.At(j).Scope().Name() != "tercios" {
							t.Fatal("missing instrumentation scope")
						}
						batch := scopes.At(j).Spans()
						spans += batch.Len()
						for k := 0; k < batch.Len(); k++ {
							traceIDs[batch.At(k).TraceID().String()] = true
						}
					}
				}
				lines++
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if len(traceIDs) != fixture.requests || spans == 0 || (!fixture.streaming && lines != fixture.requests) || (fixture.streaming && lines <= fixture.requests) {
				t.Fatalf("unexpected output: traces=%d, spans=%d, lines=%d", len(traceIDs), spans, lines)
			}
			for _, text := range []string{fmt.Sprintf("Sent %d requests", fixture.requests), fmt.Sprintf("Successful spans: %d", spans), "Failures: 0"} {
				if !strings.Contains(stderr.String(), text) {
					t.Fatalf("summary missing %q: %s", text, &stderr)
				}
			}
			if strings.Contains(stderr.String(), "preflight") {
				t.Fatal("dry-run must not contact a collector")
			}
		})
	}
}

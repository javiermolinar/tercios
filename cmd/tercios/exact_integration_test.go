package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Run the real entrypoint in a subprocess without refactoring production flags.
func TestExactCLIProcess(t *testing.T) {
	if os.Getenv("TERCIOS_CLI_TEST") != "1" {
		return
	}
	os.Args = append([]string{"tercios"}, os.Args[slices.Index(os.Args, "--")+1:]...)
	flag.CommandLine = flag.NewFlagSet("tercios", flag.ExitOnError)
	main()
	os.Exit(0)
}

func TestExactCLIDryRun(t *testing.T) {
	document := func(nodes, edges string) string {
		return `{"name":"cli","seed":42,"services":{"svc":{"resource":{"service.name":{"type":"string","value":"app"}}}},"nodes":` + nodes + `,"edges":` + edges + `}`
	}
	root := `"a":{"service":"svc","span_name":"request","kind":"SERVER","duration_ms":4,"status":{"code":"ERROR","description":"raw error"},"span_attributes":{"http.method":{"type":"string","value":"POST"}}}`
	child := `"b":{"service":"svc","span_name":"query","kind":"CLIENT","start_offset_ms":1,"duration_ms":0}`
	single := document(`{`+root+`}`, `[{"from":"a"}]`)
	request := document(`{`+root+`,`+child+`}`, `[{"from":"a","to":"b"}]`)
	forest := strings.Replace(request, `"duration_ms":0}`, `"duration_ms":0,"parent":null}`, 1)
	legacy := strings.Replace(document(`{"a":{"service":"svc","span_name":"request"},"b":{"service":"svc","span_name":"query"}}`, `[{"from":"a","to":"b","kind":"internal","repeat":1,"duration_ms":1}]`), `"nodes":`, `"root":"a","nodes":`, 1)
	for _, fixture := range []struct {
		name          string
		files         []string
		counts, roots []int
		streaming     bool
	}{
		{"singleton", []string{single}, []int{1}, []int{1}, false},
		{"request-query", []string{request}, []int{2}, []int{1}, false},
		{"forest", []string{forest}, []int{2}, []int{2}, true},
		{"mixed-files", []string{request, legacy}, []int{2, 2}, []int{1, 1}, false},
		{"mixed-styles", []string{strings.Replace(request, `"edges":[{"from":"a","to":"b"}]`, `"edges":[{"from":"a","to":"b"},{"from":"a","to":"b","kind":"internal","repeat":1,"duration_ms":1}]`, 1)}, nil, nil, false},
		{"embedded-default", nil, nil, nil, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			requests := max(1, len(fixture.files))
			args := []string{"-test.run=^TestExactCLIProcess$", "--", "--dry-run", "-o=json", "--exporters=1", fmt.Sprintf("--max-requests=%d", requests), "--request-interval=0", "--scenario-run-seed=77"}
			if fixture.streaming {
				args = append(args, "--streaming")
			}
			for i, data := range fixture.files {
				path := filepath.Join(t.TempDir(), fmt.Sprintf("%d.json", i))
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--scenario-file="+path)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
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
			if fixture.name == "mixed-styles" {
				if err == nil || !strings.Contains(stderr.String(), "cannot mix") || stdout.Len() != 0 {
					t.Fatalf("mixed styles must fail before output: %v\n%s", err, &stderr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CLI: %v\n%s", err, &stderr)
			}
			type span struct {
				TraceID              string `json:"trace_id"`
				SpanID               string `json:"span_id"`
				Parent               string `json:"parent_span_id"`
				Name, Kind           string
				Start                time.Time `json:"start_time"`
				End                  time.Time `json:"end_time"`
				Duration             int64     `json:"duration_ms"`
				Attributes, Resource map[string]any
				Status               struct{ Code, Message string }
			}
			traces, order := map[string][]span{}, []string{}
			decoder := json.NewDecoder(&stdout)
			for {
				var batch struct{ Spans []span }
				if err := decoder.Decode(&batch); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				for _, s := range batch.Spans {
					if _, exists := traces[s.TraceID]; !exists {
						order = append(order, s.TraceID)
					}
					traces[s.TraceID] = append(traces[s.TraceID], s)
				}
			}
			if len(order) != requests {
				t.Fatalf("traces=%d, want %d", len(order), requests)
			}
			total := 0
			for i, id := range order {
				spans, roots, byName := traces[id], 0, map[string]span{}
				for _, s := range spans {
					if s.Parent == "" {
						roots++
					}
					byName[s.Name] = s
					if s.SpanID == "" || s.TraceID == "" {
						t.Fatal("missing IDs")
					}
				}
				total += len(spans)
				if fixture.files == nil {
					continue
				}
				if len(spans) != fixture.counts[i] || roots != fixture.roots[i] {
					t.Fatal("count/forest topology differs")
				}
				if fixture.files[i] == legacy {
					continue
				}
				a := byName["request"]
				if a.Kind != "server" || a.Status.Code != "Error" || a.Status.Message != "raw error" || a.Duration != 4 || a.Attributes["http.method"] != "POST" || a.Resource["service.name"] != "app" {
					t.Fatalf("native root differs: %+v", a)
				}
				if len(spans) == 2 {
					b := byName["query"]
					if b.Kind != "client" || b.Status.Code != "Unset" || b.Duration != 0 || !b.Start.Equal(a.Start.Add(time.Millisecond)) || !b.End.Equal(b.Start) || (roots == 1 && b.Parent != a.SpanID) {
						t.Fatalf("native child differs: %+v", b)
					}
				}
			}
			for _, text := range []string{fmt.Sprintf("Sent %d requests", requests), fmt.Sprintf("Successful spans: %d", total), "Failures: 0"} {
				if !slices.Contains(strings.Split(stderr.String(), "\n"), text) {
					t.Fatalf("summary missing %q: %s", text, &stderr)
				}
			}
		})
	}
}

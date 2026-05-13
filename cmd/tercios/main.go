package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/javiermolinar/tercios/internal/chaos"
	"github.com/javiermolinar/tercios/internal/config"
	"github.com/javiermolinar/tercios/internal/metrics"
	"github.com/javiermolinar/tercios/internal/otlp"
	"github.com/javiermolinar/tercios/internal/pipeline"
	"github.com/javiermolinar/tercios/internal/scenario"
)

func main() {
	var (
		endpoint                 string
		protocol                 string
		insecure                 bool
		tlsCACert                string
		tlsSkipVerify            bool
		exporters                int
		requestsPerExporter      int
		requestIntervalSeconds   float64
		requestForSeconds        float64
		rampUpSeconds            float64
		exportTimeoutSeconds     float64
		scenarioFiles            scenario.FileFlags
		scenarioStrategy         string
		scenarioRunSeed          int64
		chaosPoliciesFile        string
		chaosSeed                int64
		dryRun                   bool
		streaming                bool
		output                   string
		summaryTraceIDs          bool
		summaryTraceIDsLimit     int
		headers                  config.HeaderFlags
		slowResponseDelaySeconds float64
	)

	flag.Usage = usage
	defaults := config.DefaultConfig()
	flag.StringVar(&endpoint, "endpoint", defaults.Endpoint.Address, "OTLP endpoint (for HTTP, prefer http(s)://host:port/v1/traces)")
	flag.StringVar(&protocol, "protocol", string(defaults.Endpoint.Protocol), "OTLP protocol: grpc or http")
	flag.BoolVar(&insecure, "insecure", defaults.Endpoint.Insecure, "send OTLP over plaintext instead of TLS (https/grpcs endpoints default to false)")
	flag.StringVar(&tlsCACert, "tls-ca-cert", "", "path to PEM CA certificate file for server verification")
	flag.BoolVar(&tlsSkipVerify, "tls-skip-verify", false, "skip TLS certificate verification")
	flag.IntVar(&exporters, "exporters", defaults.Concurrency.Exporters, "number of concurrent exporters (connections)")
	flag.IntVar(&requestsPerExporter, "max-requests", defaults.Requests.PerExporter, "requests per exporter (0 for no request limit)")
	flag.Float64Var(&requestIntervalSeconds, "request-interval", defaults.Requests.Interval.Seconds(), "seconds between requests per exporter (0 for no delay)")
	flag.Float64Var(&requestForSeconds, "for", defaults.Requests.For.Seconds(), "seconds to send traces per exporter (0 for no duration limit)")
	flag.Float64Var(&rampUpSeconds, "ramp-up", defaults.Requests.RampUp.Seconds(), "seconds to linearly ramp exporter workers from 0 to max concurrency")
	flag.Float64Var(&exportTimeoutSeconds, "export-timeout", defaults.Requests.ExportTimeout.Seconds(), "seconds before each export attempt times out; applied to both the pipeline context and the OTLP SDK client (0 disables the pipeline timeout and keeps the SDK default of 10s)")
	flag.Var(&scenarioFiles, "scenario-file", "path to scenario JSON file; repeatable")
	flag.Var(&scenarioFiles, "s", "path to scenario JSON file (shorthand); repeatable")
	flag.StringVar(&scenarioStrategy, "scenario-strategy", string(scenario.SelectionStrategyRoundRobin), "scenario selection strategy when multiple scenarios: round-robin or random")
	flag.Int64Var(&scenarioRunSeed, "scenario-run-seed", 0, "seed namespace for scenario trace/span IDs (0 = auto-random per process)")
	flag.StringVar(&chaosPoliciesFile, "chaos-policies-file", "", "path to chaos policies JSON file")
	flag.Int64Var(&chaosSeed, "chaos-seed", 0, "override chaos policy seed (0 uses file/default)")
	flag.BoolVar(&dryRun, "dry-run", false, "generate traces without exporting to OTLP")
	flag.BoolVar(&streaming, "streaming", false, "pace each batch by span EndTime so backends see end_times <= wall-clock-now; required for long-running traces. See docs/streaming.md")
	flag.StringVar(&output, "output", string(otlp.DryRunOutputSummary), "output format: summary or json")
	flag.StringVar(&output, "o", string(otlp.DryRunOutputSummary), "output format shorthand: summary or json")
	flag.BoolVar(&summaryTraceIDs, "summary-trace-ids", false, "include sampled trace IDs in summary output")
	flag.IntVar(&summaryTraceIDsLimit, "summary-trace-ids-limit", 10, "maximum number of sampled trace IDs to include in summary")
	flag.Var(&headers, "header", "header in Key=Value or Key: Value format; repeatable")
	flag.Float64Var(&slowResponseDelaySeconds, "slow-response-delay", 0, "seconds to delay reading each HTTP response body, simulating a slow client (HTTP only, 0 disables)")
	flag.Parse()
	if flag.NFlag() == 0 {
		usage()
		os.Exit(2)
	}
	setFlags := map[string]struct{}{}
	flag.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = struct{}{}
	})
	isFlagSet := func(name string) bool {
		_, ok := setFlags[name]
		return ok
	}
	insecureExplicit := isFlagSet("insecure")
	if err := applyOTLPEnvOverrides(&endpoint, &protocol, &insecure, isFlagSet); err != nil {
		log.Fatalf("invalid OTLP environment override: %v", err)
	}
	if !insecureExplicit {
		if _, ok := firstNonEmptyEnv(envOTLPTracesInsecure, envOTLPInsecure); ok {
			insecureExplicit = true
		}
	}
	if err := applyEndpointSchemeSecurityDefaults(endpoint, &insecure, insecureExplicit); err != nil {
		log.Fatalf("invalid endpoint security: %v", err)
	}

	requestInterval := time.Duration(requestIntervalSeconds * float64(time.Second))
	requestFor := time.Duration(requestForSeconds * float64(time.Second))
	rampUp := time.Duration(rampUpSeconds * float64(time.Second))
	exportTimeout := time.Duration(exportTimeoutSeconds * float64(time.Second))
	slowResponseDelay := time.Duration(slowResponseDelaySeconds * float64(time.Second))
	cfg := config.Config{
		Endpoint: config.EndpointConfig{
			Address:  endpoint,
			Protocol: config.Protocol(protocol),
			Insecure: insecure,
			Headers:  headers.Values(),
		},
		Concurrency: config.ConcurrencyConfig{
			Exporters: exporters,
		},
		Requests: config.RequestConfig{
			PerExporter:   requestsPerExporter,
			Interval:      config.Duration{Duration: requestInterval},
			For:           config.Duration{Duration: requestFor},
			RampUp:        config.Duration{Duration: rampUp},
			ExportTimeout: config.Duration{Duration: exportTimeout},
		},
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}
	if summaryTraceIDsLimit < 0 {
		log.Fatalf("invalid summary config: --summary-trace-ids-limit must be >= 0")
	}
	if summaryTraceIDs && summaryTraceIDsLimit == 0 {
		log.Fatalf("invalid summary config: --summary-trace-ids requires --summary-trace-ids-limit > 0")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	outputFormat, err := otlp.ParseDryRunOutput(output)
	if err != nil {
		log.Fatalf("invalid output format: %v", err)
	}
	if !dryRun && outputFormat != otlp.DryRunOutputSummary {
		log.Fatalf("-o/--output=%s requires --dry-run", outputFormat)
	}
	if !dryRun {
		if err := validateTLSConfiguration(insecure, tlsCACert, tlsSkipVerify); err != nil {
			log.Fatalf("invalid TLS configuration: %v", err)
		}
	}

	var factory pipeline.ExporterFactory
	if dryRun {
		dryRunFactory := otlp.NewDryRunExporterFactory(outputFormat, os.Stdout)
		factory = dryRunFactory
	} else {
		if slowResponseDelay > 0 && cfg.Endpoint.Protocol != config.ProtocolHTTP {
			log.Printf("warning: --slow-response-delay has no effect with protocol=%s (HTTP only)", cfg.Endpoint.Protocol)
		}
		otlpFactory := otlp.ExporterFactory{
			Protocol:          cfg.Endpoint.Protocol,
			Endpoint:          cfg.Endpoint.Address,
			Insecure:          cfg.Endpoint.Insecure,
			Headers:           cfg.Endpoint.Headers,
			SlowResponseDelay: slowResponseDelay,
			TLSCACert:         tlsCACert,
			TLSSkipVerify:     tlsSkipVerify,
			ExportTimeout:     cfg.Requests.ExportTimeout.Duration,
		}
		factory = otlpFactory
		_, _ = fmt.Fprintln(os.Stderr, "Running exporter preflight check...")
		if err := otlp.RunPreflight(ctx, otlpFactory, cfg.Requests.ExportTimeout.Duration); err != nil {
			log.Fatalf("preflight failed: %v", err)
		}
		_, _ = fmt.Fprintln(os.Stderr, "Preflight check passed")
	}

	if streaming {
		factory = otlp.NewStreamingExporterFactory(factory)
	}

	runner := pipeline.NewConcurrencyRunner(cfg.Concurrency.Exporters, cfg.Requests.PerExporter)
	stages := make([]pipeline.BatchStage, 0, 2)
	files := scenarioFiles.Values()
	if len(files) > 0 {
		strategy, err := scenario.ParseSelectionStrategy(scenarioStrategy)
		if err != nil {
			log.Fatalf("invalid scenario strategy: %v", err)
		}
		scenarioGenerator, err := scenario.NewBatchGeneratorFromFilesWithRunSeed(files, strategy, scenarioRunSeed)
		if err != nil {
			log.Fatalf("invalid scenario setup: %v", err)
		}
		stages = append(stages, pipeline.NewScenarioStage(scenarioGenerator))
	} else {
		defaultGenerator, err := scenario.DefaultGenerator(scenarioRunSeed)
		if err != nil {
			log.Fatalf("embedded scenario failed: %v", err)
		}
		stages = append(stages, pipeline.NewScenarioStage(defaultGenerator))
	}
	if chaosPoliciesFile != "" {
		chaosCfg, err := chaos.LoadFromJSON(chaosPoliciesFile)
		if err != nil {
			log.Fatalf("invalid chaos policies: %v", err)
		}
		if chaosSeed != 0 {
			chaosCfg.Seed = chaosSeed
		}
		chaosEngine, err := chaos.NewEngine(chaosCfg)
		if err != nil {
			log.Fatalf("create chaos engine: %v", err)
		}
		chaosDecider := chaos.NewSeededShouldApply(chaosCfg.Seed)
		stages = append(stages, pipeline.NewChaosStage(chaosEngine, chaosDecider))
	}

	pipe := pipeline.New(stages...)
	traceIDSampleLimit := 0
	if summaryTraceIDs {
		traceIDSampleLimit = summaryTraceIDsLimit
	}
	progressInterval := 5 * time.Second
	// Streaming exports pace each batch by span EndTime, so a single
	// ExportBatch call can take the full scenario duration. The pipeline's
	// per-batch timeout would kill that; disable it when streaming. The
	// OTLP SDK's own per-request timeout (also set from --export-timeout)
	// still applies to each inner request inside the streaming exporter.
	pipelineExportTimeout := cfg.Requests.ExportTimeout.Duration
	if streaming {
		pipelineExportTimeout = 0
	}
	err = pipe.RunWithProgress(ctx, runner, factory, cfg.Requests.Interval.Duration, cfg.Requests.For.Duration, cfg.Requests.RampUp.Duration, pipelineExportTimeout, traceIDSampleLimit, progressInterval, os.Stderr)
	summary := metrics.FormatSummary(pipe.Summary())
	if dryRun && outputFormat == otlp.DryRunOutputJSON {
		_, _ = fmt.Fprintln(os.Stderr, summary)
	} else {
		_, _ = fmt.Println(summary)
	}
	if err != nil {
		log.Printf("pipeline failed: %v", err)
		os.Exit(1)
	}
}

func usage() {
	w := os.Stderr
	_, _ = fmt.Fprintf(w, `tercios — OTLP trace generator for load testing collectors and tracing pipelines.

Usage:
  tercios [flags]

Examples:
  # Quick local test (embedded 5-service scenario, no collector needed)
  tercios --dry-run

  # See generated spans as JSON
  tercios --dry-run -o json 2>/dev/null

  # Stress test a collector (50 workers, max speed, 60s)
  tercios --endpoint=localhost:4317 --exporters=50 --max-requests=0 --for=60 --request-interval=0

  # Custom scenario + chaos
  tercios -s my-scenario.json --chaos-policies-file=my-chaos.json --exporters=10 --max-requests=100

Connection:
`)
	printFlag(w, "endpoint", "protocol", "insecure", "header", "tls-ca-cert", "tls-skip-verify")
	_, _ = fmt.Fprintf(w, "\nLoad:\n")
	printFlag(w, "exporters", "max-requests", "request-interval", "for", "ramp-up", "export-timeout", "slow-response-delay")
	_, _ = fmt.Fprintf(w, "\nScenarios:\n")
	printFlag(w, "scenario-file", "scenario-strategy", "scenario-run-seed")
	_, _ = fmt.Fprintf(w, "\nChaos:\n")
	printFlag(w, "chaos-policies-file", "chaos-seed")
	_, _ = fmt.Fprintf(w, "\nOutput:\n")
	printFlag(w, "dry-run", "output", "summary-trace-ids", "summary-trace-ids-limit")
}

func printFlag(w *os.File, names ...string) {
	for _, name := range names {
		f := flag.Lookup(name)
		if f == nil {
			continue
		}
		def := ""
		if f.DefValue != "" && f.DefValue != "0" && f.DefValue != "false" {
			def = fmt.Sprintf(" (default %s)", f.DefValue)
		}
		_, _ = fmt.Fprintf(w, "  --%s\n        %s%s\n", f.Name, f.Usage, def)
	}
}

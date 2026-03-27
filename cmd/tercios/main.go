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
	"github.com/javiermolinar/tercios/internal/tracegen"
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
		services                 int
		maxDepth                 int
		maxSpans                 int
		errorRate                float64
		serviceName              string
		spanName                 string
		scenarioFiles            scenario.FileFlags
		scenarioStrategy         string
		scenarioRunSeed          int64
		chaosPoliciesFile        string
		chaosSeed                int64
		dryRun                   bool
		output                   string
		summaryTraceIDs          bool
		summaryTraceIDsLimit     int
		headers                  config.HeaderFlags
		slowResponseDelaySeconds float64
	)

	defaults := config.DefaultConfig()
	flag.StringVar(&endpoint, "endpoint", defaults.Endpoint.Address, "OTLP endpoint (for HTTP, prefer http(s)://host:port/v1/traces)")
	flag.StringVar(&protocol, "protocol", string(defaults.Endpoint.Protocol), "OTLP protocol: grpc or http")
	flag.BoolVar(&insecure, "insecure", defaults.Endpoint.Insecure, "disable TLS for OTLP exporters")
	flag.StringVar(&tlsCACert, "tls-ca-cert", "", "path to PEM CA certificate file for server verification")
	flag.BoolVar(&tlsSkipVerify, "tls-skip-verify", false, "skip TLS certificate verification")
	flag.IntVar(&exporters, "exporters", defaults.Concurrency.Exporters, "number of concurrent exporters (connections)")
	flag.IntVar(&requestsPerExporter, "max-requests", defaults.Requests.PerExporter, "requests per exporter (0 for no request limit)")
	flag.Float64Var(&requestIntervalSeconds, "request-interval", defaults.Requests.Interval.Seconds(), "seconds between requests per exporter (0 for no delay)")
	flag.Float64Var(&requestForSeconds, "for", defaults.Requests.For.Seconds(), "seconds to send traces per exporter (0 for no duration limit)")
	flag.Float64Var(&rampUpSeconds, "ramp-up", defaults.Requests.RampUp.Seconds(), "seconds to linearly ramp exporter workers from 0 to max concurrency")
	flag.Float64Var(&exportTimeoutSeconds, "export-timeout", defaults.Requests.ExportTimeout.Seconds(), "seconds before each export attempt times out (0 disables per-export timeout)")
	flag.IntVar(&services, "services", defaults.Generator.Services, "number of distinct service names to emit")
	flag.IntVar(&maxDepth, "max-depth", defaults.Generator.MaxDepth, "maximum span depth per trace")
	flag.IntVar(&maxSpans, "max-spans", defaults.Generator.MaxSpans, "maximum spans per trace")
	flag.Float64Var(&errorRate, "error-rate", defaults.Generator.ErrorRate, "probability (0..1) of spans marked as error")
	flag.StringVar(&serviceName, "service-name", defaults.Generator.ServiceName, "service.name attribute for spans")
	flag.StringVar(&spanName, "span-name", defaults.Generator.SpanName, "span name to emit")
	flag.Var(&scenarioFiles, "scenario-file", "path to scenario JSON file; repeatable")
	flag.Var(&scenarioFiles, "s", "path to scenario JSON file (shorthand); repeatable")
	flag.StringVar(&scenarioStrategy, "scenario-strategy", string(scenario.SelectionStrategyRoundRobin), "scenario selection strategy when multiple scenarios: round-robin or random")
	flag.Int64Var(&scenarioRunSeed, "scenario-run-seed", 0, "seed namespace for scenario trace/span IDs (0 = auto-random per process)")
	flag.StringVar(&chaosPoliciesFile, "chaos-policies-file", "", "path to chaos policies JSON file")
	flag.Int64Var(&chaosSeed, "chaos-seed", 0, "override chaos policy seed (0 uses file/default)")
	flag.BoolVar(&dryRun, "dry-run", false, "generate traces without exporting to OTLP")
	flag.StringVar(&output, "output", string(otlp.DryRunOutputSummary), "output format: summary or json")
	flag.StringVar(&output, "o", string(otlp.DryRunOutputSummary), "output format shorthand: summary or json")
	flag.BoolVar(&summaryTraceIDs, "summary-trace-ids", false, "include sampled trace IDs in summary output")
	flag.IntVar(&summaryTraceIDsLimit, "summary-trace-ids-limit", 10, "maximum number of sampled trace IDs to include in summary")
	flag.Var(&headers, "header", "Header in Key=Value or Key: Value format; repeatable")
	flag.Float64Var(&slowResponseDelaySeconds, "slow-response-delay", 0, "seconds to delay reading each HTTP response body, simulating a slow client (HTTP only, 0 disables)")
	flag.Parse()
	if flag.NFlag() == 0 {
		fmt.Fprintln(os.Stderr, "error: no arguments provided; use --dry-run to generate locally or -h for help")
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
	if err := applyOTLPEnvOverrides(&endpoint, &protocol, &insecure, isFlagSet); err != nil {
		log.Fatalf("invalid OTLP environment override: %v", err)
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
		Generator: config.GeneratorConfig{
			Services:    services,
			MaxDepth:    maxDepth,
			MaxSpans:    maxSpans,
			ErrorRate:   errorRate,
			ServiceName: serviceName,
			SpanName:    spanName,
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
	if insecure && (isFlagSet("tls-ca-cert") || isFlagSet("tls-skip-verify")) {
		log.Printf("warning: --tls-ca-cert and --tls-skip-verify are ignored when --insecure is set")
		tlsCACert = ""
		tlsSkipVerify = false
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
		}
		factory = otlpFactory
		fmt.Fprintln(os.Stderr, "Running exporter preflight check...")
		if err := otlp.RunPreflight(ctx, otlpFactory, cfg.Requests.ExportTimeout.Duration); err != nil {
			log.Fatalf("preflight failed: %v", err)
		}
		fmt.Fprintln(os.Stderr, "Preflight check passed")
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
		generator := tracegen.Generator{
			ServiceName: cfg.Generator.ServiceName,
			SpanName:    cfg.Generator.SpanName,
			Services:    cfg.Generator.Services,
			MaxDepth:    cfg.Generator.MaxDepth,
			MaxSpans:    cfg.Generator.MaxSpans,
			ErrorRate:   cfg.Generator.ErrorRate,
		}
		stages = append(stages, pipeline.NewGeneratorStage(&generator))
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
	err = pipe.Run(ctx, runner, factory, cfg.Requests.Interval.Duration, cfg.Requests.For.Duration, cfg.Requests.RampUp.Duration, cfg.Requests.ExportTimeout.Duration, traceIDSampleLimit)
	summary := metrics.FormatSummary(pipe.Summary())
	if dryRun && outputFormat == otlp.DryRunOutputJSON {
		fmt.Fprintln(os.Stderr, summary)
	} else {
		fmt.Println(summary)
	}
	if err != nil {
		log.Printf("pipeline failed: %v", err)
		os.Exit(1)
	}
}

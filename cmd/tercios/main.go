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
	"github.com/javiermolinar/tercios/internal/tracegen"
)

func main() {
	var (
		endpoint               string
		protocol               string
		insecure               bool
		exporters              int
		requestsPerExporter    int
		requestIntervalSeconds float64
		requestForSeconds      float64
		services               int
		maxDepth               int
		maxSpans               int
		errorRate              float64
		serviceName            string
		spanName               string
		chaosPoliciesFile      string
		chaosSeed              int64
		dryRun                 bool
		output                 string
		headers                config.HeaderFlags
	)

	defaults := config.DefaultConfig()
	flag.StringVar(&endpoint, "endpoint", defaults.Endpoint.Address, "OTLP endpoint (for HTTP, prefer http(s)://host:port/v1/traces)")
	flag.StringVar(&protocol, "protocol", string(defaults.Endpoint.Protocol), "OTLP protocol: grpc or http")
	flag.BoolVar(&insecure, "insecure", defaults.Endpoint.Insecure, "disable TLS for OTLP exporters")
	flag.IntVar(&exporters, "exporters", defaults.Concurrency.Exporters, "number of concurrent exporters (connections)")
	flag.IntVar(&requestsPerExporter, "max-requests", defaults.Requests.PerExporter, "requests per exporter (0 for no request limit)")
	flag.Float64Var(&requestIntervalSeconds, "request-interval", defaults.Requests.Interval.Seconds(), "seconds between requests per exporter (0 for no delay)")
	flag.Float64Var(&requestForSeconds, "for", defaults.Requests.For.Seconds(), "seconds to send traces per exporter (0 for no duration limit)")
	flag.IntVar(&services, "services", defaults.Generator.Services, "number of distinct service names to emit")
	flag.IntVar(&maxDepth, "max-depth", defaults.Generator.MaxDepth, "maximum span depth per trace")
	flag.IntVar(&maxSpans, "max-spans", defaults.Generator.MaxSpans, "maximum spans per trace")
	flag.Float64Var(&errorRate, "error-rate", defaults.Generator.ErrorRate, "probability (0..1) of spans marked as error")
	flag.StringVar(&serviceName, "service-name", defaults.Generator.ServiceName, "service.name attribute for spans")
	flag.StringVar(&spanName, "span-name", defaults.Generator.SpanName, "span name to emit")
	flag.StringVar(&chaosPoliciesFile, "chaos-policies-file", "", "path to chaos policies JSON file")
	flag.Int64Var(&chaosSeed, "chaos-seed", 0, "override chaos policy seed (0 uses file/default)")
	flag.BoolVar(&dryRun, "dry-run", false, "generate traces without exporting to OTLP")
	flag.StringVar(&output, "output", string(otlp.DryRunOutputSummary), "output format: summary or json")
	flag.StringVar(&output, "o", string(otlp.DryRunOutputSummary), "output format shorthand: summary or json")
	flag.Var(&headers, "header", "Header in Key=Value or Key: Value format; repeatable")
	flag.Parse()
	requestInterval := time.Duration(requestIntervalSeconds * float64(time.Second))
	requestFor := time.Duration(requestForSeconds * float64(time.Second))
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
			PerExporter: requestsPerExporter,
			Interval:    config.Duration{Duration: requestInterval},
			For:         config.Duration{Duration: requestFor},
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	outputFormat, err := otlp.ParseDryRunOutput(output)
	if err != nil {
		log.Fatalf("invalid output format: %v", err)
	}
	if !dryRun && outputFormat != otlp.DryRunOutputSummary {
		log.Fatalf("-o/--output=%s requires --dry-run", outputFormat)
	}

	var factory pipeline.ExporterFactory
	if dryRun {
		dryRunFactory := otlp.NewDryRunExporterFactory(outputFormat, os.Stdout)
		factory = dryRunFactory
	} else {
		factory = otlp.ExporterFactory{
			Protocol: cfg.Endpoint.Protocol,
			Endpoint: cfg.Endpoint.Address,
			Insecure: cfg.Endpoint.Insecure,
			Headers:  cfg.Endpoint.Headers,
		}
	}

	generator := tracegen.Generator{
		ServiceName: cfg.Generator.ServiceName,
		SpanName:    cfg.Generator.SpanName,
		Services:    cfg.Generator.Services,
		MaxDepth:    cfg.Generator.MaxDepth,
		MaxSpans:    cfg.Generator.MaxSpans,
		ErrorRate:   cfg.Generator.ErrorRate,
	}

	runner := pipeline.NewConcurrencyRunner(cfg.Concurrency.Exporters, cfg.Requests.PerExporter)
	stages := []pipeline.BatchStage{
		pipeline.NewGeneratorStage(&generator),
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
	err = pipe.Run(ctx, runner, factory, cfg.Requests.Interval.Duration, cfg.Requests.For.Duration)
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

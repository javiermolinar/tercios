package otlp

import (
	"context"
	"fmt"

	"github.com/javiermolinar/tercios/internal/config"
	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

type directBatchExporter struct {
	client otlptrace.Client
}

func (e *directBatchExporter) ExportBatch(ctx context.Context, batch model.Batch) error {
	if len(batch) == 0 {
		return nil
	}
	resourceSpans := modelBatchToProto(batch)
	if len(resourceSpans) == 0 {
		return nil
	}
	return e.client.UploadTraces(ctx, resourceSpans)
}

func (e *directBatchExporter) Shutdown(ctx context.Context) error {
	if e == nil || e.client == nil {
		return nil
	}
	return e.client.Stop(ctx)
}

func (f ExporterFactory) NewBatchExporter(ctx context.Context) (model.BatchExporter, error) {
	client, err := f.newOTLPClient()
	if err != nil {
		return nil, err
	}
	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("start otlp client: %w", err)
	}
	return &directBatchExporter{client: client}, nil
}

func (f ExporterFactory) newOTLPClient() (otlptrace.Client, error) {
	endpoint, path, err := parseEndpoint(f.Endpoint)
	if err != nil {
		return nil, err
	}

	if f.Protocol == config.ProtocolHTTP {
		options := []otlptracehttp.Option{otlptracehttp.WithEndpoint(endpoint)}
		if f.Insecure {
			options = append(options, otlptracehttp.WithInsecure())
		}
		if path != "" {
			options = append(options, otlptracehttp.WithURLPath(path))
		}
		if len(f.Headers) > 0 {
			options = append(options, otlptracehttp.WithHeaders(f.Headers))
		}
		return otlptracehttp.NewClient(options...), nil
	}

	options := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
	if f.Insecure {
		options = append(options, otlptracegrpc.WithInsecure())
	}
	if len(f.Headers) > 0 {
		options = append(options, otlptracegrpc.WithHeaders(f.Headers))
	}
	return otlptracegrpc.NewClient(options...), nil
}

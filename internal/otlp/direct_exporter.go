package otlp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/javiermolinar/tercios/internal/config"
	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"google.golang.org/grpc/credentials"
)

type directBatchExporter struct {
	client   otlptrace.Client
	protocol config.Protocol
	endpoint string
}

func (e *directBatchExporter) ExportBatch(ctx context.Context, batch model.Batch) error {
	if len(batch) == 0 {
		return nil
	}
	resourceSpans := modelBatchToProto(batch)
	if len(resourceSpans) == 0 {
		return nil
	}
	if err := e.client.UploadTraces(ctx, resourceSpans); err != nil {
		return fmt.Errorf("upload traces protocol=%s endpoint=%s: %w", e.protocol, e.endpoint, err)
	}
	return nil
}

func (e *directBatchExporter) Shutdown(ctx context.Context) error {
	if e == nil || e.client == nil {
		return nil
	}
	if err := e.client.Stop(ctx); err != nil {
		return fmt.Errorf("stop otlp client protocol=%s endpoint=%s: %w", e.protocol, e.endpoint, err)
	}
	return nil
}

func (f ExporterFactory) NewBatchExporter(ctx context.Context) (model.BatchExporter, error) {
	client, err := f.newOTLPClient()
	if err != nil {
		return nil, err
	}
	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("start otlp client protocol=%s endpoint=%s: %w", f.Protocol, f.Endpoint, err)
	}
	return &directBatchExporter{client: client, protocol: f.Protocol, endpoint: f.Endpoint}, nil
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
		if f.SlowResponseDelay > 0 {
			base := http.DefaultTransport.(*http.Transport).Clone()
			if tlsCfg, err := f.tlsConfig(); err != nil {
				return nil, err
			} else if tlsCfg != nil {
				base.TLSClientConfig = tlsCfg
			}
			options = append(options, otlptracehttp.WithHTTPClient(&http.Client{
				Transport: &slowRoundTripper{wrapped: base, delay: f.SlowResponseDelay},
			}))
		} else if tlsCfg, err := f.tlsConfig(); err != nil {
			return nil, err
		} else if tlsCfg != nil {
			options = append(options, otlptracehttp.WithTLSClientConfig(tlsCfg))
		}
		return otlptracehttp.NewClient(options...), nil
	}

	options := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
	if f.Insecure {
		options = append(options, otlptracegrpc.WithInsecure())
	} else if tlsCfg, err := f.tlsConfig(); err != nil {
		return nil, err
	} else if tlsCfg != nil {
		options = append(options, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)))
	}
	if len(f.Headers) > 0 {
		options = append(options, otlptracegrpc.WithHeaders(f.Headers))
	}
	return otlptracegrpc.NewClient(options...), nil
}

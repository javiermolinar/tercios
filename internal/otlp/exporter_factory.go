package otlp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/javiermolinar/tercios/internal/config"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/trace"
)

type ExporterFactory struct {
	Protocol config.Protocol
	Endpoint string
	Insecure bool
	Headers  map[string]string
}

func (f ExporterFactory) NewExporter(ctx context.Context) (trace.SpanExporter, error) {
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
		return otlptracehttp.New(ctx, options...)
	}

	options := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
	if f.Insecure {
		options = append(options, otlptracegrpc.WithInsecure())
	}
	if len(f.Headers) > 0 {
		options = append(options, otlptracegrpc.WithHeaders(f.Headers))
	}
	return otlptracegrpc.New(ctx, options...)
}

func parseEndpoint(raw string) (endpoint string, path string, err error) {
	if raw == "" {
		return "", "", fmt.Errorf("endpoint is required")
	}
	parsed, parseErr := url.Parse(raw)
	if parseErr == nil && parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "grpc":
			// Scheme is only used for parsing host/path; security is explicit.
		case "https", "grpcs":
			// Scheme is only used for parsing host/path; security is explicit.
		default:
			if strings.Contains(raw, "://") {
				return "", "", fmt.Errorf("unsupported scheme %q", parsed.Scheme)
			}
			return raw, "", nil
		}
		endpoint = parsed.Host
		path = strings.TrimSpace(parsed.Path)
		if endpoint == "" {
			return "", "", fmt.Errorf("endpoint host is required")
		}
		return endpoint, path, nil
	}

	return raw, "", nil
}

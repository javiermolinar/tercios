package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	envOTLPTracesEndpoint = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envOTLPEndpoint       = "OTEL_EXPORTER_OTLP_ENDPOINT"

	envOTLPTracesProtocol = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	envOTLPProtocol       = "OTEL_EXPORTER_OTLP_PROTOCOL"

	envOTLPTracesInsecure = "OTEL_EXPORTER_OTLP_TRACES_INSECURE"
	envOTLPInsecure       = "OTEL_EXPORTER_OTLP_INSECURE"
)

func applyOTLPEnvOverrides(endpoint *string, protocol *string, insecure *bool, isFlagSet func(string) bool) error {
	if !isFlagSet("endpoint") {
		if value, ok := firstNonEmptyEnv(envOTLPTracesEndpoint, envOTLPEndpoint); ok {
			*endpoint = value
		}
	}

	if !isFlagSet("protocol") {
		if value, ok := firstNonEmptyEnv(envOTLPTracesProtocol, envOTLPProtocol); ok {
			normalized, err := normalizeOTLPProtocol(value)
			if err != nil {
				return fmt.Errorf("invalid %s/%s value %q: %w", envOTLPTracesProtocol, envOTLPProtocol, value, err)
			}
			*protocol = normalized
		}
	}

	if !isFlagSet("insecure") {
		if value, ok := firstNonEmptyEnv(envOTLPTracesInsecure, envOTLPInsecure); ok {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("invalid %s/%s value %q: %w", envOTLPTracesInsecure, envOTLPInsecure, value, err)
			}
			*insecure = parsed
		}
	}

	return nil
}

func applyEndpointSchemeSecurityDefaults(endpoint string, insecure *bool, insecureExplicit bool) error {
	usesTLS, scheme, ok, err := endpointSchemeUsesTLS(endpoint)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if insecure == nil {
		return fmt.Errorf("insecure setting is not configured")
	}
	if insecureExplicit {
		if usesTLS && *insecure {
			return fmt.Errorf("endpoint scheme %q implies TLS but insecure mode was explicitly enabled", scheme)
		}
		return nil
	}

	*insecure = !usesTLS
	return nil
}

func endpointSchemeUsesTLS(raw string) (usesTLS bool, scheme string, ok bool, err error) {
	if !strings.Contains(raw, "://") {
		return false, "", false, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false, "", false, err
	}
	scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	switch scheme {
	case "https", "grpcs":
		return true, scheme, true, nil
	case "http", "grpc":
		return false, scheme, true, nil
	default:
		return false, scheme, false, nil
	}
}

func validateTLSConfiguration(insecure bool, tlsCACert string, tlsSkipVerify bool) error {
	if !insecure {
		return nil
	}
	if strings.TrimSpace(tlsCACert) != "" {
		return fmt.Errorf("--tls-ca-cert requires TLS; use --insecure=false or an https/grpcs endpoint")
	}
	if tlsSkipVerify {
		return fmt.Errorf("--tls-skip-verify requires TLS; use --insecure=false or an https/grpcs endpoint")
	}
	return nil
}

func firstNonEmptyEnv(keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		return trimmed, true
	}
	return "", false
}

func normalizeOTLPProtocol(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "grpc":
		return "grpc", nil
	case "http", "http/protobuf":
		return "http", nil
	default:
		return "", fmt.Errorf("expected grpc, http, or http/protobuf")
	}
}

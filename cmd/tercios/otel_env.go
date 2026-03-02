package main

import (
	"fmt"
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

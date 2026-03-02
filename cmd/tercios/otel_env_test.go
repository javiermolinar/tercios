package main

import (
	"testing"
)

func TestApplyOTLPEnvOverrides_UsesTraceSpecificEndpoint(t *testing.T) {
	t.Setenv(envOTLPEndpoint, "global:4317")
	t.Setenv(envOTLPTracesEndpoint, "trace-specific:4318")

	endpoint := "default:4317"
	protocol := "grpc"
	insecure := true

	err := applyOTLPEnvOverrides(&endpoint, &protocol, &insecure, flagSetFn())
	if err != nil {
		t.Fatalf("apply overrides failed: %v", err)
	}
	if endpoint != "trace-specific:4318" {
		t.Fatalf("expected trace-specific endpoint, got %q", endpoint)
	}
}

func TestApplyOTLPEnvOverrides_RespectsFlagPriority(t *testing.T) {
	t.Setenv(envOTLPTracesEndpoint, "env-endpoint:4318")
	t.Setenv(envOTLPTracesProtocol, "http")
	t.Setenv(envOTLPTracesInsecure, "false")

	endpoint := "flag-endpoint:4317"
	protocol := "grpc"
	insecure := true

	err := applyOTLPEnvOverrides(&endpoint, &protocol, &insecure, flagSetFn("endpoint", "protocol", "insecure"))
	if err != nil {
		t.Fatalf("apply overrides failed: %v", err)
	}
	if endpoint != "flag-endpoint:4317" {
		t.Fatalf("expected endpoint from flag, got %q", endpoint)
	}
	if protocol != "grpc" {
		t.Fatalf("expected protocol from flag, got %q", protocol)
	}
	if !insecure {
		t.Fatalf("expected insecure from flag")
	}
}

func TestApplyOTLPEnvOverrides_ProtocolNormalization(t *testing.T) {
	t.Setenv(envOTLPTracesProtocol, "http/protobuf")

	endpoint := "localhost:4317"
	protocol := "grpc"
	insecure := true

	err := applyOTLPEnvOverrides(&endpoint, &protocol, &insecure, flagSetFn())
	if err != nil {
		t.Fatalf("apply overrides failed: %v", err)
	}
	if protocol != "http" {
		t.Fatalf("expected http protocol, got %q", protocol)
	}
}

func TestApplyOTLPEnvOverrides_InsecureBoolParsing(t *testing.T) {
	t.Setenv(envOTLPTracesInsecure, "false")

	endpoint := "localhost:4317"
	protocol := "grpc"
	insecure := true

	err := applyOTLPEnvOverrides(&endpoint, &protocol, &insecure, flagSetFn())
	if err != nil {
		t.Fatalf("apply overrides failed: %v", err)
	}
	if insecure {
		t.Fatalf("expected insecure=false from env override")
	}
}

func TestApplyOTLPEnvOverrides_InvalidProtocol(t *testing.T) {
	t.Setenv(envOTLPTracesProtocol, "invalid")

	endpoint := "localhost:4317"
	protocol := "grpc"
	insecure := true

	err := applyOTLPEnvOverrides(&endpoint, &protocol, &insecure, flagSetFn())
	if err == nil {
		t.Fatalf("expected error for invalid protocol")
	}
}

func flagSetFn(names ...string) func(string) bool {
	set := map[string]struct{}{}
	for _, name := range names {
		set[name] = struct{}{}
	}
	return func(name string) bool {
		_, ok := set[name]
		return ok
	}
}

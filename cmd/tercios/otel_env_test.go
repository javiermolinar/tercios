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

func TestApplyEndpointSchemeSecurityDefaults_HTTPSDefaultsToTLS(t *testing.T) {
	insecure := true

	err := applyEndpointSchemeSecurityDefaults("https://collector.example:4318/v1/traces", &insecure, false)
	if err != nil {
		t.Fatalf("apply endpoint security failed: %v", err)
	}
	if insecure {
		t.Fatalf("expected https endpoint to default to TLS")
	}
}

func TestApplyEndpointSchemeSecurityDefaults_ExplicitInsecureConflictsWithHTTPS(t *testing.T) {
	insecure := true

	err := applyEndpointSchemeSecurityDefaults("https://collector.example:4318/v1/traces", &insecure, true)
	if err == nil {
		t.Fatalf("expected conflict for explicit insecure=true with https endpoint")
	}
}

func TestApplyEndpointSchemeSecurityDefaults_ExplicitInsecureFalseWinsForHostPort(t *testing.T) {
	insecure := false

	err := applyEndpointSchemeSecurityDefaults("collector.example:4317", &insecure, true)
	if err != nil {
		t.Fatalf("apply endpoint security failed: %v", err)
	}
	if insecure {
		t.Fatalf("expected explicit insecure=false to be preserved")
	}
}

func TestValidateTLSConfigurationRejectsTLSFlagsWhenInsecure(t *testing.T) {
	if err := validateTLSConfiguration(true, "ca.pem", false); err == nil {
		t.Fatalf("expected --tls-ca-cert to require TLS")
	}
	if err := validateTLSConfiguration(true, "", true); err == nil {
		t.Fatalf("expected --tls-skip-verify to require TLS")
	}
	if err := validateTLSConfiguration(false, "ca.pem", true); err != nil {
		t.Fatalf("expected TLS options to be valid when TLS is enabled: %v", err)
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

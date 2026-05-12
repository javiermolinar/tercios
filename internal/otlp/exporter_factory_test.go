package otlp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/javiermolinar/tercios/internal/config"
)

func TestMergedTLSConfig_PreservesEnvClientCertificateWhenFlagCACertSet(t *testing.T) {
	caCertPEM, _ := testCertificatePairPEM(t)
	clientCertPEM, clientKeyPEM := testCertificatePairPEM(t)

	dir := t.TempDir()
	caPath := writeTestPEMFile(t, dir, "ca.pem", caCertPEM)
	clientCertPath := writeTestPEMFile(t, dir, "client-cert.pem", clientCertPEM)
	clientKeyPath := writeTestPEMFile(t, dir, "client-key.pem", clientKeyPEM)

	cfg, err := mergedTLSConfig(
		ExporterFactory{TLSCACert: caPath},
		testEnvLookup(map[string]string{
			envOTLPClientCertificate: clientCertPath,
			envOTLPClientKey:         clientKeyPath,
		}),
		os.ReadFile,
	)
	if err != nil {
		t.Fatalf("mergedTLSConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatalf("mergedTLSConfig() returned nil config")
	}
	if cfg.RootCAs == nil || cfg.RootCAs.Equal(x509.NewCertPool()) {
		t.Fatalf("expected RootCAs from --tls-ca-cert to be preserved")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected env client certificate to be preserved, got %d certs", len(cfg.Certificates))
	}
}

func TestMergedTLSConfig_PreservesEnvClientCertificateWhenSkipVerifySet(t *testing.T) {
	clientCertPEM, clientKeyPEM := testCertificatePairPEM(t)

	dir := t.TempDir()
	clientCertPath := writeTestPEMFile(t, dir, "client-cert.pem", clientCertPEM)
	clientKeyPath := writeTestPEMFile(t, dir, "client-key.pem", clientKeyPEM)

	cfg, err := mergedTLSConfig(
		ExporterFactory{TLSSkipVerify: true},
		testEnvLookup(map[string]string{
			envOTLPClientCertificate: clientCertPath,
			envOTLPClientKey:         clientKeyPath,
		}),
		os.ReadFile,
	)
	if err != nil {
		t.Fatalf("mergedTLSConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatalf("mergedTLSConfig() returned nil config")
	}
	if !cfg.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify to be enabled")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected env client certificate to be preserved, got %d certs", len(cfg.Certificates))
	}
}

func TestMergedTLSConfig_FallsBackToGenericEnvClientCertificateWhenTraceSpecificIsInvalid(t *testing.T) {
	genericCertPEM, genericKeyPEM := testCertificatePairPEM(t)
	traceCertPEM, _ := testCertificatePairPEM(t)

	dir := t.TempDir()
	genericCertPath := writeTestPEMFile(t, dir, "generic-client-cert.pem", genericCertPEM)
	genericKeyPath := writeTestPEMFile(t, dir, "generic-client-key.pem", genericKeyPEM)
	traceCertPath := writeTestPEMFile(t, dir, "trace-client-cert.pem", traceCertPEM)
	missingTraceKeyPath := filepath.Join(dir, "missing-trace-client-key.pem")

	cfg, err := mergedTLSConfig(
		ExporterFactory{},
		testEnvLookup(map[string]string{
			envOTLPClientCertificate:       genericCertPath,
			envOTLPClientKey:               genericKeyPath,
			envOTLPTracesClientCertificate: traceCertPath,
			envOTLPTracesClientKey:         missingTraceKeyPath,
		}),
		os.ReadFile,
	)
	if err != nil {
		t.Fatalf("mergedTLSConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatalf("mergedTLSConfig() returned nil config")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected generic env client certificate fallback, got %d certs", len(cfg.Certificates))
	}
}

func TestNewBatchExporter_HonorsTLSConfigForHTTP(t *testing.T) {
	_, err := (ExporterFactory{
		Protocol:  config.ProtocolHTTP,
		Endpoint:  "https://localhost:4318/v1/traces",
		TLSCACert: filepath.Join(t.TempDir(), "missing-ca.pem"),
	}).NewBatchExporter(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "read TLS CA cert") {
		t.Fatalf("expected TLS config error, got %v", err)
	}
}

func TestNewBatchExporter_HonorsTLSConfigForGRPC(t *testing.T) {
	_, err := (ExporterFactory{
		Protocol:  config.ProtocolGRPC,
		Endpoint:  "localhost:4317",
		TLSCACert: filepath.Join(t.TempDir(), "missing-ca.pem"),
	}).NewBatchExporter(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "read TLS CA cert") {
		t.Fatalf("expected TLS config error, got %v", err)
	}
}

func TestNewOTLPClient_AcceptsExportTimeout(t *testing.T) {
	cases := []struct {
		name     string
		protocol config.Protocol
		endpoint string
	}{
		{name: "grpc", protocol: config.ProtocolGRPC, endpoint: "localhost:4317"},
		{name: "http", protocol: config.ProtocolHTTP, endpoint: "http://localhost:4318/v1/traces"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			factory := ExporterFactory{
				Protocol:      tc.protocol,
				Endpoint:      tc.endpoint,
				Insecure:      true,
				ExportTimeout: 30 * time.Second,
			}
			client, err := factory.newOTLPClient()
			if err != nil {
				t.Fatalf("newOTLPClient() error = %v", err)
			}
			if client == nil {
				t.Fatalf("newOTLPClient() returned nil client")
			}
		})
	}
}

func testEnvLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func writeTestPEMFile(t *testing.T, dir string, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}

func testCertificatePairPEM(t *testing.T) ([]byte, []byte) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "tercios.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return certPEM, keyPEM
}

package otlp

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/javiermolinar/tercios/internal/config"
)

const (
	envOTLPCertificate             = "OTEL_EXPORTER_OTLP_CERTIFICATE"
	envOTLPTracesCertificate       = "OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE"
	envOTLPClientCertificate       = "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE"
	envOTLPClientKey               = "OTEL_EXPORTER_OTLP_CLIENT_KEY"
	envOTLPTracesClientCertificate = "OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE"
	envOTLPTracesClientKey         = "OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY"
)

type ExporterFactory struct {
	Protocol          config.Protocol
	Endpoint          string
	Insecure          bool
	Headers           map[string]string
	SlowResponseDelay time.Duration
	TLSCACert         string
	TLSSkipVerify     bool
}

func (f ExporterFactory) tlsConfig() (*tls.Config, error) {
	return mergedTLSConfig(f, os.LookupEnv, os.ReadFile)
}

func mergedTLSConfig(
	f ExporterFactory,
	lookupEnv func(string) (string, bool),
	readFile func(string) ([]byte, error),
) (*tls.Config, error) {
	cfg := &tls.Config{}

	applyCertPoolFromEnv(cfg, lookupEnv, readFile, envOTLPCertificate)
	applyCertPoolFromEnv(cfg, lookupEnv, readFile, envOTLPTracesCertificate)
	applyClientCertificateFromEnv(cfg, lookupEnv, readFile, envOTLPClientCertificate, envOTLPClientKey)
	applyClientCertificateFromEnv(cfg, lookupEnv, readFile, envOTLPTracesClientCertificate, envOTLPTracesClientKey)

	if f.TLSCACert != "" {
		pool, err := loadCertPool(f.TLSCACert, readFile)
		if err != nil {
			return nil, fmt.Errorf("read TLS CA cert %q: %w", f.TLSCACert, err)
		}
		cfg.RootCAs = pool
	}
	if f.TLSSkipVerify {
		cfg.InsecureSkipVerify = true //nolint:gosec
	}
	if cfg.RootCAs == nil && len(cfg.Certificates) == 0 && !cfg.InsecureSkipVerify {
		return nil, nil
	}
	return cfg, nil
}

func applyCertPoolFromEnv(
	cfg *tls.Config,
	lookupEnv func(string) (string, bool),
	readFile func(string) ([]byte, error),
	envKey string,
) {
	path, ok := lookupNonEmptyEnv(lookupEnv, envKey)
	if !ok {
		return
	}
	pool, err := loadCertPool(path, readFile)
	if err != nil {
		return
	}
	cfg.RootCAs = pool
}

func applyClientCertificateFromEnv(
	cfg *tls.Config,
	lookupEnv func(string) (string, bool),
	readFile func(string) ([]byte, error),
	certEnvKey string,
	keyEnvKey string,
) {
	certPath, ok := lookupNonEmptyEnv(lookupEnv, certEnvKey)
	if !ok {
		return
	}
	keyPath, ok := lookupNonEmptyEnv(lookupEnv, keyEnvKey)
	if !ok {
		return
	}

	certPEM, err := readFile(certPath)
	if err != nil {
		return
	}
	keyPEM, err := readFile(keyPath)
	if err != nil {
		return
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return
	}
	cfg.Certificates = []tls.Certificate{cert}
}

func loadCertPool(path string, readFile func(string) ([]byte, error)) (*x509.CertPool, error) {
	pem, err := readFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no valid PEM certificates found in %q", path)
	}
	return pool, nil
}

func lookupNonEmptyEnv(lookupEnv func(string) (string, bool), key string) (string, bool) {
	value, ok := lookupEnv(key)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
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

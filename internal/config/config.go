package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

type Protocol string

const (
	ProtocolGRPC Protocol = "grpc"
	ProtocolHTTP Protocol = "http"
)

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "" {
			d.Duration = 0
			return nil
		}
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		d.Duration = parsed
		return nil
	}

	var seconds float64
	if err := json.Unmarshal(data, &seconds); err == nil {
		d.Duration = time.Duration(seconds * float64(time.Second))
		return nil
	}

	return fmt.Errorf("invalid duration %q", string(data))
}

func (d Duration) Seconds() float64 {
	return d.Duration.Seconds()
}

type EndpointConfig struct {
	Address  string            `json:"address"`
	Protocol Protocol          `json:"protocol"`
	Insecure bool              `json:"insecure"`
	Headers  map[string]string `json:"headers,omitempty"`
}

type ConcurrencyConfig struct {
	Exporters int `json:"exporters"`
}

type RequestConfig struct {
	PerExporter int      `json:"per_exporter"`
	Interval    Duration `json:"interval"`
	For         Duration `json:"for"`
}

type GeneratorConfig struct {
	Services    int     `json:"services"`
	MaxDepth    int     `json:"max_depth"`
	MaxSpans    int     `json:"max_spans"`
	ErrorRate   float64 `json:"error_rate"`
	ServiceName string  `json:"service_name"`
	SpanName    string  `json:"span_name"`
}

type Config struct {
	Endpoint    EndpointConfig    `json:"endpoint"`
	Concurrency ConcurrencyConfig `json:"concurrency"`
	Requests    RequestConfig     `json:"requests"`
	Generator   GeneratorConfig   `json:"generator"`
}

func DefaultConfig() Config {
	return Config{
		Endpoint: EndpointConfig{
			Address:  "localhost:4317",
			Protocol: ProtocolGRPC,
			Insecure: true,
			Headers:  map[string]string{},
		},
		Concurrency: ConcurrencyConfig{
			Exporters: 1,
		},
		Requests: RequestConfig{
			PerExporter: 1,
			Interval:    Duration{Duration: 0},
			For:         Duration{Duration: 0},
		},
		Generator: GeneratorConfig{
			Services:    3,
			MaxDepth:    3,
			MaxSpans:    10,
			ErrorRate:   0.2,
			ServiceName: "",
			SpanName:    "",
		},
	}
}

func LoadFromJSON(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	return DecodeJSON(file)
}

func DecodeJSON(r io.Reader) (Config, error) {
	cfg := DefaultConfig()
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("invalid JSON: %w", err)
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Endpoint.Address == "" {
		return fmt.Errorf("endpoint is required")
	}
	if c.Endpoint.Protocol != ProtocolGRPC && c.Endpoint.Protocol != ProtocolHTTP {
		return fmt.Errorf("unsupported protocol %q", c.Endpoint.Protocol)
	}
	if c.Concurrency.Exporters <= 0 {
		return fmt.Errorf("exporters must be > 0")
	}
	if c.Requests.PerExporter <= 0 {
		return fmt.Errorf("max requests must be > 0")
	}
	if c.Requests.Interval.Duration < 0 {
		return fmt.Errorf("request interval must be >= 0")
	}
	if c.Requests.For.Duration < 0 {
		return fmt.Errorf("request duration must be >= 0")
	}
	if c.Generator.Services <= 0 {
		return fmt.Errorf("services must be > 0")
	}
	if c.Generator.MaxDepth <= 0 {
		return fmt.Errorf("max depth must be > 0")
	}
	if c.Generator.MaxSpans <= 0 {
		return fmt.Errorf("max spans must be > 0")
	}
	if c.Generator.ErrorRate < 0 || c.Generator.ErrorRate > 1 {
		return fmt.Errorf("error rate must be between 0 and 1")
	}
	return nil
}

package config

import (
	"encoding/json"
	"fmt"
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
	return json.Marshal(d.String())
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
	Address       string            `json:"address"`
	Protocol      Protocol          `json:"protocol"`
	Insecure      bool              `json:"insecure"`
	Headers       map[string]string `json:"headers,omitempty"`
	TLSCACert     string            `json:"tls_ca_cert,omitempty"`
	TLSSkipVerify bool              `json:"tls_skip_verify,omitempty"`
}

type ConcurrencyConfig struct {
	Exporters int `json:"exporters"`
}

type RequestConfig struct {
	PerExporter   int      `json:"per_exporter"`
	Interval      Duration `json:"interval"`
	For           Duration `json:"for"`
	RampUp        Duration `json:"ramp_up"`
	ExportTimeout Duration `json:"export_timeout"`
}

type Config struct {
	Endpoint    EndpointConfig    `json:"endpoint"`
	Concurrency ConcurrencyConfig `json:"concurrency"`
	Requests    RequestConfig     `json:"requests"`
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
			PerExporter:   1,
			Interval:      Duration{Duration: 0},
			For:           Duration{Duration: 0},
			RampUp:        Duration{Duration: 0},
			ExportTimeout: Duration{Duration: 10 * time.Second},
		},
	}
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
	if c.Requests.PerExporter < 0 {
		return fmt.Errorf("max requests must be >= 0")
	}
	if c.Requests.Interval.Duration < 0 {
		return fmt.Errorf("request interval must be >= 0")
	}
	if c.Requests.For.Duration < 0 {
		return fmt.Errorf("request duration must be >= 0")
	}
	if c.Requests.RampUp.Duration < 0 {
		return fmt.Errorf("ramp-up must be >= 0")
	}
	if c.Requests.ExportTimeout.Duration < 0 {
		return fmt.Errorf("export timeout must be >= 0")
	}
	return nil
}

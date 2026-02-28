package chaos

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type PolicyMode string

const (
	PolicyModeAll        PolicyMode = "all"
	PolicyModeFirstMatch PolicyMode = "first_match"
)

type Config struct {
	Seed       int64      `json:"seed"`
	PolicyMode PolicyMode `json:"policy_mode"`
	Policies   []Policy   `json:"policies"`
}

type Policy struct {
	Name        string   `json:"name"`
	Probability float64  `json:"probability"`
	Match       Match    `json:"match"`
	Actions     []Action `json:"actions"`
}

type Match struct {
	ServiceName string         `json:"service_name"`
	SpanName    string         `json:"span_name"`
	SpanKinds   []string       `json:"span_kinds"`
	Attributes  map[string]any `json:"attributes"`
}

type Action struct {
	Type string `json:"type"`

	// set_attribute
	Scope string `json:"scope,omitempty"`
	Name  string `json:"name,omitempty"`
	Value any    `json:"value,omitempty"`

	// set_status
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func DefaultConfig() Config {
	return Config{PolicyMode: PolicyModeAll}
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
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.PolicyMode != "" && c.PolicyMode != PolicyModeAll && c.PolicyMode != PolicyModeFirstMatch {
		return fmt.Errorf("unsupported policy mode %q", c.PolicyMode)
	}
	for i, policy := range c.Policies {
		if strings.TrimSpace(policy.Name) == "" {
			return fmt.Errorf("policy %d: name is required", i)
		}
		if policy.Probability < 0 || policy.Probability > 1 {
			return fmt.Errorf("policy %s: probability must be between 0 and 1", policy.Name)
		}
		if len(policy.Actions) == 0 {
			return fmt.Errorf("policy %s: at least one action is required", policy.Name)
		}
		for _, action := range policy.Actions {
			if err := validateAction(policy.Name, action); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAction(policyName string, action Action) error {
	typeName := strings.ToLower(strings.TrimSpace(action.Type))
	switch typeName {
	case "set_attribute":
		scope := strings.ToLower(strings.TrimSpace(action.Scope))
		if scope != "span" && scope != "resource" {
			return fmt.Errorf("policy %s: set_attribute scope must be span or resource", policyName)
		}
		if strings.TrimSpace(action.Name) == "" {
			return fmt.Errorf("policy %s: set_attribute requires name", policyName)
		}
	case "set_status":
		code := strings.ToLower(strings.TrimSpace(action.Code))
		if code != "ok" && code != "error" && code != "unset" {
			return fmt.Errorf("policy %s: set_status code must be ok, error, or unset", policyName)
		}
	default:
		return fmt.Errorf("policy %s: unsupported action type %q", policyName, action.Type)
	}
	return nil
}

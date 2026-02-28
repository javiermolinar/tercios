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

type ValueType string

const (
	ValueTypeString ValueType = "string"
	ValueTypeInt    ValueType = "int"
	ValueTypeFloat  ValueType = "float"
	ValueTypeBool   ValueType = "bool"
)

type TypedValue struct {
	Type  ValueType `json:"type"`
	Value any       `json:"value"`
}

type Match struct {
	ServiceName string                `json:"service_name"`
	SpanName    string                `json:"span_name"`
	SpanKinds   []string              `json:"span_kinds"`
	Attributes  map[string]TypedValue `json:"attributes"`
}

type Action struct {
	Type string `json:"type"`

	// set_attribute
	Scope string     `json:"scope,omitempty"`
	Name  string     `json:"name,omitempty"`
	Value TypedValue `json:"value,omitempty"`

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
		for key, value := range policy.Match.Attributes {
			if err := value.Validate(fmt.Sprintf("policy %s: match attribute %q", policy.Name, key)); err != nil {
				return err
			}
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
		if err := action.Value.Validate(fmt.Sprintf("policy %s: set_attribute %q", policyName, action.Name)); err != nil {
			return err
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

func (v TypedValue) Validate(field string) error {
	typeName := strings.ToLower(strings.TrimSpace(string(v.Type)))
	if typeName == "" {
		return fmt.Errorf("%s: value type is required", field)
	}
	if v.Value == nil {
		return fmt.Errorf("%s: value is required", field)
	}

	switch ValueType(typeName) {
	case ValueTypeString:
		if _, ok := v.Value.(string); !ok {
			return fmt.Errorf("%s: expected string value", field)
		}
	case ValueTypeBool:
		if _, ok := v.Value.(bool); !ok {
			return fmt.Errorf("%s: expected bool value", field)
		}
	case ValueTypeInt:
		if _, ok := toInt64(v.Value); !ok {
			return fmt.Errorf("%s: expected int value", field)
		}
	case ValueTypeFloat:
		if _, ok := toFloat64(v.Value); !ok {
			return fmt.Errorf("%s: expected float value", field)
		}
	default:
		return fmt.Errorf("%s: unsupported value type %q", field, v.Type)
	}
	return nil
}

func (v TypedValue) Normalized() (any, bool) {
	switch ValueType(strings.ToLower(strings.TrimSpace(string(v.Type)))) {
	case ValueTypeString:
		s, ok := v.Value.(string)
		return s, ok
	case ValueTypeBool:
		b, ok := v.Value.(bool)
		return b, ok
	case ValueTypeInt:
		i, ok := toInt64(v.Value)
		return i, ok
	case ValueTypeFloat:
		f, ok := toFloat64(v.Value)
		return f, ok
	default:
		return nil, false
	}
}

func toInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		return int64(v), true
	case float64:
		i := int64(v)
		return i, float64(i) == v
	case float32:
		i := int64(v)
		return i, float32(i) == v
	default:
		return 0, false
	}
}

func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	default:
		return 0, false
	}
}

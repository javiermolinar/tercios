package chaos

import (
	"strings"
	"testing"
)

func TestDecodeJSONValid(t *testing.T) {
	input := `{
  "seed": 42,
  "policy_mode": "first_match",
  "policies": [
    {
      "name": "post-500",
      "probability": 0.2,
      "match": {
        "service_name": "post-service",
        "span_kinds": ["server"],
        "attributes": {
          "http.route": "/posts"
        }
      },
      "actions": [
        { "type": "set_attribute", "scope": "span", "name": "http.response.status_code", "value": 500 },
        { "type": "set_status", "code": "error", "message": "simulated failure" }
      ]
    }
  ]
}`

	cfg, err := DecodeJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}
	if cfg.PolicyMode != PolicyModeFirstMatch {
		t.Fatalf("expected policy_mode first_match, got %q", cfg.PolicyMode)
	}
	if len(cfg.Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(cfg.Policies))
	}
}

func TestDecodeJSONInvalidActionScope(t *testing.T) {
	input := `{
  "policies": [
    {
      "name": "invalid-scope",
      "probability": 1,
      "match": {},
      "actions": [
        { "type": "set_attribute", "scope": "trace", "name": "service.version", "value": "2.11.0" }
      ]
    }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestDecodeJSONInvalidProbability(t *testing.T) {
	input := `{
  "policies": [
    {
      "name": "invalid-probability",
      "probability": 1.5,
      "match": {},
      "actions": [
        { "type": "set_status", "code": "ok" }
      ]
    }
  ]
}`

	_, err := DecodeJSON(strings.NewReader(input))
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

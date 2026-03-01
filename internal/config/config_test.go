package config

import "testing"

func TestValidateAllowsZeroMaxRequests(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Requests.PerExporter = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config with max-requests=0, got %v", err)
	}
}

func TestValidateRejectsNegativeMaxRequests(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Requests.PerExporter = -1
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for negative max-requests")
	}
}

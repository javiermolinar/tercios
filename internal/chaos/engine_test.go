package chaos

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestEngineSetAttributeSpanScopeOnlyWhenExists(t *testing.T) {
	engine, err := NewEngine(Config{
		Seed:       7,
		PolicyMode: PolicyModeAll,
		Policies: []Policy{
			{
				Name:        "set-http-status",
				Probability: 1,
				Match:       Match{ServiceName: "post-service"},
				Actions: []Action{
					{Type: "set_attribute", Scope: "span", Name: "http.response.status_code", Value: TypedValue{Type: ValueTypeInt, Value: 500}},
					{Type: "set_attribute", Scope: "span", Name: "non.existing", Value: TypedValue{Type: ValueTypeString, Value: "x"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	input := []Span{
		{
			Name:       "POST /posts",
			Kind:       oteltrace.SpanKindServer,
			Attributes: map[string]attribute.Value{"service.name": attribute.StringValue("post-service"), "http.response.status_code": attribute.Int64Value(200)},
			ResourceAttributes: map[string]attribute.Value{
				"service.name": attribute.StringValue("post-service"),
			},
			StatusCode: codes.Ok,
		},
	}

	out := engine.Apply(input, func(float64) bool { return true })
	if got := out[0].Attributes["http.response.status_code"]; got.Type() != attribute.INT64 || got.AsInt64() != int64(500) {
		t.Fatalf("expected http.response.status_code=500, got %v", got)
	}
	if _, exists := out[0].Attributes["non.existing"]; exists {
		t.Fatalf("expected non.existing to be ignored when missing")
	}
}

func TestEngineSetAttributeResourceScope(t *testing.T) {
	engine, err := NewEngine(Config{
		Seed: 1,
		Policies: []Policy{
			{
				Name:        "set-version",
				Probability: 1,
				Match:       Match{ServiceName: "post-service"},
				Actions: []Action{{
					Type:  "set_attribute",
					Scope: "resource",
					Name:  "service.version",
					Value: TypedValue{Type: ValueTypeString, Value: "2.11.0"},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	out := engine.Apply([]Span{{
		Name:               "POST /posts",
		Kind:               oteltrace.SpanKindServer,
		Attributes:         map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		ResourceAttributes: map[string]attribute.Value{"service.name": attribute.StringValue("post-service"), "service.version": attribute.StringValue("2.10.0")},
	}}, func(float64) bool { return true })

	if got := out[0].ResourceAttributes["service.version"]; got.Type() != attribute.STRING || got.AsString() != "2.11.0" {
		t.Fatalf("expected service.version=2.11.0, got %v", got)
	}
}

func TestEngineFirstMatchStops(t *testing.T) {
	engine, err := NewEngine(Config{
		Seed:       1,
		PolicyMode: PolicyModeFirstMatch,
		Policies: []Policy{
			{
				Name:        "first",
				Probability: 1,
				Match:       Match{ServiceName: "post-service"},
				Actions:     []Action{{Type: "set_status", Code: "error", Message: "failed"}},
			},
			{
				Name:        "second",
				Probability: 1,
				Match:       Match{ServiceName: "post-service"},
				Actions:     []Action{{Type: "set_status", Code: "ok"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	out := engine.Apply([]Span{{
		Name:               "POST /posts",
		Kind:               oteltrace.SpanKindServer,
		Attributes:         map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		ResourceAttributes: map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		StatusCode:         codes.Ok,
	}}, func(float64) bool { return true })

	if out[0].StatusCode != codes.Error {
		t.Fatalf("expected status error, got %s", out[0].StatusCode)
	}
	if out[0].StatusDescription != "failed" {
		t.Fatalf("expected status message 'failed', got %q", out[0].StatusDescription)
	}
}

func TestEngineMatchAttributesTyped(t *testing.T) {
	engine, err := NewEngine(Config{
		Policies: []Policy{{
			Name:        "match-int",
			Probability: 1,
			Match: Match{Attributes: map[string]TypedValue{
				"http.response.status_code": {Type: ValueTypeInt, Value: 200},
			}},
			Actions: []Action{{Type: "set_status", Code: "error"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	out := engine.Apply([]Span{{
		Attributes:         map[string]attribute.Value{"http.response.status_code": attribute.Int64Value(200)},
		ResourceAttributes: map[string]attribute.Value{},
		StatusCode:         codes.Ok,
	}}, func(float64) bool { return true })

	if out[0].StatusCode != codes.Error {
		t.Fatalf("expected status error, got %s", out[0].StatusCode)
	}
}

func TestEngineReturnsInputWhenNoPolicyApplies(t *testing.T) {
	engine, err := NewEngine(Config{
		Policies: []Policy{{
			Name:        "no-match",
			Probability: 1,
			Match:       Match{ServiceName: "different-service"},
			Actions:     []Action{{Type: "set_status", Code: "error"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	in := []Span{{
		Name:               "POST /posts",
		Kind:               oteltrace.SpanKindServer,
		Attributes:         map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		ResourceAttributes: map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		StatusCode:         codes.Ok,
	}}

	out := engine.Apply(in, func(float64) bool { return true })
	if &out[0] != &in[0] {
		t.Fatalf("expected input slice to be returned when no policy applies")
	}
}

func TestEngineNilDeciderSkipsProbabilisticPolicies(t *testing.T) {
	engine, err := NewEngine(Config{
		Policies: []Policy{{
			Name:        "probabilistic",
			Probability: 0.5,
			Match:       Match{ServiceName: "post-service"},
			Actions:     []Action{{Type: "set_status", Code: "error"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	out := engine.Apply([]Span{{
		Name:               "POST /posts",
		Kind:               oteltrace.SpanKindServer,
		Attributes:         map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		ResourceAttributes: map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		StatusCode:         codes.Ok,
	}}, nil)

	if out[0].StatusCode != codes.Ok {
		t.Fatalf("expected unchanged status when decider is nil, got %s", out[0].StatusCode)
	}
}

func TestNewSeededShouldApplyRespectsBounds(t *testing.T) {
	shouldApply := NewSeededShouldApply(42)
	if shouldApply(0) {
		t.Fatalf("expected probability 0 to be false")
	}
	if !shouldApply(1) {
		t.Fatalf("expected probability 1 to be true")
	}
}

func TestEngineDoesNotMutateInput(t *testing.T) {
	engine, err := NewEngine(Config{
		Policies: []Policy{{
			Name:        "set-status",
			Probability: 1,
			Match:       Match{ServiceName: "post-service"},
			Actions:     []Action{{Type: "set_status", Code: "error"}},
		}},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	in := []Span{{
		Name:               "POST /posts",
		Kind:               oteltrace.SpanKindServer,
		Attributes:         map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		ResourceAttributes: map[string]attribute.Value{"service.name": attribute.StringValue("post-service")},
		StatusCode:         codes.Ok,
	}}

	out := engine.Apply(in, func(float64) bool { return true })
	if in[0].StatusCode != codes.Ok {
		t.Fatalf("expected input to remain unchanged, got %s", in[0].StatusCode)
	}
	if out[0].StatusCode != codes.Error {
		t.Fatalf("expected output status error, got %s", out[0].StatusCode)
	}
}

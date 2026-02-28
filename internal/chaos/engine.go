package chaos

import (
	"fmt"
	"maps"
	"math/rand"
	"strings"
	"time"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type Span = model.Span

type ShouldApplyFunc func(probability float64) bool

type Engine struct {
	mode     PolicyMode
	policies []compiledPolicy
}

type compiledPolicy struct {
	probability float64
	match       compiledMatch
	actions     []compiledAction
}

type compiledMatch struct {
	serviceName string
	spanName    string
	spanKinds   map[string]struct{}
	attributes  map[string]attribute.Value
}

type actionKind int

const (
	actionKindSetAttribute actionKind = iota + 1
	actionKindSetStatus
	actionKindAddLatency
)

type compiledAction struct {
	kind actionKind

	scope string
	name  string
	value attribute.Value

	statusCode    codes.Code
	statusMessage string

	latencyDelta time.Duration
}

func NewEngine(cfg Config) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	mode := cfg.PolicyMode
	if mode == "" {
		mode = PolicyModeAll
	}

	policies, err := compilePolicies(cfg.Policies)
	if err != nil {
		return nil, err
	}

	return &Engine{
		mode:     mode,
		policies: policies,
	}, nil
}

// NewSeededShouldApply returns a probability decider backed by a seeded RNG.
//
// The returned function is not safe for concurrent use. Create one decider per
// worker/goroutine when applying policies in parallel.
func NewSeededShouldApply(seed int64) ShouldApplyFunc {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))
	return func(probability float64) bool {
		if probability <= 0 {
			return false
		}
		if probability >= 1 {
			return true
		}
		return rng.Float64() < probability
	}
}

func (e *Engine) Apply(spans []Span, shouldApply ShouldApplyFunc) []Span {
	if e == nil || len(spans) == 0 || len(e.policies) == 0 {
		return spans
	}

	var out []Span
	var writable []bool

	ensureWritable := func(index int) *Span {
		if out == nil {
			out = make([]Span, len(spans))
			copy(out, spans)
		}
		if writable == nil {
			writable = make([]bool, len(spans))
		}
		if !writable[index] {
			out[index].Attributes = cloneMap(out[index].Attributes)
			out[index].ResourceAttributes = cloneMap(out[index].ResourceAttributes)
			writable[index] = true
		}
		return &out[index]
	}

	for i := range spans {
		var current *Span
		if out != nil {
			current = &out[i]
		} else {
			current = &spans[i]
		}

		for _, policy := range e.policies {
			if !matches(current, policy.match) {
				continue
			}
			if !shouldApplyPolicy(policy.probability, shouldApply) {
				continue
			}

			target := ensureWritable(i)
			for _, action := range policy.actions {
				applyAction(target, action)
			}
			current = target

			if e.mode == PolicyModeFirstMatch {
				break
			}
		}
	}

	if out == nil {
		return spans
	}
	return out
}

func compilePolicies(policies []Policy) ([]compiledPolicy, error) {
	out := make([]compiledPolicy, 0, len(policies))
	for _, policy := range policies {
		compiled, err := compilePolicy(policy)
		if err != nil {
			return nil, fmt.Errorf("policy %s: %w", policy.Name, err)
		}
		out = append(out, compiled)
	}
	return out, nil
}

func compilePolicy(policy Policy) (compiledPolicy, error) {
	compiledMatch, err := compileMatch(policy.Match)
	if err != nil {
		return compiledPolicy{}, err
	}
	actions, err := compileActions(policy.Actions)
	if err != nil {
		return compiledPolicy{}, err
	}
	return compiledPolicy{
		probability: policy.Probability,
		match:       compiledMatch,
		actions:     actions,
	}, nil
}

func compileMatch(match Match) (compiledMatch, error) {
	compiled := compiledMatch{
		serviceName: strings.TrimSpace(match.ServiceName),
		spanName:    strings.TrimSpace(match.SpanName),
		spanKinds:   make(map[string]struct{}, len(match.SpanKinds)),
		attributes:  make(map[string]attribute.Value, len(match.Attributes)),
	}
	for _, kind := range match.SpanKinds {
		normalized := strings.ToLower(strings.TrimSpace(kind))
		if normalized == "" {
			continue
		}
		compiled.spanKinds[normalized] = struct{}{}
	}
	for key, value := range match.Attributes {
		normalized, err := compileTypedValue(value)
		if err != nil {
			return compiledMatch{}, fmt.Errorf("invalid match attribute %q: %w", key, err)
		}
		compiled.attributes[key] = normalized
	}
	return compiled, nil
}

func compileActions(actions []Action) ([]compiledAction, error) {
	out := make([]compiledAction, 0, len(actions))
	for _, action := range actions {
		compiled, err := compileAction(action)
		if err != nil {
			return nil, err
		}
		out = append(out, compiled)
	}
	return out, nil
}

func compileAction(action Action) (compiledAction, error) {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "set_attribute":
		normalized, err := compileTypedValue(action.Value)
		if err != nil {
			return compiledAction{}, fmt.Errorf("invalid set_attribute value for %q: %w", action.Name, err)
		}
		return compiledAction{
			kind:  actionKindSetAttribute,
			scope: strings.ToLower(strings.TrimSpace(action.Scope)),
			name:  strings.TrimSpace(action.Name),
			value: normalized,
		}, nil
	case "set_status":
		statusCode, err := parseStatusCode(action.Code)
		if err != nil {
			return compiledAction{}, err
		}
		return compiledAction{
			kind:          actionKindSetStatus,
			statusCode:    statusCode,
			statusMessage: action.Message,
		}, nil
	case "add_latency":
		return compiledAction{
			kind:         actionKindAddLatency,
			latencyDelta: time.Duration(action.DeltaMs) * time.Millisecond,
		}, nil
	default:
		return compiledAction{}, fmt.Errorf("unsupported action type %q", action.Type)
	}
}

func compileTypedValue(value TypedValue) (attribute.Value, error) {
	normalizedType := ValueType(strings.ToLower(strings.TrimSpace(string(value.Type))))
	switch normalizedType {
	case ValueTypeString:
		s, ok := value.Value.(string)
		if !ok {
			return attribute.Value{}, fmt.Errorf("expected string value")
		}
		return attribute.StringValue(s), nil
	case ValueTypeBool:
		b, ok := value.Value.(bool)
		if !ok {
			return attribute.Value{}, fmt.Errorf("expected bool value")
		}
		return attribute.BoolValue(b), nil
	case ValueTypeInt:
		i, ok := toInt64(value.Value)
		if !ok {
			return attribute.Value{}, fmt.Errorf("expected int value")
		}
		return attribute.Int64Value(i), nil
	case ValueTypeFloat:
		f, ok := toFloat64(value.Value)
		if !ok {
			return attribute.Value{}, fmt.Errorf("expected float value")
		}
		return attribute.Float64Value(f), nil
	default:
		return attribute.Value{}, fmt.Errorf("unsupported value type %q", value.Type)
	}
}

func shouldApplyPolicy(probability float64, shouldApply ShouldApplyFunc) bool {
	if probability <= 0 {
		return false
	}
	if probability >= 1 {
		return true
	}
	if shouldApply == nil {
		return false
	}
	return shouldApply(probability)
}

func cloneMap(values map[string]attribute.Value) map[string]attribute.Value {
	if len(values) == 0 {
		return map[string]attribute.Value{}
	}
	copy := make(map[string]attribute.Value, len(values))
	maps.Copy(copy, values)
	return copy
}

func matches(span *Span, match compiledMatch) bool {
	if span == nil {
		return false
	}
	if match.spanName != "" && span.Name != match.spanName {
		return false
	}
	if len(match.spanKinds) > 0 {
		if _, ok := match.spanKinds[strings.ToLower(span.Kind.String())]; !ok {
			return false
		}
	}
	if match.serviceName != "" {
		if !hasServiceName(span, match.serviceName) {
			return false
		}
	}
	for key, want := range match.attributes {
		got, ok := span.Attributes[key]
		if !ok {
			got, ok = span.ResourceAttributes[key]
		}
		if !ok || !matchesTypedValue(got, want) {
			return false
		}
	}
	return true
}

func hasServiceName(span *Span, serviceName string) bool {
	if span == nil {
		return false
	}
	if got, ok := span.Attributes["service.name"]; ok {
		if got.Type() == attribute.STRING && got.AsString() == serviceName {
			return true
		}
	}
	if got, ok := span.ResourceAttributes["service.name"]; ok {
		if got.Type() == attribute.STRING && got.AsString() == serviceName {
			return true
		}
	}
	return false
}

func applyAction(span *Span, action compiledAction) {
	if span == nil {
		return
	}
	switch action.kind {
	case actionKindSetAttribute:
		applySetAttribute(span, action)
	case actionKindSetStatus:
		span.StatusCode = action.statusCode
		span.StatusDescription = action.statusMessage
	case actionKindAddLatency:
		applyLatency(span, action.latencyDelta)
	}
}

func applySetAttribute(span *Span, action compiledAction) {
	if action.name == "" {
		return
	}
	switch action.scope {
	case "span":
		if _, exists := span.Attributes[action.name]; !exists {
			return
		}
		span.Attributes[action.name] = action.value
	case "resource":
		if _, exists := span.ResourceAttributes[action.name]; !exists {
			return
		}
		span.ResourceAttributes[action.name] = action.value
	}
}

func applyLatency(span *Span, delta time.Duration) {
	if span == nil || delta == 0 {
		return
	}
	newEnd := span.EndTime.Add(delta)
	if !newEnd.After(span.StartTime) {
		newEnd = span.StartTime.Add(1 * time.Millisecond)
	}
	span.EndTime = newEnd
}

func parseStatusCode(code string) (codes.Code, error) {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "ok":
		return codes.Ok, nil
	case "error":
		return codes.Error, nil
	case "unset":
		return codes.Unset, nil
	default:
		return codes.Unset, fmt.Errorf("invalid status code %q", code)
	}
}

func matchesTypedValue(got attribute.Value, want attribute.Value) bool {
	if got.Type() != want.Type() {
		return false
	}
	switch want.Type() {
	case attribute.STRING:
		return got.AsString() == want.AsString()
	case attribute.BOOL:
		return got.AsBool() == want.AsBool()
	case attribute.INT64:
		return got.AsInt64() == want.AsInt64()
	case attribute.FLOAT64:
		return got.AsFloat64() == want.AsFloat64()
	default:
		return false
	}
}

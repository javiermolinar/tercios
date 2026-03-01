package typedvalue

import (
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

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
		if _, ok := ToInt64(v.Value); !ok {
			return fmt.Errorf("%s: expected int value", field)
		}
	case ValueTypeFloat:
		if _, ok := ToFloat64(v.Value); !ok {
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
		i, ok := ToInt64(v.Value)
		return i, ok
	case ValueTypeFloat:
		f, ok := ToFloat64(v.Value)
		return f, ok
	default:
		return nil, false
	}
}

func (v TypedValue) ToAttributeValue() (attribute.Value, error) {
	normalizedType := ValueType(strings.ToLower(strings.TrimSpace(string(v.Type))))
	switch normalizedType {
	case ValueTypeString:
		s, ok := v.Value.(string)
		if !ok {
			return attribute.Value{}, fmt.Errorf("expected string value")
		}
		return attribute.StringValue(s), nil
	case ValueTypeBool:
		b, ok := v.Value.(bool)
		if !ok {
			return attribute.Value{}, fmt.Errorf("expected bool value")
		}
		return attribute.BoolValue(b), nil
	case ValueTypeInt:
		i, ok := ToInt64(v.Value)
		if !ok {
			return attribute.Value{}, fmt.Errorf("expected int value")
		}
		return attribute.Int64Value(i), nil
	case ValueTypeFloat:
		f, ok := ToFloat64(v.Value)
		if !ok {
			return attribute.Value{}, fmt.Errorf("expected float value")
		}
		return attribute.Float64Value(f), nil
	default:
		return attribute.Value{}, fmt.Errorf("unsupported value type %q", v.Type)
	}
}

func ToInt64(value any) (int64, bool) {
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

func ToFloat64(value any) (float64, bool) {
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

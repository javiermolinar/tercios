package typedvalue

import (
	_ "embed"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

//go:embed seed.txt
var blobSeed string

const defaultBlobSize = 100

type ValueType string

const (
	ValueTypeString ValueType = "string"
	ValueTypeInt    ValueType = "int"
	ValueTypeFloat  ValueType = "float"
	ValueTypeBool   ValueType = "bool"

	ValueTypeStringArray ValueType = "string_array"
	ValueTypeIntArray    ValueType = "int_array"
	ValueTypeFloatArray  ValueType = "float_array"
	ValueTypeBoolArray   ValueType = "bool_array"
)

type TypedValue struct {
	Type  ValueType `json:"type"`
	Value any       `json:"value"`
	Size  *int      `json:"size,omitempty"`
}

func (v TypedValue) Validate(field string) error {
	typeName := strings.ToLower(strings.TrimSpace(string(v.Type)))
	if typeName == "" {
		return fmt.Errorf("%s: value type is required", field)
	}
	switch ValueType(typeName) {
	case ValueTypeString:
		if v.Size != nil {
			if *v.Size <= 0 {
				return fmt.Errorf("%s: size must be > 0", field)
			}
			if v.Value != nil {
				if _, ok := v.Value.(string); !ok {
					return fmt.Errorf("%s: expected string value", field)
				}
			}
			return nil
		}
		if v.Value == nil {
			return fmt.Errorf("%s: value is required", field)
		}
		if _, ok := v.Value.(string); !ok {
			return fmt.Errorf("%s: expected string value", field)
		}
	case ValueTypeBool:
		if v.Value == nil {
			return fmt.Errorf("%s: value is required", field)
		}
		if _, ok := v.Value.(bool); !ok {
			return fmt.Errorf("%s: expected bool value", field)
		}
	case ValueTypeInt:
		if v.Value == nil {
			return fmt.Errorf("%s: value is required", field)
		}
		if _, ok := ToInt64(v.Value); !ok {
			return fmt.Errorf("%s: expected int value", field)
		}
	case ValueTypeFloat:
		if v.Value == nil {
			return fmt.Errorf("%s: value is required", field)
		}
		if _, ok := ToFloat64(v.Value); !ok {
			return fmt.Errorf("%s: expected float value", field)
		}
	case ValueTypeStringArray:
		if v.Value == nil {
			return fmt.Errorf("%s: value is required", field)
		}
		if _, err := toStringSlice(v.Value); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	case ValueTypeIntArray:
		if v.Value == nil {
			return fmt.Errorf("%s: value is required", field)
		}
		if _, err := toInt64Slice(v.Value); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	case ValueTypeFloatArray:
		if v.Value == nil {
			return fmt.Errorf("%s: value is required", field)
		}
		if _, err := toFloat64Slice(v.Value); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	case ValueTypeBoolArray:
		if v.Value == nil {
			return fmt.Errorf("%s: value is required", field)
		}
		if _, err := toBoolSlice(v.Value); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	default:
		return fmt.Errorf("%s: unsupported value type %q", field, v.Type)
	}
	return nil
}

func (v TypedValue) Normalized() (any, bool) {
	switch ValueType(strings.ToLower(strings.TrimSpace(string(v.Type)))) {
	case ValueTypeString:
		if v.Size != nil {
			seed := blobSeed
			if s, ok := v.Value.(string); ok && s != "" {
				seed = s
			}
			return generateBlob(seed, *v.Size), true
		}
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
	case ValueTypeStringArray:
		s, err := toStringSlice(v.Value)
		return s, err == nil
	case ValueTypeIntArray:
		s, err := toInt64Slice(v.Value)
		return s, err == nil
	case ValueTypeFloatArray:
		s, err := toFloat64Slice(v.Value)
		return s, err == nil
	case ValueTypeBoolArray:
		s, err := toBoolSlice(v.Value)
		return s, err == nil
	default:
		return nil, false
	}
}

func (v TypedValue) ToAttributeValue() (attribute.Value, error) {
	normalizedType := ValueType(strings.ToLower(strings.TrimSpace(string(v.Type))))
	switch normalizedType {
	case ValueTypeString:
		if v.Size != nil {
			seed := blobSeed
			if s, ok := v.Value.(string); ok && s != "" {
				seed = s
			}
			return attribute.StringValue(generateBlob(seed, *v.Size)), nil
		}
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
	case ValueTypeStringArray:
		s, err := toStringSlice(v.Value)
		if err != nil {
			return attribute.Value{}, err
		}
		return attribute.StringSliceValue(s), nil
	case ValueTypeIntArray:
		s, err := toInt64Slice(v.Value)
		if err != nil {
			return attribute.Value{}, err
		}
		return attribute.Int64SliceValue(s), nil
	case ValueTypeFloatArray:
		s, err := toFloat64Slice(v.Value)
		if err != nil {
			return attribute.Value{}, err
		}
		return attribute.Float64SliceValue(s), nil
	case ValueTypeBoolArray:
		s, err := toBoolSlice(v.Value)
		if err != nil {
			return attribute.Value{}, err
		}
		return attribute.BoolSliceValue(s), nil
	default:
		return attribute.Value{}, fmt.Errorf("unsupported value type %q", v.Type)
	}
}

func toStringSlice(value any) ([]string, error) {
	arr, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array value")
	}
	out := make([]string, len(arr))
	for i, elem := range arr {
		s, ok := elem.(string)
		if !ok {
			return nil, fmt.Errorf("element %d: expected string", i)
		}
		out[i] = s
	}
	return out, nil
}

func toInt64Slice(value any) ([]int64, error) {
	arr, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array value")
	}
	out := make([]int64, len(arr))
	for i, elem := range arr {
		v, ok := ToInt64(elem)
		if !ok {
			return nil, fmt.Errorf("element %d: expected int", i)
		}
		out[i] = v
	}
	return out, nil
}

func toFloat64Slice(value any) ([]float64, error) {
	arr, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array value")
	}
	out := make([]float64, len(arr))
	for i, elem := range arr {
		v, ok := ToFloat64(elem)
		if !ok {
			return nil, fmt.Errorf("element %d: expected float", i)
		}
		out[i] = v
	}
	return out, nil
}

func toBoolSlice(value any) ([]bool, error) {
	arr, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array value")
	}
	out := make([]bool, len(arr))
	for i, elem := range arr {
		b, ok := elem.(bool)
		if !ok {
			return nil, fmt.Errorf("element %d: expected bool", i)
		}
		out[i] = b
	}
	return out, nil
}

func generateBlob(seed string, size int) string {
	if size <= 0 {
		size = defaultBlobSize
	}
	if len(seed) == 0 {
		seed = blobSeed
	}
	if size <= len(seed) {
		return seed[:size]
	}
	buf := make([]byte, size)
	for i := 0; i < size; {
		i += copy(buf[i:], seed)
	}
	return string(buf)
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

package typedvalue

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestScalarValidateAndConvert(t *testing.T) {
	cases := []struct {
		name     string
		tv       TypedValue
		wantType attribute.Type
	}{
		{"string", TypedValue{Type: ValueTypeString, Value: "hello"}, attribute.STRING},
		{"int", TypedValue{Type: ValueTypeInt, Value: int64(42)}, attribute.INT64},
		{"float", TypedValue{Type: ValueTypeFloat, Value: 3.14}, attribute.FLOAT64},
		{"bool", TypedValue{Type: ValueTypeBool, Value: true}, attribute.BOOL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.tv.Validate("test"); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			av, err := tc.tv.ToAttributeValue()
			if err != nil {
				t.Fatalf("ToAttributeValue() error = %v", err)
			}
			if av.Type() != tc.wantType {
				t.Fatalf("expected type %s, got %s", tc.wantType, av.Type())
			}
		})
	}
}

func TestStringArrayValidateAndConvert(t *testing.T) {
	tv := TypedValue{Type: ValueTypeStringArray, Value: []any{"GET", "POST", "PUT"}}
	if err := tv.Validate("test"); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	av, err := tv.ToAttributeValue()
	if err != nil {
		t.Fatalf("ToAttributeValue() error = %v", err)
	}
	if av.Type() != attribute.STRINGSLICE {
		t.Fatalf("expected STRINGSLICE, got %s", av.Type())
	}
	got := av.AsStringSlice()
	want := []string{"GET", "POST", "PUT"}
	if len(got) != len(want) {
		t.Fatalf("expected %d elements, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("element %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestIntArrayValidateAndConvert(t *testing.T) {
	tv := TypedValue{Type: ValueTypeIntArray, Value: []any{float64(1), float64(2), float64(3)}}
	if err := tv.Validate("test"); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	av, err := tv.ToAttributeValue()
	if err != nil {
		t.Fatalf("ToAttributeValue() error = %v", err)
	}
	if av.Type() != attribute.INT64SLICE {
		t.Fatalf("expected INT64SLICE, got %s", av.Type())
	}
	got := av.AsInt64Slice()
	want := []int64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("expected %d elements, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("element %d: expected %d, got %d", i, want[i], got[i])
		}
	}
}

func TestFloatArrayValidateAndConvert(t *testing.T) {
	tv := TypedValue{Type: ValueTypeFloatArray, Value: []any{1.1, 2.2, 3.3}}
	if err := tv.Validate("test"); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	av, err := tv.ToAttributeValue()
	if err != nil {
		t.Fatalf("ToAttributeValue() error = %v", err)
	}
	if av.Type() != attribute.FLOAT64SLICE {
		t.Fatalf("expected FLOAT64SLICE, got %s", av.Type())
	}
	got := av.AsFloat64Slice()
	want := []float64{1.1, 2.2, 3.3}
	if len(got) != len(want) {
		t.Fatalf("expected %d elements, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("element %d: expected %f, got %f", i, want[i], got[i])
		}
	}
}

func TestBoolArrayValidateAndConvert(t *testing.T) {
	tv := TypedValue{Type: ValueTypeBoolArray, Value: []any{true, false, true}}
	if err := tv.Validate("test"); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	av, err := tv.ToAttributeValue()
	if err != nil {
		t.Fatalf("ToAttributeValue() error = %v", err)
	}
	if av.Type() != attribute.BOOLSLICE {
		t.Fatalf("expected BOOLSLICE, got %s", av.Type())
	}
	got := av.AsBoolSlice()
	want := []bool{true, false, true}
	if len(got) != len(want) {
		t.Fatalf("expected %d elements, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("element %d: expected %v, got %v", i, want[i], got[i])
		}
	}
}

func TestEmptyArrayValidateAndConvert(t *testing.T) {
	tv := TypedValue{Type: ValueTypeStringArray, Value: []any{}}
	if err := tv.Validate("test"); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	av, err := tv.ToAttributeValue()
	if err != nil {
		t.Fatalf("ToAttributeValue() error = %v", err)
	}
	if av.Type() != attribute.STRINGSLICE {
		t.Fatalf("expected STRINGSLICE, got %s", av.Type())
	}
	if len(av.AsStringSlice()) != 0 {
		t.Fatalf("expected empty slice, got %d elements", len(av.AsStringSlice()))
	}
}

func TestArrayValidateRejectsNonArray(t *testing.T) {
	tv := TypedValue{Type: ValueTypeStringArray, Value: "not-an-array"}
	if err := tv.Validate("test"); err == nil {
		t.Fatalf("expected error for non-array value")
	}
}

func TestArrayValidateRejectsWrongElementType(t *testing.T) {
	cases := []struct {
		name string
		tv   TypedValue
	}{
		{"string_array with int", TypedValue{Type: ValueTypeStringArray, Value: []any{123}}},
		{"int_array with string", TypedValue{Type: ValueTypeIntArray, Value: []any{"nope"}}},
		{"float_array with bool", TypedValue{Type: ValueTypeFloatArray, Value: []any{true}}},
		{"bool_array with string", TypedValue{Type: ValueTypeBoolArray, Value: []any{"nope"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.tv.Validate("test"); err == nil {
				t.Fatalf("expected error for wrong element type")
			}
		})
	}
}

func TestArrayNormalized(t *testing.T) {
	tv := TypedValue{Type: ValueTypeStringArray, Value: []any{"a", "b"}}
	val, ok := tv.Normalized()
	if !ok {
		t.Fatalf("Normalized() returned false")
	}
	s, ok := val.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", val)
	}
	if len(s) != 2 || s[0] != "a" || s[1] != "b" {
		t.Fatalf("unexpected normalized value: %v", s)
	}
}

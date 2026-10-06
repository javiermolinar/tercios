package typedvalue

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestJSONIntegerPrecisionAndRange(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		want  int64
		valid bool
	}{
		{"9007199254740993", 9007199254740993, true},
		{"-9007199254740993", -9007199254740993, true},
		{"9223372036854775807", math.MaxInt64, true},
		{"-9223372036854775808", math.MinInt64, true},
		{"1.0", 1, true},
		{"1e3", 1000, true},
		{"100e-2", 1, true},
		{"0.00100E+3", 1, true},
		{"9007199254740993.0", 9007199254740993, true},
		{"9.223372036854775807e18", math.MaxInt64, true},
		{"-922337203685477580800e-2", math.MinInt64, true},
		{"-0.0e999999999999999999999", 0, true},
		{"9223372036854775808", 0, false},
		{"-9223372036854775809", 0, false},
		{"18446744073709551615", 0, false},
		{"9.223372036854775808e18", 0, false},
		{"1.5", 0, false},
		{"9007199254740993.0001", 0, false},
		{"1.00000000000000000001", 0, false},
		{"1e-999999999999999999999", 0, false},
		{"1e999999999999999999999", 0, false},
		{`"42"`, 0, false},
	} {
		for _, kind := range []ValueType{ValueTypeInt, ValueTypeIntArray} {
			t.Run(string(kind)+"/"+tc.raw, func(t *testing.T) {
				raw, want, normalizedWant := tc.raw, attribute.Int64Value(tc.want), any(tc.want)
				if kind == ValueTypeIntArray {
					raw = "[" + raw + "]"
					want = attribute.Int64SliceValue([]int64{tc.want})
					normalizedWant = []int64{tc.want}
				}
				var value TypedValue
				if err := json.Unmarshal([]byte(fmt.Sprintf(`{"type":%q,"value":%s}`, kind, raw)), &value); err != nil {
					t.Fatal(err)
				}
				if err := value.Validate("attribute"); (err == nil) != tc.valid {
					t.Fatalf("Validate() = %v, valid=%v", err, tc.valid)
				}
				normalized, ok := value.Normalized()
				got, err := value.ToAttributeValue()
				if ok != tc.valid || (err == nil) != tc.valid {
					t.Fatalf("Normalized valid=%v, conversion error=%v, want valid=%v", ok, err, tc.valid)
				}
				if tc.valid && (!reflect.DeepEqual(normalized, normalizedWant) || !reflect.DeepEqual(got, want)) {
					t.Fatalf("normalized=%v, attribute=%v, want %v", normalized, got, normalizedWant)
				}
			})
		}
	}
}

func TestJSONTypedValueFloatAndStrictDecoding(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want attribute.Value
	}{
		{`{"type":"float","value":1.25}`, attribute.Float64Value(1.25)},
		{`{"type":"float_array","value":[1.25,2e3]}`, attribute.Float64SliceValue([]float64{1.25, 2000})},
	} {
		var value TypedValue
		if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
			t.Fatal(err)
		}
		if err := value.Validate("float"); err != nil {
			t.Fatal(err)
		}
		got, err := value.ToAttributeValue()
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("float conversion=%v, error=%v, want %v", got, err, tc.want)
		}
	}
	for _, raw := range []string{
		`{"type":"int","value":1,"typo":true}`,
		`{"type":"int","value":1} {"type":"int","value":2}`,
	} {
		var value TypedValue
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatalf("accepted invalid typed value %s", raw)
		}
	}
}

func TestToInt64CheckedNativeValues(t *testing.T) {
	for _, value := range []any{uint64(math.MaxInt64) + 1, uint64(math.MaxUint64), json.Number("NaN"), json.Number("01"), float64(0x1p63), float32(0x1p63), math.Inf(1), math.Inf(-1), math.NaN(), 1.5} {
		if _, ok := ToInt64(value); ok {
			t.Errorf("accepted out-of-range or nonintegral %T(%v)", value, value)
		}
	}
	if _, ok := ToInt64(uint(math.MaxUint)); ok != (uint64(math.MaxUint) <= math.MaxInt64) {
		t.Error("incorrect platform uint range check")
	}
	for _, value := range []any{uint64(math.MaxInt64), float64(-0x1p63), float32(-0x1p63), float64(42), uint32(math.MaxUint32)} {
		if _, ok := ToInt64(value); !ok {
			t.Errorf("rejected integral %T(%v)", value, value)
		}
	}
}

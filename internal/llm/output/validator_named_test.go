package output

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// Named types with the same underlying kind are used to lock the REVIEW #13
// validator refactor: the checks moved from exact type assertions to
// reflect.Kind so a domain type like "type Occasion string" is accepted as a
// string, while structurally-wrong values are still rejected.

type testNamedString string
type testNamedBool bool
type testNamedInt int
type testNamedFloat float64

func TestValidator_toInt64(t *testing.T) {
	cases := []struct {
		name  string
		in    interface{}
		wantV int64
		want  bool
	}{
		{"int", 5, 5, true},
		{"int32", int32(-3), -3, true},
		{"int64", int64(1) << 40, int64(1) << 40, true},
		{"named int is accepted", testNamedInt(7), 7, true},
		{"uint within range", uint(42), 42, true},
		{"uint64 within range", uint64(99), 99, true},
		{"uint64 exceeds int64 max rejected", uint64(1) << 63, 0, false},
		// JSON Schema "integer" accepts 1.0 but not 1.5: the old assertion locked in
		// silent truncation, which made dirty data look valid.
		{"float with fraction rejected", 42.75, 0, false},
		{"float without fraction accepted", 42.0, 42, true},
		// int64's true max (2^63-1) is not representable as a float64, so this
		// expression rounds up to exactly 2^63, which is out of range: int64(f)
		// would be undefined behaviour per the Go spec.
		{"float at int64 max overflow bound rejected", float64(^uint64(0) >> 1), 0, false},
		{"float above int64 max rejected", 1e19, 0, false},
		{"float at int64 min accepted", float64(int64(-1) << 63), int64(-1) << 63, true},
		{"float below int64 min rejected", -1e19, 0, false},
		{"NaN rejected", math.NaN(), 0, false},
		{"positive infinity rejected", math.Inf(1), 0, false},
		{"negative infinity rejected", math.Inf(-1), 0, false},
		{"string rejected", "42", 0, false},
		{"bool rejected", true, 0, false},
		{"nil rejected", nil, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toInt64(tc.in)
			if got != tc.wantV || ok != tc.want {
				t.Fatalf("toInt64(%#v) = %v,%v; want %v,%v", tc.in, got, ok, tc.wantV, tc.want)
			}
		})
	}
}

func TestValidator_toFloat64(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want float64
	}{
		{"float64", 1.5, 1.5},
		{"float32", float32(2), 2},
		{"int", 3, 3},
		{"uint64", uint64(4), 4},
		{"named float accepted", testNamedFloat(5.5), 5.5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := toFloat64(tc.in); !ok || got != tc.want {
				t.Fatalf("toFloat64(%#v) = %v,%v; want %v,true", tc.in, got, ok, tc.want)
			}
		})
	}

	t.Run("string rejected", func(t *testing.T) {
		if _, ok := toFloat64("1.5"); ok {
			t.Fatal("string must not convert to float64")
		}
	})
	t.Run("nil rejected", func(t *testing.T) {
		if _, ok := toFloat64(nil); ok {
			t.Fatal("nil must not convert to float64")
		}
	})
}

func TestValidator_typeFetchSupportsNamedTypes(t *testing.T) {
	t.Run("asString", func(t *testing.T) {
		if v, ok := asString("plain"); !ok || v != "plain" {
			t.Fatalf("asString(plain) = %q,%v", v, ok)
		}
		if v, ok := asString(testNamedString("named")); !ok || v != "named" {
			t.Fatalf("asString(named) = %q,%v", v, ok)
		}
		if _, ok := asString(3); ok {
			t.Fatal("int is not a string")
		}
		if _, ok := asString(nil); ok {
			t.Fatal("nil is not a string")
		}
	})

	t.Run("asBool", func(t *testing.T) {
		if v, ok := asBool(true); !ok || !v {
			t.Fatalf("asBool(true) = %v,%v", v, ok)
		}
		if v, ok := asBool(testNamedBool(false)); !ok || v {
			t.Fatalf("asBool(named false) = %v,%v", v, ok)
		}
		if _, ok := asBool("true"); ok {
			t.Fatal("string is not a bool")
		}
		if _, ok := asBool(nil); ok {
			t.Fatal("nil is not a bool")
		}
	})
}

func TestValidator_canonicalValue(t *testing.T) {
	require.Equal(t, "plain", canonicalValue("plain"))
	require.Equal(t, "named", canonicalValue(testNamedString("named")))
	require.Equal(t, true, canonicalValue(testNamedBool(true)))
	require.Equal(t, int64(7), canonicalValue(testNamedInt(7)))
	require.Equal(t, uint64(9), canonicalValue(uint64(9)))
	require.Equal(t, 1.5, canonicalValue(testNamedFloat(1.5)))
	require.Nil(t, canonicalValue(nil))
}

func TestValidator_validateEnum(t *testing.T) {
	v := NewValidator()
	enum := []interface{}{"casual", "formal"}

	t.Run("named string matches enum", func(t *testing.T) {
		require.NoError(t, v.validateEnum("casual", &Schema{Enum: enum}, "p"))
		require.NoError(t, v.validateEnum(testNamedString("formal"), &Schema{Enum: enum}, "p"))
		require.Error(t, v.validateEnum("blacktie", &Schema{Enum: enum}, "p"))
	})

	// Empty is only "absent" where the schema opts in. It used to be an
	// unconditional bypass, which let a required enum field pass with "".
	t.Run("empty string absent only when AllowEmpty", func(t *testing.T) {
		require.NoError(t, v.validateEnum("", &Schema{Enum: enum, AllowEmpty: true}, "p"))
		require.Error(t, v.validateEnum("", &Schema{Enum: enum}, "p"))
	})

	t.Run("numeric enum", func(t *testing.T) {
		nums := []interface{}{1, 2, 3}
		require.NoError(t, v.validateEnum(3, &Schema{Enum: nums}, "p"))
		require.NoError(t, v.validateEnum(testNamedInt(2), &Schema{Enum: nums}, "p"))
		require.Error(t, v.validateEnum(9, &Schema{Enum: nums}, "p"))
	})
}

func TestValidator_requiredEnumRejectsEmpty(t *testing.T) {
	v := NewValidator()
	item := map[string]interface{}{
		"item_id":  "i1",
		"name":     "n1",
		"category": "",
		"price":    1.0,
	}
	require.Error(t, v.validateValue(item, GetRecommendItemSchema(), "root"),
		"a required enum field must not validate with an empty value")
}

func TestValidator_validateTypeNamedSupport(t *testing.T) {
	v := NewValidator()

	require.NoError(t, v.validateType(testNamedString("x"), schemaTypeString, "p"))
	require.NoError(t, v.validateType(testNamedBool(true), "boolean", "p"))
	require.NoError(t, v.validateType(5, schemaTypeNumber, "p"))
	require.NoError(t, v.validateType(testNamedFloat(1.0), schemaTypeNumber, "p"))
	require.Error(t, v.validateType(5, schemaTypeString, "p"))
}

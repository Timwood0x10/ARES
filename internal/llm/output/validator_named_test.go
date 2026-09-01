package output

import (
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
		{"float within range truncates", 42.75, 42, true},
		{"float at int64 max accepted", float64(^uint64(0) >> 1), int64(^uint64(0) >> 1), true},
		{"float above int64 max rejected", 1e19, 0, false},
		{"float at int64 min accepted", float64(int64(-1) << 63), int64(-1) << 63, true},
		{"float below int64 min rejected", -1e19, 0, false},
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

	t.Run("named string matches enum", func(t *testing.T) {
		require.NoError(t, v.validateEnum("casual", []interface{}{"casual", "formal"}, "p"))
		require.NoError(t, v.validateEnum(testNamedString("formal"), []interface{}{"casual", "formal"}, "p"))
		require.Error(t, v.validateEnum("blacktie", []interface{}{"casual", "formal"}, "p"))
	})

	t.Run("empty string treated as absent", func(t *testing.T) {
		require.NoError(t, v.validateEnum("", []interface{}{"casual", "formal"}, "p"))
	})

	t.Run("numeric enum", func(t *testing.T) {
		require.NoError(t, v.validateEnum(3, []interface{}{1, 2, 3}, "p"))
		require.NoError(t, v.validateEnum(testNamedInt(2), []interface{}{1, 2, 3}, "p"))
		require.Error(t, v.validateEnum(9, []interface{}{1, 2, 3}, "p"))
	})
}

func TestValidator_validateTypeNamedSupport(t *testing.T) {
	v := NewValidator()

	require.NoError(t, v.validateType(testNamedString("x"), schemaTypeString, "p"))
	require.NoError(t, v.validateType(testNamedBool(true), "boolean", "p"))
	require.NoError(t, v.validateType(5, schemaTypeNumber, "p"))
	require.NoError(t, v.validateType(testNamedFloat(1.0), schemaTypeNumber, "p"))
	require.Error(t, v.validateType(5, schemaTypeString, "p"))
}

// deepcopy_test.go locks the nested-value isolation of dupStrategy
// (REVIEW 3.4#7): it previously shallow-copied its Params map, so nested
// maps/slices stayed shared between the stored copy and every caller.
// (The ShadowExecutor.cloneTask half of the original test was removed with
// the unwired shadow executor.)
package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDupStrategyDeepCopiesNestedParams(t *testing.T) {
	store := NewMemoryStrategyStore(10)
	require.NoError(t, store.SetActive(context.Background(), &Strategy{
		ID: "s-1",
		Params: map[string]any{
			"tools": []any{"search", "calculator"},
			"limits": map[string]any{
				"max_tokens": 4096,
				"nested":     map[string]any{"deep": true},
			},
		},
	}))

	got, err := store.GetActive(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)

	// Corrupt everything nested through the returned copy.
	got.Params["limits"].(map[string]any)["max_tokens"] = 1
	got.Params["limits"].(map[string]any)["nested"].(map[string]any)["deep"] = false
	got.Params["tools"].([]any)[0] = "hijacked"

	again, err := store.GetActive(context.Background())
	require.NoError(t, err)
	limits := again.Params["limits"].(map[string]any)
	assert.Equal(t, 4096, limits["max_tokens"], "nested map must not alias the returned copy")
	assert.Equal(t, true, limits["nested"].(map[string]any)["deep"], "doubly nested map must not alias")
	assert.Equal(t, []any{"search", "calculator"}, again.Params["tools"], "nested slice must not alias")
}

// TestDeepCopyValueShapes unit-locks the primitive copier itself: nested
// containers are isolated, scalars pass through, and opaque non-JSON values
// are shared by contract.
func TestDeepCopyValueShapes(t *testing.T) {
	src := map[string]any{
		"list":   []any{"a", map[string]any{"k": "v"}},
		"strs":   []string{"x", "y"},
		"scalar": 42,
	}
	cp := deepCopyValue(src).(map[string]any)

	cp["list"].([]any)[1].(map[string]any)["k"] = "hijacked"
	cp["strs"].([]string)[0] = "hijacked"

	assert.Equal(t, "v", src["list"].([]any)[1].(map[string]any)["k"],
		"nested map inside slice must be isolated")
	assert.Equal(t, "x", src["strs"].([]string)[0],
		"string slice must be isolated")
	assert.Equal(t, 42, src["scalar"])
}

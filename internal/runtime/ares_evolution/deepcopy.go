// deepcopy.go provides the recursive copy used by the strategy
// defensive-copy path (memory_strategy_store.dupStrategy). A struct-level
// shallow copy still aliases nested maps/slices, so a caller mutating a
// nested value would corrupt the stored copy (REVIEW 3.4#7).
package evolution

// deepCopyValue returns a recursive copy of JSON-shaped values
// (map[string]any, []any, []string). Scalars are copied by assignment.
// Values of other types are returned as-is (shared) — they are treated as
// opaque by every payload producer in this package.
func deepCopyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(val))
		for k, vv := range val {
			cp[k] = deepCopyValue(vv)
		}
		return cp
	case []any:
		cp := make([]any, len(val))
		for i, vv := range val {
			cp[i] = deepCopyValue(vv)
		}
		return cp
	case []string:
		cp := make([]string, len(val))
		copy(cp, val)
		return cp
	default:
		return v
	}
}

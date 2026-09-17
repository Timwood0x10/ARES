package agents

import (
	"testing"
)

func TestToolWhitelistFromParams(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   map[string]bool
	}{
		{
			name:   "nil_params_returns_nil",
			params: nil,
			want:   nil,
		},
		{
			name:   "empty_params_returns_nil",
			params: map[string]any{},
			want:   nil,
		},
		{
			name:   "missing_tools_key_returns_nil",
			params: map[string]any{"temperature": 0.5},
			want:   nil,
		},
		{
			name:   "empty_tools_string_returns_nil",
			params: map[string]any{"tools": ""},
			want:   nil,
		},
		{
			name:   "whitespace_only_tools_returns_nil",
			params: map[string]any{"tools": "   "},
			want:   nil,
		},
		{
			name:   "single_tool",
			params: map[string]any{"tools": "web_search"},
			want:   map[string]bool{"web_search": true},
		},
		{
			name:   "multiple_tools",
			params: map[string]any{"tools": "web_search,calculator,code_exec"},
			want:   map[string]bool{"web_search": true, "calculator": true, "code_exec": true},
		},
		{
			name:   "tools_with_whitespace_trimmed",
			params: map[string]any{"tools": " web_search , calculator "},
			want:   map[string]bool{"web_search": true, "calculator": true},
		},
		{
			name:   "empty_entries_skipped",
			params: map[string]any{"tools": "web_search,,calculator,"},
			want:   map[string]bool{"web_search": true, "calculator": true},
		},
		{
			name:   "non_string_tools_returns_nil",
			params: map[string]any{"tools": 123},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolWhitelistFromParams(tt.params)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected non-nil whitelist, got nil")
			}
			if len(got) != len(tt.want) {
				t.Errorf("whitelist size = %d, want %d", len(got), len(tt.want))
			}
			for name := range tt.want {
				if !got[name] {
					t.Errorf("expected tool %q in whitelist", name)
				}
			}
		})
	}
}

func TestParamKeyTools(t *testing.T) {
	if ParamKeyTools != "tools" {
		t.Errorf("ParamKeyTools = %q, want %q", ParamKeyTools, "tools")
	}
}

// TestToolNamesFromParams covers the shared parser directly. It is exported
// separately from ToolWhitelistFromParams because the selection-time guardrail
// needs a countable slice while the executors need a lookup set; both must
// derive from this one parse so a "how many tools does this strategy enable"
// answer cannot differ between selection and runtime.
func TestToolNamesFromParams(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   []string
	}{
		{name: "nil_params", params: nil, want: nil},
		{name: "empty_params", params: map[string]any{}, want: nil},
		{name: "missing_key", params: map[string]any{"temperature": 0.5}, want: nil},
		{name: "empty_string", params: map[string]any{"tools": ""}, want: nil},
		{name: "whitespace_only", params: map[string]any{"tools": "  "}, want: nil},
		{name: "non_string_value", params: map[string]any{"tools": 123}, want: nil},
		{
			name:   "declaration_order_preserved",
			params: map[string]any{"tools": "web_search,calculator"},
			want:   []string{"web_search", "calculator"},
		},
		{
			// Guided mutation really does emit sloppy separators; the count the
			// guardrail sees must match the tools the executor enables (2, not 4).
			name:   "empty_entries_skipped",
			params: map[string]any{"tools": " a , , b , "},
			want:   []string{"a", "b"},
		},
		{
			// A repeated name enables ONE tool; if the slice kept the duplicate
			// the guardrail would bound 2 while the executor filters on 1.
			name:   "duplicates_collapsed",
			params: map[string]any{"tools": "a,a,b,a"},
			want:   []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolNamesFromParams(tt.params)
			if len(got) != len(tt.want) {
				t.Fatalf("names = %v (len %d), want %v (len %d)", got, len(got), tt.want, len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("names[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestToolNamesAndWhitelistAgree locks the two views onto the same parse: the
// set size the executor filters with must equal the count the guardrail bounds.
func TestToolNamesAndWhitelistAgree(t *testing.T) {
	for _, raw := range []string{"a", "a,b", " a , , b , ", "a,a", ""} {
		params := map[string]any{ParamKeyTools: raw}
		names := ToolNamesFromParams(params)
		set := ToolWhitelistFromParams(params)
		if len(names) != len(set) {
			t.Errorf("raw=%q: len(names)=%d != len(set)=%d", raw, len(names), len(set))
		}
		for _, n := range names {
			if !set[n] {
				t.Errorf("raw=%q: name %q missing from whitelist set", raw, n)
			}
		}
		if len(names) == 0 && set != nil {
			t.Errorf("raw=%q: no names parsed but whitelist is non-nil (%v)", raw, set)
		}
	}
}

func TestMergeNodeParams(t *testing.T) {
	global := map[string]any{
		ParamKeyTools:  "web_search,calculator",
		"temperature":  0.5,
		ParamKeyBudget: "50",
	}

	t.Run("node_overrides_global_tools", func(t *testing.T) {
		got := MergeNodeParams(copyMap(global), map[string]any{ParamKeyTools: "code_exec"})
		if got[ParamKeyTools] != "code_exec" {
			t.Fatalf("tools = %v, want %q (node must override global)", got[ParamKeyTools], "code_exec")
		}
		// Non-nodal keys preserved from global.
		if got["temperature"] != 0.5 {
			t.Fatalf("temperature = %v, want 0.5", got["temperature"])
		}
	})

	t.Run("node_budget_and_prior_promoted", func(t *testing.T) {
		got := MergeNodeParams(copyMap(global), map[string]any{
			ParamKeyBudget: "10",
			ParamKeyPrior:  "strong",
		})
		if got[ParamKeyBudget] != "10" {
			t.Fatalf("budget = %v, want 10", got[ParamKeyBudget])
		}
		if got[ParamKeyPrior] != "strong" {
			t.Fatalf("prior = %v, want strong", got[ParamKeyPrior])
		}
	})

	t.Run("nil_payload_leaves_params_untouched", func(t *testing.T) {
		got := MergeNodeParams(copyMap(global), nil)
		if got[ParamKeyTools] != "web_search,calculator" {
			t.Fatalf("tools = %v, want unchanged global", got[ParamKeyTools])
		}
	})

	t.Run("nil_params_initialized", func(t *testing.T) {
		got := MergeNodeParams(nil, map[string]any{ParamKeyTools: "a"})
		if got[ParamKeyTools] != "a" {
			t.Fatalf("tools = %v, want a", got[ParamKeyTools])
		}
	})

	t.Run("no_node_attrs_keeps_global", func(t *testing.T) {
		got := MergeNodeParams(copyMap(global), map[string]any{"unrelated": 1})
		if got[ParamKeyTools] != "web_search,calculator" {
			t.Fatalf("tools = %v, want global preserved", got[ParamKeyTools])
		}
	})
}

// copyMap shallow-copies a map so tests never mutate the shared global fixture.
func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

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

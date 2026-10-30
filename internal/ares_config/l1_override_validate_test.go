package ares_config

import (
	"strings"
	"testing"
)

// TestValidateKernelL1 pins the three ways a per-tool declaration can be wrong.
//
// They are rejected at load time on purpose: discovered at first use, "max_attempts: 0" presents as
// "retries did not happen" rather than as a configuration error, and the operator has no reason to
// look at the configuration.
func TestValidateKernelL1(t *testing.T) {
	maxAttempts := func(n int) *int { return &n }
	budget := func(n int) *int { return &n }

	cases := []struct {
		name    string
		tools   map[string]L1ToolOverride
		wantErr string
	}{
		{
			name:  "nothing declared is valid",
			tools: nil,
		},
		{
			name:  "valid declaration",
			tools: map[string]L1ToolOverride{"rg": {MaxAttempts: maxAttempts(1), TaskTimeout: "30s", Budget: budget(0)}},
		},
		{
			name:    "zero attempts",
			tools:   map[string]L1ToolOverride{"rg": {MaxAttempts: maxAttempts(0)}},
			wantErr: "at least 1",
		},
		{
			name:    "negative budget",
			tools:   map[string]L1ToolOverride{"rg": {Budget: budget(-1)}},
			wantErr: "must not be negative",
		},
		{
			name:    "unparseable timeout",
			tools:   map[string]L1ToolOverride{"rg": {TaskTimeout: "thirty"}},
			wantErr: "task_timeout",
		},
		{
			name:    "non-positive timeout",
			tools:   map[string]L1ToolOverride{"rg": {TaskTimeout: "0s"}},
			wantErr: "must be positive",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Kernel: KernelConfig{L1: &L1Config{Tools: tc.tools}}}
			err := cfg.validateKernelL1()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

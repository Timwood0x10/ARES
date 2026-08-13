package core

import (
	"testing"
)

// TestCapabilityConstants tests all capability constants.
func TestCapabilityConstants(t *testing.T) {
	tests := []struct {
		name string
		cap  Capability
		want string
	}{
		{
			name: "math capability",
			cap:  CapabilityMath,
			want: "math",
		},
		{
			name: "knowledge capability",
			cap:  CapabilityKnowledge,
			want: "knowledge",
		},
		{
			name: "memory capability",
			cap:  CapabilityMemory,
			want: "memory",
		},
		{
			name: "text capability",
			cap:  CapabilityText,
			want: "text",
		},
		{
			name: "network capability",
			cap:  CapabilityNetwork,
			want: "network",
		},
		{
			name: "time capability",
			cap:  CapabilityTime,
			want: "time",
		},
		{
			name: "file capability",
			cap:  CapabilityFile,
			want: "file",
		},
		{
			name: "external capability",
			cap:  CapabilityExternal,
			want: "external",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.cap) != tt.want {
				t.Errorf("got %q, want %q", tt.cap, tt.want)
			}
		})
	}
}

// TestCapabilityUniqueness ensures all capabilities are unique.
func TestCapabilityUniqueness(t *testing.T) {
	caps := map[Capability]bool{
		CapabilityMath:      true,
		CapabilityKnowledge: true,
		CapabilityMemory:    true,
		CapabilityText:      true,
		CapabilityNetwork:   true,
		CapabilityTime:      true,
		CapabilityFile:      true,
		CapabilityExternal:  true,
	}

	if len(caps) != 8 {
		t.Errorf("expected 8 unique capabilities, got %d", len(caps))
	}
}

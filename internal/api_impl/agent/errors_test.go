package agentapi

import (
	"testing"
)

// TestErrorVariables verifies that all error variables are properly defined.
func TestErrorVariables(t *testing.T) {
	tests := []struct {
		name  string
		error error
		want  string
	}{
		{
			name:  "ErrInvalidAgentID",
			error: ErrInvalidAgentID,
			want:  "invalid agent ID",
		},
		{
			name:  "ErrAgentNotFound",
			error: ErrAgentNotFound,
			want:  "agent not found: not found",
		},
		{
			name:  "ErrAgentAlreadyExists",
			error: ErrAgentAlreadyExists,
			want:  "agent already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.error == nil {
				t.Errorf("expected error variable to be non-nil")
			}
			if tt.error.Error() != tt.want {
				t.Errorf("error message mismatch: got %q, want %q", tt.error.Error(), tt.want)
			}
		})
	}
}

// TestErrorNilCheck ensures error variables are not nil.
func TestErrorNilCheck(t *testing.T) {
	errorVars := []error{
		ErrInvalidAgentID,
		ErrAgentNotFound,
		ErrAgentAlreadyExists,
	}

	for _, err := range errorVars {
		if err == nil {
			t.Errorf("error variable should not be nil")
		}
	}
}

// TestErrorUniqueness ensures all error messages are unique.
func TestErrorUniqueness(t *testing.T) {
	errorMessages := map[string]bool{
		ErrInvalidAgentID.Error():     true,
		ErrAgentNotFound.Error():      true,
		ErrAgentAlreadyExists.Error(): true,
	}

	if len(errorMessages) != 3 {
		t.Errorf("expected 3 unique error messages, got %d", len(errorMessages))
	}
}

package retrieval

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
			name:  "ErrInvalidTenantID",
			error: ErrInvalidTenantID,
			want:  "invalid tenant ID",
		},
		{
			name:  "ErrInvalidQuery",
			error: ErrInvalidQuery,
			want:  "invalid query",
		},
		{
			name:  "ErrNoRetrievalService",
			error: ErrNoRetrievalService,
			want:  "no retrieval service configured",
		},
		{
			name:  "ErrSearchFailed",
			error: ErrSearchFailed,
			want:  "search failed",
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
		ErrInvalidTenantID,
		ErrInvalidQuery,
		ErrNoRetrievalService,
		ErrSearchFailed,
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
		ErrInvalidTenantID.Error():    true,
		ErrInvalidQuery.Error():       true,
		ErrNoRetrievalService.Error(): true,
		ErrSearchFailed.Error():       true,
	}

	if len(errorMessages) != 4 {
		t.Errorf("expected 4 unique error messages, got %d", len(errorMessages))
	}
}

package memory

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
			name:  "ErrInvalidUserID",
			error: ErrInvalidUserID,
			want:  "invalid user ID",
		},
		{
			name:  "ErrInvalidSessionID",
			error: ErrInvalidSessionID,
			want:  "invalid session ID",
		},
		{
			name:  "ErrInvalidRole",
			error: ErrInvalidRole,
			want:  "invalid role",
		},
		{
			name:  "ErrInvalidContent",
			error: ErrInvalidContent,
			want:  "invalid content",
		},
		{
			name:  "ErrInvalidTaskID",
			error: ErrInvalidTaskID,
			want:  "invalid task ID",
		},
		{
			name:  "ErrInvalidQuery",
			error: ErrInvalidQuery,
			want:  "invalid query",
		},
		{
			name:  "ErrInvalidLimit",
			error: ErrInvalidLimit,
			want:  "invalid limit",
		},
		{
			name:  "ErrSessionNotFound",
			error: ErrSessionNotFound,
			want:  "session not found",
		},
		{
			name:  "ErrTaskNotFound",
			error: ErrTaskNotFound,
			want:  "task not found",
		},
		{
			name:  "ErrInvalidConversationID",
			error: ErrInvalidConversationID,
			want:  "invalid conversation ID",
		},
		{
			name:  "ErrNoMessages",
			error: ErrNoMessages,
			want:  "no messages provided",
		},
		{
			name:  "ErrInvalidTenantID",
			error: ErrInvalidTenantID,
			want:  "invalid tenant ID",
		},
		{
			name:  "ErrInvalidConfig",
			error: ErrInvalidConfig,
			want:  "invalid configuration",
		},
		{
			name:  "ErrDistillationFailed",
			error: ErrDistillationFailed,
			want:  "distillation failed",
		},
		{
			name:  "ErrEmbeddingFailed",
			error: ErrEmbeddingFailed,
			want:  "embedding generation failed",
		},
		{
			name:  "ErrVectorSearchFailed",
			error: ErrVectorSearchFailed,
			want:  "vector search failed",
		},
		{
			name:  "ErrInvalidMemoryID",
			error: ErrInvalidMemoryID,
			want:  "invalid memory ID",
		},
		{
			name:  "ErrMemoryNotFound",
			error: ErrMemoryNotFound,
			want:  "memory not found",
		},
		{
			name:  "ErrMemoryUpdateFailed",
			error: ErrMemoryUpdateFailed,
			want:  "memory update failed",
		},
		{
			name:  "ErrMemoryDeleteFailed",
			error: ErrMemoryDeleteFailed,
			want:  "memory deletion failed",
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
		ErrInvalidUserID,
		ErrInvalidSessionID,
		ErrInvalidRole,
		ErrInvalidContent,
		ErrInvalidTaskID,
		ErrInvalidQuery,
		ErrInvalidLimit,
		ErrSessionNotFound,
		ErrTaskNotFound,
		ErrInvalidConversationID,
		ErrNoMessages,
		ErrInvalidTenantID,
		ErrInvalidConfig,
		ErrDistillationFailed,
		ErrEmbeddingFailed,
		ErrVectorSearchFailed,
		ErrInvalidMemoryID,
		ErrMemoryNotFound,
		ErrMemoryUpdateFailed,
		ErrMemoryDeleteFailed,
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
		ErrInvalidUserID.Error():         true,
		ErrInvalidSessionID.Error():      true,
		ErrInvalidRole.Error():           true,
		ErrInvalidContent.Error():        true,
		ErrInvalidTaskID.Error():         true,
		ErrInvalidQuery.Error():          true,
		ErrInvalidLimit.Error():          true,
		ErrSessionNotFound.Error():       true,
		ErrTaskNotFound.Error():          true,
		ErrInvalidConversationID.Error(): true,
		ErrNoMessages.Error():            true,
		ErrInvalidTenantID.Error():       true,
		ErrInvalidConfig.Error():         true,
		ErrDistillationFailed.Error():    true,
		ErrEmbeddingFailed.Error():       true,
		ErrVectorSearchFailed.Error():    true,
		ErrInvalidMemoryID.Error():       true,
		ErrMemoryNotFound.Error():        true,
		ErrMemoryUpdateFailed.Error():    true,
		ErrMemoryDeleteFailed.Error():    true,
	}

	if len(errorMessages) != 20 {
		t.Errorf("expected 20 unique error messages, got %d", len(errorMessages))
	}
}

package kernel

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// TestUserIDFromPayload pins the scheduler's M5 user source: the caller-
// declared user rides the task payload (already durable in the checkpoint
// envelope), and anything other than a non-empty string means "no user known"
// so the execution context falls back to the tenant scope.
func TestUserIDFromPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{name: "nil payload", payload: nil, want: ""},
		{name: "absent key", payload: map[string]any{"input": "hi"}, want: ""},
		{name: "non-string value", payload: map[string]any{ares_events.EventKeyUserID: 42}, want: ""},
		{name: "empty string", payload: map[string]any{ares_events.EventKeyUserID: ""}, want: ""},
		{name: "present", payload: map[string]any{ares_events.EventKeyUserID: "user-1"}, want: "user-1"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := userIDFromPayload(tc.payload); got != tc.want {
				t.Errorf("userIDFromPayload() = %q, want %q", got, tc.want)
			}
		})
	}
}

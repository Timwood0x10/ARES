package sub

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestDistillUserID pins the M5 write-side user attribution source: the user is
// read from the task payload — the durable carrier the checkpoint envelope
// already persists across yield→resume — so the emitter cannot silently drift
// to another key or fall back to the agent id (the pre-M5 stand-in).
func TestDistillUserID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		task *models.Task
		want string
	}{
		{name: "nil task", task: nil, want: ""},
		{name: "nil payload", task: &models.Task{}, want: ""},
		{name: "absent key", task: &models.Task{Payload: map[string]any{"input": "hi"}}, want: ""},
		{name: "non-string value", task: &models.Task{Payload: map[string]any{ares_events.EventKeyUserID: 42}}, want: ""},
		{name: "present", task: &models.Task{Payload: map[string]any{ares_events.EventKeyUserID: "user-1"}}, want: "user-1"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := distillUserID(tc.task); got != tc.want {
				t.Errorf("distillUserID() = %q, want %q", got, tc.want)
			}
		})
	}
}

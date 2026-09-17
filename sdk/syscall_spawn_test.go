package sdk

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestL2RouterExecutorIdentity verifies the identity contract of a
// syscall-spawned peer's executor body: the typ field carries the DECLARED
// capability (so create_task sub-tasks match the peer by capability, not by
// its generated agent id), and the id stays the generated agent id.
func TestL2RouterExecutorIdentity(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		typ      models.AgentType
		wantType models.AgentType
	}{
		{
			name:     "declared capability is scheduler-facing",
			id:       "spawned-researcher-1",
			typ:      models.AgentType("researcher"),
			wantType: models.AgentType("researcher"),
		},
		{
			name:     "empty typ surfaces empty type",
			id:       "coder",
			typ:      "",
			wantType: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := &l2RouterExecutor{id: tc.id, typ: tc.typ}
			if got := exec.Type(); got != tc.wantType {
				t.Fatalf("Type() = %q, want %q", got, tc.wantType)
			}
			if got := exec.ID(); got != tc.id {
				t.Fatalf("ID() = %q, want %q", got, tc.id)
			}
		})
	}
}

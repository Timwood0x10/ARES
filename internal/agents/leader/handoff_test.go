package leader

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRoleRegistry registers the built-in roles (planner/researcher/
// coder/reviewer) into a fresh registry.
func newTestRoleRegistry(t *testing.T) *agents.ProfileRegistry {
	t.Helper()
	registry := agents.NewProfileRegistry()
	for _, p := range agents.DefaultProfiles() {
		registry.Register(p)
	}
	return registry
}

// newTask builds a task with the given agent type and payload.
func newTask(agentType models.AgentType) *models.Task {
	return &models.Task{
		TaskID:    string(agentType) + "-1",
		AgentType: agentType,
		Payload:   map[string]any{"raw": "sensitive upstream message body"},
	}
}

// TestBuildHandoff_SwitchesRoles verifies Ch.10 multi-stage role switching:
// the dispatch context must carry a different active profile per role, and
// the Handoff must not include raw upstream message bodies.
func TestBuildHandoff_SwitchesRoles(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	a := &leaderAgent{
		id:              "leader1",
		profileRegistry: newTestRoleRegistry(t),
		eventStore:      store,
	}

	tasks := []*models.Task{newTask(models.AgentType("researcher"))}
	ctx := context.Background()

	// Stage 1: researcher.
	dispatchCtx, handoff := a.buildHandoff(ctx, tasks)
	require.NotNil(t, handoff, "handoff must be built for a non-empty task list")
	assert.Equal(t, agents.RolePlanner, handoff.From)
	assert.Equal(t, agents.RoleResearcher, handoff.To)

	active := agents.GetFromContext(dispatchCtx)
	require.NotNil(t, active, "dispatch context must carry the active role")
	assert.Equal(t, agents.RoleResearcher, active.Role)

	// Stage 2: coder — derived from the previous dispatch context.
	dispatchCtx2, handoff2 := a.buildHandoff(dispatchCtx, []*models.Task{newTask(models.AgentType("coder"))})
	require.NotNil(t, handoff2)
	assert.Equal(t, agents.RoleCoder, handoff2.To)
	active2 := agents.GetFromContext(dispatchCtx2)
	require.NotNil(t, active2)
	assert.Equal(t, agents.RoleCoder, active2.Role)

	// Stage 3: reviewer.
	dispatchCtx3, handoff3 := a.buildHandoff(dispatchCtx2, []*models.Task{newTask(models.AgentType("reviewer"))})
	require.NotNil(t, handoff3)
	assert.Equal(t, agents.RoleReviewer, handoff3.To)
	active3 := agents.GetFromContext(dispatchCtx3)
	require.NotNil(t, active3)
	assert.Equal(t, agents.RoleReviewer, active3.Role)

	// Every stage must carry a different profile.
	assert.NotEqual(t, active.Role, active2.Role)
	assert.NotEqual(t, active2.Role, active3.Role)
}

// TestBuildHandoff_HandoffCarriesStructuredContextOnly verifies the Handoff
// context holds structured data (counts, types) but never the raw message body.
func TestBuildHandoff_HandoffCarriesStructuredContextOnly(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	a := &leaderAgent{
		id:              "leader1",
		profileRegistry: newTestRoleRegistry(t),
		eventStore:      store,
	}

	tasks := []*models.Task{
		newTask(models.AgentType("researcher")),
		newTask(models.AgentType("coder")),
	}
	_, handoff := a.buildHandoff(context.Background(), tasks)
	require.NotNil(t, handoff)

	// Structured context only: counts and types, no raw content.
	assert.Equal(t, 2, handoff.Context["task_count"])
	types, ok := handoff.Context["agent_types"].([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"researcher", "coder"}, types)
	assert.NotContains(t, handoff.Context, "raw")

	// Artifacts reference outputs by path/type/summary, not inline content.
	require.Len(t, handoff.Artifacts, 2)
	for _, artifact := range handoff.Artifacts {
		assert.NotEmpty(t, artifact.Path)
		assert.Equal(t, "task", artifact.Type)
		assert.NotContains(t, artifact.Summary, "sensitive upstream message body")
	}
}

// TestBuildHandoff_EmitsHandoffEvent verifies an EventHandoff is written to
// the event store with from/to and artifact counts.
func TestBuildHandoff_EmitsHandoffEvent(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	a := &leaderAgent{
		id:              "leader1",
		profileRegistry: newTestRoleRegistry(t),
		eventStore:      store,
	}

	tasks := []*models.Task{newTask(models.AgentType("researcher"))}
	_, handoff := a.buildHandoff(context.Background(), tasks)
	require.NotNil(t, handoff)

	events, err := store.ReadAll(context.Background(), ares_events.ReadOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, events)

	var handoffEvents []*ares_events.Event
	for _, ev := range events {
		if ev.Type == ares_events.EventHandoff {
			handoffEvents = append(handoffEvents, ev)
		}
	}
	require.Len(t, handoffEvents, 1, "exactly one handoff event expected")
	assert.Equal(t, agents.RolePlanner, handoffEvents[0].Payload[ares_events.EventKeyHandoffFrom])
	assert.Equal(t, agents.RoleResearcher, handoffEvents[0].Payload[ares_events.EventKeyHandoffTo])
	assert.Equal(t, 1, handoffEvents[0].Payload[ares_events.EventKeyHandoffArtifactCount])
	assert.Equal(t, 2, handoffEvents[0].Payload[ares_events.EventKeyHandoffContextKeys])
}

// TestBuildHandoff_EmptyTasks returns nil handoff and unchanged context.
func TestBuildHandoff_EmptyTasks(t *testing.T) {
	a := &leaderAgent{
		id:              "leader1",
		profileRegistry: newTestRoleRegistry(t),
	}
	ctx := context.Background()
	dispatchCtx, handoff := a.buildHandoff(ctx, nil)
	assert.Nil(t, handoff)
	assert.Equal(t, ctx, dispatchCtx)
	assert.Nil(t, agents.GetFromContext(dispatchCtx))
}

// TestBuildHandoff_FallsBackToPlanner verifies that an unregistered agent type
// produces no handoff and no profile application: the planner fallback is not
// a real role switch, so it must not leak instructions into the dispatch.
func TestBuildHandoff_FallsBackToPlanner(t *testing.T) {
	a := &leaderAgent{
		id:              "leader1",
		profileRegistry: newTestRoleRegistry(t),
	}
	ctx := context.Background()
	dispatchCtx, handoff := a.buildHandoff(ctx, []*models.Task{
		newTask(models.AgentType("unknown_type")),
	})
	assert.Nil(t, handoff, "no real role match must produce no handoff")
	assert.Equal(t, ctx, dispatchCtx, "dispatch context must remain untouched")
	assert.Nil(t, agents.GetFromContext(dispatchCtx), "no profile must be applied")
}

// TestBuildHandoff_NilRegistry treats a missing registry as planner-only and
// never panics.
func TestBuildHandoff_NilRegistry(t *testing.T) {
	a := &leaderAgent{id: "leader1"} // nil profileRegistry
	ctx := context.Background()
	dispatchCtx, handoff := a.buildHandoff(ctx, []*models.Task{newTask(models.AgentType("coder"))})
	assert.Nil(t, handoff)
	assert.Equal(t, ctx, dispatchCtx)
	assert.Nil(t, agents.GetFromContext(dispatchCtx))
}

// TestSelectRole verifies role selection across registered and unknown types.
func TestSelectRole(t *testing.T) {
	a := &leaderAgent{profileRegistry: newTestRoleRegistry(t)}

	tests := []struct {
		name        string
		tasks       []*models.Task
		wantRole    string
		wantMatched bool
	}{
		{
			name:        "registered type maps to its role",
			tasks:       []*models.Task{newTask(models.AgentType("researcher"))},
			wantRole:    agents.RoleResearcher,
			wantMatched: true,
		},
		{
			name: "first registered type wins",
			tasks: []*models.Task{
				newTask(models.AgentType("coder")),
				newTask(models.AgentType("reviewer")),
			},
			wantRole:    agents.RoleCoder,
			wantMatched: true,
		},
		{
			name:        "unknown type does not match",
			tasks:       []*models.Task{newTask(models.AgentType("mystery"))},
			wantRole:    agents.RolePlanner,
			wantMatched: false,
		},
		{
			name: "nil tasks are skipped",
			tasks: []*models.Task{
				nil,
				newTask(models.AgentType("reviewer")),
			},
			wantRole:    agents.RoleReviewer,
			wantMatched: true,
		},
		{
			name:        "empty list does not match",
			tasks:       []*models.Task{},
			wantRole:    agents.RolePlanner,
			wantMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, matched := a.selectRole(tt.tasks)
			assert.Equal(t, tt.wantRole, role)
			assert.Equal(t, tt.wantMatched, matched)
		})
	}
}

// TestTaskAgentTypes verifies the structured type extraction helper.
func TestTaskAgentTypes(t *testing.T) {
	assert.Empty(t, taskAgentTypes(nil))
	assert.Empty(t, taskAgentTypes([]*models.Task{nil}))
	assert.ElementsMatch(t,
		[]string{"coder", "reviewer"},
		taskAgentTypes([]*models.Task{
			newTask(models.AgentType("coder")),
			nil,
			newTask(models.AgentType("reviewer")),
		}),
	)
}

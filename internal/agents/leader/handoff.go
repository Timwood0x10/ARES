package leader

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// buildHandoff selects the target role for the given tasks from the profile
// registry, switches the context to that role, and builds an explicit Handoff
// carrying only structured data (Ch.10: structured deliverables, no full
// conversation history). It emits an EventHandoff for evidence tracing.
//
// When no task agent type matches a registered profile, no role is applied:
// the planner fallback is NOT a role switch, so the dispatch context is
// returned untouched and the handoff is nil. This prevents the fallback
// profile's instructions from leaking into sub-agent prompts for pre-existing
// task types that never opted into role switching.
//
// Args:
//
//	ctx - caller context.
//	tasks - planned tasks to dispatch.
//
// Returns:
//
//	ctx - derived context with the active role applied; the input context when
//	  tasks is empty, the registry is nil, or no task type matched a profile.
//	handoff - the structured handoff, or nil when no role switch applies.
func (a *leaderAgent) buildHandoff(ctx context.Context, tasks []*models.Task) (context.Context, *agents.Handoff) {
	if len(tasks) == 0 || a.profileRegistry == nil {
		return ctx, nil
	}
	role, matched := a.selectRole(tasks)
	if !matched {
		// No task type matched a registered profile: this is not a real role
		// switch, so do not apply the planner fallback to the context.
		return ctx, nil
	}
	roleCtx, profile, err := a.profileRegistry.ApplyToContext(ctx, role)
	if err != nil {
		// Dispatch proceeds without a role; the handoff is skipped.
		log.Warn("leader: apply role failed", "role", role, "error", err)
		return ctx, nil
	}

	handoff := agents.NewHandoff(agents.RolePlanner, role, summarizeTasks(tasks))
	handoff.WithContext("task_count", len(tasks))
	handoff.WithContext("agent_types", taskAgentTypes(tasks))
	for _, t := range tasks {
		if t == nil {
			continue
		}
		handoff.WithArtifact(t.TaskID, "task", string(t.AgentType))
	}

	a.emitEvent(roleCtx, ares_events.EventHandoff, map[string]any{
		ares_events.EventKeyHandoffFrom:          handoff.From,
		ares_events.EventKeyHandoffTo:            handoff.To,
		ares_events.EventKeyHandoffArtifactCount: len(handoff.Artifacts),
		ares_events.EventKeyHandoffContextKeys:   len(handoff.Context),
	})
	_ = profile
	return roleCtx, handoff
}

// selectRole picks the role for a batch of tasks: the first non-empty agent
// type that has a registered profile. The second return value reports whether
// a real role match was found; when false, the caller must not apply any
// profile (the planner fallback is not a role switch).
func (a *leaderAgent) selectRole(tasks []*models.Task) (string, bool) {
	if a.profileRegistry == nil {
		return agents.RolePlanner, false
	}
	for _, t := range tasks {
		if t == nil {
			continue
		}
		candidate := string(t.AgentType)
		if a.profileRegistry.Has(candidate) {
			return candidate, true
		}
	}
	return agents.RolePlanner, false
}

// taskAgentTypes returns the agent types of the given tasks.
func taskAgentTypes(tasks []*models.Task) []string {
	types := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t == nil {
			continue
		}
		types = append(types, string(t.AgentType))
	}
	return types
}

// summarizeTasks builds a short task description for the handoff.
func summarizeTasks(tasks []*models.Task) string {
	if len(tasks) == 0 {
		return ""
	}
	if len(tasks) == 1 && tasks[0] != nil {
		return string(tasks[0].AgentType) + " task"
	}
	return fmt.Sprintf("%d tasks", len(tasks))
}

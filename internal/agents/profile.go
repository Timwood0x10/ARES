// Package agents provides AgentProfile — a simple specialization mechanism
// for multi-role agent collaboration in ARES 0.3.0.
//
// Design principle (from AI Agents in Depth Ch.10):
// "Multiple agents don't need complex registries or message buses.
// They need explicit handoffs between roles with different instructions
// and tool sets, all within the same runtime."
//
// (The ProfileRegistry, DefaultProfiles role set, and the context-switching
// helpers were removed as dead code: zero production callers — the live
// profile consumers are the evolution candidate pipeline and its GA fixture
// tests, which construct AgentProfile values directly.)
package agents

// AgentProfile defines a specialized agent role.
// Each profile has its own system prompt (Instructions) and tool set.
// Roles are switched via Handoff, not by creating new agent instances.
type AgentProfile struct {
	// ID is the unique identifier for this profile.
	ID string

	// Role is the human-readable role name.
	// Examples: "researcher", "coder", "reviewer", "planner"
	Role string

	// Instructions is the system prompt for this role.
	// This replaces the static system prompt when this role is active.
	Instructions string

	// Tools is the list of tool names available to this role.
	// Empty means "use all tools".
	Tools []string

	// Metadata carries optional role-specific configuration.
	Metadata map[string]any
}

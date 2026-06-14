// Package arena provides a chaos engineering layer that proves GoAgentX is a
// self-healing runtime. Arena deliberately calls dangerous APIs on existing
// systems; the existing resurrection plugin, failover, and checkpoint recovery
// handle the rest.
package arena

import "time"

// ActionType classifies an arena action.
type ActionType string

const (
	ActionKillLeader ActionType = "kill_leader"
	ActionKillAgent  ActionType = "kill_agent"
	ActionRemoveNode ActionType = "remove_node"
	ActionRemoveEdge ActionType = "remove_edge"
)

// Action represents a single chaos action to inject.
type Action struct {
	ID        string         `json:"id"`
	Type      ActionType     `json:"type"`
	TargetID  string         `json:"target_id"`
	SourceID  string         `json:"source_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// Result holds the outcome of an action.
type Result struct {
	Success  bool          `json:"success"`
	Action   Action        `json:"action"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
}

// Stats aggregates arena action statistics.
type Stats struct {
	TotalActions      int       `json:"total_actions"`
	SuccessfulActions int       `json:"successful_actions"`
	FailedActions     int       `json:"failed_actions"`
	LastAction        time.Time `json:"last_action"`
}

// VerifyResult holds the result of a single verification test.
type VerifyResult struct {
	Name     string        `json:"name"`
	Passed   bool          `json:"passed"`
	Detail   string        `json:"detail"`
	Duration time.Duration `json:"duration"`
}

// VerifyReport holds the full verification report.
type VerifyReport struct {
	Tests  []VerifyResult `json:"tests"`
	Passed int            `json:"passed"`
	Failed int            `json:"failed"`
	Total  int            `json:"total"`
}

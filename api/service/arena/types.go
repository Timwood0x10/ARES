// Package arena provides public types for chaos engineering operations.
package arena

import "time"

// ActionType classifies an arena action.
type ActionType string

const (
	ActionKillLeader       ActionType = "kill_leader"
	ActionKillAgent        ActionType = "kill_agent"
	ActionRemoveNode       ActionType = "remove_node"
	ActionRemoveEdge       ActionType = "remove_edge"
	ActionPauseAgent       ActionType = "pause_agent"
	ActionResumeAgent      ActionType = "resume_agent"
	ActionSlowAgent        ActionType = "slow_agent"
	ActionKillOrchestrator ActionType = "kill_orchestrator"
	ActionNetworkPartition ActionType = "network_partition"
	ActionToolTimeout      ActionType = "tool_timeout"
	ActionMemoryCorrupt    ActionType = "memory_corrupt"
	ActionMCPDisconnect    ActionType = "mcp_disconnect"
	ActionLLMFailure       ActionType = "llm_failure"
)

// Action represents a single chaos action to inject.
type Action struct {
	ID        string         `json:"id" yaml:"id"`
	Type      ActionType     `json:"type" yaml:"type"`
	TargetID  string         `json:"target_id" yaml:"target_id"`
	SourceID  string         `json:"source_id,omitempty" yaml:"source_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at" yaml:"created_at"`
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

// MetricsSnapshot holds a point-in-time view of arena metrics.
type MetricsSnapshot struct {
	AvgRecoveryTime     time.Duration           `json:"avg_recovery_time"`
	MinRecoveryTime     time.Duration           `json:"min_recovery_time"`
	MaxRecoveryTime     time.Duration           `json:"max_recovery_time"`
	LastRecoveryTime    time.Duration           `json:"last_recovery_time"`
	FailoverCount       int                     `json:"failover_count"`
	TotalRecoveries     int                     `json:"total_recoveries"`
	FailedRecoveries    int                     `json:"failed_recoveries"`
	DataConsistencyRate float64                 `json:"data_consistency_rate"`
	ActionStats         map[string]ActionMetric `json:"action_stats"`
}

// ActionMetric holds aggregated metrics for a single action type.
type ActionMetric struct {
	Total       int           `json:"total"`
	Success     int           `json:"success"`
	Failed      int           `json:"failed"`
	AvgDuration time.Duration `json:"avg_duration"`
}

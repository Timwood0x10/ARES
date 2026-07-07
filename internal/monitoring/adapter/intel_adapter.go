// Package adapter bridges dashboard intelligence into monitoring console.
package adapter

import (
	"github.com/Timwood0x10/ares/internal/dashboard"
)

// IntelAdapter wraps dashboard.Engine to implement monitoring.IntelProvider.
type IntelAdapter struct {
	engine *dashboard.Engine
}

// NewIntelAdapter creates an adapter that feeds Engine data into the console.
func NewIntelAdapter(engine *dashboard.Engine) *IntelAdapter {
	return &IntelAdapter{engine: engine}
}

// AnomalyCount returns the number of active anomalies.
func (a *IntelAdapter) AnomalyCount() int {
	return len(a.engine.Anomalies())
}

// InsightCount returns the number of unacknowledged insights.
func (a *IntelAdapter) InsightCount() int {
	return len(a.engine.Insights())
}

// SystemLevel returns the overall system health level.
func (a *IntelAdapter) SystemLevel() string {
	return string(a.engine.SystemHealth().Level)
}

// AgentLevel returns the health level of a specific agent.
func (a *IntelAdapter) AgentLevel(agentID string) string {
	return string(a.engine.Health(agentID).Level)
}

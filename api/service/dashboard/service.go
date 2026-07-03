// Package dashboard provides the public API for the monitoring dashboard.
package dashboard

import (
	"github.com/Timwood0x10/ares/internal/dashboard"
)

// Dashboard wraps internal/dashboard for public consumption.
type Dashboard struct {
	inner *dashboard.Orchestrator
}

// New creates a new dashboard with the given MCP and LLM executors.
func New(mcp dashboard.MCPExecutor, llm dashboard.LLMExecutor) *Dashboard {
	return &Dashboard{inner: dashboard.NewOrchestrator(mcp, llm)}
}

// Orchestrator returns the underlying dashboard orchestrator.
func (d *Dashboard) Orchestrator() *dashboard.Orchestrator {
	return d.inner
}

// Package dashboard provides the public API for the monitoring dashboard.
package dashboard

import (
	"github.com/Timwood0x10/ares/internal/dashboard"
)

// Dashboard wraps internal/dashboard for public consumption.
type Dashboard struct {
	inner *dashboard.Orchestrator
}

// MCPExecutor abstracts MCP execution for the dashboard.
type MCPExecutor = dashboard.MCPExecutor

// LLMExecutor abstracts LLM execution for the dashboard.
type LLMExecutor = dashboard.LLMExecutor

// AgentInfo holds basic agent information for the dashboard.
type AgentInfo = dashboard.AgentInfo

// MCPServerStatusView is a dashboard-safe view of MCP server status.
type MCPServerStatusView = dashboard.MCPServerStatusView

// MCPToolView represents an MCP tool in the dashboard.
type MCPToolView = dashboard.MCPToolView

// New creates a new dashboard with the given MCP and LLM executors.
func New(mcp MCPExecutor, llm LLMExecutor) *Dashboard {
	return &Dashboard{inner: dashboard.NewOrchestrator(mcp, llm)}
}

// Stop gracefully shuts down the dashboard and waits for running agents.
func (d *Dashboard) Stop() {
	d.inner.Stop()
}

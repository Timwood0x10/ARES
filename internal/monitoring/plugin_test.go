package monitoring

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConsole(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		p := NewConsole()
		require.NotNil(t, p)
		mp, ok := p.(*MonitorPlugin)
		require.True(t, ok)
		assert.NotNil(t, mp.mainPage)
		assert.NotNil(t, mp.engine)
		assert.NotNil(t, mp.publisher)
	})

	t.Run("with hub", func(t *testing.T) {
		hub := newMockHub()
		p := NewConsole(WithWSHub(hub))
		require.NotNil(t, p)
	})

	t.Run("with interval", func(t *testing.T) {
		p := NewConsole(WithSnapshotInterval(500 * time.Millisecond))
		require.NotNil(t, p)
	})
}

func TestMonitorPlugin_Name(t *testing.T) {
	p := NewConsole()
	assert.Equal(t, "monitor", p.(ares_runtime.RuntimePlugin).Name())
}

func TestMonitorPlugin_Capabilities(t *testing.T) {
	p := NewConsole()
	caps := p.(ares_runtime.RuntimePlugin).Capabilities()
	assert.Contains(t, caps, ares_runtime.CapObserver)
}

func TestMonitorPlugin_StartStop(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	p := NewConsole()
	plugin := p.(ares_runtime.RuntimePlugin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := plugin.Start(ctx, bus)
	require.NoError(t, err)

	err = plugin.Stop(ctx)
	require.NoError(t, err)
}

func TestMonitorPlugin_StartAlreadyStarted(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	p := NewConsole()
	plugin := p.(ares_runtime.RuntimePlugin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := plugin.Start(ctx, bus)
	require.NoError(t, err)

	err = plugin.Start(ctx, bus)
	assert.ErrorIs(t, err, ErrPluginAlreadyStarted)

	_ = plugin.Stop(ctx)
}

func TestMonitorPlugin_StopIdempotent(t *testing.T) {
	p := NewConsole()
	plugin := p.(ares_runtime.RuntimePlugin)

	ctx := context.Background()

	err := plugin.Stop(ctx)
	require.NoError(t, err)

	err = plugin.Stop(ctx)
	require.NoError(t, err)
}

func TestMonitorPlugin_ProcessesEvents(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	p := NewConsole()
	plugin := p.(ares_runtime.RuntimePlugin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := plugin.Start(ctx, bus)
	require.NoError(t, err)

	// Emit events via bus.
	bus.Emit(ctx, "s1", ares_events.EventAgentStarted, "test", map[string]any{
		"agent_id": "a1", "name": "worker",
	})
	bus.Emit(ctx, "s1", ares_events.EventLLMCall, "test", map[string]any{
		"agent_id": "a1", "estimated_cost": 0.05,
	})

	time.Sleep(50 * time.Millisecond)

	// Verify snapshot reflects events.
	snap, err := p.Snapshot(ctx)
	require.NoError(t, err)
	assert.InDelta(t, 0.05, snap.Cost.Total, 0.0001)

	_ = plugin.Stop(ctx)
}

func TestMonitorPlugin_Snapshot(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	snap, err := p.Snapshot(ctx)
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.False(t, snap.UpdateTime.IsZero())
}

func TestMonitorPlugin_DAG(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	d, err := p.DAG(ctx)
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Empty(t, d.Nodes)
}

func TestMonitorPlugin_Events(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.Events(ctx, 10)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestMonitorPlugin_Tasks(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	tasks, err := p.Tasks(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestMonitorPlugin_Timeline(t *testing.T) {
	t.Run("node not found", func(t *testing.T) {
		p := NewConsole()
		ctx := context.Background()
		_, err := p.Timeline(ctx, "missing")
		assert.Error(t, err)
	})

	t.Run("node with timeline", func(t *testing.T) {
		p := NewConsole()
		mp := p.(*MonitorPlugin)
		_ = mp.engine.AddNode(&dag.DAGNode{
			ID:       "n1",
			Name:     "test",
			Type:     "agent",
			Status:   dag.StatusPending,
			Timeline: []dag.TimelineEvent{},
		})

		ctx := context.Background()
		tl, err := p.Timeline(ctx, "n1")
		require.NoError(t, err)
		assert.Empty(t, tl)
	})
}

func TestMonitorPlugin_Actions(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	actions, err := p.Actions(ctx, "n1")
	require.NoError(t, err)
	assert.Len(t, actions, 4)
}

func TestMonitorPlugin_ExecuteAction(t *testing.T) {
	t.Run("no interaction engine", func(t *testing.T) {
		p := NewConsole()
		ctx := context.Background()
		_, err := p.ExecuteAction(ctx, "kill")
		assert.Error(t, err)
	})
}

func TestMonitorPlugin_AgentCost(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		p := NewConsole()
		ctx := context.Background()
		_, err := p.AgentCost(ctx, "missing")
		assert.Error(t, err)
	})

	t.Run("found", func(t *testing.T) {
		p := NewConsole()
		mp := p.(*MonitorPlugin)
		mp.mainPage.costBar.HandleEvent(&ares_events.Event{
			ID: "e1", StreamID: "s1", Type: ares_events.EventLLMCall,
			Payload: map[string]any{
				"agent_id": "a1", "estimated_cost": 0.01,
			},
			Timestamp: time.Now(),
		})

		ctx := context.Background()
		cost, err := p.AgentCost(ctx, "a1")
		require.NoError(t, err)
		assert.InDelta(t, 0.01, cost.EstimatedCost, 0.0001)
	})
}

func TestMonitorPlugin_CostBreakdown(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	cb, err := p.CostBreakdown(ctx)
	require.NoError(t, err)
	assert.NotNil(t, cb)
}

func TestMonitorPlugin_CostAlerts(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.CostAlerts(ctx)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestMonitorPlugin_Interactions(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.Interactions(ctx, 10)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestMonitorPlugin_Traces(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.Traces(ctx, "trace-1")
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestMonitorPlugin_AgentMemory(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.AgentMemory(ctx, "a1")
	assert.Error(t, err)
}

func TestMonitorPlugin_AgentEvolution(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.AgentEvolution(ctx, "a1")
	assert.Error(t, err)
}

func TestMonitorPlugin_MCPToolCalls(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.MCPToolCalls(ctx, "a1", 10)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestMonitorPlugin_LLMCalls(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.LLMCalls(ctx, "a1", 10)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestMonitorPlugin_Recommendations(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.Recommendations(ctx)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestMonitorPlugin_RegisterRoutes(t *testing.T) {
	p := NewConsole().(*MonitorPlugin)
	mux := http.NewServeMux()
	p.RegisterRoutes(mux)
	// Should not panic.
}

func TestMonitorPlugin_Agent(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.Agent(ctx, "a1")
	assert.Error(t, err)
}

func TestMonitorPlugin_Detail(t *testing.T) {
	t.Run("no detail panel", func(t *testing.T) {
		p := NewConsole()
		ctx := context.Background()
		_, err := p.Detail(ctx, "agent", "a1")
		assert.Error(t, err)
	})
}

func TestMonitorPlugin_WithRuntimeManager(t *testing.T) {
	p := NewConsole(WithRuntimeManager(nil))
	assert.NotNil(t, p)
}

func TestMonitorPlugin_WithOrchestrator(t *testing.T) {
	p := NewConsole(WithOrchestrator(nil))
	assert.NotNil(t, p)
}

func TestMonitorPlugin_WithCostAlertThreshold(t *testing.T) {
	p := NewConsole(WithCostAlertThreshold(100.0))
	assert.NotNil(t, p)
}

func TestMonitorPlugin_WithMCP(t *testing.T) {
	mock := &mockMCPManager{
		tools: []MCPToolInfo{{Name: "tool1"}},
	}
	p := NewConsole(WithMCP(mock))
	require.NotNil(t, p)

	mp := p.(*MonitorPlugin)
	assert.NotNil(t, mp.mcp)
}

func TestMonitorPlugin_WithPruneConfig(t *testing.T) {
	cfg := PruneConfig{MaxAgentAge: 1 * time.Hour, PruneInterval: 30 * time.Second}
	p := NewConsole(WithPruneConfig(cfg))
	require.NotNil(t, p)

	mp := p.(*MonitorPlugin)
	assert.NotNil(t, mp.pruner)
}

func TestMonitorPlugin_ListMCPTools_NoManager(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.ListMCPTools(ctx)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestMonitorPlugin_ListMCPTools_WithManager(t *testing.T) {
	mock := &mockMCPManager{
		tools: []MCPToolInfo{{Name: "read_file", Description: "Read"}},
	}
	p := NewConsole(WithMCP(mock))
	ctx := context.Background()
	tools, err := p.ListMCPTools(ctx)
	require.NoError(t, err)
	assert.Len(t, tools, 1)
	assert.Equal(t, "read_file", tools[0].Name)
}

func TestMonitorPlugin_CallMCPTool_NoManager(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.CallMCPTool(ctx, "tool1", nil)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestMonitorPlugin_CallMCPTool_WithManager(t *testing.T) {
	mock := &mockMCPManager{
		result: &MCPToolResult{ToolName: "tool1", Output: map[string]any{"ok": true}},
	}
	p := NewConsole(WithMCP(mock))
	ctx := context.Background()
	result, err := p.CallMCPTool(ctx, "tool1", nil)
	require.NoError(t, err)
	assert.Equal(t, "tool1", result.ToolName)
	assert.True(t, result.Output["ok"].(bool))
}

func TestMonitorPlugin_RunHTTPServer(t *testing.T) {
	// RunHTTPServer creates a server; just verify it doesn't panic.
	p := NewConsole().(*MonitorPlugin)
	// We can't actually start the server in a test without a port,
	// but we can verify the method exists and the server is created.
	srv := NewHTTPServer(p)
	assert.NotNil(t, srv)
}

package main

import (
	"context"
	"encoding/json"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/introspect"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// akfToolAdapter adapts an AKF MCP tool (func(ctx, input string) -> string)
// to the core_tools.Tool interface so it can be registered in the internal
// tool registry and used by agents through the ToolBinder. This is the wiring
// that makes knowledge genome patches affect the agent's knowledge tools —
// because both share the same comp.KnowledgeRuntime instance.
type akfToolAdapter struct {
	name string
	desc string
	fn   func(ctx context.Context, input string) (string, error)
}

// Name returns the tool name.
func (a *akfToolAdapter) Name() string { return a.name }

// Description returns the tool description.
func (a *akfToolAdapter) Description() string { return a.desc }

// Category returns the tool category.
func (a *akfToolAdapter) Category() core_tools.ToolCategory { return core_tools.CategoryKnowledge }

func (a *akfToolAdapter) Capabilities() []core_tools.Capability {
	return []core_tools.Capability{core_tools.CapabilityKnowledge}
}

func (a *akfToolAdapter) Parameters() *core_tools.ParameterSchema { return nil }

func (a *akfToolAdapter) Execute(ctx context.Context, params map[string]interface{}) (core_tools.Result, error) {
	input, _ := params["input"].(string)
	if input == "" {
		// Serialize the whole params map as JSON input.
		b, _ := json.Marshal(params)
		input = string(b)
	}
	out, err := a.fn(ctx, input)
	if err != nil {
		return core_tools.NewErrorResult(err.Error()), nil
	}
	return core_tools.NewResult(true, map[string]interface{}{"output": out}), nil
}

// fabricAgentSource adapts *agentfabric.Fabric to introspect.AgentSource so
// the control plane lists the live fabric population.
type fabricAgentSource struct {
	fabric *agentfabric.Fabric
}

// ListAgents implements introspect.AgentSource.
func (s *fabricAgentSource) ListAgents() []introspect.AgentView {
	views := s.fabric.AgentsView()
	out := make([]introspect.AgentView, 0, len(views))
	for _, v := range views {
		row := introspect.AgentView{
			ID:     v.Identity,
			Name:   v.Identity,
			Status: string(v.State),
		}
		if len(v.Capabilities) > 0 {
			row.Role = v.Capabilities[0]
		}
		out = append(out, row)
	}
	return out
}

// package graph - provides dynamic agent orchestration with pluggable scheduling.

package graph

import (
	"context"
	"fmt"

	"goagentx/internal/agents/base"
	"goagentx/internal/errors"
	"goagentx/internal/tools/resources/core"
)

// Node represents an executable unit in the graph.
type Node interface {
	// Execute runs the node with the given state.
	Execute(ctx context.Context, state *State) error
	// ID returns the unique identifier of the node.
	ID() string
}

// AgentNode wraps an existing agent to be used as a node.
type AgentNode struct {
	agent base.Agent
}

// NewAgentNode creates a new agent node.
//
// NOTE: This function will panic if agent is nil. This is intentional as it
// indicates a programming error in the calling code. These constructors are
// used during workflow graph initialization (startup phase), and invalid
// parameters represent fatal startup failures that should prevent application
// launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// agent - agent instance, must not be nil.
// Returns new agent node.
func NewAgentNode(agent base.Agent) *AgentNode {
	if agent == nil {
		panic("agent cannot be nil: nil agent is a programming error")
	}
	return &AgentNode{agent: agent}
}

// Execute runs the agent node.
func (n *AgentNode) Execute(ctx context.Context, state *State) error {
	if n == nil || n.agent == nil {
		return fmt.Errorf("agent node is not initialized")
	}

	input, exists := state.Get("input")
	if !exists || input == nil {
		return fmt.Errorf("agent %s: input not found in state", n.ID())
	}
	result, err := n.agent.Process(ctx, input)
	if err != nil {
		return errors.Wrapf(err, "agent %s execution failed", n.ID())
	}

	state.Set("node."+n.ID(), result)
	return nil
}

// ID returns the agent ID.
func (n *AgentNode) ID() string {
	if n == nil || n.agent == nil {
		return ""
	}
	return n.agent.ID()
}

// ToolNode wraps an existing tool to be used as a node.
type ToolNode struct {
	tool core.Tool
}

// NewToolNode creates a new tool node.
//
// NOTE: This function will panic if tool is nil. This is intentional as it
// indicates a programming error in the calling code. These constructors are
// used during workflow graph initialization (startup phase), and invalid
// parameters represent fatal startup failures that should prevent application
// launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// tool - tool instance, must not be nil.
// Returns new tool node.
func NewToolNode(tool core.Tool) *ToolNode {
	if tool == nil {
		panic("tool cannot be nil: nil tool is a programming error")
	}
	return &ToolNode{tool: tool}
}

// Execute runs the tool node.
func (n *ToolNode) Execute(ctx context.Context, state *State) error {
	if n == nil || n.tool == nil {
		return fmt.Errorf("tool node is not initialized")
	}

	params := state.ToParams()
	result, err := n.tool.Execute(ctx, params)
	if err != nil {
		return errors.Wrapf(err, "tool %s execution failed", n.ID())
	}

	if result.Success {
		state.Set("node."+n.ID(), result.Data)
	} else {
		state.Set("node."+n.ID(), result.Error)
	}
	return nil
}

// ID returns the tool name.
func (n *ToolNode) ID() string {
	if n == nil || n.tool == nil {
		return ""
	}
	return n.tool.Name()
}

// FuncNode wraps a simple function to be used as a node.
type FuncNode struct {
	id string
	fn func(context.Context, *State) error
}

// NewFuncNode creates a new function node.
//
// NOTE: This function will panic if id is empty or fn is nil. This is intentional
// as it indicates a programming error in the calling code. These constructors are
// used during workflow graph initialization (startup phase), and invalid
// parameters represent fatal startup failures that should prevent application
// launch. This follows the coding standard allowing panic for fatal startup errors.
//
// Args:
// id - unique node identifier, must not be empty.
// fn - function to execute, must not be nil.
// Returns new function node.
func NewFuncNode(id string, fn func(context.Context, *State) error) *FuncNode {
	if id == "" {
		panic("node id cannot be empty: empty id is a programming error")
	}
	if fn == nil {
		panic("function cannot be nil: nil function is a programming error")
	}
	return &FuncNode{id: id, fn: fn}
}

// Execute runs the function node.
func (n *FuncNode) Execute(ctx context.Context, state *State) error {
	if n == nil || n.fn == nil {
		return fmt.Errorf("function node is not initialized")
	}

	err := n.fn(ctx, state)
	if err != nil {
		return errors.Wrapf(err, "function %s execution failed", n.ID())
	}

	return nil
}

// ID returns the function node ID.
func (n *FuncNode) ID() string {
	if n == nil {
		return ""
	}
	return n.id
}

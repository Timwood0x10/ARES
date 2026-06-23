// package graph - provides dynamic agent orchestration with pluggable scheduling.

package graph

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/events"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
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
// It supports optional lifecycle hooks for structured message recording
// and event emission during tool execution.
type ToolNode struct {
	tool        core.Tool
	nodeID      string
	executionID string
	eventSink   func(ctx context.Context, eventType events.EventType, payload map[string]any)
}

// WithNodeID sets the node identifier for event payload correlation.
func (n *ToolNode) WithNodeID(id string) *ToolNode {
	n.nodeID = id
	return n
}

// WithExecutionID sets the execution/graph instance identifier for event correlation.
func (n *ToolNode) WithExecutionID(id string) *ToolNode {
	n.executionID = id
	return n
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

// WithEventSink attaches an event sink for lifecycle events.
// The sink is called before and after tool execution with EventToolCallStarted
// and EventToolCallCompleted events respectively.
func (n *ToolNode) WithEventSink(sink func(ctx context.Context, eventType events.EventType, payload map[string]any)) *ToolNode {
	n.eventSink = sink
	return n
}

// Execute runs the tool node with optional lifecycle hooks.
// Before execution, emits EventToolCallStarted; after execution,
// emits EventToolCallCompleted with the result summary.
// The event payload includes correlation IDs for ReAct Runtime Trace.
func (n *ToolNode) Execute(ctx context.Context, state *State) error {
	if n == nil || n.tool == nil {
		return fmt.Errorf("tool node is not initialized")
	}

	startTime := time.Now()
	toolName := n.tool.Name()
	nodeID := n.nodeID
	if nodeID == "" {
		nodeID = toolName
	}
	// Generate deterministic input hash (used for both tool_call_id and event payload).
	params := state.ToParams()
	inputHash := hashInput(params)

	// tool_call_id: deterministic so event replay produces the same ID.
	toolCallID := fmt.Sprintf("tool_%s_%s", nodeID, inputHash)

	// Pre-execution hook: emit tool call started event with correlation IDs.
	if n.eventSink != nil {
		n.eventSink(ctx, events.EventToolCallStarted, map[string]any{
			"tool":         toolName,
			"tool_call_id": toolCallID,
			"node_id":      nodeID,
			"execution_id": n.executionID,
			"input_hash":   inputHash,
			"timestamp":    startTime,
		})
	}

	result, err := n.tool.Execute(ctx, params)

	durationMs := time.Since(startTime).Milliseconds()

	// Post-execution hook: emit tool call completed event with structured result.
	if n.eventSink != nil {
		payload := map[string]any{
			"tool":         toolName,
			"tool_call_id": toolCallID,
			"node_id":      nodeID,
			"execution_id": n.executionID,
			"input_hash":   inputHash,
			"duration_ms":  durationMs,
			"timestamp":    time.Now(),
		}
		if err != nil {
			payload["status"] = "error"
			payload["error"] = err.Error()
		} else if !result.Success {
			payload["status"] = "failed"
			payload["summary"] = truncateString(result.Error, 200)
		} else {
			payload["status"] = "success"
			payload["summary"] = truncateString(fmt.Sprintf("%v", result.Data), 200)
		}
		n.eventSink(ctx, events.EventToolCallCompleted, payload)
	}

	if err != nil {
		return errors.Wrapf(err, "tool %s execution failed", toolName)
	}

	if result.Success {
		state.Set("node."+toolName, result.Data)
	} else {
		state.Set("node."+toolName, result.Error)
	}
	return nil
}

// truncateString truncates a string to maxLen runes.
func truncateString(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
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

// hashInput generates a deterministic hash from tool input parameters.
func hashInput(params map[string]any) string {
	data := fmt.Sprintf("%v", params)
	h := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", h[:8])
}

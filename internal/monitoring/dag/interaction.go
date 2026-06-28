// Package dag provides the node interaction engine for ARES Console.
package dag

import (
	"context"
	"errors"
	"fmt"
)

// Sentinel errors for the interaction engine.
var (
	ErrUnknownAction = errors.New("unknown action")
)

// RuntimeController abstracts the runtime layer for agent lifecycle operations.
type RuntimeController interface {
	// NotifyAgentDead signals that an agent should be marked dead.
	NotifyAgentDead(agentID, reason string)
	// GetAgentInfo returns runtime metadata for the given agent.
	GetAgentInfo(agentID string) (*AgentInfo, bool)
	// RestartAgent restarts a stopped or dead agent.
	RestartAgent(ctx context.Context, agentID string) error
}

// OrchestratorController abstracts the orchestrator layer for agent management.
type OrchestratorController interface {
	// CancelAgent cancels an orchestrator-managed agent. Returns true if found.
	CancelAgent(id string) bool
	// CreateAgent creates a new agent from the request payload.
	CreateAgent(req any) (string, error)
}

// AgentInfo holds runtime metadata about a single agent.
type AgentInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Source string `json:"source"`
}

// Publisher pushes action results to external consumers such as WebSocket hubs.
type Publisher interface {
	Publish(result *ActionResult)
}

// ActionResult is the outcome of executing an interaction on a DAG node.
type ActionResult struct {
	ActionID string `json:"action_id"`
	NodeID   string `json:"node_id"`
	Action   string `json:"action"`
	Success  bool   `json:"success"`
	Message  string `json:"message"`
}

// InteractionEngine dispatches actions to DAG nodes and coordinates with
// the runtime and orchestrator layers.
type InteractionEngine struct {
	dag     *Engine
	runtime RuntimeController
	orch    OrchestratorController
	pub     Publisher
}

// NewInteractionEngine creates a new InteractionEngine.
// The dag parameter must not be nil; runtime and orch may be nil
// but actions that require them will return errors.
func NewInteractionEngine(dag *Engine, runtime RuntimeController, orch OrchestratorController) *InteractionEngine {
	if dag == nil {
		panic("dag engine must not be nil")
	}
	return &InteractionEngine{
		dag:     dag,
		runtime: runtime,
		orch:    orch,
	}
}

// SetPublisher sets the publisher for pushing action results.
func (ie *InteractionEngine) SetPublisher(pub Publisher) {
	ie.pub = pub
}

// ExecuteAction dispatches an action to the specified node.
// Supported actions: kill, resume, retry, inspect.
func (ie *InteractionEngine) ExecuteAction(ctx context.Context, nodeID, action string) (*ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}

	var result *ActionResult
	var err error

	switch action {
	case "kill":
		result, err = ie.killNode(ctx, nodeID)
	case "resume":
		result, err = ie.resumeNode(ctx, nodeID)
	case "retry":
		result, err = ie.retryNode(ctx, nodeID)
	case "inspect":
		result, err = ie.inspectNode(nodeID)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownAction, action)
	}

	if err != nil {
		return nil, err
	}

	ie.publishResult(result)
	return result, nil
}

// killNode terminates a node. For runtime agents it calls NotifyAgentDead;
// for orchestrator agents it calls CancelAgent. Updates the DAG status to dead.
func (ie *InteractionEngine) killNode(ctx context.Context, nodeID string) (*ActionResult, error) {
	node, ok := ie.dag.GetNode(nodeID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}

	result := &ActionResult{
		ActionID: fmt.Sprintf("act-kill-%s", nodeID),
		NodeID:   nodeID,
		Action:   "kill",
	}

	source := nodeSource(node)

	switch source {
	case "runtime":
		if ie.runtime == nil {
			return nil, fmt.Errorf("runtime controller not available")
		}
		ie.runtime.NotifyAgentDead(nodeID, "killed by operator")
		result.Success = true
		result.Message = fmt.Sprintf("runtime agent %s killed", nodeID)
	case "orchestrator":
		if ie.orch == nil {
			return nil, fmt.Errorf("orchestrator controller not available")
		}
		if !ie.orch.CancelAgent(nodeID) {
			result.Success = false
			result.Message = fmt.Sprintf("orchestrator agent %s not found", nodeID)
			return result, nil
		}
		result.Success = true
		result.Message = fmt.Sprintf("orchestrator agent %s canceled", nodeID)
	default:
		result.Success = true
		result.Message = fmt.Sprintf("node %s killed (source: %s)", nodeID, source)
	}

	// Update DAG status to dead if the node is running.
	if node.Status == StatusRunning {
		if err := ie.dag.UpdateNodeStatus(nodeID, StatusDead, "killed by operator"); err != nil {
			return nil, fmt.Errorf("update DAG status: %w", err)
		}
	}

	return result, nil
}

// resumeNode restarts a dead or stopped node via the runtime controller.
func (ie *InteractionEngine) resumeNode(ctx context.Context, nodeID string) (*ActionResult, error) {
	_, ok := ie.dag.GetNode(nodeID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}

	result := &ActionResult{
		ActionID: fmt.Sprintf("act-resume-%s", nodeID),
		NodeID:   nodeID,
		Action:   "resume",
	}

	if ie.runtime == nil {
		return nil, fmt.Errorf("runtime controller not available")
	}

	if err := ie.runtime.RestartAgent(ctx, nodeID); err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("resume failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("agent %s resumed", nodeID)
	return result, nil
}

// retryNode is similar to resume but records a retry attempt.
func (ie *InteractionEngine) retryNode(ctx context.Context, nodeID string) (*ActionResult, error) {
	_, ok := ie.dag.GetNode(nodeID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}

	result := &ActionResult{
		ActionID: fmt.Sprintf("act-retry-%s", nodeID),
		NodeID:   nodeID,
		Action:   "retry",
	}

	if ie.runtime == nil {
		return nil, fmt.Errorf("runtime controller not available")
	}

	if err := ie.runtime.RestartAgent(ctx, nodeID); err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("retry failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("agent %s retried", nodeID)
	return result, nil
}

// inspectNode returns the current state of a node as an ActionResult.
func (ie *InteractionEngine) inspectNode(nodeID string) (*ActionResult, error) {
	node, ok := ie.dag.GetNode(nodeID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}

	return &ActionResult{
		ActionID: fmt.Sprintf("act-inspect-%s", nodeID),
		NodeID:   nodeID,
		Action:   "inspect",
		Success:  true,
		Message:  fmt.Sprintf("node %s: status=%s type=%s", nodeID, node.Status, node.Type),
	}, nil
}

// publishResult sends the result to the publisher if one is set.
func (ie *InteractionEngine) publishResult(result *ActionResult) {
	if ie.pub != nil && result != nil {
		ie.pub.Publish(result)
	}
}

// nodeSource determines the source of a node from its metadata.
// Returns "runtime", "orchestrator", or "unknown".
func nodeSource(node *DAGNode) string {
	if node == nil || node.Metadata == nil {
		return "unknown"
	}
	src, ok := node.Metadata["source"]
	if !ok {
		return "unknown"
	}
	s, ok := src.(string)
	if !ok {
		return "unknown"
	}
	return s
}

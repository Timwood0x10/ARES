package dag

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// Sentinel errors for the DAG engine.
var (
	ErrNodeNotFound = errors.New("node not found")
	ErrEdgeNotFound = errors.New("edge not found")
	ErrNodeExists   = errors.New("node already exists")
	ErrEdgeExists   = errors.New("edge already exists")
	ErrInvalidEdge  = errors.New("invalid edge: missing from or to node")
	ErrNilNode      = errors.New("node must not be nil")
	ErrNilEdge      = errors.New("edge must not be nil")
)

// DAGSnapshot is the full DAG state at a point in time.
type DAGSnapshot struct {
	Nodes map[string]*DAGNode `json:"nodes"`
	Edges map[string]*DAGEdge `json:"edges"`
	Stats DAGStats            `json:"stats"`
}

// DAGStats provides aggregate statistics over the DAG.
type DAGStats struct {
	TotalNodes        int `json:"total_nodes"`
	RunningNodes      int `json:"running_nodes"`
	CompletedNodes    int `json:"completed_nodes"`
	FailedNodes       int `json:"failed_nodes"`
	DeadNodes         int `json:"dead_nodes"`
	PendingNodes      int `json:"pending_nodes"`
	ResurrectingNodes int `json:"resurrecting_nodes"`
	TotalEdges        int `json:"total_edges"`
}

// Engine is the core DAG engine that manages nodes, edges, and their lifecycle.
// All public methods are safe for concurrent use.
type Engine struct {
	mu    sync.RWMutex
	nodes map[string]*DAGNode
	edges map[string]*DAGEdge
}

// NewEngine creates a new DAG engine with empty node and edge maps.
func NewEngine() *Engine {
	return &Engine{
		nodes: make(map[string]*DAGNode),
		edges: make(map[string]*DAGEdge),
	}
}

// AddNode inserts a node into the DAG.
// Returns ErrNodeExists if a node with the same ID already exists.
func (e *Engine) AddNode(node *DAGNode) error {
	if node == nil {
		return ErrNilNode
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.nodes[node.ID]; exists {
		return fmt.Errorf("%w: %s", ErrNodeExists, node.ID)
	}
	now := time.Now()
	node.CreatedAt = now
	node.UpdatedAt = now
	if node.Status == "" {
		node.Status = StatusPending
	}
	if node.Timeline == nil {
		node.Timeline = make([]TimelineEvent, 0)
	}
	e.nodes[node.ID] = node
	return nil
}

// AddEdge inserts a directed edge into the DAG.
// Returns ErrEdgeExists if the edge ID is already used.
// Returns ErrInvalidEdge if from or to node does not exist.
func (e *Engine) AddEdge(edge *DAGEdge) error {
	if edge == nil {
		return ErrNilEdge
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.edges[edge.ID]; exists {
		return fmt.Errorf("%w: %s", ErrEdgeExists, edge.ID)
	}
	if _, ok := e.nodes[edge.FromID]; !ok {
		return fmt.Errorf("%w: from node %q", ErrInvalidEdge, edge.FromID)
	}
	if _, ok := e.nodes[edge.ToID]; !ok {
		return fmt.Errorf("%w: to node %q", ErrInvalidEdge, edge.ToID)
	}
	edge.CreatedAt = time.Now()
	e.edges[edge.ID] = edge
	return nil
}

// RemoveNode removes a node and all edges connected to it.
func (e *Engine) RemoveNode(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.nodes[id]; !exists {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	delete(e.nodes, id)
	for edgeID, edge := range e.edges {
		if edge.FromID == id || edge.ToID == id {
			delete(e.edges, edgeID)
		}
	}
	return nil
}

// UpdateNodeStatus transitions a node to a new status and records a timeline event.
// Returns ErrNodeNotFound if the node does not exist.
func (e *Engine) UpdateNodeStatus(id string, status NodeStatus, msg string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	node, exists := e.nodes[id]
	if !exists {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	if err := ValidateTransition(node.Status, status); err != nil {
		return fmt.Errorf("node %q: %w", id, err)
	}
	node.Status = status
	node.Message = msg
	node.UpdatedAt = time.Now()
	evt := TimelineEvent{
		ID:        fmt.Sprintf("tl-%s-%d", id, time.Now().UnixNano()),
		NodeID:    id,
		Type:      string(status),
		Message:   msg,
		Level:     levelForStatus(status),
		Timestamp: time.Now(),
	}
	node.Timeline = append(node.Timeline, evt)
	return nil
}

// GetNode returns a deep copy of the node by ID. The returned pointer is safe
// to read without holding the engine lock. Use UpdateNodeStatus for state changes.
func (e *Engine) GetNode(id string) (*DAGNode, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	node, ok := e.nodes[id]
	if !ok {
		return nil, false
	}
	return copyNode(node), true
}

// GetEdge returns a copy of the edge by ID.
func (e *Engine) GetEdge(id string) (*DAGEdge, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	edge, ok := e.edges[id]
	if !ok {
		return nil, false
	}
	cp := *edge
	return &cp, true
}

// Snapshot returns a deep copy of all nodes, edges, and aggregate stats.
func (e *Engine) Snapshot() DAGSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	snap := DAGSnapshot{
		Nodes: make(map[string]*DAGNode, len(e.nodes)),
		Edges: make(map[string]*DAGEdge, len(e.edges)),
	}
	for id, n := range e.nodes {
		nodeCopy := copyNode(n)
		snap.Nodes[id] = nodeCopy
		snap.Stats.TotalNodes++
		switch n.Status {
		case StatusRunning:
			snap.Stats.RunningNodes++
		case StatusCompleted:
			snap.Stats.CompletedNodes++
		case StatusFailed:
			snap.Stats.FailedNodes++
		case StatusDead:
			snap.Stats.DeadNodes++
		case StatusPending:
			snap.Stats.PendingNodes++
		case StatusResurrecting:
			snap.Stats.ResurrectingNodes++
		}
	}
	for id, edge := range e.edges {
		edgeCopy := *edge
		snap.Edges[id] = &edgeCopy
	}
	snap.Stats.TotalEdges = len(e.edges)
	return snap
}

// TrimTimeline keeps at most maxLen timeline events per node, discarding the oldest.
func (e *Engine) TrimTimeline(id string, maxLen int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	node, exists := e.nodes[id]
	if !exists {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	if maxLen > 0 && len(node.Timeline) > maxLen {
		node.Timeline = node.Timeline[len(node.Timeline)-maxLen:]
	}
	return nil
}

// Nodes returns a snapshot of all node IDs and their current status.
// This is intended for pruning and diagnostics, not for display.
func (e *Engine) Nodes() map[string]NodeStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make(map[string]NodeStatus, len(e.nodes))
	for id, n := range e.nodes {
		result[id] = n.Status
	}
	return result
}

// Children returns all direct child nodes (nodes with an edge FROM the given node).
func (e *Engine) Children(id string) []*DAGNode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var result []*DAGNode
	for _, edge := range e.edges {
		if edge.FromID == id {
			if child, ok := e.nodes[edge.ToID]; ok {
				result = append(result, child)
			}
		}
	}
	return result
}

// Parents returns all direct parent nodes (nodes with an edge TO the given node).
func (e *Engine) Parents(id string) []*DAGNode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var result []*DAGNode
	for _, edge := range e.edges {
		if edge.ToID == id {
			if parent, ok := e.nodes[edge.FromID]; ok {
				result = append(result, parent)
			}
		}
	}
	return result
}

// HandleEvent processes an ares_events.Event and updates the DAG accordingly.
// Agent lifecycle events create or update nodes; task events update status;
// operational events (LLM, tool calls) are recorded as timeline events.
func (e *Engine) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}
	switch evt.Type {
	case ares_events.EventAgentStarted:
		e.handleAgentStarted(evt)
	case ares_events.EventAgentStopped:
		e.handleAgentStopped(evt)
	case ares_events.EventFailoverTriggered:
		e.handleFailoverTriggered(evt)
	case ares_events.EventFailoverCompleted:
		e.handleFailoverCompleted(evt)
	case ares_events.EventTaskCreated:
		e.handleTaskCreated(evt)
	case ares_events.EventTaskCompleted:
		e.handleTaskCompleted(evt)
	case ares_events.EventTaskFailed, ares_events.EventStepFailed:
		e.handleTaskFailed(evt)
	default:
		e.handleTimelineEvent(evt)
	}
}

// handleAgentStarted creates or reuses a node for the agent and sets it to running.
// Lightweight display fields (Label, Source, AgentType) are populated from the
// event payload so the node can be rendered without loading full agent data.
func (e *Engine) handleAgentStarted(evt *ares_events.Event) {
	nodeID := extractNodeID(evt)
	if nodeID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	node, exists := e.nodes[nodeID]
	if !exists {
		name := extractPayloadString(evt, "name")
		node = &DAGNode{
			ID:        nodeID,
			Name:      name,
			Type:      "agent",
			Status:    StatusPending,
			ParentID:  extractPayloadString(evt, "parent_id"),
			Label:     name,
			Source:    extractPayloadString(evt, "source"),
			AgentType: extractPayloadString(evt, "agent_type"),
			Timeline:  make([]TimelineEvent, 0),
		}
		node.CreatedAt = evt.Timestamp
		e.nodes[nodeID] = node
	}
	if err := validateAndTransition(node, StatusRunning); err == nil {
		node.Status = StatusRunning
		node.UpdatedAt = evt.Timestamp
		node.Timeline = append(node.Timeline, makeTimeline(nodeID, "agent.started", "Agent started", "info", evt))
	}
}

// handleAgentStopped transitions the agent node to completed.
func (e *Engine) handleAgentStopped(evt *ares_events.Event) {
	e.updateNodeFromEvent(evt, StatusCompleted, "agent.stopped", "Agent stopped")
}

// handleFailoverTriggered transitions the agent node to dead.
func (e *Engine) handleFailoverTriggered(evt *ares_events.Event) {
	e.updateNodeFromEvent(evt, StatusDead, "failover.triggered", "Failover triggered")
}

// handleFailoverCompleted transitions the agent node through resurrecting to running.
func (e *Engine) handleFailoverCompleted(evt *ares_events.Event) {
	nodeID := extractNodeID(evt)
	if nodeID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	node, exists := e.nodes[nodeID]
	if !exists {
		return
	}
	// Two-step transition: dead -> resurrecting -> running.
	if err := validateAndTransition(node, StatusResurrecting); err == nil {
		node.Status = StatusResurrecting
		node.UpdatedAt = evt.Timestamp
		node.Timeline = append(node.Timeline, makeTimeline(nodeID, "failover.resurrecting", "Resurrecting", "info", evt))
	}
	if err := validateAndTransition(node, StatusRunning); err == nil {
		node.Status = StatusRunning
		node.UpdatedAt = evt.Timestamp
		node.Timeline = append(node.Timeline, makeTimeline(nodeID, "failover.completed", "Failover completed", "info", evt))
	}
}

// handleTaskCreated creates a new task node in pending state.
func (e *Engine) handleTaskCreated(evt *ares_events.Event) {
	taskID := extractPayloadString(evt, "task_id")
	if taskID == "" {
		taskID = evt.StreamID
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.nodes[taskID]; exists {
		return
	}
	name := extractPayloadString(evt, "name")
	node := &DAGNode{
		ID:        taskID,
		Name:      name,
		Type:      "task",
		Status:    StatusRunning,
		ParentID:  extractPayloadString(evt, "agent_id"),
		Label:     name,
		Source:    extractPayloadString(evt, "source"),
		AgentType: extractPayloadString(evt, "agent_type"),
		Timeline:  make([]TimelineEvent, 0),
	}
	node.CreatedAt = evt.Timestamp
	node.UpdatedAt = evt.Timestamp
	e.nodes[taskID] = node
}

// handleTaskCompleted transitions the task node to completed.
func (e *Engine) handleTaskCompleted(evt *ares_events.Event) {
	taskID := extractPayloadString(evt, "task_id")
	if taskID == "" {
		taskID = evt.StreamID
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	node, exists := e.nodes[taskID]
	if !exists {
		return
	}
	if err := validateAndTransition(node, StatusCompleted); err == nil {
		node.Status = StatusCompleted
		node.UpdatedAt = evt.Timestamp
		node.Timeline = append(node.Timeline, makeTimeline(taskID, "task.completed", "Task completed", "info", evt))
	}
}

// handleTaskFailed transitions the task node to failed.
func (e *Engine) handleTaskFailed(evt *ares_events.Event) {
	taskID := extractPayloadString(evt, "task_id")
	if taskID == "" {
		taskID = evt.StreamID
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	node, exists := e.nodes[taskID]
	if !exists {
		return
	}
	if err := validateAndTransition(node, StatusFailed); err == nil {
		node.Status = StatusFailed
		node.UpdatedAt = evt.Timestamp
		msg := fmt.Sprintf("Event %s", evt.Type)
		node.Timeline = append(node.Timeline, makeTimeline(taskID, string(evt.Type), msg, "error", evt))
	}
}

// handleTimelineEvent appends a timeline entry to the node identified in the event payload.
func (e *Engine) handleTimelineEvent(evt *ares_events.Event) {
	nodeID := extractNodeID(evt)
	if nodeID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	node, exists := e.nodes[nodeID]
	if !exists {
		return
	}
	node.Timeline = append(node.Timeline, makeTimeline(nodeID, string(evt.Type), string(evt.Type), "info", evt))
	node.UpdatedAt = evt.Timestamp
}

// updateNodeFromEvent is a helper that transitions a node to a given status.
func (e *Engine) updateNodeFromEvent(evt *ares_events.Event, status NodeStatus, evtType, msg string) {
	nodeID := extractNodeID(evt)
	if nodeID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	node, exists := e.nodes[nodeID]
	if !exists {
		return
	}
	if err := validateAndTransition(node, status); err == nil {
		node.Status = status
		node.UpdatedAt = evt.Timestamp
		node.Timeline = append(node.Timeline, makeTimeline(nodeID, evtType, msg, "info", evt))
	}
}

// validateAndTransition checks if a status transition is allowed without returning an error value.
func validateAndTransition(node *DAGNode, target NodeStatus) error {
	return ValidateTransition(node.Status, target)
}

// copyNode creates a deep copy of a node including its timeline.
func copyNode(n *DAGNode) *DAGNode {
	cp := *n
	if n.Timeline != nil {
		cp.Timeline = make([]TimelineEvent, len(n.Timeline))
		copy(cp.Timeline, n.Timeline)
	}
	if n.Tags != nil {
		cp.Tags = make([]string, len(n.Tags))
		copy(cp.Tags, n.Tags)
	}
	if n.Metadata != nil {
		cp.Metadata = make(map[string]any, len(n.Metadata))
		for k, v := range n.Metadata {
			cp.Metadata[k] = v
		}
	}
	return &cp
}

// extractNodeID retrieves the agent_id or task_id from the event payload,
// falling back to the StreamID.
func extractNodeID(evt *ares_events.Event) string {
	if id := extractPayloadString(evt, "agent_id"); id != "" {
		return id
	}
	if id := extractPayloadString(evt, "task_id"); id != "" {
		return id
	}
	return evt.StreamID
}

// extractPayloadString reads a string value from the event payload.
func extractPayloadString(evt *ares_events.Event, key string) string {
	if evt.Payload == nil {
		return ""
	}
	v, ok := evt.Payload[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// makeTimeline creates a TimelineEvent from an ares_events.Event.
func makeTimeline(nodeID, evtType, msg, level string, evt *ares_events.Event) TimelineEvent {
	return TimelineEvent{
		ID:        fmt.Sprintf("tl-%s-%d", nodeID, evt.Timestamp.UnixNano()),
		NodeID:    nodeID,
		Type:      evtType,
		Message:   msg,
		Level:     level,
		Timestamp: evt.Timestamp,
	}
}

// levelForStatus maps a node status to a log level string.
func levelForStatus(status NodeStatus) string {
	switch status {
	case StatusFailed, StatusDead:
		return "error"
	case StatusResurrecting:
		return "warn"
	default:
		return "info"
	}
}

// Package dag implements the directed acyclic graph engine for the ARES monitoring console.
package dag

import (
	"fmt"
	"time"
)

// NodeStatus represents the lifecycle state of a DAG node.
type NodeStatus string

const (
	StatusPending      NodeStatus = "pending"
	StatusRunning      NodeStatus = "running"
	StatusCompleted    NodeStatus = "completed"
	StatusFailed       NodeStatus = "failed"
	StatusDead         NodeStatus = "dead"
	StatusResurrecting NodeStatus = "resurrecting"
)

// validTransitions defines the allowed state transitions.
// Key: current status, Value: set of statuses it can transition to.
var validTransitions = map[NodeStatus]map[NodeStatus]bool{
	StatusPending: {
		StatusRunning: true,
	},
	StatusRunning: {
		StatusCompleted: true,
		StatusFailed:    true,
		StatusDead:      true,
	},
	StatusDead: {
		StatusResurrecting: true,
	},
	StatusResurrecting: {
		StatusRunning: true,
	},
}

// ValidateTransition checks whether a status transition is allowed.
// Returns nil if valid, or an error describing the violation.
func ValidateTransition(from, to NodeStatus) error {
	targets, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("no transitions allowed from status %q", from)
	}
	if !targets[to] {
		return fmt.Errorf("invalid transition from %q to %q", from, to)
	}
	return nil
}

// TimelineEvent records a discrete event on a node's timeline.
type TimelineEvent struct {
	ID        string         `json:"id"`
	NodeID    string         `json:"node_id"`
	Type      string         `json:"type"`
	Message   string         `json:"message"`
	Level     string         `json:"level"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// DAGNode represents a single node in the execution graph.
// Nodes store only lightweight display fields. Full agent data is loaded
// on demand via AgentTracker.GetAgent(id) for the detail panel.
type DAGNode struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Status   NodeStatus `json:"status"`
	Message  string     `json:"message,omitempty"`
	ParentID string     `json:"parent_id,omitempty"`

	// Lightweight display fields populated from events.
	Label     string `json:"label,omitempty"`
	Source    string `json:"source,omitempty"`
	AgentType string `json:"agent_type,omitempty"`

	Tags      []string        `json:"tags,omitempty"`
	Metadata  map[string]any  `json:"metadata,omitempty"`
	Timeline  []TimelineEvent `json:"timeline"`
	Position  Point           `json:"position"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Point represents a 2D coordinate for layout rendering.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

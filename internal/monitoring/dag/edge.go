package dag

import "time"

// EdgeType classifies the relationship between two nodes.
type EdgeType string

const (
	EdgeTypeParent  EdgeType = "parent"
	EdgeTypeDepends EdgeType = "depends"
	EdgeTypeTrigger EdgeType = "trigger"
	EdgeTypeData    EdgeType = "data"
)

// DAGEdge represents a directed edge between two DAG nodes.
type DAGEdge struct {
	ID        string    `json:"id"`
	FromID    string    `json:"from_id"`
	ToID      string    `json:"to_id"`
	Type      EdgeType  `json:"type"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

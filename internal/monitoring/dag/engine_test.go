package dag

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEngine(t *testing.T) {
	eng := NewEngine()
	assert.NotNil(t, eng)
	assert.NotNil(t, eng.nodes)
	assert.NotNil(t, eng.edges)
	assert.Empty(t, eng.nodes)
	assert.Empty(t, eng.edges)
}

func TestAddNode(t *testing.T) {
	tests := []struct {
		name    string
		node    *DAGNode
		wantErr error
	}{
		{
			name: "valid node",
			node: &DAGNode{ID: "n1", Name: "node-1", Type: "agent"},
		},
		{
			name:    "nil node",
			node:    nil,
			wantErr: ErrNilNode,
		},
		{
			name:    "duplicate node",
			node:    &DAGNode{ID: "n1", Name: "node-1-dup", Type: "agent"},
			wantErr: ErrNodeExists,
		},
		{
			name: "node with empty status gets pending",
			node: &DAGNode{ID: "n2", Name: "node-2"},
		},
	}

	eng := NewEngine()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := eng.AddNode(tt.node)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr.Error())
				return
			}
			require.NoError(t, err)
			if tt.node != nil {
				got, ok := eng.GetNode(tt.node.ID)
				require.True(t, ok)
				assert.Equal(t, tt.node.ID, got.ID)
				assert.False(t, got.CreatedAt.IsZero())
				if tt.node.Status == "" {
					assert.Equal(t, StatusPending, got.Status)
				}
			}
		})
	}
}

func TestAddEdge(t *testing.T) {
	eng := NewEngine()
	require.NoError(t, eng.AddNode(&DAGNode{ID: "a", Name: "A"}))
	require.NoError(t, eng.AddNode(&DAGNode{ID: "b", Name: "B"}))

	tests := []struct {
		name    string
		edge    *DAGEdge
		wantErr error
	}{
		{
			name: "valid edge",
			edge: &DAGEdge{ID: "e1", FromID: "a", ToID: "b", Type: EdgeTypeParent},
		},
		{
			name:    "nil edge",
			edge:    nil,
			wantErr: ErrNilEdge,
		},
		{
			name:    "duplicate edge",
			edge:    &DAGEdge{ID: "e1", FromID: "a", ToID: "b"},
			wantErr: ErrEdgeExists,
		},
		{
			name:    "missing from node",
			edge:    &DAGEdge{ID: "e2", FromID: "missing", ToID: "b"},
			wantErr: ErrInvalidEdge,
		},
		{
			name:    "missing to node",
			edge:    &DAGEdge{ID: "e3", FromID: "a", ToID: "missing"},
			wantErr: ErrInvalidEdge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := eng.AddEdge(tt.edge)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr.Error())
				return
			}
			require.NoError(t, err)
			got, ok := eng.GetEdge(tt.edge.ID)
			require.True(t, ok)
			assert.Equal(t, tt.edge.ID, got.ID)
			assert.False(t, got.CreatedAt.IsZero())
		})
	}
}

func TestRemoveNode(t *testing.T) {
	eng := NewEngine()
	require.NoError(t, eng.AddNode(&DAGNode{ID: "a", Name: "A"}))
	require.NoError(t, eng.AddNode(&DAGNode{ID: "b", Name: "B"}))
	require.NoError(t, eng.AddEdge(&DAGEdge{ID: "e1", FromID: "a", ToID: "b"}))

	// Removing node a should also remove edge e1.
	err := eng.RemoveNode("a")
	require.NoError(t, err)
	_, ok := eng.GetNode("a")
	assert.False(t, ok)
	_, ok = eng.GetEdge("e1")
	assert.False(t, ok)
	// Node b should still exist.
	_, ok = eng.GetNode("b")
	assert.True(t, ok)

	// Removing non-existent node.
	err = eng.RemoveNode("missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

func TestUpdateNodeStatus(t *testing.T) {
	tests := []struct {
		name      string
		initial   NodeStatus
		target    NodeStatus
		wantErr   bool
		errTarget error
	}{
		{
			name:    "pending to running",
			initial: StatusPending,
			target:  StatusRunning,
		},
		{
			name:    "running to completed",
			initial: StatusRunning,
			target:  StatusCompleted,
		},
		{
			name:    "running to failed",
			initial: StatusRunning,
			target:  StatusFailed,
		},
		{
			name:    "running to dead",
			initial: StatusRunning,
			target:  StatusDead,
		},
		{
			name:    "dead to resurrecting",
			initial: StatusDead,
			target:  StatusResurrecting,
		},
		{
			name:    "resurrecting to running",
			initial: StatusResurrecting,
			target:  StatusRunning,
		},
		{
			name:      "pending to completed is invalid",
			initial:   StatusPending,
			target:    StatusCompleted,
			wantErr:   true,
			errTarget: errors.New("invalid transition"),
		},
		{
			name:      "completed to running is invalid",
			initial:   StatusCompleted,
			target:    StatusRunning,
			wantErr:   true,
			errTarget: errors.New("no transitions allowed"),
		},
		{
			name:      "dead to completed is invalid",
			initial:   StatusDead,
			target:    StatusCompleted,
			wantErr:   true,
			errTarget: errors.New("invalid transition"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := NewEngine()
			node := &DAGNode{ID: "n1", Name: "test", Status: tt.initial}
			require.NoError(t, eng.AddNode(node))

			err := eng.UpdateNodeStatus("n1", tt.target, "test transition")
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.errTarget.Error())
				return
			}
			require.NoError(t, err)

			got, _ := eng.GetNode("n1")
			assert.Equal(t, tt.target, got.Status)
			assert.Equal(t, "test transition", got.Message)
			assert.Len(t, got.Timeline, 1)
			assert.Equal(t, string(tt.target), got.Timeline[0].Type)
		})
	}

	t.Run("non-existent node", func(t *testing.T) {
		eng := NewEngine()
		err := eng.UpdateNodeStatus("missing", StatusRunning, "msg")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNodeNotFound)
	})
}

func TestSnapshot(t *testing.T) {
	eng := NewEngine()
	require.NoError(t, eng.AddNode(&DAGNode{ID: "n1", Name: "A", Status: StatusRunning}))
	require.NoError(t, eng.AddNode(&DAGNode{ID: "n2", Name: "B", Status: StatusCompleted}))
	require.NoError(t, eng.AddNode(&DAGNode{ID: "n3", Name: "C", Status: StatusFailed}))
	require.NoError(t, eng.AddNode(&DAGNode{ID: "n4", Name: "D", Status: StatusDead}))
	require.NoError(t, eng.AddNode(&DAGNode{ID: "n5", Name: "E", Status: StatusPending}))
	require.NoError(t, eng.AddEdge(&DAGEdge{ID: "e1", FromID: "n1", ToID: "n2"}))

	snap := eng.Snapshot()
	assert.Len(t, snap.Nodes, 5)
	assert.Len(t, snap.Edges, 1)
	assert.Equal(t, 5, snap.Stats.TotalNodes)
	assert.Equal(t, 1, snap.Stats.RunningNodes)
	assert.Equal(t, 1, snap.Stats.CompletedNodes)
	assert.Equal(t, 1, snap.Stats.FailedNodes)
	assert.Equal(t, 1, snap.Stats.DeadNodes)
	assert.Equal(t, 1, snap.Stats.PendingNodes)
	assert.Equal(t, 0, snap.Stats.ResurrectingNodes)
	assert.Equal(t, 1, snap.Stats.TotalEdges)

	// Verify snapshot is a deep copy — mutating it doesn't affect the engine.
	snap.Nodes["n1"].Name = "mutated"
	orig, _ := eng.GetNode("n1")
	assert.Equal(t, "A", orig.Name)
}

func TestChildrenAndParents(t *testing.T) {
	eng := NewEngine()
	require.NoError(t, eng.AddNode(&DAGNode{ID: "root", Name: "Root"}))
	require.NoError(t, eng.AddNode(&DAGNode{ID: "c1", Name: "Child1"}))
	require.NoError(t, eng.AddNode(&DAGNode{ID: "c2", Name: "Child2"}))
	require.NoError(t, eng.AddEdge(&DAGEdge{ID: "e1", FromID: "root", ToID: "c1"}))
	require.NoError(t, eng.AddEdge(&DAGEdge{ID: "e2", FromID: "root", ToID: "c2"}))

	children := eng.Children("root")
	assert.Len(t, children, 2)

	parents := eng.Parents("c1")
	assert.Len(t, parents, 1)
	assert.Equal(t, "root", parents[0].ID)

	noChildren := eng.Children("c1")
	assert.Empty(t, noChildren)

	noParents := eng.Parents("root")
	assert.Empty(t, noParents)
}

func TestHandleEvent_AgentStarted(t *testing.T) {
	eng := NewEngine()
	evt := &ares_events.Event{
		ID:        "evt-1",
		StreamID:  "stream-1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "agent-1", "name": "writer"},
		Timestamp: time.Now(),
	}

	eng.HandleEvent(evt)
	node, ok := eng.GetNode("agent-1")
	require.True(t, ok)
	assert.Equal(t, StatusRunning, node.Status)
	assert.Equal(t, "writer", node.Name)
	assert.Len(t, node.Timeline, 1)
}

func TestHandleEvent_AgentStopped(t *testing.T) {
	eng := NewEngine()
	startEvt := &ares_events.Event{
		ID:        "evt-1",
		StreamID:  "stream-1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "agent-1", "name": "writer"},
		Timestamp: time.Now(),
	}
	eng.HandleEvent(startEvt)

	stopEvt := &ares_events.Event{
		ID:        "evt-2",
		StreamID:  "stream-1",
		Type:      ares_events.EventAgentStopped,
		Payload:   map[string]any{"agent_id": "agent-1"},
		Timestamp: time.Now(),
	}
	eng.HandleEvent(stopEvt)

	node, _ := eng.GetNode("agent-1")
	assert.Equal(t, StatusCompleted, node.Status)
	assert.Len(t, node.Timeline, 2)
}

func TestHandleEvent_Failover(t *testing.T) {
	eng := NewEngine()
	// Create running agent.
	eng.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a1", "name": "agent"},
		Timestamp: time.Now(),
	})
	// Trigger failover.
	eng.HandleEvent(&ares_events.Event{
		ID: "e2", StreamID: "s1", Type: ares_events.EventFailoverTriggered,
		Payload:   map[string]any{"agent_id": "a1"},
		Timestamp: time.Now(),
	})
	node, _ := eng.GetNode("a1")
	assert.Equal(t, StatusDead, node.Status)

	// Complete failover — should go through resurrecting to running.
	eng.HandleEvent(&ares_events.Event{
		ID: "e3", StreamID: "s1", Type: ares_events.EventFailoverCompleted,
		Payload:   map[string]any{"agent_id": "a1"},
		Timestamp: time.Now(),
	})
	node, _ = eng.GetNode("a1")
	assert.Equal(t, StatusRunning, node.Status)
	assert.Len(t, node.Timeline, 4) // started, dead, resurrecting, running
}

func TestHandleEvent_TaskLifecycle(t *testing.T) {
	eng := NewEngine()
	now := time.Now()

	eng.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventTaskCreated,
		Payload:   map[string]any{"task_id": "t1", "name": "build"},
		Timestamp: now,
	})
	node, ok := eng.GetNode("t1")
	require.True(t, ok)
	assert.Equal(t, StatusRunning, node.Status)
	assert.Equal(t, "task", node.Type)

	eng.HandleEvent(&ares_events.Event{
		ID: "e2", StreamID: "s1", Type: ares_events.EventTaskCompleted,
		Payload:   map[string]any{"task_id": "t1"},
		Timestamp: now.Add(time.Second),
	})
	node, _ = eng.GetNode("t1")
	assert.Equal(t, StatusCompleted, node.Status)
}

func TestHandleEvent_TaskFailed(t *testing.T) {
	eng := NewEngine()
	now := time.Now()

	eng.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventTaskCreated,
		Payload:   map[string]any{"task_id": "t1", "name": "deploy"},
		Timestamp: now,
	})
	eng.HandleEvent(&ares_events.Event{
		ID: "e2", StreamID: "s1", Type: ares_events.EventTaskFailed,
		Payload:   map[string]any{"task_id": "t1"},
		Timestamp: now.Add(time.Second),
	})
	node, _ := eng.GetNode("t1")
	assert.Equal(t, StatusFailed, node.Status)
}

func TestHandleEvent_StepFailed(t *testing.T) {
	eng := NewEngine()
	now := time.Now()

	eng.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventTaskCreated,
		Payload:   map[string]any{"task_id": "t1", "name": "compile"},
		Timestamp: now,
	})
	eng.HandleEvent(&ares_events.Event{
		ID: "e2", StreamID: "s1", Type: ares_events.EventStepFailed,
		Payload:   map[string]any{"task_id": "t1"},
		Timestamp: now.Add(time.Second),
	})
	node, _ := eng.GetNode("t1")
	assert.Equal(t, StatusFailed, node.Status)
}

func TestHandleEvent_TimelineOnly(t *testing.T) {
	eng := NewEngine()
	require.NoError(t, eng.AddNode(&DAGNode{ID: "a1", Name: "agent", Status: StatusRunning}))

	eng.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload:   map[string]any{"agent_id": "a1"},
		Timestamp: time.Now(),
	})
	node, _ := eng.GetNode("a1")
	assert.Equal(t, StatusRunning, node.Status)
	assert.Len(t, node.Timeline, 1)
	assert.Equal(t, string(ares_events.EventLLMCall), node.Timeline[0].Type)
}

func TestHandleEvent_NilEvent(t *testing.T) {
	eng := NewEngine()
	// Should not panic.
	eng.HandleEvent(nil)
}

func TestHandleEvent_IgnoresUnknownNode(t *testing.T) {
	eng := NewEngine()
	// Events for unknown nodes should be silently ignored.
	eng.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStopped,
		Payload:   map[string]any{"agent_id": "unknown"},
		Timestamp: time.Now(),
	})
	_, ok := eng.GetNode("unknown")
	assert.False(t, ok)
}

func TestValidateTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    NodeStatus
		to      NodeStatus
		wantErr bool
	}{
		{"pending to running", StatusPending, StatusRunning, false},
		{"running to completed", StatusRunning, StatusCompleted, false},
		{"running to failed", StatusRunning, StatusFailed, false},
		{"running to dead", StatusRunning, StatusDead, false},
		{"dead to resurrecting", StatusDead, StatusResurrecting, false},
		{"resurrecting to running", StatusResurrecting, StatusRunning, false},
		{"pending to completed", StatusPending, StatusCompleted, true},
		{"pending to dead", StatusPending, StatusDead, true},
		{"completed to running", StatusCompleted, StatusRunning, true},
		{"completed to pending", StatusCompleted, StatusPending, true},
		{"failed to running", StatusFailed, StatusRunning, true},
		{"dead to running", StatusDead, StatusRunning, true},
		{"resurrecting to completed", StatusResurrecting, StatusCompleted, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTransition(tt.from, tt.to)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	eng := NewEngine()
	for i := 0; i < 10; i++ {
		require.NoError(t, eng.AddNode(&DAGNode{
			ID:     fmt.Sprintf("n%d", i),
			Name:   fmt.Sprintf("node-%d", i),
			Status: StatusPending,
		}))
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			_ = eng.UpdateNodeStatus(fmt.Sprintf("n%d", i), StatusRunning, "running")
		}
		close(done)
	}()

	go func() {
		for i := 0; i < 10; i++ {
			_ = eng.Snapshot()
		}
	}()

	<-done
	snap := eng.Snapshot()
	assert.Equal(t, 10, snap.Stats.RunningNodes)
}

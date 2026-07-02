package data

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
	"github.com/Timwood0x10/ares/internal/monitoring/eventutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleEvent_NilEvent(t *testing.T) {
	at := NewAgentTracker()
	// Should not panic.
	at.HandleEvent(nil)
	assert.Empty(t, at.ListAgents())
}

func TestHandleEvent_AgentStarted(t *testing.T) {
	tests := []struct {
		name     string
		payload  map[string]any
		wantName string
		wantRole string
	}{
		{
			name: "full payload",
			payload: map[string]any{
				"agent_id":   "a1",
				"name":       "writer",
				"role":       "coder",
				"model_name": "gpt-4",
				"task_id":    "t1",
			},
			wantName: "writer",
			wantRole: "coder",
		},
		{
			name: "minimal payload",
			payload: map[string]any{
				"agent_id": "a2",
			},
			wantName: "",
			wantRole: "",
		},
		{
			name:     "empty payload uses stream ID",
			payload:  map[string]any{},
			wantName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := NewAgentTracker()
			evt := &ares_events.Event{
				ID:        "evt-1",
				StreamID:  "stream-1",
				Type:      ares_events.EventAgentStarted,
				Payload:   tt.payload,
				Timestamp: time.Now(),
			}
			at.HandleEvent(evt)

			// Determine expected agent ID.
			agentID := eventutil.ExtractString(evt, "agent_id")
			if agentID == "" {
				agentID = evt.StreamID
			}

			agent, ok := at.GetAgent(agentID)
			require.True(t, ok)
			assert.Equal(t, dag.StatusRunning, agent.Status)
			assert.Equal(t, tt.wantName, agent.Name)
			assert.Equal(t, tt.wantRole, agent.Role)
		})
	}
}

func TestHandleEvent_StatusTransitions(t *testing.T) {
	tests := []struct {
		name       string
		events     []ares_events.EventType
		wantStatus dag.NodeStatus
	}{
		{
			name:       "started then stopped",
			events:     []ares_events.EventType{ares_events.EventAgentStarted, ares_events.EventAgentStopped},
			wantStatus: dag.StatusCompleted,
		},
		{
			name:       "started then failover triggered",
			events:     []ares_events.EventType{ares_events.EventAgentStarted, ares_events.EventFailoverTriggered},
			wantStatus: dag.StatusDead,
		},
		{
			name:       "started then failover triggered then completed",
			events:     []ares_events.EventType{ares_events.EventAgentStarted, ares_events.EventFailoverTriggered, ares_events.EventFailoverCompleted},
			wantStatus: dag.StatusRunning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := NewAgentTracker()
			for i, evtType := range tt.events {
				evt := &ares_events.Event{
					ID:        fmt.Sprintf("evt-%d", i),
					StreamID:  "s1",
					Type:      evtType,
					Payload:   map[string]any{"agent_id": "a1", "name": "worker"},
					Timestamp: time.Now(),
				}
				at.HandleEvent(evt)
			}

			agent, ok := at.GetAgent("a1")
			require.True(t, ok)
			assert.Equal(t, tt.wantStatus, agent.Status)
		})
	}
}

func TestHandleEvent_CostAccumulation(t *testing.T) {
	at := NewAgentTracker()

	// Create agent first.
	at.HandleEvent(&ares_events.Event{
		ID:        "e0",
		StreamID:  "s1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a1", "name": "worker"},
		Timestamp: time.Now(),
	})

	// Send two LLM call events.
	now := time.Now()
	at.HandleEvent(&ares_events.Event{
		ID:       "e1",
		StreamID: "s1",
		Type:     ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id":       "a1",
			"input_tokens":   float64(100),
			"output_tokens":  float64(50),
			"estimated_cost": 0.005,
			"model_name":     "gpt-4",
		},
		Timestamp: now,
	})
	at.HandleEvent(&ares_events.Event{
		ID:       "e2",
		StreamID: "s1",
		Type:     ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id":       "a1",
			"input_tokens":   float64(200),
			"output_tokens":  float64(100),
			"estimated_cost": 0.010,
			"model_name":     "gpt-4",
		},
		Timestamp: now.Add(time.Second),
	})

	cost, ok := at.GetCost("a1")
	require.True(t, ok)
	assert.Equal(t, int64(300), cost.InputTokens)
	assert.Equal(t, int64(150), cost.OutputTokens)
	assert.Equal(t, int64(450), cost.TotalTokens)
	assert.InDelta(t, 0.015, cost.EstimatedCost, 0.0001)
	assert.Equal(t, 2, cost.CallCount)
	assert.Equal(t, "USD", cost.Currency)

	// Verify model name propagated to agent.
	agent, ok := at.GetAgent("a1")
	require.True(t, ok)
	assert.Equal(t, "gpt-4", agent.ModelName)
}

func TestHandleEvent_CostNoAgent(t *testing.T) {
	at := NewAgentTracker()

	// LLM event without prior agent started — cost should still be tracked.
	at.HandleEvent(&ares_events.Event{
		ID:       "e1",
		StreamID: "s1",
		Type:     ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id":       "orphan",
			"input_tokens":   float64(100),
			"output_tokens":  float64(50),
			"estimated_cost": 0.005,
		},
		Timestamp: time.Now(),
	})

	cost, ok := at.GetCost("orphan")
	require.True(t, ok)
	assert.Equal(t, int64(150), cost.TotalTokens)

	// Agent should not exist.
	_, ok = at.GetAgent("orphan")
	assert.False(t, ok)
}

func TestGetAgent_NotFound(t *testing.T) {
	at := NewAgentTracker()
	_, ok := at.GetAgent("missing")
	assert.False(t, ok)
}

func TestGetCost_NotFound(t *testing.T) {
	at := NewAgentTracker()
	_, ok := at.GetCost("missing")
	assert.False(t, ok)
}

func TestListAgents(t *testing.T) {
	at := NewAgentTracker()
	now := time.Now()

	for i := 0; i < 5; i++ {
		at.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("e%d", i),
			StreamID:  "s1",
			Type:      ares_events.EventAgentStarted,
			Payload:   map[string]any{"agent_id": fmt.Sprintf("a%d", i)},
			Timestamp: now,
		})
	}

	agents := at.ListAgents()
	assert.Len(t, agents, 5)
}

func TestSnapshot(t *testing.T) {
	at := NewAgentTracker()
	now := time.Now()

	// Two running, one completed.
	at.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: now,
	})
	at.HandleEvent(&ares_events.Event{
		ID: "e2", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{"agent_id": "a2"}, Timestamp: now,
	})
	at.HandleEvent(&ares_events.Event{
		ID: "e3", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{"agent_id": "a3"}, Timestamp: now,
	})
	at.HandleEvent(&ares_events.Event{
		ID: "e4", StreamID: "s1", Type: ares_events.EventAgentStopped,
		Payload: map[string]any{"agent_id": "a3"}, Timestamp: now,
	})
	// Add cost.
	at.HandleEvent(&ares_events.Event{
		ID: "e5", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id": "a1", "input_tokens": float64(100),
			"output_tokens": float64(50), "estimated_cost": 0.01,
		}, Timestamp: now,
	})

	stats := at.Snapshot()
	assert.Equal(t, 3, stats.TotalTasks)
	assert.Equal(t, 2, stats.ActiveAgents)
	assert.Equal(t, 2, stats.RunningTasks)
	assert.InDelta(t, 0.01, stats.TotalCost, 0.0001)
}

func TestGetAgent_ReturnsCopy(t *testing.T) {
	at := NewAgentTracker()
	at.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a1", "name": "original"},
		Timestamp: time.Now(),
	})

	agent, _ := at.GetAgent("a1")
	agent.Name = "mutated"

	// Original should be unchanged.
	orig, _ := at.GetAgent("a1")
	assert.Equal(t, "original", orig.Name)
}

func TestConcurrentAccess(t *testing.T) {
	at := NewAgentTracker()
	now := time.Now()

	var wg sync.WaitGroup

	// Writers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agentID := fmt.Sprintf("a%d", id%10)
			at.HandleEvent(&ares_events.Event{
				ID:        fmt.Sprintf("e-%d", id),
				StreamID:  "s1",
				Type:      ares_events.EventAgentStarted,
				Payload:   map[string]any{"agent_id": agentID},
				Timestamp: now,
			})
		}(i)
	}

	// Readers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = at.ListAgents()
			_ = at.Snapshot()
			at.GetAgent(fmt.Sprintf("a%d", id%10))
		}(i)
	}

	wg.Wait()
	agents := at.ListAgents()
	assert.Len(t, agents, 10)
}

func TestHandleEvent_UnknownEventType(t *testing.T) {
	at := NewAgentTracker()
	// Unknown event types should be silently ignored.
	at.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventType("custom.unknown"),
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: time.Now(),
	})
	_, ok := at.GetAgent("a1")
	assert.False(t, ok)
}

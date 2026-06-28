package monitoring

import (
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCostBar(t *testing.T) {
	cb := NewCostBar()
	require.NotNil(t, cb)
	snap := cb.Snapshot()
	assert.InDelta(t, 0, snap.Total, 0.0001)
	assert.Empty(t, snap.Entries)
	assert.Equal(t, "", snap.Currency)
}

func TestCostBar_HandleEvent_NilEvent(t *testing.T) {
	cb := NewCostBar()
	cb.HandleEvent(nil)
	snap := cb.Snapshot()
	assert.InDelta(t, 0, snap.Total, 0.0001)
}

func TestCostBar_HandleEvent_NonLLMEvent(t *testing.T) {
	cb := NewCostBar()
	cb.HandleEvent(&ares_events.Event{
		ID:        "e1",
		StreamID:  "s1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a1"},
		Timestamp: time.Now(),
	})
	snap := cb.Snapshot()
	assert.InDelta(t, 0, snap.Total, 0.0001)
	assert.Empty(t, snap.Entries)
}

func TestCostBar_HandleEvent_SingleAgent(t *testing.T) {
	cb := NewCostBar()
	cb.HandleEvent(&ares_events.Event{
		ID:       "e1",
		StreamID: "s1",
		Type:     ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id":       "a1",
			"input_tokens":   float64(100),
			"output_tokens":  float64(50),
			"estimated_cost": 0.005,
		},
		Timestamp: time.Now(),
	})

	snap := cb.Snapshot()
	assert.InDelta(t, 0.005, snap.Total, 0.0001)
	require.Len(t, snap.Entries, 1)
	assert.Equal(t, "a1", snap.Entries[0].AgentID)
	assert.InDelta(t, 0.005, snap.Entries[0].EstimatedCost, 0.0001)
	assert.Equal(t, 1, snap.Entries[0].CallCount)
}

func TestCostBar_HandleEvent_MultipleAgents(t *testing.T) {
	cb := NewCostBar()
	now := time.Now()

	events := []*ares_events.Event{
		{
			ID: "e1", StreamID: "s1", Type: ares_events.EventLLMCall,
			Payload: map[string]any{
				"agent_id": "a1", "input_tokens": float64(100),
				"output_tokens": float64(50), "estimated_cost": 0.01,
			},
			Timestamp: now,
		},
		{
			ID: "e2", StreamID: "s1", Type: ares_events.EventLLMCall,
			Payload: map[string]any{
				"agent_id": "a2", "input_tokens": float64(200),
				"output_tokens": float64(100), "estimated_cost": 0.02,
			},
			Timestamp: now,
		},
		{
			ID: "e3", StreamID: "s1", Type: ares_events.EventLLMCall,
			Payload: map[string]any{
				"agent_id": "a1", "input_tokens": float64(50),
				"output_tokens": float64(25), "estimated_cost": 0.005,
			},
			Timestamp: now,
		},
	}

	for _, evt := range events {
		cb.HandleEvent(evt)
	}

	snap := cb.Snapshot()
	assert.InDelta(t, 0.035, snap.Total, 0.0001)
	require.Len(t, snap.Entries, 2)

	// Entries should be sorted by cost descending: a2=0.02, a1=0.015.
	assert.Equal(t, "a2", snap.Entries[0].AgentID)
	assert.InDelta(t, 0.02, snap.Entries[0].EstimatedCost, 0.0001)
	assert.Equal(t, "a1", snap.Entries[1].AgentID)
	assert.InDelta(t, 0.015, snap.Entries[1].EstimatedCost, 0.0001)
	assert.Equal(t, 2, snap.Entries[1].CallCount)
}

func TestCostBar_Total(t *testing.T) {
	cb := NewCostBar()
	cb.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id": "a1", "input_tokens": float64(100),
			"output_tokens": float64(50), "estimated_cost": 0.01,
		},
		Timestamp: time.Now(),
	})

	total := cb.Total()
	assert.Equal(t, int64(100), total.InputTokens)
	assert.Equal(t, int64(50), total.OutputTokens)
	assert.Equal(t, int64(150), total.TotalTokens)
	assert.InDelta(t, 0.01, total.EstimatedCost, 0.0001)
	assert.Equal(t, 1, total.CallCount)
}

func TestCostBar_GetCost(t *testing.T) {
	cb := NewCostBar()
	cb.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id": "a1", "input_tokens": float64(100),
			"output_tokens": float64(50), "estimated_cost": 0.01,
		},
		Timestamp: time.Now(),
	})

	cost, ok := cb.GetCost("a1")
	require.True(t, ok)
	assert.Equal(t, "a1", cost.AgentID)
	assert.InDelta(t, 0.01, cost.EstimatedCost, 0.0001)

	_, ok = cb.GetCost("missing")
	assert.False(t, ok)
}

func TestCostBar_DefaultCurrency(t *testing.T) {
	cb := NewCostBar()
	cb.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id": "a1", "estimated_cost": 0.01,
		},
		Timestamp: time.Now(),
	})

	snap := cb.Snapshot()
	assert.Equal(t, "USD", snap.Currency)
}

func TestCostBar_CustomCurrency(t *testing.T) {
	cb := NewCostBar()
	cb.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id": "a1", "estimated_cost": 0.01, "currency": "EUR",
		},
		Timestamp: time.Now(),
	})

	snap := cb.Snapshot()
	assert.Equal(t, "EUR", snap.Currency)
}

func TestCostBar_FallbackToStreamID(t *testing.T) {
	cb := NewCostBar()
	cb.HandleEvent(&ares_events.Event{
		ID: "e1", StreamID: "stream-agent-1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{
			"estimated_cost": 0.01,
		},
		Timestamp: time.Now(),
	})

	snap := cb.Snapshot()
	require.Len(t, snap.Entries, 1)
	assert.Equal(t, "stream-agent-1", snap.Entries[0].AgentID)
}

func TestCostBar_ConcurrentAccess(t *testing.T) {
	cb := NewCostBar()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cb.HandleEvent(&ares_events.Event{
				ID:        "e",
				StreamID:  "s1",
				Type:      ares_events.EventLLMCall,
				Payload:   map[string]any{"agent_id": "a1", "estimated_cost": 0.001},
				Timestamp: time.Now(),
			})
		}(i)
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cb.Snapshot()
			_ = cb.Total()
			cb.GetCost("a1")
		}()
	}

	wg.Wait()
	snap := cb.Snapshot()
	assert.InDelta(t, 0.1, snap.Total, 0.001)
}

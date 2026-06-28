package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
)

func TestLLMTab_Interface(t *testing.T) {
	var tab Tab = NewLLMTab()
	assert.Equal(t, "llm", tab.Name())
	assert.Equal(t, "LLM", tab.Label())
}

func TestLLMTab_HandleLLMCall(t *testing.T) {
	tab := NewLLMTab()
	tab.HandleEvent(&ares_events.Event{
		ID:   "llm1",
		Type: ares_events.EventLLMCall,
		Payload: map[string]any{
			"agent_id":      "a1",
			"model":         "gpt-4",
			"input_tokens":  float64(1000),
			"output_tokens": float64(500),
			"cost":          float64(0.03),
			"duration":      float64(2000000000), // 2s in ns
		},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(LLMTabSnapshot)
	assert.Len(t, snap.Calls, 1)
	assert.Equal(t, "gpt-4", snap.Calls[0].ModelName)
	assert.Equal(t, int64(1000), snap.Calls[0].InputTokens)
	assert.Equal(t, int64(500), snap.Calls[0].OutputTokens)
}

func TestLLMTab_Stats(t *testing.T) {
	tab := NewLLMTab()
	events := []*ares_events.Event{
		{
			ID:   "llm1",
			Type: ares_events.EventLLMCall,
			Payload: map[string]any{
				"input_tokens":  float64(1000),
				"output_tokens": float64(500),
				"cost":          float64(0.03),
			},
			Timestamp: time.Now(),
		},
		{
			ID:   "llm2",
			Type: ares_events.EventLLMCall,
			Payload: map[string]any{
				"input_tokens":  float64(2000),
				"output_tokens": float64(1000),
				"cost":          float64(0.06),
			},
			Timestamp: time.Now(),
		},
	}
	for _, evt := range events {
		tab.HandleEvent(evt)
	}

	stats := tab.Stats()
	assert.Equal(t, 2, stats.TotalCalls)
	assert.Equal(t, int64(3000), stats.TotalInputTokens)
	assert.Equal(t, int64(1500), stats.TotalOutputTokens)
	assert.Equal(t, int64(4500), stats.TotalTokens)
	assert.InDelta(t, 0.09, stats.TotalCost, 0.0001)
	assert.InDelta(t, float64(1500), stats.AvgInputTokens, 0.0001)
	assert.InDelta(t, float64(750), stats.AvgOutputTokens, 0.0001)
}

func TestLLMTab_IgnoresNonLLMEvents(t *testing.T) {
	tab := NewLLMTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(LLMTabSnapshot)
	assert.Empty(t, snap.Calls)
}

func TestLLMTab_NilEvent(t *testing.T) {
	tab := NewLLMTab()
	tab.HandleEvent(nil)
}

func TestLLMTab_Capacity(t *testing.T) {
	tab := NewLLMTab()
	for i := 0; i < maxLLMCalls+10; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("llm%d", i),
			Type:      ares_events.EventLLMCall,
			Payload:   map[string]any{"input_tokens": float64(100)},
			Timestamp: time.Now(),
		})
	}
	snap := tab.Snapshot().(LLMTabSnapshot)
	assert.Equal(t, maxLLMCalls, len(snap.Calls))
}

func TestLLMTab_EmptyStats(t *testing.T) {
	tab := NewLLMTab()
	stats := tab.Stats()
	assert.Equal(t, 0, stats.TotalCalls)
	assert.InDelta(t, float64(0), stats.AvgInputTokens, 0.0001)
}

func TestLLMTab_MissingPayload(t *testing.T) {
	tab := NewLLMTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "llm1",
		Type:      ares_events.EventLLMCall,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(LLMTabSnapshot)
	assert.Len(t, snap.Calls, 1)
	assert.Equal(t, int64(0), snap.Calls[0].InputTokens)
}

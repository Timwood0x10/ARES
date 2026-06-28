package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

func TestLLMTab_Interface(t *testing.T) {
	var tab Tab = NewLLMTab()
	if tab.Name() != "llm" {
		t.Errorf("Name() = %q, want %q", tab.Name(), "llm")
	}
	if tab.Label() != "LLM" {
		t.Errorf("Label() = %q, want %q", tab.Label(), "LLM")
	}
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
	if len(snap.Calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(snap.Calls))
	}
	if snap.Calls[0].ModelName != "gpt-4" {
		t.Errorf("ModelName = %q, want %q", snap.Calls[0].ModelName, "gpt-4")
	}
	if snap.Calls[0].InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", snap.Calls[0].InputTokens)
	}
	if snap.Calls[0].OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", snap.Calls[0].OutputTokens)
	}
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
	if stats.TotalCalls != 2 {
		t.Errorf("TotalCalls = %d, want 2", stats.TotalCalls)
	}
	if stats.TotalInputTokens != 3000 {
		t.Errorf("TotalInputTokens = %d, want 3000", stats.TotalInputTokens)
	}
	if stats.TotalOutputTokens != 1500 {
		t.Errorf("TotalOutputTokens = %d, want 1500", stats.TotalOutputTokens)
	}
	if stats.TotalTokens != 4500 {
		t.Errorf("TotalTokens = %d, want 4500", stats.TotalTokens)
	}
	if stats.TotalCost != 0.09 {
		t.Errorf("TotalCost = %f, want 0.09", stats.TotalCost)
	}
	if stats.AvgInputTokens != 1500 {
		t.Errorf("AvgInputTokens = %f, want 1500", stats.AvgInputTokens)
	}
	if stats.AvgOutputTokens != 750 {
		t.Errorf("AvgOutputTokens = %f, want 750", stats.AvgOutputTokens)
	}
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
	if len(snap.Calls) != 0 {
		t.Error("non-LLM events should be ignored")
	}
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
	if len(snap.Calls) != maxLLMCalls {
		t.Errorf("call count = %d, want %d", len(snap.Calls), maxLLMCalls)
	}
}

func TestLLMTab_EmptyStats(t *testing.T) {
	tab := NewLLMTab()
	stats := tab.Stats()
	if stats.TotalCalls != 0 {
		t.Errorf("TotalCalls = %d, want 0", stats.TotalCalls)
	}
	if stats.AvgInputTokens != 0 {
		t.Errorf("AvgInputTokens = %f, want 0", stats.AvgInputTokens)
	}
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
	if len(snap.Calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(snap.Calls))
	}
	// Should default to zero values, not panic.
	if snap.Calls[0].InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", snap.Calls[0].InputTokens)
	}
}

package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

func TestEvolutionTab_Interface(t *testing.T) {
	var tab Tab = NewEvolutionTab()
	if tab.Name() != "evolution" {
		t.Errorf("Name() = %q, want %q", tab.Name(), "evolution")
	}
	if tab.Label() != "Evolution" {
		t.Errorf("Label() = %q, want %q", tab.Label(), "Evolution")
	}
}

func TestEvolutionTab_HandleAgentStarted(t *testing.T) {
	tab := NewEvolutionTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "g1",
		Type:      ares_events.EventAgentStarted,
		Payload:   map[string]any{"agent_id": "a1", "generation": float64(3), "parent_id": "a0"},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(EvolutionTabSnapshot)
	if len(snap.Genomes) != 1 {
		t.Fatalf("got %d genomes, want 1", len(snap.Genomes))
	}
	if snap.Genomes[0].AgentID != "a1" {
		t.Errorf("AgentID = %q, want %q", snap.Genomes[0].AgentID, "a1")
	}
	if snap.Genomes[0].Generation != 3 {
		t.Errorf("Generation = %d, want 3", snap.Genomes[0].Generation)
	}
}

func TestEvolutionTab_HandleMutation(t *testing.T) {
	tab := NewEvolutionTab()
	tab.HandleEvent(&ares_events.Event{
		ID:   "m1",
		Type: eventEvolutionMutated,
		Payload: map[string]any{
			"agent_id":    "a1",
			"description": "optimized prompt",
			"before":      "old prompt",
			"after":       "new prompt",
		},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(EvolutionTabSnapshot)
	if len(snap.Mutations) != 1 {
		t.Fatalf("got %d mutations, want 1", len(snap.Mutations))
	}
	if snap.Mutations[0].Description != "optimized prompt" {
		t.Errorf("Description = %q, want %q", snap.Mutations[0].Description, "optimized prompt")
	}
}

func TestEvolutionTab_HandleRecommendation(t *testing.T) {
	tab := NewEvolutionTab()
	tab.HandleEvent(&ares_events.Event{
		ID:   "r1",
		Type: eventEvolutionRecommended,
		Payload: map[string]any{
			"agent_id": "a1",
			"category": "performance",
			"text":     "increase batch size",
			"priority": "high",
		},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(EvolutionTabSnapshot)
	if len(snap.Recommendations) != 1 {
		t.Fatalf("got %d recommendations, want 1", len(snap.Recommendations))
	}
	if snap.Recommendations[0].Priority != "high" {
		t.Errorf("Priority = %q, want %q", snap.Recommendations[0].Priority, "high")
	}
}

func TestEvolutionTab_IgnoresIrrelevantEvents(t *testing.T) {
	tab := NewEvolutionTab()
	tab.HandleEvent(&ares_events.Event{
		ID:        "1",
		Type:      ares_events.EventLLMCall,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	})
	snap := tab.Snapshot().(EvolutionTabSnapshot)
	if len(snap.Genomes) != 0 || len(snap.Mutations) != 0 || len(snap.Recommendations) != 0 {
		t.Error("non-evolution events should be ignored")
	}
}

func TestEvolutionTab_NilEvent(t *testing.T) {
	tab := NewEvolutionTab()
	tab.HandleEvent(nil)
}

func TestEvolutionTab_Capacity(t *testing.T) {
	tab := NewEvolutionTab()
	for i := 0; i < maxMutations+10; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID:        fmt.Sprintf("m%d", i),
			Type:      eventEvolutionMutated,
			Payload:   map[string]any{"agent_id": "a1"},
			Timestamp: time.Now(),
		})
	}
	snap := tab.Snapshot().(EvolutionTabSnapshot)
	if len(snap.Mutations) != maxMutations {
		t.Errorf("mutation count = %d, want %d", len(snap.Mutations), maxMutations)
	}
}

func TestEvolutionTab_GenomeUsesModuleName(t *testing.T) {
	tab := NewEvolutionTab()
	tab.HandleEvent(&ares_events.Event{
		ID:         "g1",
		Type:       ares_events.EventAgentStarted,
		ModuleName: "fallback-agent",
		Payload:    map[string]any{},
		Timestamp:  time.Now(),
	})
	snap := tab.Snapshot().(EvolutionTabSnapshot)
	if snap.Genomes[0].AgentID != "fallback-agent" {
		t.Errorf("AgentID = %q, want %q", snap.Genomes[0].AgentID, "fallback-agent")
	}
}

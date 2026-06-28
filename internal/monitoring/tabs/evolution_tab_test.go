package tabs

import (
	"fmt"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
)

func TestEvolutionTab_Interface(t *testing.T) {
	var tab Tab = NewEvolutionTab()
	assert.Equal(t, "evolution", tab.Name())
	assert.Equal(t, "Evolution", tab.Label())
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
	assert.Len(t, snap.Genomes, 1)
	assert.Equal(t, "a1", snap.Genomes[0].AgentID)
	assert.Equal(t, 3, snap.Genomes[0].Generation)
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
	assert.Len(t, snap.Mutations, 1)
	assert.Equal(t, "optimized prompt", snap.Mutations[0].Description)
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
	assert.Len(t, snap.Recommendations, 1)
	assert.Equal(t, "high", snap.Recommendations[0].Priority)
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
	assert.Empty(t, snap.Genomes)
	assert.Empty(t, snap.Mutations)
	assert.Empty(t, snap.Recommendations)
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
	assert.Equal(t, maxMutations, len(snap.Mutations))
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
	assert.Equal(t, "fallback-agent", snap.Genomes[0].AgentID)
}

func TestEvolutionTab_Trim(t *testing.T) {
	tab := NewEvolutionTab()
	for i := 0; i < 10; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID: fmt.Sprintf("g%d", i), Type: ares_events.EventAgentStarted,
			Payload: map[string]any{"agent_id": fmt.Sprintf("a%d", i)}, Timestamp: time.Now(),
		})
	}
	for i := 0; i < 12; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID: fmt.Sprintf("m%d", i), Type: eventEvolutionMutated,
			Payload: map[string]any{"agent_id": "a1"}, Timestamp: time.Now(),
		})
	}
	for i := 0; i < 8; i++ {
		tab.HandleEvent(&ares_events.Event{
			ID: fmt.Sprintf("r%d", i), Type: eventEvolutionRecommended,
			Payload: map[string]any{"agent_id": "a1"}, Timestamp: time.Now(),
		})
	}

	tab.Trim(5)
	assert.Equal(t, 5, len(tab.genomes))
	assert.Equal(t, 5, len(tab.mutations))
	assert.Equal(t, 5, len(tab.recommendations))
}

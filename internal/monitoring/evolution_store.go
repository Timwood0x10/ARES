// Package monitoring — evolution store bridge.
//
// EvolutionStore reads agent genealogy data from the flight recorder
// and serves it through the ConsoleAPI's AgentEvolution endpoint.
// This bridges the existing flight genealogy tracking with the console
// display, with no additional storage backend required.
package monitoring

import (
	"sync"
	"time"

	flight "github.com/Timwood0x10/ares/internal/ares_flight"
)

// EvolutionStore bridges flight genealogy data into ConsoleAPI's AgentEvolution.
type EvolutionStore struct {
	mu    sync.RWMutex
	store map[string]*agentEvo
}

type agentEvo struct {
	agentID    string
	mutations  []MutationRecord
	generation int
	parentID   string
	startedAt  time.Time
}

// NewEvolutionStore creates an empty evolution store.
func NewEvolutionStore() *EvolutionStore {
	return &EvolutionStore{
		store: make(map[string]*agentEvo),
	}
}

// RecordSpawn records an agent spawn from flight genealogy data.
func (s *EvolutionStore) RecordSpawn(agentID, parentID, agentType string, spawnedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	evo, ok := s.store[agentID]
	if !ok {
		evo = &agentEvo{
			agentID:   agentID,
			mutations: make([]MutationRecord, 0),
			startedAt: spawnedAt,
		}
		s.store[agentID] = evo
	}

	evo.parentID = parentID

	if parentID != "" {
		if parent, ok := s.store[parentID]; ok {
			evo.generation = parent.generation + 1
		}
	}

	desc := "agent spawned"
	if agentType != "" {
		desc = agentType + " spawned"
	}

	evo.mutations = append(evo.mutations, MutationRecord{
		ID:          "spawn-" + agentID,
		AgentID:     agentID,
		Description: desc,
		Timestamp:   spawnedAt,
	})
}

// RecordDeath records an agent death.
func (s *EvolutionStore) RecordDeath(agentID string, diedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	evo, ok := s.store[agentID]
	if !ok {
		return
	}

	evo.mutations = append(evo.mutations, MutationRecord{
		ID:          "death-" + agentID + "-" + diedAt.Format("150405"),
		AgentID:     agentID,
		Description: "agent died",
		After:       "dead",
		Before:      "alive",
		Timestamp:   diedAt,
	})
}

// RecordResurrection records an agent resurrection.
func (s *EvolutionStore) RecordResurrection(oldID, newID string, resurrectedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Carry forward parent mutation history from old agent.
	if old, ok := s.store[oldID]; ok {
		evo := &agentEvo{
			agentID:    newID,
			mutations:  make([]MutationRecord, len(old.mutations)),
			generation: old.generation + 1,
			parentID:   oldID,
			startedAt:  resurrectedAt,
		}
		copy(evo.mutations, old.mutations)
		evo.mutations = append(evo.mutations, MutationRecord{
			ID:          "resurrect-" + newID,
			AgentID:     newID,
			Description: "resurrected from " + oldID,
			Before:      oldID,
			After:       newID,
			Timestamp:   resurrectedAt,
		})
		s.store[newID] = evo
	}
}

// GetEvolution returns the evolution history for an agent.
func (s *EvolutionStore) GetEvolution(agentID string) *AgentEvolution {
	s.mu.RLock()
	defer s.mu.RUnlock()

	evo, ok := s.store[agentID]
	if !ok {
		return &AgentEvolution{
			AgentID:    agentID,
			Mutations:  []MutationRecord{},
			Generation: 0,
		}
	}

	muts := make([]MutationRecord, len(evo.mutations))
	copy(muts, evo.mutations)

	return &AgentEvolution{
		AgentID:    agentID,
		Mutations:  muts,
		Generation: evo.generation,
		ParentID:   evo.parentID,
		StartedAt:  evo.startedAt,
	}
}

// LoadFromGenealogy populates the store from a flight genealogy.
func (s *EvolutionStore) LoadFromGenealogy(g *flight.Genealogy) {
	if g == nil {
		return
	}

	roots := g.Roots()
	var walk func(nodes []*flight.LineageNode)
	walk = func(nodes []*flight.LineageNode) {
		for _, n := range nodes {
			s.RecordSpawn(n.ID, n.ParentID, n.Type, n.SpawnedAt)
			if !n.IsAlive {
				s.RecordDeath(n.ID, n.DiedAt)
			}
			if len(n.Children) > 0 {
				walk(n.Children)
			}
		}
	}
	walk(roots)
}

// SeedFromFlight populates the store from a flight recorder for testing.
func (s *EvolutionStore) SeedFromFlight(fr *flight.FlightRecorder) {
	if fr == nil {
		return
	}
	frGenealogy := fr.Genealogy()
	if frGenealogy == nil {
		return
	}
	s.LoadFromGenealogy(frGenealogy)
}

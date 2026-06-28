package tabs

import (
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring"
)

const (
	maxGenomes         = 100
	maxMutations       = 500
	maxRecommendations = 200
)

// EvolutionTabSnapshot is the snapshot payload returned by EvolutionTab.Snapshot.
type EvolutionTabSnapshot struct {
	Genomes         []monitoring.AgentEvolution `json:"genomes"`
	Mutations       []monitoring.MutationRecord `json:"mutations"`
	Recommendations []monitoring.Recommendation `json:"recommendations"`
}

// EvolutionTab implements the Tab interface for the Evolution tab.
// It tracks agent genomes, mutations, and recommendations.
type EvolutionTab struct {
	mu              sync.RWMutex
	genomes         []monitoring.AgentEvolution
	mutations       []monitoring.MutationRecord
	recommendations []monitoring.Recommendation
}

// NewEvolutionTab creates a new EvolutionTab instance.
func NewEvolutionTab() *EvolutionTab {
	return &EvolutionTab{
		genomes:         make([]monitoring.AgentEvolution, 0, maxGenomes),
		mutations:       make([]monitoring.MutationRecord, 0, maxMutations),
		recommendations: make([]monitoring.Recommendation, 0, maxRecommendations),
	}
}

// Name returns the tab identifier.
func (t *EvolutionTab) Name() string { return "evolution" }

// Label returns the human-readable tab name.
func (t *EvolutionTab) Label() string { return "Evolution" }

// HandleEvent processes evolution-related events.
func (t *EvolutionTab) HandleEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}
	switch evt.Type {
	case ares_events.EventAgentStarted:
		t.handleGenome(evt)
	case eventEvolutionMutated:
		t.handleMutation(evt)
	case eventEvolutionRecommended:
		t.handleRecommendation(evt)
	}
}

// Snapshot returns the current evolution state.
func (t *EvolutionTab) Snapshot() any {
	t.mu.RLock()
	defer t.mu.RUnlock()

	genomes := make([]monitoring.AgentEvolution, len(t.genomes))
	copy(genomes, t.genomes)
	mutations := make([]monitoring.MutationRecord, len(t.mutations))
	copy(mutations, t.mutations)
	recs := make([]monitoring.Recommendation, len(t.recommendations))
	copy(recs, t.recommendations)

	return EvolutionTabSnapshot{
		Genomes:         genomes,
		Mutations:       mutations,
		Recommendations: recs,
	}
}

func (t *EvolutionTab) handleGenome(evt *ares_events.Event) {
	genome := monitoring.AgentEvolution{
		AgentID:    getString(evt.Payload, "agent_id"),
		Generation: int(getInt64(evt.Payload, "generation")),
		ParentID:   getString(evt.Payload, "parent_id"),
		StartedAt:  evt.Timestamp,
	}
	if genome.AgentID == "" {
		genome.AgentID = evt.ModuleName
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.genomes) >= maxGenomes {
		t.genomes = t.genomes[1:]
	}
	t.genomes = append(t.genomes, genome)
}

func (t *EvolutionTab) handleMutation(evt *ares_events.Event) {
	mutation := monitoring.MutationRecord{
		ID:          evt.ID,
		AgentID:     getString(evt.Payload, "agent_id"),
		Description: getString(evt.Payload, "description"),
		Before:      getString(evt.Payload, "before"),
		After:       getString(evt.Payload, "after"),
		Timestamp:   evt.Timestamp,
	}
	if mutation.AgentID == "" {
		mutation.AgentID = evt.ModuleName
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.mutations) >= maxMutations {
		t.mutations = t.mutations[1:]
	}
	t.mutations = append(t.mutations, mutation)
}

func (t *EvolutionTab) handleRecommendation(evt *ares_events.Event) {
	rec := monitoring.Recommendation{
		ID:       evt.ID,
		AgentID:  getString(evt.Payload, "agent_id"),
		Category: getString(evt.Payload, "category"),
		Text:     getString(evt.Payload, "text"),
		Priority: getString(evt.Payload, "priority"),
	}
	if rec.AgentID == "" {
		rec.AgentID = evt.ModuleName
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.recommendations) >= maxRecommendations {
		t.recommendations = t.recommendations[1:]
	}
	t.recommendations = append(t.recommendations, rec)
}

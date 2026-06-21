package evolution

import (
	"encoding/json"
	"fmt"
	"os"

	"goagentx/internal/evolution/mutation"
)

// EvolutionRunSnapshot captures the complete state of an evolution run
// for serialization and restoration. It includes configuration, all agents,
// generation counter, and full genealogy.
type EvolutionRunSnapshot struct {
	// Config is the system configuration used for this evolution run.
	Config SystemConfig `json:"config"`

	// Generation is the current generation number.
	Generation int `json:"generation"`

	// Agents holds all strategies in the population (deep-copied).
	Agents []*mutation.Strategy `json:"agents"`

	// Lineages holds all recorded genealogy entries.
	Lineages []StrategyLineage `json:"lineages,omitempty"`
}

// SaveEvolutionRun persists the complete state of a wired evolution system
// to a JSON file. The snapshot includes configuration, all agents, generation
// counter, and genealogy records — sufficient to fully restore and continue
// an evolution run.
//
// Args:
//
//	filepath - the output file path (existing file will be overwritten).
//	system - the wired evolution system to snapshot.
//
// Returns:
//
//	error - non-nil if the system is nil, file creation fails, or JSON marshal fails.
func SaveEvolutionRun(filepath string, system *WiredEvolutionSystem) error {
	if system == nil {
		return fmt.Errorf("system must not be nil")
	}
	if system.Population == nil {
		return fmt.Errorf("system population must not be nil")
	}

	agents, generation := system.Population.Snapshot()

	var lineages []StrategyLineage
	if system.Genealogy != nil {
		lineages = system.Genealogy.Lineages()
	}

	snapshot := &EvolutionRunSnapshot{
		Config:     system.config,
		Generation: generation,
		Agents:     agents,
		Lineages:   lineages,
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal evolution snapshot: %w", err)
	}

	if err := os.WriteFile(filepath, data, 0644); err != nil {
		return fmt.Errorf("write evolution snapshot: %w", err)
	}

	return nil
}

// LoadEvolutionRun reads a previously saved evolution run snapshot from a JSON file.
// Returns the deserialized snapshot, which can be used to inspect or reconstruct
// the evolution state.
//
// Args:
//
//	filepath - the input file path.
//
// Returns:
//
//	*EvolutionRunSnapshot - the deserialized snapshot.
//	error - non-nil if the file cannot be read or JSON unmarshal fails.
func LoadEvolutionRun(filepath string) (*EvolutionRunSnapshot, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("read evolution snapshot: %w", err)
	}

	snapshot := &EvolutionRunSnapshot{}
	if err := json.Unmarshal(data, snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal evolution snapshot: %w", err)
	}

	if snapshot.Agents == nil {
		snapshot.Agents = []*mutation.Strategy{}
	}
	if snapshot.Lineages == nil {
		snapshot.Lineages = []StrategyLineage{}
	}

	// Basic consistency validation.
	if err := snapshot.Validate(); err != nil {
		return nil, fmt.Errorf("validate evolution snapshot: %w", err)
	}

	return snapshot, nil
}

// Validate checks the internal consistency of a loaded snapshot.
// It verifies that generation count is non-negative and agents are not empty
// for non-zero generations.
func (s *EvolutionRunSnapshot) Validate() error {
	if s.Generation < 0 {
		return fmt.Errorf("invalid generation: %d (must be >= 0)", s.Generation)
	}
	if s.Generation > 0 && len(s.Agents) == 0 {
		return fmt.Errorf("generation %d has zero agents", s.Generation)
	}
	return nil
}

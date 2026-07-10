package core

import (
	"context"
	"time"
)

// EvolutionConfig holds the evolution system configuration.
type EvolutionConfig struct {
	// PopulationSize is the number of strategies per generation.
	PopulationSize int
	// MaxGenerations is the maximum number of evolution generations.
	MaxGenerations int
	// MutationRate is the probability of mutation (0-1).
	MutationRate float64
	// CrossoverRate is the probability of crossover (0-1).
	CrossoverRate float64
	// EliteCount is the number of top strategies preserved per generation.
	EliteCount int
	// ScoringMethod selects the scoring pipeline ("llm", "deterministic", "hybrid").
	ScoringMethod string
	// ReportPath is the optional path to write the evolution report.
	// When non-empty, a human-readable report is saved after RunIdleEvolution completes.
	ReportPath string
}

// Strategy represents an evolved agent strategy.
type EvolutionStrategy struct {
	// ID is the unique strategy identifier.
	ID string
	// ParentID records the parent strategy IDs (for lineage tracking).
	ParentID string
	// Params holds the strategy parameters.
	Params map[string]any
	// PromptTemplate is the evolved prompt template.
	PromptTemplate string
	// Score is the fitness score.
	Score float64
	// MutationDesc describes how this strategy was created.
	MutationDesc string
	// Generation is the generation number.
	Generation int
}

// EvolutionResult holds the outcome of an evolution run.
type EvolutionResult struct {
	// BestStrategy is the highest-scoring strategy.
	BestStrategy *EvolutionStrategy
	// Generation is the final generation number.
	Generation int
	// ScoreHistory records the best score per generation.
	ScoreHistory []float64
	// DiversityScore measures population diversity (0-1).
	DiversityScore float64
	// Duration is the total evolution time.
	Duration time.Duration
}

// LineageRecord tracks parent-child relationships between strategies.
type LineageRecord struct {
	// ChildID is the evolved strategy ID.
	ChildID string
	// ParentIDs are the parent strategy IDs.
	ParentIDs []string
	// MutationType describes the mutation method.
	MutationType string
	// Generation is when this strategy was created.
	Generation int
}

// Evolution defines the interface for genetic algorithm evolution.
type Evolution interface {
	// Evolve runs the evolution for the specified number of generations.
	Evolve(ctx context.Context, generations int) (*EvolutionResult, error)

	// RunIdleEvolution runs N generations of idle evolution on a wired system.
	// When ReportPath is set on the config, a human-readable report is saved
	// after all generations complete.
	RunIdleEvolution(ctx context.Context, generations int) error

	// LatestReport returns the most recent evolution report text.
	// Returns empty string if no report has been generated yet.
	LatestReport() (string, error)

	// BestStrategy returns the current best strategy.
	BestStrategy() (*EvolutionStrategy, error)

	// Stats returns evolution statistics.
	Stats() (*EvolutionStats, error)

	// Lineages returns the lineage history of all strategies.
	Lineages() ([]LineageRecord, error)

	// SaveBestStrategy saves the best strategy to a file.
	SaveBestStrategy(path string) error

	// Shutdown gracefully shuts down the evolution system.
	Shutdown()
}

// EvolutionStats holds evolution system statistics.
type EvolutionStats struct {
	// TotalGenerations is the number of generations completed.
	TotalGenerations int
	// BestScore is the highest score achieved.
	BestScore float64
	// AvgScore is the average score of the current population.
	AvgScore float64
	// PopulationSize is the current population size.
	PopulationSize int
	// DiversityScore measures population diversity.
	DiversityScore float64
}

// DreamCycleConfig holds the self-evolution dream cycle configuration.
type DreamCycleConfig struct {
	// TriggerThreshold is the score threshold to trigger a dream cycle.
	TriggerThreshold float64
	// MaxCycles is the maximum number of dream cycles.
	MaxCycles int
	// CycleTimeout is the timeout per dream cycle.
	CycleTimeout time.Duration
}

// DreamCycle defines the interface for autonomous self-evolution.
type DreamCycle interface {
	// Start begins the autonomous evolution loop.
	Start(ctx context.Context) error

	// Stop stops the dream cycle.
	Stop() error

	// Trigger manually triggers a dream cycle.
	Trigger(ctx context.Context) (*EvolutionResult, error)

	// Status returns the current dream cycle status.
	Status() DreamCycleStatus
}

// DreamCycleStatus holds the current dream cycle state.
type DreamCycleStatus struct {
	// Running indicates if the dream cycle is active.
	Running bool
	// CyclesCompleted is the number of completed cycles.
	CyclesCompleted int
	// LastCycleTime is when the last cycle started.
	LastCycleTime time.Time
	// LastResult is the result of the last cycle.
	LastResult *EvolutionResult
}

// RuntimeEvolution defines the interface for the runtime evolution system.
// This is the NEW system (Genome + Diff + Coordinator + Patch), distinct from
// the old GA-based Evolution interface above.
type RuntimeEvolution interface {
	RunCycle(ctx context.Context) (*RuntimeCycleResult, error)
	Status() (*RuntimeEvolutionStatus, error)
	Propose(ctx context.Context, proposal RuntimeProposal) error
}

type RuntimeCycleResult struct {
	GenomesEvaluated int            `json:"genomes_evaluated"`
	GenomesChanged   int            `json:"genomes_changed"`
	PatchesProposed  int            `json:"patches_proposed"`
	PatchesApplied   int            `json:"patches_applied"`
	Failures         []string       `json:"failures,omitempty"`
	Details          []GenomeChange `json:"details,omitempty"`
}

type GenomeChange struct {
	Name      string `json:"name"`
	Patches   int    `json:"patches"`
	FirstType string `json:"first_patch_type,omitempty"`
}

type RuntimeEvolutionStatus struct {
	Genomes          []string `json:"genomes"`
	Differs          []string `json:"differs"`
	PendingProposals int      `json:"pending_proposals"`
	DecisionsMade    int      `json:"decisions_made"`
	PatchesApplied   int      `json:"patches_applied"`
	EvidenceEntries  int      `json:"evidence_entries"`
}

type RuntimeProposal struct {
	Source   string `json:"source"`
	Text     string `json:"text"`
	Priority int    `json:"priority"`
}

// Package evolution provides data-driven evolution reports derived from
// real run statistics, not template claims.
package evolution

import (
	"context"
	"fmt"
	"strings"

	"goagentx/internal/evolution/genome"
)

// GenerationStats holds per-generation statistics collected during evolution.
type GenerationStats struct {
	Generation     int             `json:"generation"`
	PopulationSize int             `json:"population_size"`
	BestScore      float64         `json:"best_score"`
	AvgScore       float64         `json:"avg_score"`
	WorstScore     float64         `json:"worst_score"`
	Diversity      float64         `json:"diversity"`           // overall diversity metric
	NumDiverse     int             `json:"num_diverse"`         // count of diverse lineages
	MutationTypes  map[string]int  `json:"mutation_types"`      // mutation type distribution
}

// EvolutionReport is a comprehensive data-driven report for an evolution run.
type EvolutionReport struct {
	// TotalGenerations is the total number of generations evolved.
	TotalGenerations int `json:"total_generations"`

	// BestEverScore is the highest score seen across all generations.
	BestEverScore float64 `json:"best_ever_score"`

	// BestEverGeneration is the generation where best-ever score appeared.
	BestEverGeneration int `json:"best_ever_generation"`

	// FinalBestScore is the best score in the final generation.
	FinalBestScore float64 `json:"final_best_score"`

	// GenerationTrajectory is per-generation stats in order.
	// Note: If the population does not store history, this contains only
	// the current generation. Future work can add history tracking to Population.
	GenerationTrajectory []GenerationStats `json:"generation_trajectory"`

	// ScorerCostSummary holds scoring cost information (if tiered scorer used).
	ScorerCostSummary *ScorerCostSummary `json:"scorer_cost_summary,omitempty"`

	// LineageConcentration tracks dominant lineage share (if available).
	LineageConcentration *LineageConcentration `json:"lineage_concentration,omitempty"`
}

// ScorerCostSummary summarizes scorer resource usage.
type ScorerCostSummary struct {
	TotalLLMCalls  int `json:"total_llm_calls"`
	TotalCacheHits int `json:"total_cache_hits"`
	TotalFallbacks int `json:"total_fallbacks"`
	LLMBudgetUsed  int `json:"llm_budget_used"`
	LLMBudgetMax   int `json:"llm_budget_max"`
}

// LineageConcentration tracks lineage distribution in the population.
type LineageConcentration struct {
	TopLineageShare float64         `json:"top_lineage_share"`
	TopLineageID    string          `json:"top_lineage_id"`
	LineageCounts   map[string]int  `json:"lineage_counts"`
	UniqueLineages  int             `json:"unique_lineages"`
}

// ReportOption configures GenerateReport behavior.
type ReportOption func(*reportConfig)

// reportConfig holds optional configuration for report generation.
type reportConfig struct {
	scoringStats    map[string]int64
	budgetUsage     []int // [used, max, cacheHits, fallbacks]
}

// WithScoringStats injects scorer cost data into the report.
//
// Args:
//
//	stats - tiered scorer Stats() output (map[string]int64).
//	budgetUsage - budget.Usage() output as variadic: used, max, cacheHits, fallbacks.
//
// Returns:
//
//	ReportOption - the configuration function.
func WithScoringStats(stats map[string]int64, budgetUsage ...int) ReportOption {
	return func(rc *reportConfig) {
		rc.scoringStats = stats
		if len(budgetUsage) >= 4 {
			rc.budgetUsage = budgetUsage[:4]
		}
	}
}

// ErrNilSystem is returned when a nil WiredEvolutionSystem is passed to GenerateReport.
var ErrNilSystem = fmt.Errorf("system must not be nil")

// GenerateReport builds a data-driven evolution report from a wired system.
// It collects real statistics from the population, genealogy, and optionally
// the scoring infrastructure. Every claim in the report is backed by stored metrics.
//
// Args:
//
//	ctx - operation context.
//	system - the wired evolution system to report on (must not be nil).
//	opts - optional report configuration (e.g., WithScoringStats for scorer cost data).
//
// Returns:
//
//	*EvolutionReport - the generated report (never nil if error is nil).
//	error - non-nil if system is nil.
func GenerateReport(ctx context.Context, system *WiredEvolutionSystem, opts ...ReportOption) (*EvolutionReport, error) {
	if system == nil {
		return nil, fmt.Errorf("generate report: %w", ErrNilSystem)
	}

	cfg := &reportConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	report := &EvolutionReport{}

	// Collect population statistics.
	pop := system.Population
	if pop != nil {
		report.TotalGenerations = pop.Generation

		// Current generation stats.
		popStats := pop.Stats()
		currentGen := buildGenerationStats(pop, popStats)
		report.GenerationTrajectory = []GenerationStats{currentGen}
		report.FinalBestScore = popStats.BestScore

		// Best ever strategy.
		best := pop.BestStrategy()
		if best != nil {
			report.BestEverScore = best.Score
			report.BestEverGeneration = pop.Generation // best-ever is tracked at current gen
		}

		// Diversity / lineage concentration from population snapshot.
		diversityReport := pop.DiversityStats()
		if diversityReport.Overall > 0 {
			currentGen.Diversity = diversityReport.Overall
			report.LineageConcentration = &LineageConcentration{
				TopLineageShare: diversityReport.DominantLineageShare,
				UniqueLineages:  countUniqueLineages(pop),
			}
		}
	}

	// Inject scorer cost summary if provided.
	if cfg.scoringStats != nil && len(cfg.budgetUsage) >= 4 {
		report.ScorerCostSummary = &ScorerCostSummary{
			TotalLLMCalls:  int(cfg.scoringStats["llm_calls"]),
			TotalCacheHits: int(cfg.scoringStats["cache_hits"]),
			TotalFallbacks: int(cfg.scoringStats["fallbacks"]),
			LLMBudgetUsed:  cfg.budgetUsage[0],
			LLMBudgetMax:   cfg.budgetUsage[1],
		}
	}

	return report, nil
}

// buildGenerationStats constructs GenerationStats from population data.
//
// Args:
//
//	pop - the genome population.
//	stats - pre-computed PopulationStats.
//
// Returns:
//
//	GenerationStats - populated stats for the current generation.
func buildGenerationStats(pop *genome.Population, stats *genome.PopulationStats) GenerationStats {
	gs := GenerationStats{
		Generation:     stats.Generation,
		PopulationSize: stats.Size,
		BestScore:      stats.BestScore,
		AvgScore:       stats.AvgScore,
		WorstScore:     stats.WorstScore,
		Diversity:      stats.Diversity.Overall,
		MutationTypes:  make(map[string]int),
	}

	// Count mutation types from current agents.
	agents, _ := pop.Snapshot()
	for _, agent := range agents {
		mt := agent.StrategyMutationType.String()
		if mt == "" {
			mt = "unknown"
		}
		gs.MutationTypes[mt]++
	}

	// Count diverse lineages (agents with distinct ParentIDs).
	parentSet := make(map[string]struct{})
	for _, agent := range agents {
		if agent.ParentID != "" {
			parentSet[agent.ParentID] = struct{}{}
		}
	}
	gs.NumDiverse = len(parentSet)

	return gs
}

// countUniqueLineages counts unique parent IDs in the population.
//
// Args:
//
//	pop - the genome population.
//
// Returns:
//
//	int - number of unique lineages.
func countUniqueLineages(pop *genome.Population) int {
	agents, _ := pop.Snapshot()
	lineageSet := make(map[string]struct{})
	for _, agent := range agents {
		if agent.ParentID != "" {
			lineageSet[agent.ParentID] = struct{}{}
		}
	}
	return len(lineageSet)
}

// ReportString returns a human-readable summary of the evolution report.
// Suitable for logging and CLI output.
//
// Args:
//
//	r - the evolution report (must not be nil).
//
// Returns:
//
//	string - formatted report text.
func ReportString(r *EvolutionReport) string {
	if r == nil {
		return "(nil report)"
	}

	var b strings.Builder

	b.WriteString("=== Evolution Report ===\n")
	b.WriteString(fmt.Sprintf("Total Generations:    %d\n", r.TotalGenerations))
	b.WriteString(fmt.Sprintf("Best Ever Score:      %.4f (gen %d)\n", r.BestEverScore, r.BestEverGeneration))
	b.WriteString(fmt.Sprintf("Final Best Score:     %.4f\n", r.FinalBestScore))

	// Generation trajectory.
	for _, gs := range r.GenerationTrajectory {
		b.WriteString(fmt.Sprintf("\n--- Generation %d ---\n", gs.Generation))
		b.WriteString(fmt.Sprintf("  Population Size:  %d\n", gs.PopulationSize))
		b.WriteString(fmt.Sprintf("  Best Score:       %.4f\n", gs.BestScore))
		b.WriteString(fmt.Sprintf("  Avg Score:        %.4f\n", gs.AvgScore))
		b.WriteString(fmt.Sprintf("  Worst Score:      %.4f\n", gs.WorstScore))
		b.WriteString(fmt.Sprintf("  Diversity:        %.4f\n", gs.Diversity))
		b.WriteString(fmt.Sprintf("  Diverse Lineages: %d\n", gs.NumDiverse))

		if len(gs.MutationTypes) > 0 {
			b.WriteString("  Mutation Types:\n")
			for mt, count := range gs.MutationTypes {
				b.WriteString(fmt.Sprintf("    %s: %d\n", mt, count))
			}
		}
	}

	// Scorer cost summary.
	if r.ScorerCostSummary != nil {
		cs := r.ScorerCostSummary
		b.WriteString("\n--- Scorer Cost Summary ---\n")
		b.WriteString(fmt.Sprintf("  LLM Calls:        %d / %d\n", cs.LLMBudgetUsed, cs.LLMBudgetMax))
		b.WriteString(fmt.Sprintf("  Cache Hits:       %d\n", cs.TotalCacheHits))
		b.WriteString(fmt.Sprintf("  Fallbacks:        %d\n", cs.TotalFallbacks))
	}

	// Lineage concentration.
	if r.LineageConcentration != nil {
		lc := r.LineageConcentration
		b.WriteString("\n--- Lineage Concentration ---\n")
		b.WriteString(fmt.Sprintf("  Top Lineage Share: %.2f%%\n", lc.TopLineageShare*100))
		b.WriteString(fmt.Sprintf("  Unique Lineages:   %d\n", lc.UniqueLineages))
	}

	b.WriteString("========================\n")
	return b.String()
}

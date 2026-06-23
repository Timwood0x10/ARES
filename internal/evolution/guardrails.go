// Package evolution provides production guardrails for the autonomous
// evolution system. These guardrails detect dangerous conditions and
// trigger protective actions before they cause harm.
package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// GuardrailLevel indicates the severity of a guardrail trigger.
type GuardrailLevel int

const (
	// GuardrailInfo is informational; no action required.
	GuardrailInfo GuardrailLevel = iota + 1
	// GuardrailWarning indicates a concerning condition that should be monitored.
	GuardrailWarning
	// GuardrailCritical requires immediate intervention (e.g., stop evolution).
	GuardrailCritical
)

// GuardrailEvent records a guardrail trigger with context.
type GuardrailEvent struct {
	// Level is the severity level.
	Level GuardrailLevel
	// Rule is the name of the guardrail rule that triggered.
	Rule string
	// Message describes what happened.
	Message string
	// Generation when this event occurred.
	Generation int
	// Timestamp when this event occurred.
	Timestamp time.Time
	// SuggestedAction is the recommended remediation.
	SuggestedAction string
}

// GuardrailResult is the outcome of running all guardrails.
type GuardrailResult struct {
	// ShouldStop indicates evolution should halt immediately.
	ShouldStop bool
	// Events lists all triggered guardrails (may include non-critical ones).
	Events []GuardrailEvent
}

// EvolutionGuardrails runs safety checks before and after each evolution cycle.
type EvolutionGuardrails struct {
	mu sync.RWMutex

	// BaselineScore is the score to beat; strategies below this are regressions.
	BaselineScore float64

	// MaxStagnantGenerations triggers warning after this many gens without improvement.
	MaxStagnantGenerations int

	// StagnantCount counts consecutive generations without improvement.
	stagnantCount int

	// BestKnownScore tracks the best score ever seen.
	bestKnownScore float64

	// LastImprovementGeneration records which generation last saw improvement.
	lastImprovementGen int

	// MaxLineageShare is the maximum allowed share for a single lineage (0-1, 0=disabled).
	MaxLineageShare float64

	// Events stores historical guardrail events.
	events []GuardrailEvent

	// MaxEvents limits stored events (0=unlimited).
	MaxEvents int
}

// GuardrailOption configures EvolutionGuardrails.
type GuardrailOption func(*EvolutionGuardrails)

// WithBaselineScore sets the minimum acceptable strategy score.
func WithBaselineScore(score float64) GuardrailOption {
	return func(g *EvolutionGuardrails) {
		g.BaselineScore = score
	}
}

// WithMaxStagnantGenerations sets the stagnation detection threshold.
func WithMaxStagnantGenerations(n int) GuardrailOption {
	return func(g *EvolutionGuardrails) {
		g.MaxStagnantGenerations = n
	}
}

// WithMaxLineageShare sets the maximum allowed lineage concentration.
func WithMaxLineageShare(share float64) GuardrailOption {
	return func(g *EvolutionGuardrails) {
		g.MaxLineageShare = share
	}
}

// NewEvolutionGuardrails creates a new guardrail checker.
//
// Args:
//   - opts: configuration options for the guardrail checker
//
// Returns:
//   - *EvolutionGuardrails: configured guardrail instance
//   - error: always nil (reserved for future validation)
func NewEvolutionGuardrails(opts ...GuardrailOption) (*EvolutionGuardrails, error) {
	g := &EvolutionGuardrails{
		BaselineScore:          0,
		MaxStagnantGenerations: 10,
		MaxLineageShare:        0.8,
		MaxEvents:              1000,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g, nil
}

// PreEvolveCheck runs guardrails BEFORE an evolution cycle.
//
// Checks:
//  1. All individuals evaluated guardrail — if >50% of population has Score==-1 (unevaluated), return Critical
//  2. Stagnation check — if stagnantCount >= MaxStagnantGenerations, return Warning
//
// Args:
//   - ctx: context for cancellation
//   - currentBest: current population's best score
//   - generation: current generation number
//   - totalPop: total population size
//   - unevaluatedCount: number of individuals with Score == -1
//
// Returns:
//   - *GuardrailResult: result containing any triggered guardrails and stop recommendation
func (g *EvolutionGuardrails) PreEvolveCheck(ctx context.Context, currentBest float64, generation int, totalPop, unevaluatedCount int) *GuardrailResult {
	result := &GuardrailResult{
		ShouldStop: false,
		Events:     []GuardrailEvent{},
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Check 1: Unevaluated population guardrail
	if totalPop > 0 {
		unevaluatedRatio := float64(unevaluatedCount) / float64(totalPop)
		if unevaluatedRatio > 0.5 {
			event := GuardrailEvent{
				Level:           GuardrailCritical,
				Rule:            "unevaluated_population",
				Message:         "majority population unevaluated",
				Generation:      generation,
				Timestamp:       time.Now(),
				SuggestedAction: "evaluate all individuals before proceeding",
			}
			slog.Warn("guardrail: critical - majority population unevaluated",
				"generation", generation,
				"ratio", unevaluatedRatio,
				"total_pop", totalPop,
				"unevaluated", unevaluatedCount,
			)
			result.Events = append(result.Events, event)
			result.ShouldStop = true
			g.recordEventLocked(event)
		}
	}

	// Check 2: Stagnation guardrail
	if g.stagnantCount >= g.MaxStagnantGenerations && g.MaxStagnantGenerations > 0 {
		event := GuardrailEvent{
			Level:           GuardrailWarning,
			Rule:            "stagnation",
			Message:         fmt.Sprintf("no improvement for %d generations", g.stagnantCount),
			Generation:      generation,
			Timestamp:       time.Now(),
			SuggestedAction: "consider increasing mutation rate or introducing diversity",
		}
		slog.Warn("guardrail: warning - stagnation detected",
			"generation", generation,
			"stagnant_count", g.stagnantCount,
			"threshold", g.MaxStagnantGenerations,
		)
		result.Events = append(result.Events, event)
		g.recordEventLocked(event)
	}

	return result
}

// PostEvolveCheck runs guardrails AFTER an evolution cycle.
//
// Checks:
//  1. Best regression — if new best < BaselineScore, return Critical ("strategy failed to beat baseline")
//  2. Improvement tracking — update stagnation counter
//  3. Lineage concentration — if top lineage > MaxLineageShare, return Warning
//
// Args:
//   - ctx: context for cancellation
//   - newBest: new population's best score after evolution
//   - generation: generation number
//   - lineageShares: map[lineageID]count (can be nil if unavailable)
//
// Returns:
//   - *GuardrailResult: result containing any triggered guardrails and stop recommendation
func (g *EvolutionGuardrails) PostEvolveCheck(ctx context.Context, newBest float64, generation int, lineageShares map[string]int) *GuardrailResult {
	result := &GuardrailResult{
		ShouldStop: false,
		Events:     []GuardrailEvent{},
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Check 1: Baseline regression guardrail
	if newBest < g.BaselineScore && g.BaselineScore > 0 {
		event := GuardrailEvent{
			Level:           GuardrailCritical,
			Rule:            "baseline_regression",
			Message:         "best score regressed below baseline",
			Generation:      generation,
			Timestamp:       time.Now(),
			SuggestedAction: "review recent changes and consider reverting to previous best strategy",
		}
		slog.Warn("guardrail: critical - baseline regression",
			"generation", generation,
			"new_best", newBest,
			"baseline", g.BaselineScore,
		)
		result.Events = append(result.Events, event)
		result.ShouldStop = true
		g.recordEventLocked(event)
	}

	// Check 2: Improvement tracking and stagnation counter update
	if newBest > g.bestKnownScore {
		g.stagnantCount = 0
		g.bestKnownScore = newBest
		g.lastImprovementGen = generation
		slog.Info("guardrail: improvement detected",
			"generation", generation,
			"new_best", newBest,
			"previous_best", g.bestKnownScore,
		)
	} else {
		g.stagnantCount++
		slog.Info("guardrail: no improvement",
			"generation", generation,
			"new_best", newBest,
			"best_known", g.bestKnownScore,
			"stagnant_count", g.stagnantCount,
		)
	}

	// Check 3: Lineage concentration guardrail
	if lineageShares != nil && g.MaxLineageShare > 0 {
		total := 0
		for _, count := range lineageShares {
			total += count
		}
		if total > 0 {
			maxCount := 0
			for _, count := range lineageShares {
				if count > maxCount {
					maxCount = count
				}
			}
			maxShare := float64(maxCount) / float64(total)
			if maxShare > g.MaxLineageShare {
				event := GuardrailEvent{
					Level:           GuardrailWarning,
					Rule:            "lineage_concentration",
					Message:         fmt.Sprintf("lineage concentration %.2f exceeds threshold %.2f", maxShare, g.MaxLineageShare),
					Generation:      generation,
					Timestamp:       time.Now(),
					SuggestedAction: "increase selection pressure or introduce external diversity",
				}
				slog.Warn("guardrail: warning - lineage concentration",
					"generation", generation,
					"max_share", maxShare,
					"threshold", g.MaxLineageShare,
				)
				result.Events = append(result.Events, event)
				g.recordEventLocked(event)
			}
		}
	}

	return result
}

// RecordEvent stores a guardrail event for later review.
//
// Args:
//   - event: the guardrail event to record
func (g *EvolutionGuardrails) RecordEvent(event GuardrailEvent) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.recordEventLocked(event)
}

// recordEventLocked stores an event (caller must hold lock).
func (g *EvolutionGuardrails) recordEventLocked(event GuardrailEvent) {
	g.events = append(g.events, event)
	// Enforce MaxEvents limit
	if g.MaxEvents > 0 && len(g.events) > g.MaxEvents {
		g.events = g.events[len(g.events)-g.MaxEvents:]
	}
}

// Events returns all recorded events (copy).
//
// Returns:
//   - []GuardrailEvent: copy of all stored events
func (g *EvolutionGuardrails) Events() []GuardrailEvent {
	g.mu.RLock()
	defer g.mu.RUnlock()

	eventsCopy := make([]GuardrailEvent, len(g.events))
	copy(eventsCopy, g.events)
	return eventsCopy
}

// Reset clears stagnation counters and events.
func (g *EvolutionGuardrails) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.stagnantCount = 0
	g.bestKnownScore = 0
	g.lastImprovementGen = 0
	g.events = []GuardrailEvent{}
}

// StagnantCount returns the current stagnation counter.
//
// Returns:
//   - int: number of consecutive generations without improvement
func (g *EvolutionGuardrails) StagnantCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.stagnantCount
}

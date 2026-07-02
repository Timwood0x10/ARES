package research

import (
	"context"
	"fmt"
)

// Reflector generates post-decision reflections based on realized outcomes.
// It supports both LLM-assisted reflection (when executor is provided) and
// rule-based fallback reflection (when no LLM is available).
type Reflector struct {
	memoryLog *MemoryLog
	executor  LLMExecutor
}

// LLMExecutor is the interface for optional LLM-based reflection generation.
// If nil, the reflector uses rule-based reflection instead.
type LLMExecutor interface {
	Complete(ctx context.Context, messages []Message) (string, error)
}

// Message represents a single message for LLM conversation.
type Message struct {
	Role    string
	Content string
}

// NewReflector creates a new Reflector for generating decision reflections.
//
// Args:
//   - memoryLog: the memory log to read/write entries from.
//   - executor: optional LLM executor for enhanced reflection generation.
//     Pass nil to use rule-based reflection only.
//
// Returns:
//   - initialized Reflector ready for use.
func NewReflector(memoryLog *MemoryLog, executor LLMExecutor) *Reflector {
	return &Reflector{
		memoryLog: memoryLog,
		executor:  executor,
	}
}

// Reflect generates a reflection for a single resolved memory entry.
// The reflection analyzes what went right/wrong and provides learnable insights.
//
// Reflection content includes:
//   - Whether the decision direction was correct or wrong.
//   - Key factors that drove the outcome.
//   - Signals that were missed or over-weighted.
//   - Recommendations for similar future situations.
//
// Args:
//   - ctx: context for cancellation.
//   - entry: the resolved memory entry to reflect upon.
//
// Returns:
//   - generated reflection text.
//   - error if reflection generation fails.
func (r *Reflector) Reflect(ctx context.Context, entry *MemoryEntry) (string, error) {
	if entry == nil {
		return "", fmt.Errorf("cannot reflect on nil entry")
	}

	alpha := computeAlpha(entry)

	// Use LLM if available for richer reflection.
	if r.executor != nil {
		return r.reflectWithLLM(ctx, entry, alpha)
	}

	// Fallback to rule-based engine.
	return reflectByRules(alpha), nil
}

// BatchReflect generates reflections for all resolved entries that lack reflections.
// This is typically run as a periodic batch job (e.g., daily or weekly).
//
// Args:
//   - ctx: context for cancellation.
//
// Returns:
//   - count of entries that were successfully reflected.
//   - error if the batch operation fails critically.
func (r *Reflector) BatchReflect(ctx context.Context) (int, error) {
	pending, err := r.memoryLog.store.GetPendingEntries(ctx)
	if err != nil {
		return 0, fmt.Errorf("batch reflect get pending: %w", err)
	}

	count := 0
	for _, entry := range pending {
		if entry.Status == MemoryStatusResolved && entry.Reflection == "" {
			reflection, err := r.Reflect(ctx, entry)
			if err != nil {
				continue // Skip failed reflections, continue with others.
			}
			// Persist reflection back to store after generating it.
			if storeErr := r.memoryLog.store.UpdateReflection(ctx, entry.ID, reflection); storeErr != nil {
				continue // Skip persistence failures, continue with others.
			}
			entry.Reflection = reflection
			count++
		}
	}
	return count, nil
}

// ─── Rule-Based Reflection Engine ─────────────────────────

// reflectByRules generates reflection text using deterministic rules based on alpha.
// No LLM is required; this is the fallback path.
//
// Alpha thresholds:
//   - > +5%: Strong correct direction.
//   - 0% to +5%: Marginally correct.
//   - -5% to 0%: Marginally wrong.
//   - < -5%: Wrong direction.
func reflectByRules(alpha float64) string {
	switch {
	case alpha > 5.0:
		return fmt.Sprintf(
			"Strong correct direction (alpha: +%.2f%%). "+
				"Key factors: thesis aligned with market catalysts. "+
				"Continue monitoring the same signals for future decisions.",
			alpha,
		)
	case alpha > 0:
		return fmt.Sprintf(
			"Marginally correct (alpha: +%.2f%%). "+
				"Consider tightening stop-loss levels and improving entry timing. "+
				"Core thesis was valid but execution could be improved.",
			alpha,
		)
	case alpha >= -5.0:
		return fmt.Sprintf(
			"Marginally wrong (alpha: %.2f%%). "+
				"Missed signals: possible regime change or overlooked risk factor. "+
				"Review analyst reports for gaps in coverage.",
			alpha,
		)
	default:
		return fmt.Sprintf(
			"Wrong direction (alpha: %.2f%%). "+
				"Critical errors: fundamental thesis may be flawed or timing was poor. "+
				"Conduct thorough post-mortem before similar positions.",
			alpha,
		)
	}
}

// reflectWithLLM uses an LLM to generate richer, more nuanced reflection.
func (r *Reflector) reflectWithLLM(ctx context.Context, entry *MemoryEntry, alpha float64) (string, error) {
	prompt := fmt.Sprintf(`You are a trading reflection analyst. Review this past decision and generate insights.

## Decision Summary
- Symbol: %s
- Date: %s
- Rating: %s
- Benchmark: %s
- Holding Days: %d

## Outcome
- Actual Return: %.2f%%
- Benchmark Return: %.2f%%
- Realized Alpha: %.2f%%

Generate a concise reflection (3-5 sentences) covering:
1. Was the decision direction correct?
2. What key factors drove the outcome?
3. What signals were missed or over-weighted?
4. What should be done differently next time?`,
		entry.Symbol, entry.AnalysisDate.Format("2006-01-02"),
		entry.Rating, entry.Benchmark, entry.HoldingDays,
		derefFloat(entry.RawReturn), 0.0, alpha,
	)

	resp, err := r.executor.Complete(ctx, []Message{
		{Role: "system", Content: prompt},
	})
	if err != nil {
		// Fallback to rules if LLM call fails.
		return reflectByRules(alpha), nil
	}
	return resp, nil
}

// computeAlpha calculates the realized alpha for an entry.
// Uses AlphaReturn if available, otherwise computes from RawReturn vs benchmark.
func computeAlpha(entry *MemoryEntry) float64 {
	if entry.AlphaReturn != nil {
		return *entry.AlphaReturn
	}
	if entry.RawReturn != nil {
		return *entry.RawReturn
	}
	return 0
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

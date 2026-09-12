package ares_bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/lib/pq"

	"github.com/Timwood0x10/ares/internal/evidence"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
)

// fitnessSourceKnowledge is the AKG genome source name used in fitness
// evidence summaries (shared with the knowledge runtime vector provider).
const fitnessSourceKnowledge = "knowledge"

// fitnessSourceMemory is the memory genome source name used in fitness
// evidence summaries (shared with the memory retriever emitter).
const fitnessSourceMemory = "memory"

// fitnessSourceOrder is the stable ordering of GA genome sources whose recent
// fitness evidence is summarized into the LLM suggestion prompt.
var fitnessSourceOrder = []string{"workflow", "scheduler", "recovery", fitnessSourceMemory, fitnessSourceKnowledge}

// buildEvolutionSuggestionPrompt builds an LLM suggestion prompt grounded in
// the current evolution state: the mean fitness value of the most recent
// evidence per genome source plus the currently deployed strategy. When no
// evidence or strategy exists yet, it falls back to the generic prompt so the
// LLM still has the instruction it needs. Returns the prompt string.
//
// The summary makes the LLM's suggestions state-aware instead of blind: it can
// see which genome has low fitness (and thus deserves a patch) and which
// strategy is live (and thus should be mutated with care).
func buildEvolutionSuggestionPrompt(
	ctx context.Context,
	evStore evidence.Store,
	strategyStore evolution.StrategyStore,
) string {
	base := "Examine the current system state and suggest one evolution improvement. " +
		"Use one of: insert node, remove node, replace node, add edge, remove edge, " +
		"change scheduler, change topk, change reducer, change planner, change recovery."

	var sb strings.Builder
	sb.WriteString(base)

	if evStore != nil {
		var lines []string
		for _, src := range fitnessSourceOrder {
			mean, count, ok := recentFitnessSummary(ctx, evStore, src, fitnessWindowSize)
			if !ok {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s: mean fitness %.2f over %d evidence records", src, mean, count))
		}
		if len(lines) > 0 {
			sb.WriteString("\n\nCurrent evolution state (recent fitness evidence):\n")
			sb.WriteString(strings.Join(lines, "\n"))
		}
	}

	if strategyStore != nil {
		if st, err := strategyStore.GetActive(ctx); err == nil && st != nil {
			sb.WriteString("\n\nCurrently deployed strategy: ")
			fmt.Fprintf(&sb, "id=%s version=%d", st.ID, st.Version)
			if st.Score >= 0 {
				fmt.Fprintf(&sb, " score=%.2f", st.Score)
			}
			if st.MutationDesc != "" {
				fmt.Fprintf(&sb, " mutation=%q", st.MutationDesc)
			}
		}
	}

	sb.WriteString("\n\nRespond with exactly one suggestion in the allowed format.")
	return sb.String()
}

// fitnessWindowSize bounds how many evidence records are summarized per genome
// source so a long-running process does not read the whole store each cycle.
const fitnessWindowSize = 50

// recentFitnessSummary computes the mean fitness value over the most recent
// fitness evidence records for one genome source. It returns ok=false when
// the store is nil or no usable numeric record exists in the window.
func recentFitnessSummary(ctx context.Context, store evidence.Store, source string, limit int) (mean float64, count int, ok bool) {
	if store == nil {
		return 0, 0, false
	}
	evs, err := store.Query(ctx, evidence.Filter{
		Source: source,
		Kind:   evidence.KindFitness,
		Limit:  limit,
	})
	if err != nil {
		return 0, 0, false
	}
	var sum float64
	for _, ev := range evs {
		if len(ev.Payload) == 0 {
			continue
		}
		var fe struct {
			Value float64 `json:"value"`
		}
		if err := json.Unmarshal(ev.Payload, &fe); err != nil {
			continue
		}
		if fe.Value < 0 || fe.Value > 1 {
			continue
		}
		sum += fe.Value
		count++
	}
	if count == 0 {
		return 0, 0, false
	}
	return sum / float64(count), count, true
}

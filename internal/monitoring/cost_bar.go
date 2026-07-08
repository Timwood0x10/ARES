// Package monitoring provides the ARES Console monitoring plugin.
// cost_bar tracks resource consumption per-agent and in aggregate.
package monitoring

import (
	"sort"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/eventutil"
)

// CostBar tracks LLM resource consumption across all agents.
// All methods are safe for concurrent use.
type CostBar struct {
	mu      sync.RWMutex
	total   AgentCost
	byAgent map[string]*AgentCost
}

// NewCostBar creates a CostBar with zero costs.
func NewCostBar() *CostBar {
	return &CostBar{
		byAgent: make(map[string]*AgentCost),
	}
}

// HandleEvent processes an LLM call event and updates cost accumulators.
// Non-LLM events are silently ignored. Costs in different currencies are
// tracked separately — only same-currency amounts are summed into totals to
// avoid mixing units (e.g. adding USD to EUR).
func (cb *CostBar) HandleEvent(evt *ares_events.Event) {
	if evt == nil || evt.Type != ares_events.EventLLMCall {
		return
	}
	agentID := eventutil.ExtractAgentID(evt)
	if agentID == "" {
		return
	}

	inputTokens := eventutil.ExtractInt64(evt, "input_tokens")
	outputTokens := eventutil.ExtractInt64(evt, "output_tokens")
	estimatedCost := eventutil.ExtractFloat64(evt, "estimated_cost")
	currency := eventutil.ExtractString(evt, "currency")

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if currency == "" {
		currency = "USD"
	}

	// Update total. Token counts are currency-agnostic; cost is only summed
	// when the currency matches the primary (first-seen) currency.
	cb.total.InputTokens += inputTokens
	cb.total.OutputTokens += outputTokens
	cb.total.TotalTokens += inputTokens + outputTokens
	cb.total.CallCount++
	if cb.total.Currency == "" {
		cb.total.Currency = currency
	}
	if cb.total.Currency == currency {
		cb.total.EstimatedCost += estimatedCost
	}

	// Update per-agent. Same currency guard as above.
	cost, ok := cb.byAgent[agentID]
	if !ok {
		cost = &AgentCost{
			AgentID:  agentID,
			Currency: currency,
		}
		cb.byAgent[agentID] = cost
	}
	cost.InputTokens += inputTokens
	cost.OutputTokens += outputTokens
	cost.TotalTokens += inputTokens + outputTokens
	cost.CallCount++
	if cost.Currency == currency {
		cost.EstimatedCost += estimatedCost
	}
}

// Snapshot returns the current cost bar state with entries sorted by
// estimated cost descending.
func (cb *CostBar) Snapshot() CostBarBreakdown {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	entries := make([]CostBarEntry, 0, len(cb.byAgent))
	for _, c := range cb.byAgent {
		entries = append(entries, CostBarEntry{
			AgentID:       c.AgentID,
			EstimatedCost: c.EstimatedCost,
			Currency:      c.Currency,
			CallCount:     c.CallCount,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].EstimatedCost > entries[j].EstimatedCost
	})

	return CostBarBreakdown{
		Total:    cb.total.EstimatedCost,
		Entries:  entries,
		Currency: cb.total.Currency,
	}
}

// Total returns the aggregate cost across all agents.
func (cb *CostBar) Total() AgentCost {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	cp := cb.total
	return cp
}

// GetCost returns the cost for a specific agent, if tracked.
func (cb *CostBar) GetCost(agentID string) (*AgentCost, bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	c, ok := cb.byAgent[agentID]
	if !ok {
		return nil, false
	}
	cp := *c
	return &cp, true
}

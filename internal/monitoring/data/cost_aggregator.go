// Package data provides state aggregation for the ARES Console monitoring plugin.
package data

import (
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/eventutil"
)

// CostAggregator tracks per-agent cost from LLM call events.
// All methods are safe for concurrent use.
type CostAggregator struct {
	mu         sync.RWMutex
	costs      map[string]*monitoring.AgentCost
	thresholds map[string]float64
}

// NewCostAggregator creates a new CostAggregator with empty maps.
func NewCostAggregator() *CostAggregator {
	return &CostAggregator{
		costs:      make(map[string]*monitoring.AgentCost),
		thresholds: make(map[string]float64),
	}
}

// RecordLLMCall extracts token and cost information from an LLM call payload.
func (ca *CostAggregator) RecordLLMCall(agentID string, payload map[string]any) {
	if agentID == "" || payload == nil {
		return
	}

	inputTokens := eventutil.ExtractMapInt64(payload, "input_tokens")
	outputTokens := eventutil.ExtractMapInt64(payload, "output_tokens")
	estimatedCost := eventutil.ExtractMapFloat64(payload, "estimated_cost")

	ca.mu.Lock()
	defer ca.mu.Unlock()

	cost, ok := ca.costs[agentID]
	if !ok {
		cost = &monitoring.AgentCost{
			AgentID:  agentID,
			Currency: "USD",
		}
		ca.costs[agentID] = cost
	}
	cost.InputTokens += inputTokens
	cost.OutputTokens += outputTokens
	cost.TotalTokens += inputTokens + outputTokens
	cost.EstimatedCost += estimatedCost
	cost.CallCount++
}

// GetCost returns a copy of the cost record for the given agent.
func (ca *CostAggregator) GetCost(agentID string) monitoring.AgentCost {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	c, ok := ca.costs[agentID]
	if !ok {
		return monitoring.AgentCost{}
	}
	return *c
}

// TotalCost returns the sum of all agent costs.
func (ca *CostAggregator) TotalCost() monitoring.AgentCost {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	total := monitoring.AgentCost{Currency: "USD"}
	for _, c := range ca.costs {
		total.InputTokens += c.InputTokens
		total.OutputTokens += c.OutputTokens
		total.TotalTokens += c.TotalTokens
		total.EstimatedCost += c.EstimatedCost
		total.CallCount += c.CallCount
	}
	return total
}

// CostBreakdown returns a hierarchical breakdown of costs.
func (ca *CostAggregator) CostBreakdown() monitoring.CostBreakdown {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	breakdown := monitoring.CostBreakdown{
		ByAgent:  make(map[string]monitoring.AgentCost),
		ByModel:  make(map[string]float64),
		ByTask:   make(map[string]float64),
		Currency: "USD",
	}
	for id, c := range ca.costs {
		breakdown.ByAgent[id] = *c
		breakdown.Total += c.EstimatedCost
	}
	return breakdown
}

// SetAlert sets a cost alert threshold for the given agent.
func (ca *CostAggregator) SetAlert(agentID string, threshold float64) {
	if agentID == "" || threshold <= 0 {
		return
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.thresholds[agentID] = threshold
}

// CheckAlerts checks all thresholds and returns triggered alerts.
func (ca *CostAggregator) CheckAlerts() []monitoring.CostAlert {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	return ca.checkAlertsLocked()
}

// checkAlertsLocked returns alerts for agents exceeding their thresholds.
// Must be called with at least a read lock held.
func (ca *CostAggregator) checkAlertsLocked() []monitoring.CostAlert {
	var alerts []monitoring.CostAlert
	for agentID, threshold := range ca.thresholds {
		cost, ok := ca.costs[agentID]
		if !ok {
			continue
		}
		if cost.EstimatedCost > threshold {
			alerts = append(alerts, monitoring.CostAlert{
				ID:          fmt.Sprintf("alert-%s-%d", agentID, time.Now().UnixNano()),
				AgentID:     agentID,
				Threshold:   threshold,
				Actual:      cost.EstimatedCost,
				Message:     fmt.Sprintf("agent %s cost %.4f exceeds threshold %.4f", agentID, cost.EstimatedCost, threshold),
				Severity:    "warning",
				TriggeredAt: time.Now(),
			})
		}
	}
	return alerts
}

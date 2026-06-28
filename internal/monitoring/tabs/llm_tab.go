package tabs

import (
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring"
)

const maxLLMCalls = 1000

// LLMCallStats aggregates token and cost statistics.
type LLMCallStats struct {
	TotalCalls        int     `json:"total_calls"`
	TotalInputTokens  int64   `json:"total_input_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
	TotalTokens       int64   `json:"total_tokens"`
	TotalCost         float64 `json:"total_cost"`
	AvgInputTokens    float64 `json:"avg_input_tokens"`
	AvgOutputTokens   float64 `json:"avg_output_tokens"`
}

// LLMTabSnapshot is the snapshot payload returned by LLMTab.Snapshot.
type LLMTabSnapshot struct {
	Calls []monitoring.LLMCallRecord `json:"calls"`
	Stats LLMCallStats               `json:"stats"`
}

// LLMTab implements the Tab interface for the LLM tab.
// It tracks LLM call records and aggregates token/cost statistics.
type LLMTab struct {
	mu    sync.RWMutex
	calls []monitoring.LLMCallRecord
	stats LLMCallStats
}

// NewLLMTab creates a new LLMTab instance.
func NewLLMTab() *LLMTab {
	return &LLMTab{
		calls: make([]monitoring.LLMCallRecord, 0, maxLLMCalls),
	}
}

// Name returns the tab identifier.
func (t *LLMTab) Name() string { return "llm" }

// Label returns the human-readable tab name.
func (t *LLMTab) Label() string { return "LLM" }

// HandleEvent processes LLM call events.
func (t *LLMTab) HandleEvent(evt *ares_events.Event) {
	if evt == nil || evt.Type != ares_events.EventLLMCall {
		return
	}
	t.handleLLMCall(evt)
}

// Snapshot returns the current LLM state.
func (t *LLMTab) Snapshot() any {
	t.mu.RLock()
	defer t.mu.RUnlock()

	calls := make([]monitoring.LLMCallRecord, len(t.calls))
	copy(calls, t.calls)

	return LLMTabSnapshot{
		Calls: calls,
		Stats: t.stats,
	}
}

// Stats returns a copy of the aggregated statistics.
func (t *LLMTab) Stats() LLMCallStats {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.stats
}

// Trim retains at most maxLen calls, discarding the oldest.
func (t *LLMTab) Trim(maxLen int) {
	if maxLen <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.calls) > maxLen {
		t.calls = t.calls[len(t.calls)-maxLen:]
	}
}

func (t *LLMTab) handleLLMCall(evt *ares_events.Event) {
	inputTokens := getInt64(evt.Payload, "input_tokens")
	outputTokens := getInt64(evt.Payload, "output_tokens")
	cost := getFloat64(evt.Payload, "cost")

	record := monitoring.LLMCallRecord{
		ID:           evt.ID,
		AgentID:      getString(evt.Payload, "agent_id"),
		ModelName:    getString(evt.Payload, "model"),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Duration:     getDuration(evt.Payload, "duration"),
		Timestamp:    evt.Timestamp,
	}
	if record.AgentID == "" {
		record.AgentID = evt.ModuleName
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.calls) >= maxLLMCalls {
		t.calls = t.calls[1:]
	}
	t.calls = append(t.calls, record)

	// Update aggregate stats.
	t.stats.TotalCalls++
	t.stats.TotalInputTokens += inputTokens
	t.stats.TotalOutputTokens += outputTokens
	t.stats.TotalTokens += inputTokens + outputTokens
	t.stats.TotalCost += cost
	if t.stats.TotalCalls > 0 {
		n := float64(t.stats.TotalCalls)
		t.stats.AvgInputTokens = float64(t.stats.TotalInputTokens) / n
		t.stats.AvgOutputTokens = float64(t.stats.TotalOutputTokens) / n
	}
}

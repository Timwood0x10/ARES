package kernelscheduler

import (
	"sync"
)

// LoadTracker records per-agent execution statistics so scheduling decisions
// can use real load and confidence instead of static placeholders (v0.3.0 GAP4:
// P2 F1). The scheduler creates one tracker per scheduler instance and passes
// it to every Candidate constructor so the Score formula reflects actual agent
// load and evolution confidence.
//
// W4 Evolution feedback: SetAgentConfidence lets the evolution feedback loop
// (EvolutionExecutionFeedback.Apply) override an agent's confidence after a
// batch of task results. SetCapabilityConfidence does the same at the
// capability level (higher priority than agent-level). ConfidenceFor returns
// capability-level first, then agent-level, then the neutral default (1.0).
//
// Thread-safe: the scheduler's drain loop and the feedback loop may call
// methods concurrently (the same lock protects Load and Confidence).
type LoadTracker struct {
	mu sync.Mutex

	// done, ok, priority, load are per-agent histograms.
	done     map[string]float64
	ok       map[string]float64
	priority map[string]float64
	load     map[string]float64

	// agentConfidenceOverride and capabilityConfidenceOverride are set by the
	// evolution feedback loop (W4) and GA scheduler (F1). capability level
	// takes precedence over agent level; agent level falls back to 1.0.
	agentConfidenceOverride      map[string]float64
	capabilityConfidenceOverride map[string]float64
}

func NewLoadTracker() *LoadTracker {
	return &LoadTracker{
		done:                         make(map[string]float64),
		ok:                           make(map[string]float64),
		priority:                     make(map[string]float64),
		load:                         make(map[string]float64),
		agentConfidenceOverride:      make(map[string]float64),
		capabilityConfidenceOverride: make(map[string]float64),
	}
}

func (t *LoadTracker) SetPriority(agentID string, priority float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.priority[agentID] = priority
}

func (t *LoadTracker) Priority(agentID string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if v, ok := t.priority[agentID]; ok {
		return v
	}
	return 0.0
}

func (t *LoadTracker) Begin(agentID string) {
	t.mu.Lock()
	t.load[agentID]++
	t.mu.Unlock()
}

func (t *LoadTracker) End(agentID string, success bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done[agentID]++
	if success {
		t.ok[agentID]++
	}
}

func (t *LoadTracker) Load(agentID string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.load[agentID]
}

func (t *LoadTracker) Confidence(agentID string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if v, ok := t.agentConfidenceOverride[agentID]; ok {
		return v
	}
	if total, ok := t.done[agentID]; ok && total > 0 {
		return t.ok[agentID] / total
	}
	return 1.0
}

func (t *LoadTracker) SetAgentConfidence(agentID string, confidence float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agentConfidenceOverride[agentID] = confidence
}

func (t *LoadTracker) SetCapabilityConfidence(agentID, capability string, confidence float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := agentID + "|" + capability
	t.capabilityConfidenceOverride[key] = confidence
}

func (t *LoadTracker) ConfidenceFor(agentID, capability string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := agentID + "|" + capability
	if v, ok := t.capabilityConfidenceOverride[key]; ok {
		return v
	}
	if v, ok := t.agentConfidenceOverride[agentID]; ok {
		return v
	}
	if total, ok := t.done[agentID]; ok && total > 0 {
		return t.ok[agentID] / total
	}
	return 1.0
}

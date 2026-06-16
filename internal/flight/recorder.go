package flight

import (
	"context"
	"log/slog"
	"sync"

	"goagentx/internal/events"
	"goagentx/internal/memory"
)

// FlightRecorder is the unified entry point for all flight data.
// It aggregates Timeline, Graph, DecisionLog, DiagnosticsEngine, Genealogy, and MemoryPipeline.
type FlightRecorder struct {
	collector  *Collector
	eventStore events.EventStore
	memManager memory.MemoryManager
	genealogy  *Genealogy
	mu         sync.RWMutex
	started    bool
}

// FlightRecorderConfig holds dependencies for the flight recorder.
type FlightRecorderConfig struct {
	EventStore events.EventStore
	MemManager memory.MemoryManager
	Genealogy  *Genealogy // optional, for agent genealogy tracking
}

// NewFlightRecorder creates a new flight recorder.
func NewFlightRecorder(cfg FlightRecorderConfig) *FlightRecorder {
	collector := NewCollector(CollectorConfig{
		EventStore: cfg.EventStore,
	})

	return &FlightRecorder{
		collector:  collector,
		eventStore: cfg.EventStore,
		memManager: cfg.MemManager,
		genealogy:  cfg.Genealogy,
	}
}

// Start begins collecting flight data.
func (fr *FlightRecorder) Start(ctx context.Context) error {
	fr.mu.Lock()
	defer fr.mu.Unlock()

	if fr.started {
		return nil
	}

	if err := fr.collector.Start(ctx); err != nil {
		return err
	}

	fr.started = true
	slog.Info("flight recorder started")
	return nil
}

// Stop stops the flight recorder.
func (fr *FlightRecorder) Stop() {
	fr.mu.Lock()
	defer fr.mu.Unlock()

	if !fr.started {
		return
	}

	fr.collector.Stop()
	fr.started = false
	slog.Info("flight recorder stopped")
}

// Timeline returns the execution timeline.
func (fr *FlightRecorder) Timeline() *Timeline {
	return fr.collector.Timeline()
}

// Graph returns the call graph.
func (fr *FlightRecorder) Graph() *Graph {
	return fr.collector.Graph()
}

// Decisions returns the decision log.
func (fr *FlightRecorder) Decisions() *DecisionLog {
	return fr.collector.Decisions()
}

// Diagnostics returns the diagnostics engine.
func (fr *FlightRecorder) Diagnostics() *DiagnosticsEngine {
	return fr.collector.Diagnostics()
}

// Genealogy returns the agent genealogy tree. May be nil if not configured.
func (fr *FlightRecorder) Genealogy() *Genealogy {
	fr.mu.RLock()
	defer fr.mu.RUnlock()
	return fr.genealogy
}

// SetGenealogy attaches a genealogy tree to the flight recorder.
func (fr *FlightRecorder) SetGenealogy(g *Genealogy) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.genealogy = g
}

// Pipeline returns the memory pipeline for a session.
func (fr *FlightRecorder) Pipeline(sessionID string) *MemoryPipeline {
	return fr.collector.Pipeline(sessionID)
}

// Replay creates a replay session for a task.
func (fr *FlightRecorder) Replay(ctx context.Context, taskID string) (*ReplaySession, error) {
	return NewReplaySession(ctx, fr.eventStore, taskID)
}

// Snapshot returns a point-in-time snapshot of all flight data for an agent.
func (fr *FlightRecorder) Snapshot(agentID string) AgentSnapshot {
	return AgentSnapshot{
		AgentID:     agentID,
		Timeline:    fr.Timeline().FilterByAgent(agentID),
		Decisions:   fr.Decisions().FilterByAgent(agentID),
		Diagnostics: fr.Diagnostics().FilterByAgent(agentID),
	}
}

// AgentSnapshot is a point-in-time view of all flight data for one agent.
type AgentSnapshot struct {
	AgentID     string             `json:"agent_id"`
	Timeline    []TimelineEvent    `json:"timeline"`
	Decisions   []Decision         `json:"decisions"`
	Diagnostics []DiagnosticRecord `json:"diagnostics"`
}

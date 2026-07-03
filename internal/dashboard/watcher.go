// Package dashboard — proactive agent watcher.
//
// AgentWatcher continuously monitors agent health from the intelligence
// engine and pushes proactive state updates through the WebSocket hub.
// This replaces the passive/polled dashboard model with real-time push.
package dashboard

import (
	"context"
	"time"
)

// AgentWatcherConfig controls the watcher's polling and push behavior.
type AgentWatcherConfig struct {
	// PollInterval is how often to recompute health scores.
	PollInterval time.Duration
	// PushInterval is how often to push health snapshots to WebSocket clients.
	PushInterval time.Duration
}

// DefaultAgentWatcherConfig returns sensible defaults.
func DefaultAgentWatcherConfig() AgentWatcherConfig {
	return AgentWatcherConfig{
		PollInterval: 30 * time.Second,
		PushInterval: 5 * time.Second,
	}
}

// AgentWatcher monitors agent health and pushes proactive updates.
type AgentWatcher struct {
	intel    *Engine
	hub      *WSHub
	cfg      AgentWatcherConfig
	agentIDs []string
	lister   AgentLister
}

// AgentLister provides the list of known agent IDs for auto-discovery.
type AgentLister interface {
	ListAgentIDs() []string
}

// NewAgentWatcher creates a watcher that pushes health updates to the hub.
func NewAgentWatcher(intel *Engine, hub *WSHub, lister AgentLister, cfg AgentWatcherConfig) *AgentWatcher {
	return &AgentWatcher{
		intel: intel,
		hub:   hub,
		cfg:   cfg,
		lister: lister,
	}
}

// Start begins proactive monitoring. Runs until ctx is cancelled.
func (w *AgentWatcher) Start(ctx context.Context) {
	if w.intel == nil || w.hub == nil {
		return
	}

	// Push health snapshot proactively.
	go w.pushLoop(ctx)

	// Register insight callback for immediate push.
	w.intel.OnInsight(func(in *Insight) {
		w.hub.BroadcastToChannel(WSChannelEvents, &WSMessage{
			Type: "insight",
			Data: in,
			TS:   time.Now(),
		})
	})
}

func (w *AgentWatcher) pushLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PushInterval)
	defer ticker.Stop()

	var lastAnomalyCount int

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.discoverAgents()
			w.pushHealth()
			w.pushAnomalies(&lastAnomalyCount)
		}
	}
}

func (w *AgentWatcher) discoverAgents() {
	if w.lister == nil {
		return
	}
	ids := w.lister.ListAgentIDs()
	for _, id := range ids {
		found := false
		for _, known := range w.agentIDs {
			if known == id {
				found = true
				break
			}
		}
		if !found {
			w.agentIDs = append(w.agentIDs, id)
			// Notify hub of new agent discovery.
			if w.hub != nil {
				w.hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
					Type: "agent_discovered",
					Data: map[string]string{"agent_id": id},
					TS:   time.Now(),
				})
			}
		}
	}
}

func (w *AgentWatcher) pushHealth() {
	system := w.intel.SystemHealth()
	agents := w.intel.AllHealth()

	w.hub.BroadcastToChannel(WSChannelEvents, &WSMessage{
		Type: "health",
		Data: map[string]any{
			"system": system,
			"agents": agents,
			"ts":     time.Now(),
		},
		TS: time.Now(),
	})
}

func (w *AgentWatcher) pushAnomalies(lastCount *int) {
	anomalies := w.intel.Anomalies()
	if len(anomalies) != *lastCount {
		*lastCount = len(anomalies)
		w.hub.BroadcastToChannel(WSChannelEvents, &WSMessage{
			Type: "anomalies",
			Data: anomalies,
			TS:   time.Now(),
		})
	}

	insights := w.intel.Insights()
	if len(insights) > 0 {
		w.hub.BroadcastToChannel(WSChannelEvents, &WSMessage{
			Type: "insights",
			Data: insights,
			TS:   time.Now(),
		})
	}
}

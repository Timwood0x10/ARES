package resurrection

import (
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
)

// HeartbeatAdapter adapts ahp.HeartbeatMonitor to the HealthChecker interface.
// This decouples the resurrection plugin from the AHP package.
type HeartbeatAdapter struct {
	mon       *ahp.HeartbeatMonitor
	mu        sync.RWMutex
	onFailure func(agentID string)
}

// NewHeartbeatAdapter creates a HealthChecker backed by HeartbeatMonitor.
// Returns nil if mon is nil.
func NewHeartbeatAdapter(mon *ahp.HeartbeatMonitor) *HeartbeatAdapter {
	if mon == nil {
		return nil
	}
	a := &HeartbeatAdapter{mon: mon}
	mon.RegisterCallback(func(agentID string) {
		a.mu.RLock()
		fn := a.onFailure
		a.mu.RUnlock()
		if fn != nil {
			fn(agentID)
		}
	})
	return a
}

// RegisterAgent registers an agent for heartbeat monitoring.
func (a *HeartbeatAdapter) RegisterAgent(agentID string) {
	a.mon.RecordHeartbeat(agentID)
}

// UnregisterAgent removes an agent from heartbeat monitoring.
func (a *HeartbeatAdapter) UnregisterAgent(agentID string) {
	a.mon.RemoveAgent(agentID)
}

// RecordAlive signals that an agent is alive by recording a heartbeat.
func (a *HeartbeatAdapter) RecordAlive(agentID string) {
	a.mon.RecordHeartbeat(agentID)
}

// CheckHealth returns IDs of agents that have timed out.
func (a *HeartbeatAdapter) CheckHealth() []string {
	timedOut := a.mon.CheckTimeouts()
	if timedOut == nil {
		return []string{}
	}
	return timedOut
}

// OnFailure registers a callback invoked when an agent fails.
func (a *HeartbeatAdapter) OnFailure(fn func(agentID string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onFailure = fn
}

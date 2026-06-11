// Package main demonstrates generic agent failover — any agent type can be
// automatically recovered when it fails (not just the leader).
//
// Pattern: HeartbeatMonitor detects failure → Factory creates replacement →
// new agent starts with fresh state.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"goagent/internal/agents/base"
	"goagent/internal/core/models"
	"goagent/internal/protocol/ahp"
)

// ---------- Mock Agent (simulates any agent type) ----------

type workerAgent struct {
	id        string
	agentType models.AgentType
	status    models.AgentStatus
	taskCount atomic.Int64
	mu        sync.Mutex
}

func newWorker(id string, agentType models.AgentType) *workerAgent {
	return &workerAgent{id: id, agentType: agentType}
}

func (a *workerAgent) ID() string                     { return a.id }
func (a *workerAgent) Type() models.AgentType         { return a.agentType }
func (a *workerAgent) Status() models.AgentStatus     { a.mu.Lock(); defer a.mu.Unlock(); return a.status }
func (a *workerAgent) setStatus(s models.AgentStatus) { a.mu.Lock(); a.status = s; a.mu.Unlock() }

func (a *workerAgent) Start(_ context.Context) error {
	a.setStatus(models.AgentStatusReady)
	return nil
}

func (a *workerAgent) Stop(_ context.Context) error {
	a.setStatus(models.AgentStatusOffline)
	return nil
}

func (a *workerAgent) Process(_ context.Context, _ any) (any, error) {
	if a.Status() != models.AgentStatusReady {
		return nil, fmt.Errorf("agent %s is not ready (status: %s)", a.id, a.Status())
	}
	a.taskCount.Add(1)
	return fmt.Sprintf("task-%d-done", a.taskCount.Load()), nil
}

func (a *workerAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	close(ch)
	return ch, nil
}

// ---------- Generic Agent Supervisor ----------

// AgentSupervisor monitors ANY agent type and resurrects it on failure.
type AgentSupervisor struct {
	mu         sync.RWMutex
	agents     map[string]base.Agent
	factories  map[string]func() base.Agent // agentID → factory
	hbMon      *ahp.HeartbeatMonitor
	alive      map[string]bool
	resurrects int
}

func NewAgentSupervisor(hbMon *ahp.HeartbeatMonitor) *AgentSupervisor {
	return &AgentSupervisor{
		agents:    make(map[string]base.Agent),
		factories: make(map[string]func() base.Agent),
		hbMon:     hbMon,
		alive:     make(map[string]bool),
	}
}

// Register registers an agent with a factory for resurrection.
func (s *AgentSupervisor) Register(agent base.Agent, factory func() base.Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := agent.ID()
	s.agents[id] = agent
	s.factories[id] = factory
	s.alive[id] = true
	s.hbMon.RecordHeartbeat(id)
}

// Start begins monitoring. Call this after all agents are registered.
func (s *AgentSupervisor) Start(ctx context.Context) {
	// Register callback for timeout detection.
	s.hbMon.RegisterCallback(func(agentID string) {
		s.resurrect(ctx, agentID)
	})

	// Background: send heartbeats for alive agents + check for timeouts.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Send heartbeats for alive agents only.
				s.mu.RLock()
				for id, alive := range s.alive {
					if alive {
						s.hbMon.RecordHeartbeat(id)
					}
				}
				s.mu.RUnlock()

				// Check for timed-out agents (triggers callback → resurrect).
				s.hbMon.CheckTimeouts()
			}
		}
	}()
}

// KillAgent simulates an agent crash (stops sending heartbeats).
func (s *AgentSupervisor) KillAgent(id string) {
	s.mu.Lock()
	s.alive[id] = false
	s.mu.Unlock()
	// Stop the agent so it can't process requests.
	if agent, ok := s.agents[id]; ok {
		_ = agent.Stop(context.Background())
	}
	fmt.Printf("[KILL] Agent %s killed\n", id)
}

// resurrect creates a new agent instance via factory.
func (s *AgentSupervisor) resurrect(ctx context.Context, agentID string) {
	s.mu.Lock()
	factory, exists := s.factories[agentID]
	if !exists {
		s.mu.Unlock()
		return
	}

	// Create new instance.
	newAgent := factory()
	s.agents[agentID] = newAgent
	s.alive[agentID] = true
	s.resurrects++
	s.mu.Unlock()

	// Start the new agent.
	if err := newAgent.Start(ctx); err != nil {
		log.Printf("[RESURRECT] Failed to start new %s: %v", agentID, err)
		return
	}

	// Register heartbeat for the new instance.
	s.hbMon.RecordHeartbeat(agentID)

	fmt.Printf("[RESURRECT] Agent %s resurrected (total: %d)\n", agentID, s.resurrects)
}

// GetAgent returns the current instance of an agent.
func (s *AgentSupervisor) GetAgent(id string) base.Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agents[id]
}

// Stats returns supervisor statistics.
func (s *AgentSupervisor) Stats() (alive int, resurrects int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.alive {
		if a {
			alive++
		}
	}
	return alive, s.resurrects
}

// ---------- Main ----------

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create heartbeat monitor with aggressive timeouts for demo.
	hbMon := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  2 * time.Second,
		Timeout:   3 * time.Second,
		MaxMissed: 2,
	})

	supervisor := NewAgentSupervisor(hbMon)

	// Register 3 different agent types, each with a factory.
	agents := []struct {
		id        string
		agentType models.AgentType
	}{
		{"worker-1", models.AgentTypeBottom},
		{"worker-2", models.AgentTypeBottom},
		{"planner-1", models.AgentTypeLeader},
	}

	for _, a := range agents {
		agent := newWorker(a.id, a.agentType)
		if err := agent.Start(ctx); err != nil {
			log.Fatalf("failed to start %s: %v", a.id, err)
		}
		id, at := a.id, a.agentType // capture for closure
		supervisor.Register(agent, func() base.Agent {
			return newWorker(id, at)
		})
		fmt.Printf("[INIT] Registered %s (%s)\n", id, at)
	}

	supervisor.Start(ctx)
	fmt.Println()

	// Phase 1: Normal operation.
	fmt.Println("=== Phase 1: Normal Operation ===")
	for i := 0; i < 3; i++ {
		for _, a := range agents {
			agent := supervisor.GetAgent(a.id)
			result, _ := agent.Process(ctx, nil)
			fmt.Printf("  %s → %v\n", a.id, result)
		}
	}
	alive, resurrections := supervisor.Stats()
	fmt.Printf("  Stats: %d alive, %d resurrects\n\n", alive, resurrections)

	// Phase 2: Kill worker-1, wait for resurrection.
	fmt.Println("=== Phase 2: Kill worker-1 ===")
	supervisor.KillAgent("worker-1")

	// Wait for heartbeat timeout + resurrection.
	fmt.Println("  Waiting for heartbeat timeout...")
	time.Sleep(8 * time.Second)

	alive, resurrections = supervisor.Stats()
	fmt.Printf("  Stats: %d alive, %d resurrects\n", alive, resurrections)

	// Verify the resurrected agent works.
	agent := supervisor.GetAgent("worker-1")
	result, _ := agent.Process(ctx, nil)
	fmt.Printf("  worker-1 after resurrection → %v\n\n", result)

	// Phase 3: Kill planner-1, verify other agents unaffected.
	fmt.Println("=== Phase 3: Kill planner-1 ===")
	supervisor.KillAgent("planner-1")

	time.Sleep(8 * time.Second)

	alive, resurrections = supervisor.Stats()
	fmt.Printf("  Stats: %d alive, %d resurrects\n", alive, resurrections)

	// Verify worker-2 was never affected.
	agent = supervisor.GetAgent("worker-2")
	result, _ = agent.Process(ctx, nil)
	fmt.Printf("  worker-2 (never killed) → %v\n\n", result)

	// Final stats.
	fmt.Println("=== Final Stats ===")
	alive, resurrections = supervisor.Stats()
	fmt.Printf("  Alive: %d\n", alive)
	fmt.Printf("  Total Resurrections: %d\n", resurrections)
	fmt.Println()

	// List all agents and their status.
	fmt.Println("=== Agent Status ===")
	for _, a := range agents {
		agent := supervisor.GetAgent(a.id)
		fmt.Printf("  %s: %s\n", a.id, agent.Status())
	}

	fmt.Println("\nAgent Resurrection example completed!")
	fmt.Println("Key insight: ANY agent type can be monitored and resurrected.")
	fmt.Println("The factory function creates a fresh instance with no stale state.")
}

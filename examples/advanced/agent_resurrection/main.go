// Package main demonstrates generic agent resurrection using the plugin.
// ANY agent type (leader, worker, planner) can be monitored and resurrected.
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
	"goagent/internal/plugins/resurrection"
	"goagent/internal/protocol/ahp"
)

// workerAgent simulates any agent type.
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
		return nil, fmt.Errorf("agent %s not ready", a.id)
	}
	a.taskCount.Add(1)
	return fmt.Sprintf("task-%d-done", a.taskCount.Load()), nil
}

func (a *workerAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	close(ch)
	return ch, nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// Create heartbeat monitor.
	hbMon := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  2 * time.Second,
		Timeout:   3 * time.Second,
		MaxMissed: 2,
	})

	// Create resurrection plugin.
	health := resurrection.NewHeartbeatAdapter(hbMon)
	supervisor, err := resurrection.New(health, resurrection.Config{
		CheckInterval:     3 * time.Second,
		HeartbeatInterval: 2 * time.Second,
	}, nil)
	if err != nil {
		log.Fatalf("failed to create supervisor: %v", err)
	}

	// Register 3 different agent types.
	type agentDef struct {
		id        string
		agentType models.AgentType
	}
	defs := []agentDef{
		{"worker-1", models.AgentTypeBottom},
		{"worker-2", models.AgentTypeBottom},
		{"planner-1", models.AgentTypeLeader},
	}

	for _, d := range defs {
		agent := newWorker(d.id, d.agentType)
		if err := agent.Start(ctx); err != nil {
			log.Fatalf("failed to start %s: %v", d.id, err)
		}
		id, at := d.id, d.agentType
		supervisor.Watch(agent, func() base.Agent {
			fmt.Printf("  [FACTORY] Creating new %s instance\n", id)
			return newWorker(id, at)
		})
		fmt.Printf("[INIT] Registered %s (%s)\n", id, at)
	}

	// Start monitoring.
	if err := supervisor.Start(ctx); err != nil {
		log.Fatalf("failed to start supervisor: %v", err)
	}
	fmt.Println()

	// Phase 1: Normal operation.
	fmt.Println("=== Phase 1: Normal Operation ===")
	for _, d := range defs {
		agent := supervisor.Agent(d.id)
		result, _ := agent.Process(ctx, nil)
		fmt.Printf("  %s: %v\n", d.id, result)
	}
	fmt.Printf("  Stats: %+v\n\n", supervisor.Stats())

	// Phase 2: Kill worker-1.
	fmt.Println("=== Phase 2: Kill worker-1 ===")
	_ = supervisor.Agent("worker-1").Stop(ctx)
	fmt.Println("  worker-1 killed, waiting for resurrection...")
	time.Sleep(10 * time.Second)

	agent := supervisor.Agent("worker-1")
	if agent != nil && agent.Status() == models.AgentStatusReady {
		result, _ := agent.Process(ctx, nil)
		fmt.Printf("  worker-1 resurrected: %v\n", result)
	}
	fmt.Printf("  Stats: %+v\n\n", supervisor.Stats())

	// Phase 3: Kill planner-1, verify worker-2 unaffected.
	fmt.Println("=== Phase 3: Kill planner-1 ===")
	_ = supervisor.Agent("planner-1").Stop(ctx)
	time.Sleep(10 * time.Second)

	agent = supervisor.Agent("worker-2")
	result, _ := agent.Process(ctx, nil)
	fmt.Printf("  worker-2 (never killed): %v\n", result)
	fmt.Printf("  Stats: %+v\n\n", supervisor.Stats())

	// Final.
	fmt.Println("=== Final ===")
	stats := supervisor.Stats()
	fmt.Printf("  Alive: %d, Resurrects: %d\n", stats.Alive, stats.Resurrects)
	for id, st := range stats.Statuses {
		fmt.Printf("  %s: %s\n", id, st)
	}

	_ = supervisor.Stop()
	fmt.Println("\nAgent Resurrection example completed!")
}

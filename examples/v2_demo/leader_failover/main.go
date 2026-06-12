// Package main demonstrates Leader Failover using the resurrection plugin.
// The supervisor monitors ANY agent type and resurrects it on failure.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"goagentx/internal/agents/base"
	"goagentx/internal/core/models"
	"goagentx/internal/plugins/resurrection"
	"goagentx/internal/protocol/ahp"
)

// leaderAgent is a minimal leader agent for demonstration.
type leaderAgent struct {
	id     string
	status models.AgentStatus
}

func (a *leaderAgent) ID() string                    { return a.id }
func (a *leaderAgent) Type() models.AgentType        { return models.AgentTypeLeader }
func (a *leaderAgent) Status() models.AgentStatus    { return a.status }
func (a *leaderAgent) Start(_ context.Context) error { a.status = models.AgentStatusReady; return nil }
func (a *leaderAgent) Stop(_ context.Context) error  { a.status = models.AgentStatusOffline; return nil }
func (a *leaderAgent) Process(_ context.Context, _ any) (any, error) {
	return "leader processed", nil
}
func (a *leaderAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	close(ch)
	return ch, nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 1. Create heartbeat monitor with aggressive timeouts for demo.
	hbMon := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  2 * time.Second,
		Timeout:   3 * time.Second,
		MaxMissed: 2,
	})

	// 2. Create resurrection plugin with AHP adapter.
	health := resurrection.NewHeartbeatAdapter(hbMon)
	supervisor, err := resurrection.New(health, resurrection.Config{
		CheckInterval:     3 * time.Second,
		HeartbeatInterval: 2 * time.Second,
		ResurrectTimeout:  10 * time.Second,
		MaxAttempts:       3,
	}, nil)
	if err != nil {
		log.Fatalf("failed to create supervisor: %v", err)
	}

	// 3. Register leader agent with a factory.
	leader := &leaderAgent{id: "leader-1", status: models.AgentStatusReady}
	supervisor.Watch(leader, func() base.Agent {
		fmt.Println("  [FACTORY] Creating new leader instance...")
		return &leaderAgent{id: "leader-1"}
	})

	// 4. Start monitoring.
	if err := supervisor.Start(ctx); err != nil {
		log.Fatalf("failed to start supervisor: %v", err)
	}

	// 5. Normal operation.
	fmt.Println("=== Normal Operation ===")
	result, _ := supervisor.Agent("leader-1").Process(ctx, nil)
	fmt.Printf("  leader-1: %v\n", result)
	fmt.Printf("  Stats: %+v\n\n", supervisor.Stats())

	// 6. Simulate leader crash (stop heartbeats + mark offline).
	fmt.Println("=== Simulating Leader Crash ===")
	_ = supervisor.Agent("leader-1").Stop(ctx)
	fmt.Println("  leader-1 stopped, waiting for resurrection...")

	// 7. Wait for heartbeat timeout + resurrection.
	time.Sleep(10 * time.Second)

	// 8. Verify resurrection.
	fmt.Println("\n=== After Resurrection ===")
	agent := supervisor.Agent("leader-1")
	if agent != nil && agent.Status() == models.AgentStatusReady {
		result, _ := agent.Process(ctx, nil)
		fmt.Printf("  leader-1 resurrected: %v\n", result)
	} else {
		fmt.Println("  leader-1 not yet resurrected")
	}
	fmt.Printf("  Stats: %+v\n", supervisor.Stats())

	// 9. Cleanup.
	_ = supervisor.Stop()
	fmt.Println("\nLeader Failover example completed!")
}

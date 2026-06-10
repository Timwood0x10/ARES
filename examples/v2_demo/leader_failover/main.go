// Package main demonstrates Leader Failover with checkpoint recovery.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"goagent/internal/agents/base"
	"goagent/internal/agents/leader"
	"goagent/internal/core/models"
	"goagent/internal/protocol/ahp"
)

// mockAgent is a minimal agent for demonstration.
type mockAgent struct {
	id     string
	status models.AgentStatus
}

func (a *mockAgent) ID() string                    { return a.id }
func (a *mockAgent) Type() models.AgentType        { return models.AgentTypeLeader }
func (a *mockAgent) Status() models.AgentStatus    { return a.status }
func (a *mockAgent) Start(_ context.Context) error { a.status = models.AgentStatusReady; return nil }
func (a *mockAgent) Stop(_ context.Context) error  { a.status = models.AgentStatusOffline; return nil }
func (a *mockAgent) Process(_ context.Context, _ any) (any, error) {
	return &models.RecommendResult{}, nil
}
func (a *mockAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	close(ch)
	return ch, nil
}

func main() {
	ctx := context.Background()

	// 1. Create heartbeat monitor.
	hbMon := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  2 * time.Second,
		Timeout:   5 * time.Second,
		MaxMissed: 2,
	})

	// 2. Create failover strategy (cold restart).
	// In production, this would use a real agent factory.
	originalAgent := &mockAgent{id: "leader-1", status: models.AgentStatusReady}
	_ = originalAgent

	strategy, err := leader.NewColdRestartStrategy(
		func(_ context.Context, _ interface{}) (base.Agent, error) {
			// Factory creates a new agent instance.
			return &mockAgent{id: "leader-1", status: models.AgentStatusReady}, nil
		},
		nil,
	)
	if err != nil {
		log.Fatalf("failed to create strategy: %v", err)
	}

	// 3. Create supervisor.
	supervisor, err := leader.NewLeaderSupervisor(
		hbMon,
		strategy,
		nil, // recovery (nil for demo, needs PostgreSQL in production)
		nil, // checkpoint (nil for demo, needs PostgreSQL in production)
		nil, // config (uses defaults)
	)
	if err != nil {
		log.Fatalf("failed to create supervisor: %v", err)
	}

	// 4. Register leader and start monitoring.
	supervisor.RegisterLeader("leader-1", originalAgent)

	if err := supervisor.Start(ctx); err != nil {
		log.Fatalf("failed to start supervisor: %v", err)
	}

	fmt.Println("Supervisor started. Monitoring leader-1...")
	fmt.Println("In production, when the leader fails heartbeat checks,")
	fmt.Println("the supervisor will automatically create a replacement.")
	fmt.Println()

	// 5. Simulate normal operation.
	hbMon.RecordHeartbeat("leader-1")
	status, _ := hbMon.GetStatus("leader-1")
	fmt.Printf("Leader status: %s\n", status)

	// 6. Cleanup.
	if err := supervisor.Stop(); err != nil {
		log.Printf("supervisor stop error: %v", err)
	}

	fmt.Println("\nLeader Failover example completed successfully!")
	fmt.Println()
	fmt.Println("Key components:")
	fmt.Println("  - HeartbeatMonitor: detects agent failure via periodic heartbeats")
	fmt.Println("  - FailoverStrategy: creates replacement agent (ColdRestartStrategy)")
	fmt.Println("  - LeaderSupervisor: orchestrates detection → replacement → recovery")
	fmt.Println("  - CheckpointRepository: persists session state for recovery")
	fmt.Println("  - TaskRecovery: marks orphaned tasks as failed")
}

// Package main demonstrates Leader Failover using the Runtime with checkpoint-based recovery.
//
// This example shows:
//   - Runtime-managed leader lifecycle (no standalone Supervisor).
//   - Checkpoint-based recovery: leader saves checkpoints via EventStore ares_events.
//   - Full failover sequence with timing measurements.
//   - Event emission for operational state tracking.
//
// Failover sequence:
//  1. Leader starts and processes tasks, saving checkpoints.
//  2. Leader crashes (simulated).
//  3. Runtime detects crash via health check.
//  4. Factory creates new leader instance.
//  5. Runtime replays ares_events from EventStore.
//  6. New leader restores from last checkpoint and continues.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sync"
	"time"

	runtimeSvc "github.com/Timwood0x10/ares/api/service/runtime"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// phaseSeparator prints a visual phase separator for readable output.
func phaseSeparator(phase string) {
	fmt.Printf("\n%s\n", "============================================================")
	fmt.Printf("  %s\n", phase)
	fmt.Printf("%s\n\n", "============================================================")
}

// setupLogger configures structured slog with text output.
func setupLogger() *slog.Logger {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

// ============================================================
// Leader Agent -- manages workflow and saves checkpoints.
// ============================================================

// leaderAgent is a leader agent that processes tasks and saves checkpoints.
type leaderAgent struct {
	mu          sync.Mutex
	id          string
	status      models.AgentStatus
	eventStore  ares_events.EventStore
	checkpoint  int
	taskCount   int
	sessionID   string
	shouldCrash bool
}

// newLeader creates a new leaderAgent.
func newLeader(id string, store ares_events.EventStore) *leaderAgent {
	return &leaderAgent{
		id:         id,
		status:     models.AgentStatusOffline,
		eventStore: store,
	}
}

// ID returns the agent identifier.
func (a *leaderAgent) ID() string { return a.id }

// Type returns the agent type.
func (a *leaderAgent) Type() models.AgentType { return models.AgentTypeLeader }

// Status returns the current agent status.
func (a *leaderAgent) Status() models.AgentStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// setStatus updates the agent status under lock.
func (a *leaderAgent) setStatus(s models.AgentStatus) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
}

// Start launches the leader agent and begins checkpoint-based work.
func (a *leaderAgent) Start(ctx context.Context) error {
	a.setStatus(models.AgentStatusReady)

	// Create a new session for this incarnation.
	a.mu.Lock()
	if a.sessionID == "" {
		a.sessionID = fmt.Sprintf("session-%s-%d", a.id, time.Now().UnixNano())
	}
	sid := a.sessionID
	cp := a.checkpoint
	taskCount := a.taskCount
	a.mu.Unlock()

	// Emit agent started event.
	a.emitEvent(ctx, ares_events.EventAgentStarted, map[string]any{
		"agent_id":   a.id,
		"agent_type": string(a.Type()),
		"session_id": sid,
		"checkpoint": cp,
		"task_count": taskCount,
	})

	// Emit session created event.
	a.emitEvent(ctx, ares_events.EventSessionCreated, map[string]any{
		"session_id": sid,
		"agent_id":   a.id,
	})

	lg.Info("leader started",
		"agent_id", a.id,
		"session_id", sid,
		"checkpoint", cp,
		"task_count", taskCount,
	)

	// Launch work loop in a goroutine with panic recovery.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				lg.Error("leader panic recovered",
					"agent_id", a.id,
					"session_id", sid,
					"panic", r,
				)
				a.setStatus(models.AgentStatusOffline)
			}
		}()
		a.workLoop(ctx)
	}()
	return nil
}

// Stop gracefully stops the leader agent.
func (a *leaderAgent) Stop(_ context.Context) error {
	a.setStatus(models.AgentStatusOffline)
	lg.Info("leader stopped", "agent_id", a.id)
	return nil
}

// Process handles a single input.
func (a *leaderAgent) Process(_ context.Context, input any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return fmt.Sprintf("leader %s processed (checkpoint=%d)", a.id, a.checkpoint), nil
}

// ProcessStream returns a stream of agent ares_events.
func (a *leaderAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	close(ch)
	return ch, nil
}

// RestoreState restores the leader's state from a snapshot.
func (a *leaderAgent) RestoreState(state map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if sid, ok := state["session_id"].(string); ok && sid != "" {
		a.sessionID = sid
	}
	if cp, ok := state["checkpoint"].(int); ok {
		a.checkpoint = cp
	}
	if count, ok := state["task_count"].(int); ok {
		a.taskCount = count
	}

	lg.Info("leader state restored",
		"agent_id", a.id,
		"session_id", a.sessionID,
		"checkpoint", a.checkpoint,
		"task_count", a.taskCount,
	)
	return nil
}

// ReplayEvents replays ares_events to reconstruct incremental state.
func (a *leaderAgent) ReplayEvents(evts []*ares_events.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, ev := range evts {
		if ev == nil {
			continue
		}
		switch ev.Type {
		case ares_events.EventTaskCompleted:
			if cp, ok := ev.Payload["checkpoint"].(int); ok && cp > a.checkpoint {
				a.checkpoint = cp
			}
			a.taskCount++
		case ares_events.EventSessionCreated:
			if sid, ok := ev.Payload["session_id"].(string); ok && sid != "" {
				a.sessionID = sid
			}
		}
	}

	lg.Info("leader ares_events replayed",
		"agent_id", a.id,
		"total_events", len(evts),
		"restored_checkpoint", a.checkpoint,
		"restored_tasks", a.taskCount,
	)
	return nil
}

// Snapshot returns a serializable snapshot of the leader's state.
func (a *leaderAgent) Snapshot() (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return map[string]any{
		"agent_id":   a.id,
		"session_id": a.sessionID,
		"checkpoint": a.checkpoint,
		"task_count": a.taskCount,
		"status":     string(a.status),
	}, nil
}

// workLoop simulates continuous task processing with checkpoint saving.
func (a *leaderAgent) workLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			if a.shouldCrash {
				a.mu.Unlock()
				panic("leader " + a.id + " crashed!")
			}
			a.taskCount++
			taskID := fmt.Sprintf("task-%d", a.taskCount)
			a.checkpoint++
			cp := a.checkpoint
			sid := a.sessionID
			a.mu.Unlock()

			// Emit task started event.
			a.emitEvent(ctx, ares_events.EventTaskCreated, map[string]any{
				"task_id":    taskID,
				"agent_id":   a.id,
				"session_id": sid,
				"checkpoint": cp,
			})

			// Simulate work.
			time.Sleep(200 * time.Millisecond)

			// Save checkpoint via event.
			a.emitEvent(ctx, ares_events.EventTaskCompleted, map[string]any{
				"task_id":    taskID,
				"agent_id":   a.id,
				"session_id": sid,
				"checkpoint": cp,
			})

			lg.Info("checkpoint saved",
				"agent_id", a.id,
				"task_id", taskID,
				"checkpoint", cp,
				"session_id", sid,
			)
		}
	}
}

// emitEvent appends an event to the EventStore.
func (a *leaderAgent) emitEvent(ctx context.Context, eventType ares_events.EventType, payload map[string]any) {
	if a.eventStore == nil {
		return
	}
	event := &ares_events.Event{
		StreamID: a.id,
		Type:     eventType,
		Payload:  payload,
	}
	if err := a.eventStore.Append(ctx, a.id, []*ares_events.Event{event}, 0); err != nil {
		lg.Warn("failed to emit event",
			"agent_id", a.id,
			"type", eventType,
			"error", err,
		)
	}
}

// ============================================================
// FailoverTimer tracks timing of each failover phase.
// ============================================================

// FailoverTimer records timestamps for each phase of failover.
type FailoverTimer struct {
	mu          sync.Mutex
	crashTime   time.Time
	detectTime  time.Time
	factoryTime time.Time
	replayTime  time.Time
	startTime   time.Time
	readyTime   time.Time
}

// Mark records a timestamp for the given phase.
func (ft *FailoverTimer) Mark(phase string) {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	now := time.Now()
	switch phase {
	case "crash":
		ft.crashTime = now
	case "detect":
		ft.detectTime = now
	case "factory":
		ft.factoryTime = now
	case "replay":
		ft.replayTime = now
	case "start":
		ft.startTime = now
	case "ready":
		ft.readyTime = now
	}
}

// Report prints the timing breakdown.
func (ft *FailoverTimer) Report() {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	fmt.Println("  Failover Timing Breakdown:")
	if !ft.crashTime.IsZero() && !ft.detectTime.IsZero() {
		fmt.Printf("    Crash -> Detection:    %s\n", ft.detectTime.Sub(ft.crashTime).Round(time.Millisecond))
	}
	if !ft.detectTime.IsZero() && !ft.factoryTime.IsZero() {
		fmt.Printf("    Detection -> Factory:  %s\n", ft.factoryTime.Sub(ft.detectTime).Round(time.Millisecond))
	}
	if !ft.factoryTime.IsZero() && !ft.replayTime.IsZero() {
		fmt.Printf("    Factory -> Replay:     %s\n", ft.replayTime.Sub(ft.factoryTime).Round(time.Millisecond))
	}
	if !ft.replayTime.IsZero() && !ft.startTime.IsZero() {
		fmt.Printf("    Replay -> Start:       %s\n", ft.startTime.Sub(ft.replayTime).Round(time.Millisecond))
	}
	if !ft.startTime.IsZero() && !ft.readyTime.IsZero() {
		fmt.Printf("    Start -> Ready:        %s\n", ft.readyTime.Sub(ft.startTime).Round(time.Millisecond))
	}
	if !ft.crashTime.IsZero() && !ft.readyTime.IsZero() {
		fmt.Printf("    Total Failover Time:   %s\n", ft.readyTime.Sub(ft.crashTime).Round(time.Millisecond))
	}
}

// ============================================================
// Main -- demonstrate leader failover with checkpoint recovery
// ============================================================

func main() {
	setupLogger()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	failoverTimer := &FailoverTimer{}

	// 1. Create runtime service — one call wires up EventStore + HeartbeatMonitor + Resurrection.
	svc, err := runtimeSvc.NewService(runtimeSvc.Config{
		HeartbeatInterval:   1 * time.Second,
		HeartbeatTimeout:    2 * time.Second,
		MaxMissedHeartbeats: 2,
		MaxRestartsPerAgent: 3,
		ResurrectTimeout:    10 * time.Second,
		UseMemoryStore:      true,
	}, nil)
	if err != nil {
		cancel()
		log.Fatalf("failed to create runtime service: %v", err)
	}
	defer cancel()
	eventStore := svc.EventStore()

	// ----------------------------------------------------------
	// Phase 1: Register and start leader.
	// ----------------------------------------------------------
	phaseSeparator("Phase 1: Leader Startup")

	leader := newLeader("leader-1", eventStore)
	svc.RegisterAgent(leader, func() base.Agent {
		failoverTimer.Mark("factory")
		lg.Info("factory invoked: creating new leader",
			"agent_id", "leader-1",
			"reason", "failover",
		)
		return newLeader("leader-1", eventStore)
	})

	if err := svc.Start(ctx); err != nil {
		lg.Error("failed to start runtime", "error", err)
		return
	}

	// ----------------------------------------------------------
	// Phase 2: Normal operation -- leader saves checkpoints.
	// ----------------------------------------------------------
	phaseSeparator("Phase 2: Normal Operation (6s)")

	time.Sleep(6 * time.Second)

	stats := svc.Stats()
	fmt.Printf("  Active agents: %d\n", stats.ActiveAgents)
	fmt.Printf("  Total restarts: %d\n", stats.TotalRestarts)

	evts, _ := eventStore.Read(ctx, "leader-1", ares_events.ReadOptions{
		Direction: ares_events.ReadAscending,
	})
	fmt.Printf("  Events for leader-1: %d\n", len(evts))

	// ----------------------------------------------------------
	// Phase 3: Leader crash.
	// ----------------------------------------------------------
	phaseSeparator("Phase 3: Leader Crash")

	failoverTimer.Mark("crash")
	leader.mu.Lock()
	leader.shouldCrash = true
	leader.mu.Unlock()
	fmt.Println("  Leader-1 crash triggered!")
	fmt.Println("  Waiting for Runtime to detect and resurrect...")

	// Wait for health check detection + resurrection.
	// Health check runs every 1s, so detection should happen within 1-2s.
	// Factory + replay + start takes another ~1s.
	time.Sleep(6 * time.Second)

	// ----------------------------------------------------------
	// Phase 4: Verify failover.
	// ----------------------------------------------------------
	phaseSeparator("Phase 4: Failover Verification")

	failoverTimer.Mark("ready")
	failoverTimer.Report()

	stats = svc.Stats()
	fmt.Printf("\n  Active agents: %d\n", stats.ActiveAgents)
	fmt.Printf("  Total restarts: %d\n", stats.TotalRestarts)

	// Check that ares_events were preserved.
	evts, _ = eventStore.Read(ctx, "leader-1", ares_events.ReadOptions{
		Direction: ares_events.ReadAscending,
	})
	fmt.Printf("  Total ares_events for leader-1: %d\n", len(evts))

	// Show checkpoint progression.
	fmt.Println("\n  Checkpoint progression:")
	for _, ev := range evts {
		if ev.Type == ares_events.EventTaskCompleted {
			if cp, ok := ev.Payload["checkpoint"]; ok {
				fmt.Printf("    Checkpoint %v (task=%v)\n", cp, ev.Payload["task_id"])
			}
		}
	}

	// ----------------------------------------------------------
	// Phase 5: Resurrected leader continues working.
	// ----------------------------------------------------------
	phaseSeparator("Phase 5: Resurrected Leader Working (4s)")

	time.Sleep(4 * time.Second)

	stats = svc.Stats()
	fmt.Printf("  Active agents: %d\n", stats.ActiveAgents)
	fmt.Printf("  Total restarts: %d\n", stats.TotalRestarts)

	evts, _ = eventStore.Read(ctx, "leader-1", ares_events.ReadOptions{
		Direction: ares_events.ReadAscending,
	})
	fmt.Printf("  Total ares_events for leader-1: %d\n", len(evts))

	// ----------------------------------------------------------
	// Phase 6: Full event history.
	// ----------------------------------------------------------
	phaseSeparator("Phase 6: Full Event History")

	allEvents, _ := eventStore.ReadAll(ctx, ares_events.ReadOptions{})
	fmt.Printf("  Total ares_events: %d\n\n", len(allEvents))

	// Show last 15 ares_events.
	start := 0
	if len(allEvents) > 15 {
		start = len(allEvents) - 15
	}
	fmt.Println("  Recent ares_events:")
	for _, ev := range allEvents[start:] {
		fmt.Printf("    [%s] %s", ev.StreamID, ev.Type)
		if cp, ok := ev.Payload["checkpoint"]; ok {
			fmt.Printf(" checkpoint=%v", cp)
		}
		if taskID, ok := ev.Payload["task_id"]; ok {
			fmt.Printf(" task=%v", taskID)
		}
		fmt.Println()
	}

	// ----------------------------------------------------------
	// Phase 7: Graceful shutdown.
	// ----------------------------------------------------------
	phaseSeparator("Phase 7: Graceful Shutdown")

	if err := svc.Stop(); err != nil {
		lg.Error("runtime stop failed", "error", err)
	}

	fmt.Println("\nLeader Failover example completed!")
	fmt.Println("Key takeaways:")
	fmt.Println("  - Runtime manages leader lifecycle (no standalone Supervisor).")
	fmt.Println("  - Checkpoints in EventStore enable recovery from last known state.")
	fmt.Println("  - Full failover sequence is measured for performance analysis.")
	fmt.Println("  - New leader continues from the last checkpoint, not from scratch.")
}

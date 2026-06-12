// Package main demonstrates the complete agent lifecycle with Runtime, EventStore,
// MemoryManager, and MutableDAG workflow modification.
//
// This example shows the full lifecycle:
//  1. Create Runtime + EventStore + MemoryManager.
//  2. Register 4 agents: leader, worker-a, worker-b, planner.
//  3. Start all agents.
//  4. Worker-a processes 3 tasks, emits events.
//  5. Worker-a crashes (simulated panic).
//  6. Runtime detects crash -> creates new worker-a -> replays events -> restores state.
//  7. New worker-a continues from task 4.
//  8. Planner modifies the workflow (MutableDAG).
//  9. Worker-b processes tasks with the new workflow.
//
// 10. Graceful shutdown.
// 11. Print full event history.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"goagent/internal/agents/base"
	"goagent/internal/core/models"
	"goagent/internal/events"
	"goagent/internal/memory"
	"goagent/internal/runtime"
	"goagent/internal/workflow/engine"
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
// Lifecycle Agent -- generic agent used for leader, worker, planner roles.
// ============================================================

// lifecycleAgent is a generic agent that processes tasks and emits events.
type lifecycleAgent struct {
	mu          sync.Mutex
	id          string
	agentType   models.AgentType
	status      models.AgentStatus
	eventStore  events.EventStore
	memManager  memory.MemoryManager
	taskCount   atomic.Int64
	sessionID   string
	shouldCrash atomic.Bool
}

// newLifecycleAgent creates a new lifecycleAgent.
func newLifecycleAgent(
	id string,
	agentType models.AgentType,
	store events.EventStore,
	memMgr memory.MemoryManager,
) *lifecycleAgent {
	return &lifecycleAgent{
		id:         id,
		agentType:  agentType,
		status:     models.AgentStatusOffline,
		eventStore: store,
		memManager: memMgr,
	}
}

// ID returns the agent identifier.
func (a *lifecycleAgent) ID() string { return a.id }

// Type returns the agent type.
func (a *lifecycleAgent) Type() models.AgentType { return a.agentType }

// Status returns the current agent status.
func (a *lifecycleAgent) Status() models.AgentStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// setStatus updates the agent status under lock.
func (a *lifecycleAgent) setStatus(s models.AgentStatus) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
}

// getSessionID returns the current session ID under lock.
func (a *lifecycleAgent) getSessionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessionID
}

// setSessionID updates the session ID under lock.
func (a *lifecycleAgent) setSessionID(sid string) {
	a.mu.Lock()
	a.sessionID = sid
	a.mu.Unlock()
}

// Start launches the agent and begins task processing.
func (a *lifecycleAgent) Start(ctx context.Context) error {
	a.setStatus(models.AgentStatusReady)

	// Create or restore session.
	if a.getSessionID() == "" {
		a.setSessionID(fmt.Sprintf("session-%s-%d", a.id, time.Now().UnixNano()))
	}

	// Create session in MemoryManager if available.
	if a.memManager != nil {
		if _, err := a.memManager.CreateSession(ctx, a.id); err != nil {
			slog.Warn("failed to create memory session",
				"agent_id", a.id,
				"error", err,
			)
		}
	}

	// Emit agent started event.
	a.emitEvent(ctx, events.EventAgentStarted, map[string]any{
		"agent_id":   a.id,
		"agent_type": string(a.agentType),
		"session_id": a.getSessionID(),
	})

	// Emit session created event.
	a.emitEvent(ctx, events.EventSessionCreated, map[string]any{
		"session_id": a.getSessionID(),
		"agent_id":   a.id,
	})

	slog.Info("agent started",
		"agent_id", a.id,
		"type", a.agentType,
		"session_id", a.getSessionID(),
	)

	// Launch work loop in a goroutine with panic recovery.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("agent panic recovered",
					"agent_id", a.id,
					"type", a.agentType,
					"session_id", a.getSessionID(),
					"panic", r,
				)
				a.setStatus(models.AgentStatusOffline)
			}
		}()
		a.workLoop(ctx)
	}()
	return nil
}

// Stop gracefully stops the agent.
func (a *lifecycleAgent) Stop(_ context.Context) error {
	a.setStatus(models.AgentStatusOffline)
	slog.Info("agent stopped", "agent_id", a.id, "type", a.agentType)
	return nil
}

// Process handles a single input.
func (a *lifecycleAgent) Process(_ context.Context, _ any) (any, error) {
	return fmt.Sprintf("processed by %s", a.id), nil
}

// ProcessStream returns a stream of agent events.
func (a *lifecycleAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	close(ch)
	return ch, nil
}

// RestoreState restores the agent's state from a snapshot.
func (a *lifecycleAgent) RestoreState(state map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if sid, ok := state["session_id"].(string); ok && sid != "" {
		a.sessionID = sid
	}
	if count, ok := state["task_count"].(int64); ok {
		a.taskCount.Store(count)
	}

	slog.Info("state restored",
		"agent_id", a.id,
		"session_id", a.sessionID,
		"task_count", a.taskCount.Load(),
	)
	return nil
}

// ReplayEvents replays events to reconstruct incremental state.
func (a *lifecycleAgent) ReplayEvents(evts []*events.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, ev := range evts {
		if ev == nil {
			continue
		}
		switch ev.Type {
		case events.EventTaskCompleted:
			a.taskCount.Add(1)
		case events.EventSessionCreated:
			if sid, ok := ev.Payload["session_id"].(string); ok && sid != "" {
				a.sessionID = sid
			}
		}
	}

	slog.Info("events replayed",
		"agent_id", a.id,
		"total_events", len(evts),
		"restored_task_count", a.taskCount.Load(),
	)
	return nil
}

// Snapshot returns a serializable snapshot of the agent's current state.
func (a *lifecycleAgent) Snapshot() (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return map[string]any{
		"agent_id":   a.id,
		"session_id": a.sessionID,
		"task_count": a.taskCount.Load(),
		"status":     string(a.status),
	}, nil
}

// workLoop simulates continuous task processing.
func (a *lifecycleAgent) workLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.shouldCrash.Load() {
				panic("agent " + a.id + " crashed!")
			}

			a.taskCount.Add(1)
			taskID := fmt.Sprintf("task-%d", a.taskCount.Load())

			// Emit task created event.
			a.emitEvent(ctx, events.EventTaskCreated, map[string]any{
				"task_id":    taskID,
				"agent_id":   a.id,
				"session_id": a.getSessionID(),
			})

			// Store message in MemoryManager if available.
			if a.memManager != nil {
				msg := fmt.Sprintf("completed %s", taskID)
				if err := a.memManager.AddMessage(ctx, a.getSessionID(), "system", msg); err != nil {
					slog.Debug("failed to add memory message",
						"agent_id", a.id,
						"error", err,
					)
				}
			}

			// Simulate work.
			time.Sleep(300 * time.Millisecond)

			// Emit task completed event.
			a.emitEvent(ctx, events.EventTaskCompleted, map[string]any{
				"task_id":    taskID,
				"agent_id":   a.id,
				"session_id": a.getSessionID(),
				"result":     fmt.Sprintf("completed by %s", a.id),
			})

			slog.Info("task completed",
				"agent_id", a.id,
				"task_id", taskID,
				"session_id", a.getSessionID(),
				"total_tasks", a.taskCount.Load(),
			)
		}
	}
}

// emitEvent appends an event to the EventStore.
func (a *lifecycleAgent) emitEvent(ctx context.Context, eventType events.EventType, payload map[string]any) {
	if a.eventStore == nil {
		return
	}
	event := &events.Event{
		StreamID: a.id,
		Type:     eventType,
		Payload:  payload,
	}
	if err := a.eventStore.Append(ctx, a.id, []*events.Event{event}, 0); err != nil {
		slog.Warn("failed to emit event",
			"agent_id", a.id,
			"type", eventType,
			"error", err,
		)
	}
}

// ============================================================
// Workflow Planner -- demonstrates MutableDAG workflow modification.
// ============================================================

// workflowPlanner manages and modifies the workflow DAG.
type workflowPlanner struct {
	dag *engine.MutableDAG
}

// newWorkflowPlanner creates a workflowPlanner with an initial DAG.
func newWorkflowPlanner(initialSteps []*engine.Step) (*workflowPlanner, error) {
	dag, err := engine.NewMutableDAG(initialSteps)
	if err != nil {
		return nil, fmt.Errorf("create mutable DAG: %w", err)
	}
	return &workflowPlanner{dag: dag}, nil
}

// GetCurrentOrder returns the current execution order.
func (p *workflowPlanner) GetCurrentOrder() ([]string, error) {
	return p.dag.GetExecutionOrder()
}

// AddStep adds a new step to the workflow.
func (p *workflowPlanner) AddStep(ctx context.Context, step *engine.Step) error {
	return p.dag.AddNode(ctx, step)
}

// RemoveStep removes a step from the workflow.
func (p *workflowPlanner) RemoveStep(ctx context.Context, stepID string) error {
	return p.dag.RemoveNode(ctx, stepID)
}

// Version returns the current DAG version.
func (p *workflowPlanner) Version() uint64 {
	return p.dag.Version()
}

// ============================================================
// Main -- demonstrate the complete agent lifecycle
// ============================================================

func main() {
	setupLogger()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// ----------------------------------------------------------
	// Step 1: Create Runtime + EventStore + MemoryManager.
	// ----------------------------------------------------------
	phaseSeparator("Step 1: Create Infrastructure")

	eventStore := events.NewMemoryEventStore()
	memManager, err := memory.NewMemoryManager(memory.DefaultMemoryConfig())
	if err != nil {
		slog.Error("failed to create memory manager", "error", err)
		return
	}
	if err := memManager.Start(ctx); err != nil {
		slog.Error("failed to start memory manager", "error", err)
		return
	}

	rtConfig := &runtime.Config{
		HealthCheckInterval: 1 * time.Second,
		MaxRestartsPerAgent: 5,
		MaxReplayEvents:     1000,
	}
	rt := runtime.New(rtConfig, eventStore, memManager)

	fmt.Println("  Infrastructure created:")
	fmt.Println("    - MemoryEventStore (in-memory)")
	fmt.Println("    - MemoryManager (in-memory)")
	fmt.Println("    - Runtime (with health checks every 1s)")

	// ----------------------------------------------------------
	// Step 2: Register 4 agents.
	// ----------------------------------------------------------
	phaseSeparator("Step 2: Register Agents")

	leader := newLifecycleAgent("leader-1", models.AgentTypeLeader, eventStore, memManager)
	workerA := newLifecycleAgent("worker-a", models.AgentTypeBottom, eventStore, memManager)
	workerB := newLifecycleAgent("worker-b", models.AgentTypeBottom, eventStore, memManager)
	planner := newLifecycleAgent("planner-1", models.AgentTypeTop, eventStore, memManager)

	rt.RegisterAgent(leader, func() base.Agent {
		slog.Info("factory invoked", "agent_id", "leader-1")
		return newLifecycleAgent("leader-1", models.AgentTypeLeader, eventStore, memManager)
	})
	rt.RegisterAgent(workerA, func() base.Agent {
		slog.Info("factory invoked", "agent_id", "worker-a")
		return newLifecycleAgent("worker-a", models.AgentTypeBottom, eventStore, memManager)
	})
	rt.RegisterAgent(workerB, func() base.Agent {
		slog.Info("factory invoked", "agent_id", "worker-b")
		return newLifecycleAgent("worker-b", models.AgentTypeBottom, eventStore, memManager)
	})
	rt.RegisterAgent(planner, func() base.Agent {
		slog.Info("factory invoked", "agent_id", "planner-1")
		return newLifecycleAgent("planner-1", models.AgentTypeTop, eventStore, memManager)
	})

	fmt.Println("  Registered agents:")
	fmt.Println("    - leader-1   (leader)")
	fmt.Println("    - worker-a   (worker)")
	fmt.Println("    - worker-b   (worker)")
	fmt.Println("    - planner-1  (planner)")

	// ----------------------------------------------------------
	// Step 3: Start all agents.
	// ----------------------------------------------------------
	phaseSeparator("Step 3: Start All Agents")

	if err := rt.Start(ctx); err != nil {
		slog.Error("failed to start runtime", "error", err)
		return
	}

	stats := rt.Stats()
	fmt.Printf("  Active agents: %d\n", stats.ActiveAgents)

	// ----------------------------------------------------------
	// Step 4: Worker-a processes 3 tasks.
	// ----------------------------------------------------------
	phaseSeparator("Step 4: Worker-a Processes Tasks")

	// Wait for worker-a to process at least 3 tasks (2s interval, so ~6s).
	time.Sleep(7 * time.Second)

	// Take a snapshot before crash.
	snapshot, _ := workerA.Snapshot()
	fmt.Printf("  Worker-a snapshot before crash: %+v\n", snapshot)

	evtsA, _ := eventStore.Read(ctx, "worker-a", events.ReadOptions{
		Direction: events.ReadAscending,
	})
	fmt.Printf("  Worker-a events before crash: %d\n", len(evtsA))

	// ----------------------------------------------------------
	// Step 5: Worker-a crashes.
	// ----------------------------------------------------------
	phaseSeparator("Step 5: Worker-a Crash")

	preCrashCount := workerA.taskCount.Load()
	fmt.Printf("  Worker-a task count before crash: %d\n", preCrashCount)
	workerA.shouldCrash.Store(true)
	fmt.Println("  Worker-a crash triggered!")
	fmt.Println("  Waiting for Runtime to detect and resurrect...")

	// Wait for health check + resurrection.
	time.Sleep(5 * time.Second)

	// ----------------------------------------------------------
	// Step 6: Verify resurrection.
	// ----------------------------------------------------------
	phaseSeparator("Step 6: Resurrection Verification")

	stats = rt.Stats()
	fmt.Printf("  Active agents: %d\n", stats.ActiveAgents)
	fmt.Printf("  Total restarts: %d\n", stats.TotalRestarts)

	// Check events were preserved.
	evtsA, _ = eventStore.Read(ctx, "worker-a", events.ReadOptions{
		Direction: events.ReadAscending,
	})
	fmt.Printf("  Worker-a events after resurrection: %d\n", len(evtsA))

	// Check memory.
	messages, _ := memManager.GetMessages(ctx, workerA.getSessionID())
	fmt.Printf("  Memory entries for worker-a session: %d\n", len(messages))

	// ----------------------------------------------------------
	// Step 7: Resurrected worker-a continues from task 4+.
	// ----------------------------------------------------------
	phaseSeparator("Step 7: Resurrected Worker-a Continues")

	time.Sleep(5 * time.Second)

	stats = rt.Stats()
	fmt.Printf("  Active agents: %d\n", stats.ActiveAgents)

	evtsA, _ = eventStore.Read(ctx, "worker-a", events.ReadOptions{
		Direction: events.ReadAscending,
	})
	taskCompleted := 0
	for _, ev := range evtsA {
		if ev.Type == events.EventTaskCompleted {
			taskCompleted++
		}
	}
	fmt.Printf("  Worker-a total completed tasks (events): %d\n", taskCompleted)

	// ----------------------------------------------------------
	// Step 8: Planner modifies the workflow (MutableDAG).
	// ----------------------------------------------------------
	phaseSeparator("Step 8: Workflow Modification (MutableDAG)")

	initialSteps := []*engine.Step{
		{ID: "analyze-data", Name: "Analyze Data"},
		{ID: "process-results", Name: "Process Results", DependsOn: []string{"analyze-data"}},
		{ID: "generate-report", Name: "Generate Report", DependsOn: []string{"process-results"}},
	}

	wp, err := newWorkflowPlanner(initialSteps)
	if err != nil {
		slog.Error("failed to create workflow planner", "error", err)
		return
	}

	order, _ := wp.GetCurrentOrder()
	fmt.Printf("  Initial workflow: %v\n", order)
	fmt.Printf("  DAG version: %d\n", wp.Version())

	// Planner adds a new step: "validate-data" between analyze and process.
	fmt.Println("\n  Planner adding 'validate-data' step...")
	err = wp.AddStep(ctx, &engine.Step{
		ID:        "validate-data",
		Name:      "Validate Data",
		DependsOn: []string{"analyze-data"},
	})
	if err != nil {
		slog.Error("failed to add step", "error", err)
	} else {
		order, _ = wp.GetCurrentOrder()
		fmt.Printf("  Updated workflow: %v\n", order)
		fmt.Printf("  DAG version: %d\n", wp.Version())
	}

	// Planner adds "enrich-data" step.
	fmt.Println("\n  Planner adding 'enrich-data' step...")
	err = wp.AddStep(ctx, &engine.Step{
		ID:        "enrich-data",
		Name:      "Enrich Data",
		DependsOn: []string{"validate-data"},
	})
	if err != nil {
		slog.Error("failed to add step", "error", err)
	} else {
		order, _ = wp.GetCurrentOrder()
		fmt.Printf("  Updated workflow: %v\n", order)
		fmt.Printf("  DAG version: %d\n", wp.Version())
	}

	// Snapshot the DAG.
	snapshotDAG := wp.dag.Snapshot()
	fmt.Printf("\n  DAG snapshot: %d nodes, %d edges\n",
		len(snapshotDAG.Nodes), len(snapshotDAG.Edges))

	// ----------------------------------------------------------
	// Step 9: Worker-b processes with new workflow.
	// ----------------------------------------------------------
	phaseSeparator("Step 9: Worker-b Processes with New Workflow")

	fmt.Println("  Worker-b is processing tasks...")
	fmt.Printf("  Current workflow order: ")
	order, _ = wp.GetCurrentOrder()
	for i, step := range order {
		if i > 0 {
			fmt.Print(" -> ")
		}
		fmt.Print(step)
	}
	fmt.Println()

	time.Sleep(4 * time.Second)

	evtsB, _ := eventStore.Read(ctx, "worker-b", events.ReadOptions{
		Direction: events.ReadAscending,
	})
	taskCompletedB := 0
	for _, ev := range evtsB {
		if ev.Type == events.EventTaskCompleted {
			taskCompletedB++
		}
	}
	fmt.Printf("  Worker-b completed tasks: %d\n", taskCompletedB)

	// ----------------------------------------------------------
	// Step 10: Graceful shutdown.
	// ----------------------------------------------------------
	phaseSeparator("Step 10: Graceful Shutdown")

	// Stop memory manager.
	if err := memManager.Stop(ctx); err != nil {
		slog.Warn("memory manager stop failed", "error", err)
	}

	// Stop runtime (stops all agents).
	if err := rt.Stop(); err != nil {
		slog.Error("runtime stop failed", "error", err)
	}

	stats = rt.Stats()
	fmt.Printf("  Final active agents: %d\n", stats.ActiveAgents)
	fmt.Printf("  Final total restarts: %d\n", stats.TotalRestarts)
	fmt.Printf("  Total uptime: %s\n", stats.Uptime.Round(time.Second))

	// ----------------------------------------------------------
	// Step 11: Print full event history.
	// ----------------------------------------------------------
	phaseSeparator("Step 11: Full Event History")

	allEvents, _ := eventStore.ReadAll(ctx, events.ReadOptions{})
	fmt.Printf("  Total events in store: %d\n\n", len(allEvents))

	// Group events by agent.
	streamCounts := make(map[string]int)
	streamTypes := make(map[string]map[string]int)
	for _, ev := range allEvents {
		streamCounts[ev.StreamID]++
		if streamTypes[ev.StreamID] == nil {
			streamTypes[ev.StreamID] = make(map[string]int)
		}
		streamTypes[ev.StreamID][string(ev.Type)]++
	}

	fmt.Println("  Events per agent:")
	for stream, count := range streamCounts {
		fmt.Printf("    %s: %d events\n", stream, count)
		for evtType, typeCount := range streamTypes[stream] {
			fmt.Printf("      - %s: %d\n", evtType, typeCount)
		}
	}

	// Show timeline of significant events.
	fmt.Println("\n  Event timeline (task completions and agent lifecycle):")
	for _, ev := range allEvents {
		switch ev.Type {
		case events.EventAgentStarted:
			fmt.Printf("    [%s] %s started (session=%v)\n",
				ev.Timestamp.Format("15:04:05"), ev.StreamID, ev.Payload["session_id"])
		case events.EventTaskCompleted:
			fmt.Printf("    [%s] %s completed %v\n",
				ev.Timestamp.Format("15:04:05"), ev.StreamID, ev.Payload["task_id"])
		case events.EventSessionCreated:
			fmt.Printf("    [%s] %s session created: %v\n",
				ev.Timestamp.Format("15:04:05"), ev.StreamID, ev.Payload["session_id"])
		}
	}

	fmt.Println("\nFull Lifecycle example completed!")
	fmt.Println("Key takeaways:")
	fmt.Println("  - Runtime + EventStore + MemoryManager form the recovery infrastructure.")
	fmt.Println("  - StatefulAgent interface enables full state restoration after crash.")
	fmt.Println("  - MutableDAG allows runtime workflow modification by planner agents.")
	fmt.Println("  - Event history provides complete audit trail of agent activities.")
	fmt.Println("  - Multiple agents can crash and recover independently.")
}

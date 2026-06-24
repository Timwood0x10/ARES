// Package main demonstrates Runtime-managed agent resurrection with full state recovery.
//
// This example shows two dimensions of agent recovery:
//   - Operational recovery: EventStore replays events to reconstruct task state.
//   - Cognitive recovery: Simulated MemoryManager provides conversation context.
//
// Scenario:
//  1. Runtime starts 3 agents (leader, worker, planner), each with a session.
//  2. Each agent emits events to EventStore as it processes tasks.
//  3. Worker agent crashes (simulated panic).
//  4. Runtime detects the crash, creates new worker from factory.
//  5. Runtime replays events -> new worker restores operational state (task count, session).
//  6. Runtime enriches state with cognitive context (simulated conversation history).
//  7. New worker verifies restored state and continues from where it left off.
//
// This is "Resurrection" -- the agent dies, but its memories live on in EventStore.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"

	runtimeSvc "github.com/Timwood0x10/ares/api/service/runtime"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/events"
)

// phaseSeparator prints a visual phase separator for readable output.
func phaseSeparator(phase string) {
	fmt.Printf("\n%s\n", "============================================================")
	fmt.Printf("  %s\n", phase)
	fmt.Printf("%s\n\n", "============================================================")
}

// setupLogger configures structured slog with JSON output.
func setupLogger() *slog.Logger {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

// ============================================================
// CognitiveMemory simulates a MemoryManager for cognitive recovery.
// In production, this would be backed by the real MemoryManager.
// ============================================================

// CognitiveMemory stores simulated conversation history per session.
type CognitiveMemory struct {
	mu       sync.RWMutex
	sessions map[string][]string
}

// NewCognitiveMemory creates a new CognitiveMemory.
func NewCognitiveMemory() *CognitiveMemory {
	return &CognitiveMemory{
		sessions: make(map[string][]string),
	}
}

// AddMessage stores a message for a session.
func (c *CognitiveMemory) AddMessage(sessionID, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[sessionID] = append(c.sessions[sessionID], message)
}

// GetMessages returns all messages for a session.
func (c *CognitiveMemory) GetMessages(sessionID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessions[sessionID]
}

// ============================================================
// WorkerAgent -- a StatefulAgent that processes tasks and emits events.
// Implements RestoreState, ReplayEvents, and Snapshot for resurrection.
// ============================================================

// workerAgent is a task-processing agent with full state recovery support.
type workerAgent struct {
	mu           sync.Mutex
	id           string
	status       models.AgentStatus
	eventStore   events.EventStore
	cogMemory    *CognitiveMemory
	taskCount    atomic.Int64
	sessionID    string
	restoredFrom string // indicates how state was recovered
	shouldCrash  atomic.Bool
}

// newWorker creates a new workerAgent.
func newWorker(id string, store events.EventStore, cog *CognitiveMemory) *workerAgent {
	return &workerAgent{
		id:         id,
		status:     models.AgentStatusOffline,
		eventStore: store,
		cogMemory:  cog,
	}
}

// ID returns the agent identifier.
func (w *workerAgent) ID() string { return w.id }

// Type returns the agent type.
func (w *workerAgent) Type() models.AgentType { return models.AgentTypeBottom }

// Status returns the current agent status.
func (w *workerAgent) Status() models.AgentStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// setStatus updates the agent status under lock.
func (w *workerAgent) setStatus(s models.AgentStatus) {
	w.mu.Lock()
	w.status = s
	w.mu.Unlock()
}

// getSessionID returns the current session ID under lock.
func (w *workerAgent) getSessionID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sessionID
}

// setSessionID updates the session ID under lock.
func (w *workerAgent) setSessionID(sid string) {
	w.mu.Lock()
	w.sessionID = sid
	w.mu.Unlock()
}

// Start launches the agent and begins task processing.
func (w *workerAgent) Start(ctx context.Context) error {
	w.setStatus(models.AgentStatusReady)

	// Create or restore session.
	if w.getSessionID() == "" {
		w.setSessionID(fmt.Sprintf("session-%s-%d", w.id, time.Now().UnixNano()))
	}

	// Emit "agent started" event with session context.
	w.emitEvent(ctx, events.EventAgentStarted, map[string]any{
		"agent_id":   w.id,
		"agent_type": string(w.Type()),
		"session_id": w.getSessionID(),
	})

	if w.restoredFrom != "" {
		slog.Info("agent started (restored)",
			"agent_id", w.id,
			"session_id", w.getSessionID(),
			"restored_from", w.restoredFrom,
			"task_count", w.taskCount.Load(),
		)
	} else {
		slog.Info("agent started (fresh)",
			"agent_id", w.id,
			"session_id", w.getSessionID(),
		)
	}

	// Emit session event for future restoration.
	w.emitEvent(ctx, events.EventSessionCreated, map[string]any{
		"session_id": w.getSessionID(),
		"agent_id":   w.id,
	})

	// Launch work loop in a goroutine with panic recovery.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("agent panic recovered",
					"agent_id", w.id,
					"session_id", w.getSessionID(),
					"panic", r,
				)
				w.setStatus(models.AgentStatusOffline)
			}
		}()
		w.workLoop(ctx)
	}()
	return nil
}

// Stop gracefully stops the agent.
func (w *workerAgent) Stop(_ context.Context) error {
	w.setStatus(models.AgentStatusOffline)
	slog.Info("agent stopped", "agent_id", w.id, "session_id", w.getSessionID())
	return nil
}

// Process handles a single input.
func (w *workerAgent) Process(_ context.Context, input any) (any, error) {
	return fmt.Sprintf("processed by %s (session=%s)", w.id, w.getSessionID()), nil
}

// ProcessStream returns a stream of agent events.
func (w *workerAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	close(ch)
	return ch, nil
}

// RestoreState restores the agent's state from a snapshot map.
// This is called by the Runtime during resurrection after factory creation.
func (w *workerAgent) RestoreState(state map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if sid, ok := state["session_id"].(string); ok && sid != "" {
		w.sessionID = sid
		slog.Info("state restored: session_id",
			"agent_id", w.id,
			"session_id", sid,
		)
	}
	if count, ok := state["task_count"].(int64); ok {
		w.taskCount.Store(count)
		slog.Info("state restored: task_count",
			"agent_id", w.id,
			"task_count", count,
		)
	}
	w.restoredFrom = "snapshot+events"
	return nil
}

// ReplayEvents replays a sequence of events to reconstruct incremental state.
// Called after RestoreState to apply events that occurred after the snapshot.
func (w *workerAgent) ReplayEvents(evts []*events.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	replayed := 0
	for _, ev := range evts {
		if ev == nil {
			continue
		}
		switch ev.Type {
		case events.EventTaskCompleted:
			if taskID, ok := ev.Payload["task_id"].(string); ok {
				replayed++
				slog.Debug("replayed task event",
					"agent_id", w.id,
					"task_id", taskID,
				)
			}
		case events.EventSessionCreated:
			if sid, ok := ev.Payload["session_id"].(string); ok && sid != "" {
				w.sessionID = sid
			}
		}
	}

	// Restore task count from replayed events.
	w.taskCount.Store(int64(replayed))

	slog.Info("events replayed",
		"agent_id", w.id,
		"total_events", len(evts),
		"replayed_tasks", replayed,
	)
	return nil
}

// Snapshot returns a serializable snapshot of the agent's current state.
func (w *workerAgent) Snapshot() (map[string]any, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return map[string]any{
		"agent_id":    w.id,
		"session_id":  w.sessionID,
		"task_count":  w.taskCount.Load(),
		"status":      string(w.status),
		"snapshot_at": time.Now().Format(time.RFC3339),
	}, nil
}

// workLoop simulates continuous task processing with panic detection.
func (w *workerAgent) workLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.shouldCrash.Load() {
				panic("worker " + w.id + " crashed!")
			}

			w.taskCount.Add(1)
			taskID := fmt.Sprintf("task-%d", w.taskCount.Load())

			// Emit task created event.
			w.emitEvent(ctx, events.EventTaskCreated, map[string]any{
				"task_id":    taskID,
				"agent_id":   w.id,
				"session_id": w.getSessionID(),
			})

			// Simulate work duration.
			time.Sleep(time.Duration(rand.Intn(500)) * time.Millisecond)

			// Store cognitive memory entry.
			if w.cogMemory != nil {
				w.cogMemory.AddMessage(w.getSessionID(),
					fmt.Sprintf("completed %s at %s", taskID, time.Now().Format(time.RFC3339)))
			}

			// Emit task completed event.
			w.emitEvent(ctx, events.EventTaskCompleted, map[string]any{
				"task_id":    taskID,
				"agent_id":   w.id,
				"session_id": w.getSessionID(),
				"result":     fmt.Sprintf("completed by %s", w.id),
			})

			slog.Info("task completed",
				"agent_id", w.id,
				"task_id", taskID,
				"session_id", w.getSessionID(),
				"total_tasks", w.taskCount.Load(),
			)
		}
	}
}

// emitEvent appends an event to the EventStore.
func (w *workerAgent) emitEvent(ctx context.Context, eventType events.EventType, payload map[string]any) {
	if w.eventStore == nil {
		return
	}
	event := &events.Event{
		StreamID: w.id,
		Type:     eventType,
		Payload:  payload,
	}
	if err := w.eventStore.Append(ctx, w.id, []*events.Event{event}, 0); err != nil {
		slog.Warn("failed to emit event",
			"agent_id", w.id,
			"type", eventType,
			"error", err,
		)
	}
}

// verifyRestoredState checks that the resurrected agent has correct state.
func verifyRestoredState(agent *workerAgent, expectedMinTasks int64) bool {
	count := agent.taskCount.Load()
	session := agent.getSessionID()
	ok := count >= expectedMinTasks && session != ""
	slog.Info("restored state verification",
		"agent_id", agent.ID(),
		"task_count", count,
		"expected_min", expectedMinTasks,
		"session_id", session,
		"verified", ok,
	)
	return ok
}

// ============================================================
// Main -- demonstrate resurrection with two recovery dimensions
// ============================================================

func main() {
	setupLogger()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Shared infrastructure.
	cogMemory := NewCognitiveMemory()

	// Create runtime service — one call wires up EventStore + HeartbeatMonitor + Resurrection.
	svc, err := runtimeSvc.NewService(runtimeSvc.Config{
		HeartbeatInterval:   2 * time.Second,
		HeartbeatTimeout:    3 * time.Second,
		MaxMissedHeartbeats: 2,
		MaxRestartsPerAgent: 5,
		ResurrectTimeout:    10 * time.Second,
		UseMemoryStore:      true,
	}, nil)
	if err != nil {
		log.Fatalf("failed to create runtime service: %v", err)
	}
	eventStore := svc.EventStore()

	// ----------------------------------------------------------
	// Phase 1: Create and register agents.
	// ----------------------------------------------------------
	phaseSeparator("Phase 1: Registering Agents")

	leader := newWorker("leader-1", eventStore, cogMemory)
	worker := newWorker("worker-1", eventStore, cogMemory)
	planner := newWorker("planner-1", eventStore, cogMemory)

	svc.RegisterAgent(leader, func() base.Agent {
		slog.Info("factory invoked", "agent_id", "leader-1", "reason", "resurrection")
		return newWorker("leader-1", eventStore, cogMemory)
	})
	svc.RegisterAgent(worker, func() base.Agent {
		slog.Info("factory invoked", "agent_id", "worker-1", "reason", "resurrection")
		return newWorker("worker-1", eventStore, cogMemory)
	})
	svc.RegisterAgent(planner, func() base.Agent {
		slog.Info("factory invoked", "agent_id", "planner-1", "reason", "resurrection")
		return newWorker("planner-1", eventStore, cogMemory)
	})

	// ----------------------------------------------------------
	// Phase 2: Start Runtime and let agents work.
	// ----------------------------------------------------------
	phaseSeparator("Phase 2: Normal Operation")

	if err := svc.Start(ctx); err != nil {
		slog.Error("failed to start runtime", "error", err)
		return
	}

	// Let agents process tasks for a while.
	time.Sleep(6 * time.Second)
	printStats(svc, eventStore, ctx)

	// Take a snapshot of worker state before crash.
	snapshot, _ := worker.Snapshot()
	fmt.Printf("  Worker-1 snapshot before crash: %+v\n", snapshot)

	// ----------------------------------------------------------
	// Phase 3: Worker crash and resurrection.
	// ----------------------------------------------------------
	phaseSeparator("Phase 3: Worker-1 Crash")

	preCrashCount := worker.taskCount.Load()
	preCrashSession := worker.getSessionID()
	fmt.Printf("  Pre-crash task count: %d\n", preCrashCount)
	fmt.Printf("  Pre-crash session: %s\n", preCrashSession)

	worker.shouldCrash.Store(true)
	fmt.Println("  Worker-1 crash triggered, waiting for resurrection...")

	// Wait for crash detection + resurrection + state replay.
	time.Sleep(5 * time.Second)

	// ----------------------------------------------------------
	// Phase 4: Verify resurrection with state recovery.
	// ----------------------------------------------------------
	phaseSeparator("Phase 4: Resurrection Verification")

	// The Runtime creates a new worker via factory and replays events.
	// We need to get a reference to the resurrected agent.
	// Note: The Runtime holds the new instance internally.
	printStats(svc, eventStore, ctx)

	// Verify that events were replayed (operational recovery).
	replayedEvents, _ := eventStore.Read(ctx, "worker-1", events.ReadOptions{
		Direction: events.ReadAscending,
	})
	fmt.Printf("  Total events for worker-1: %d\n", len(replayedEvents))

	// Verify restored agent state.
	if restored := svc.GetAgent("worker-1"); restored != nil {
		if w, ok := restored.(*workerAgent); ok {
			verified := verifyRestoredState(w, 1)
			fmt.Printf("  Restored state verified: %v\n", verified)
		}
	}

	// Check cognitive memory (simulated).
	messages := cogMemory.GetMessages(preCrashSession)
	fmt.Printf("  Cognitive memory entries for session %s: %d\n", preCrashSession, len(messages))
	for i, msg := range messages {
		if i >= 5 {
			fmt.Printf("    ... and %d more\n", len(messages)-5)
			break
		}
		fmt.Printf("    [%d] %s\n", i+1, msg)
	}

	// ----------------------------------------------------------
	// Phase 5: Resurrected agent continues working.
	// ----------------------------------------------------------
	phaseSeparator("Phase 5: Resurrected Agent Working")

	time.Sleep(6 * time.Second)
	printStats(svc, eventStore, ctx)

	// ----------------------------------------------------------
	// Phase 6: Second crash (planner) to show multiple resurrections.
	// ----------------------------------------------------------
	phaseSeparator("Phase 6: Planner-1 Crash and Resurrection")

	planner.shouldCrash.Store(true)
	time.Sleep(5 * time.Second)
	printStats(svc, eventStore, ctx)

	// ----------------------------------------------------------
	// Phase 7: Final event history.
	// ----------------------------------------------------------
	phaseSeparator("Phase 7: Full Event History")

	allEvents, _ := eventStore.ReadAll(ctx, events.ReadOptions{})
	fmt.Printf("  Total events in store: %d\n\n", len(allEvents))

	// Group events by stream.
	streamCounts := make(map[string]int)
	for _, ev := range allEvents {
		streamCounts[ev.StreamID]++
	}
	fmt.Println("  Events per agent:")
	for stream, count := range streamCounts {
		fmt.Printf("    %s: %d events\n", stream, count)
	}

	fmt.Println("\n  Recent events (last 10):")
	start := 0
	if len(allEvents) > 10 {
		start = len(allEvents) - 10
	}
	for _, ev := range allEvents[start:] {
		fmt.Printf("    [%s] %s", ev.StreamID, ev.Type)
		if taskID, ok := ev.Payload["task_id"]; ok {
			fmt.Printf(" task=%v", taskID)
		}
		if sid, ok := ev.Payload["session_id"]; ok {
			fmt.Printf(" session=%v", sid)
		}
		fmt.Println()
	}

	// ----------------------------------------------------------
	// Phase 8: Cleanup.
	// ----------------------------------------------------------
	phaseSeparator("Phase 8: Graceful Shutdown")

	if err := svc.Stop(); err != nil {
		slog.Error("runtime stop failed", "error", err)
	}

	fmt.Println("\nRuntime Resurrection example completed!")
	fmt.Println("Key takeaways:")
	fmt.Println("  - Operational recovery: EventStore replays task events after crash.")
	fmt.Println("  - Cognitive recovery: Session memory provides conversation context.")
	fmt.Println("  - StatefulAgent interface: RestoreState + ReplayEvents + Snapshot.")
	fmt.Println("  - Session continuity: New agent inherits the old session ID.")
}

// printStats displays current runtime statistics.
func printStats(svc *runtimeSvc.Service, store events.EventStore, ctx context.Context) {
	stats := svc.Stats()
	fmt.Printf("  Active agents: %d\n", stats.ActiveAgents)
	fmt.Printf("  Total restarts: %d\n", stats.TotalRestarts)
	fmt.Printf("  Uptime: %s\n", stats.Uptime.Round(time.Second))

	allEvents, _ := store.ReadAll(ctx, events.ReadOptions{})
	fmt.Printf("  Total events: %d\n", len(allEvents))
}

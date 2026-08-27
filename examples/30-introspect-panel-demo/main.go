// Runtime introspection panel demo — build a real ARES peer runtime (Task
// Fabric + Agent Fabric + Kernel Scheduler + recovery) and serve the live
// introspection panel at http://localhost:5606/introspect.
//
// Unlike the LLM-backed examples, this demo runs with ZERO external
// dependencies (no LLM, no config, no API key): agents are pure in-memory
// fabric agents whose Cognition is demo-level logic standing in for a real
// LLM loop (same stance as examples/aresos-demo). The scheduling, leasing,
// lifecycle, event stream and chaos are ALL real — the panel shows real
// runtime state, not canned data.
//
// What you will see in the panel:
//
//	Kernel  card   — queue depth / scheduled count / executors / load bars
//	Fabric  table  — every task with READY→LEASED→RUNNING→COMPLETED states,
//	                 lease expiry countdown, epoch fencing, checkpoint ✓
//	Agents  wall   — analyzer / coder / reviewer / tester cards that flip
//	                 IDLE→RUNNING→(chaos kill)→SUSPENDED→recovered
//	Activity feed  — deaths / dispatches / completions as they happen
//	Chaos  section — shadow-sandbox recovery verification + live injections
//
// Learning objectives:
//   - How the peer kernel is assembled from taskfabric + agentfabric +
//     kernelscheduler (mirrors cmd/ares/peer_mode.go, no leader).
//   - How the introspection panel is wired: introspect.Collector pulls
//     Scheduler.Snapshot / Fabric.LeaseSnapshot / Agents.AgentsView every 2s;
//     introspect.Sink subscribes the EventStore; introspect.Handler serves the
//     embedded UI + JSON API.
//   - How agent death ≠ task death: a chaos-killed agent's leased task is
//     requeued after lease expiry and completed by another capable agent.
//   - How chaos is observed without touching production: a scratch-fabric
//     shadow sandbox replays the kill→recover chain and reports its outcome
//     to the panel.
//
// Core APIs used (with package paths):
//   - taskfabric.NewFabric / WithEventStore / Create / LeaseSnapshot
//     — github.com/Timwood0x10/ares/internal/taskfabric
//   - agentfabric.NewFabric / WithEventSink / Spawn / Kill / Suspend /
//     Resume / AgentsView — github.com/Timwood0x10/ares/internal/agentfabric
//   - kernelscheduler.New / WithAgentFabric / WithGovernance / WithEventStore
//     / Run / Snapshot — github.com/Timwood0x10/ares/internal/kernelscheduler
//   - aresrecovery.NewSandbox / Replay — github.com/Timwood0x10/ares/internal/aresrecovery
//   - introspect.NewCollector / NewSink / NewHandler / ChaosReporter
//     — github.com/Timwood0x10/ares/internal/introspect
//
// Run (from the repo root):
//
//	go run examples/30-introspect-panel-demo/main.go
//
// Then open http://localhost:5606/introspect in a browser. The demo runs
// for 120 seconds (Ctrl+C to stop earlier).
package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/introspect"
	"github.com/Timwood0x10/ares/internal/kernelscheduler"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ── 1. Shared event store: the single bus every subsystem publishes to ──
	store := ares_events.NewMemoryEventStore()
	defer func() { _ = store.Close() }() // best-effort: in-memory store

	// ── 2. Task Fabric (durable task state machine) + Agent Fabric (lifecycle) ──
	// Task events (created/acquired/completed/…) flow straight into the bus via
	// WithEventStore. Agent lifecycle events are forwarded by an EventSink.
	tasks := taskfabric.NewFabric().WithEventStore(store)
	agents := agentfabric.NewFabric().WithEventSink(&fabricEventSink{store: store})

	// ── 3. Agent IPC bus (real peer-to-peer collaboration) ──
	// Every agent registers a handler on the bus so demoCognition can send
	// real IPC messages to collaboration partners when it completes a task.
	// The bus is NOT mocked — messages are delivered synchronously between
	// handlers, proving the agent→agent collaboration path.
	agentBus := agentipc.NewBus()
	collab := introspect.NewCollabReporter()

	// ── 4. Spawn the peer population into the Agent Fabric ──
	// Each agent carries a CognitionFactory — its execution body. The demo
	// body sleeps a bit (simulating an LLM call) then completes the quantum.
	spawned := spawnWorkforce(ctx, agents, agentBus, collab)

	// ── 5. Kernel Scheduler: the single dispatch engine (no leader) ──
	tracker := kernelscheduler.NewLoadTracker()
	sched := kernelscheduler.New(tasks, map[string]kernelscheduler.CapabilityExecutor{}, tracker)
	sched.WithEventStore(store).
		WithAgentFabric(agents).  // fabric population IS the candidate pool (B1)
		WithGovernance(agents).   // P5 resource budgets live
		WithMaxConcurrent(5).     // up to 5 tasks in parallel
		WithTTL(15 * time.Second) // snappy leases so chaos recovery is visible

	// ── 6. Chaos: shadow-sandbox observer (never touches production agents) ──
	// The reporter is the panel's chaos source. A shadow loop re-runs the
	// canonical kill→recover chain on a SCRATCH fabric every 10s and records
	// the real outcome (recovered_ready). Live mode stays OFF.
	chaosReporter := introspect.NewChaosReporter()
	chaosReporter.SetConfig(true, "shadow")
	go shadowVerificationLoop(ctx, chaosReporter)

	// ── 7. Introspection panel: collector + sink + HTTP handler ──
	panel := &introspect.Store{}
	collector := introspect.NewCollector(introspect.Sources{
		Kernel: sched.Snapshot,
		Fabric: tasks.LeaseSnapshot,
		Agents: agents.AgentsView,
		Chaos:  chaosReporter.Snapshot,
		Collab: collab.Snapshot,
	})
	handler := introspect.NewHandler(panel)
	sink := introspect.NewSink(panel)

	// ── 8. Start the runtime loops ──
	go sched.Run(ctx)
	go runCollector(ctx, collector, panel)
	go runSink(ctx, sink, store)
	go runWorkload(ctx, sched, tasks, agents, spawned)

	// ── 9. HTTP server serving the panel ──
	addr := "127.0.0.1:5606"
	mux := http.NewServeMux()
	mux.Handle("/introspect", handler)
	mux.Handle("/introspect/", handler)
	mux.Handle("/api/v1/introspect/", handler)
	srv := &http.Server{Addr: addr, Handler: mux, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}

	fmt.Printf("\n═══ ARES Runtime Introspection Panel Demo ═══\n")
	fmt.Printf("Panel:   http://localhost:5606/introspect\n")
	fmt.Printf("Snapshot: GET /api/v1/introspect/snapshot\n")
	fmt.Printf("Events:   GET /api/v1/introspect/events\n")
	fmt.Printf("Peers spawned: %v\n", spawned)
	fmt.Printf("Run for 120s, or Ctrl+C to stop.\n\n")

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http server: %v", err)
		}
	}()
	defer func() { _ = srv.Close() }() // best-effort: process exits right after

	// ── 9. Let it run for 120s, then exit cleanly ──
	select {
	case <-ctx.Done():
		fmt.Println("\nStopping…")
	case <-time.After(120 * time.Second):
		fmt.Println("\n120s elapsed — stopping demo.")
	}
}

// ── Agent workforce ────────────────────────────────────────────────

// workforceSpec defines the demo peer population: id → capabilities.
// Partner is the downstream agent id this agent hands work to (real IPC
// collaboration). "" = no partner (last in the chain).
var workforceSpec = []struct {
	id      string
	caps    []string
	ms      int    // simulated per-quantum latency
	steps   int    // quanta per task (yield → SUSPENDED → re-acquire)
	partner string // downstream collaboration partner ("" = none)
}{
	{"analyzer-1", []string{"code", "analyze"}, 600, 3, "reviewer-1"},
	{"coder-1", []string{"code", "refactor"}, 500, 3, "reviewer-1"},
	{"reviewer-1", []string{"review"}, 400, 2, "tester-1"},
	{"tester-1", []string{"test"}, 350, 2, "ops-1"},
	{"ops-1", []string{"ops", "review"}, 700, 4, ""},
}

// spawnWorkforce spawns every configured peer into the Agent Fabric with a
// demo Cognition body. Each agent is registered on the IPC bus so it can
// receive collaboration messages, and its cognition fires a real IPC Send
// to its partner when completing a task. Returns the id list.
func spawnWorkforce(ctx context.Context, agents *agentfabric.Fabric, bus *agentipc.Bus, collab *introspect.CollabReporter) []string {
	ids := make([]string, 0, len(workforceSpec))
	for _, spec := range workforceSpec {
		spec := spec // capture
		// Register each agent on the IPC bus with a handler that simply
		// acknowledges the message (the panel reads the collab edges via
		// the reporter, not the bus).
		agentID := spec.id
		_ = bus.Register(agentID, func(ctx context.Context, msg *agentipc.Message) (*agentipc.Message, error) {
			return &agentipc.Message{
				From:    agentID,
				To:      msg.From,
				Topic:   "ack",
				Payload: map[string]any{"status": "ok"},
			}, nil
		})
		if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
			Identity:     spec.id,
			Capabilities: append([]string(nil), spec.caps...),
			CognitionFactory: func(caps []string) agentfabric.Cognition {
				return &demoCognition{
					id:        spec.id,
					caps:      caps,
					latency:   time.Duration(spec.ms) * time.Millisecond,
					steps:     spec.steps,
					partnerID: spec.partner,
					bus:       bus,
					collab:    collab,
				}
			},
			ExperiencePrior: map[string]any{"type": "boot", "problem": "demo boot prior"},
		}); err != nil {
			log.Printf("spawn %s failed: %v", spec.id, err)
			continue
		}
		ids = append(ids, spec.id)
	}
	return ids
}

// demoCognition is the demo-level execution body: it sleeps (simulating an LLM
// call) per quantum and yields with progress across `steps` quanta before
// completing. When completing a task, it fires a real agentipc.Send message
// to its collaboration partner (if any) and records the edge in the collab
// reporter — so the panel shows real agent→agent collaboration.
type demoCognition struct {
	id        string
	caps      []string
	latency   time.Duration
	steps     int
	partnerID string // downstream peer ("" = none)
	bus       *agentipc.Bus
	collab    *introspect.CollabReporter
}

// ExecuteStep implements agentfabric.Cognition. The checkpoint carries the
// number of quanta done so far, so a resumed step continues where the
// previous one left off. On final completion it fires a real IPC message to
// the collaboration partner (if configured) and records the edge.
func (d *demoCognition) ExecuteStep(ctx context.Context, task *models.Task) (*agentfabric.StepOutcome, error) {
	select {
	case <-time.After(d.latency):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	done := 0
	if task.Payload != nil {
		if n, ok := task.Payload["checkpoint"].(float64); ok {
			done = int(n)
		}
	}
	done++

	// Not finished yet: yield with progress.
	if done < d.steps {
		return &agentfabric.StepOutcome{
			Done:       false,
			Checkpoint: float64(done),
		}, nil
	}

	// Real collaboration: fire a real IPC message to the downstream partner
	// and record the edge. This is how the panel's collab graph gets real
	// agent→agent data — it's not fabricated, it's the actual handoff.
	if d.bus != nil && d.collab != nil && d.partnerID != "" {
		// Fire-and-forget Send (handler runs synchronously, no reply).
		_ = d.bus.Send(ctx, d.id, d.partnerID, "handoff",
			map[string]any{"task_id": task.TaskID, "capability": d.caps})
		d.collab.Record(introspect.CollabEdge{
			From:   d.id,
			To:     d.partnerID,
			Topic:  "handoff",
			TaskID: task.TaskID,
			TS:     time.Now(),
		})
	}

	input := "?"
	if task.Payload != nil {
		if v, ok := task.Payload["input"].(string); ok {
			input = v
		}
	}
	return &agentfabric.StepOutcome{
		Done: true,
		Result: &models.TaskResult{
			TaskID:    task.TaskID,
			AgentType: models.AgentType(d.id),
			Success:   true,
			Reason:    fmt.Sprintf("%s handled in %d steps: %s", d.id, done, truncate(input, 40)),
			Metadata:  map[string]any{"capabilities": d.caps, "steps": done},
		},
	}, nil
}

// ── Workload driver: submit real tasks, then exercise chaos ────────

// taskSpec is a demo work item: capability + a short description.
var taskSpec = []struct {
	cap string
	in  string
}{
	{"code", "review the scheduler state machine"},
	{"code", "refactor the lease fencing token"},
	{"analyze", "profile the quantum drain path"},
	{"review", "audit the recovery chain"},
	{"test", "cover the lease expiry requeue"},
	{"ops", "health-check the event bus"},
	{"code", "implement checkpoint resume"},
	{"review", "verify the spawn provenance"},
}

// runWorkload submits one task every ~1.5s for the first 60s, then runs three
// chaos rounds (kill / suspend / resume) so the panel shows the full
// lifecycle. Task submission and chaos both go through the REAL fabric.
func runWorkload(ctx context.Context, sched *kernelscheduler.Scheduler, tasks *taskfabric.Fabric, agents *agentfabric.Fabric, peers []string) {
	start := time.Now()
	var seq int
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		// Phase 1 (first 60s): steady task submission — two per round so the
		// panel shows several agents working in parallel, not one-at-a-time.
		if elapsed := time.Since(start); elapsed < 60*time.Second {
			for i := 0; i < 2; i++ {
				spec := taskSpec[seq%len(taskSpec)]
				seq++
				taskID := fmt.Sprintf("task-%03d", seq)
				err := tasks.Create(&taskfabric.Task{
					ID:          taskID,
					Capability:  spec.cap,
					Priority:    rand.Intn(3),
					RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
					Checkpoint:  &taskfabric.CheckpointEnvelope{Payload: map[string]any{"input": spec.in}},
				})
				if err != nil {
					log.Printf("create %s failed: %v", taskID, err)
				} else {
					log.Printf("[workload] submitted %s (%s) → READY", taskID, spec.cap)
				}
			}
			timer.Reset(1500 * time.Millisecond)
			continue
		}

		// Phase 2 (60–90s): three chaos rounds, ~10s apart, then idle until
		// the 120s cap so the panel settles.
		round := seq - 40 + 1
		if round <= 3 {
			runChaosRound(ctx, agents, peers, round)
			seq++
			timer.Reset(20 * time.Second)
			continue
		}

		// Idle: leave the panel to settle.
		time.Sleep(5 * time.Second)
		timer.Reset(5 * time.Second)
	}
}

// runChaosRound exercises the real agent lifecycle so the panel shows deaths
// and revivals: kill one random agent, suspend one, then resume both after a
// short window. Leased tasks of the dead agent are requeued after lease expiry
// and picked up by another capable agent — agent death ≠ task death.
func runChaosRound(ctx context.Context, agents *agentfabric.Fabric, peers []string, round int) {
	if len(peers) == 0 {
		return
	}
	kill := peers[rand.Intn(len(peers))]
	sus := peers[rand.Intn(len(peers))]
	if sus == kill {
		sus = peers[(rand.Intn(len(peers)-1)+1)%len(peers)]
	}
	log.Printf("[chaos] round %d: kill %s, suspend %s", round, kill, sus)
	if err := agents.Kill(ctx, kill); err != nil {
		log.Printf("[chaos] kill %s: %v", kill, err)
	}
	if err := agents.Suspend(ctx, sus); err != nil {
		log.Printf("[chaos] suspend %s: %v", sus, err)
	}

	// Give the scheduler a lease-expiry window to requeue the dead agent's
	// tasks, then bring both back — the replacement recovers from checkpoint.
	time.Sleep(18 * time.Second)
	if err := agents.Resume(ctx, sus); err != nil {
		log.Printf("[chaos] resume %s: %v", sus, err)
	}
	log.Printf("[chaos] round %d: resumed %s (recovery will re-execute its tasks)", round, sus)
}

// ── Shadow sandbox observer ────────────────────────────────────────

// shadowVerificationLoop periodically re-runs the canonical kill→recover chain
// on a SCRATCH fabric and records the real outcome to the panel's chaos source
// (Phase 3 observability: shadow health without touching production).
func shadowVerificationLoop(ctx context.Context, reporter *introspect.ChaosReporter) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reporter.RecordShadow(runShadowSandbox(ctx))
		}
	}
}

// runShadowSandbox builds a scratch Task+Agent fabric, replays the canonical
// agent-kill → lease-expire → recovery scenario and returns the outcome.
// Production agents are never touched — the sandbox is fully independent.
func runShadowSandbox(ctx context.Context) introspect.ShadowResult {
	scratchTasks := taskfabric.NewFabric()
	scratchAgents := agentfabric.NewFabric()
	recovery := aresrecovery.New(scratchTasks, scratchAgents, aresrecovery.DefaultRestartPolicy())
	sandbox := aresrecovery.NewSandbox(scratchTasks, scratchAgents, recovery)

	events := []aresrecovery.SandboxEvent{
		{Type: aresrecovery.SandboxEventAgentSpawn, AgentID: "shadow-agent-1"},
		{Type: aresrecovery.SandboxEventTaskCreate, TaskID: "shadow-task-1"},
		{Type: aresrecovery.SandboxEventTaskAcquire, TaskID: "shadow-task-1", AgentID: "shadow-agent-1"},
		{Type: aresrecovery.SandboxEventAgentKill, AgentID: "shadow-agent-1"},
		{Type: aresrecovery.SandboxEventLeaseExpire, TaskID: "shadow-task-1"},
		{Type: aresrecovery.SandboxEventRecoverAll},
	}

	outcomes, err := sandbox.Replay(ctx, events)
	if err != nil {
		return introspect.ShadowResult{LastRun: time.Now(), Events: len(events), Errored: true}
	}
	if len(outcomes) == 0 {
		return introspect.ShadowResult{LastRun: time.Now(), Events: len(events), Errored: true}
	}
	last := outcomes[len(outcomes)-1]
	// RecoverFromAgentDeath re-acquires the requeued task for a replacement
	// agent, so the reliable recovered signal is the recover.all outcome's
	// count, not the task state.
	recovered, _ := last.Detail["recovered"].(int)
	return introspect.ShadowResult{
		LastRun:   time.Now(),
		Events:    len(outcomes),
		Recovered: recovered > 0,
	}
}

// ── Introspection collectors ───────────────────────────────────────

// runCollector publishes a fresh snapshot to the panel every 2s (the panel's
// PULL channel).
func runCollector(ctx context.Context, collector *introspect.Collector, panel *introspect.Store) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			panel.Set(collector.Collect())
		}
	}
}

// runSink feeds the panel's activity feed from the shared event stream (the
// panel's PUSH channel).
func runSink(ctx context.Context, sink *introspect.Sink, store ares_events.EventStore) {
	if err := sink.Run(ctx, store); err != nil {
		log.Printf("[panel] sink: %v", err)
	}
}

// ── Agent fabric → event bus bridge ────────────────────────────────

// fabricEventSink forwards agentfabric lifecycle records onto the shared event
// bus so the panel's activity feed sees agent deaths/revivals immediately
// (mirrors cmd/ares fabricEventSink).
type fabricEventSink struct {
	store ares_events.EventStore
}

// Emit implements agentfabric.EventSink.
func (f *fabricEventSink) Emit(ctx context.Context, ev agentfabric.AgentEvent) error {
	if f == nil || f.store == nil {
		return nil
	}
	busType := ares_events.EventAgentStarted
	reason := string(ev.Type)
	switch ev.Type {
	case agentfabric.EventAgentSpawned, agentfabric.EventAgentResumed:
		reason = ""
	case agentfabric.EventAgentSuspended, agentfabric.EventAgentRetired,
		agentfabric.EventAgentKilled:
		busType = ares_events.EventAgentStopped
	}
	payload := map[string]any{"agent_id": ev.AgentID}
	if reason != "" {
		payload["reason"] = reason
	}
	return f.store.Append(ctx, ev.AgentID, []*ares_events.Event{{
		Type:       busType,
		ModuleName: "agentfabric",
		Payload:    payload,
		Timestamp:  ev.At,
	}}, 0)
}

// truncate shortens s to at most n runes for display.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

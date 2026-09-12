package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/agentsyscall"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// peerAssembly carries the mutable wiring state threaded through peer-mode
// kernel assembly. It exists so each assembly phase is a method (receiver
// supplies state, so signatures stay within the ≤5-param rule) instead of a
// free function that would have to thread 7-10 cross-phase locals. The method
// bodies are the original createPeerAgents blocks, moved verbatim; each starts
// by aliasing the fields it reads under their original local names so the
// bodies are unchanged.
type peerAssembly struct {
	ctx         context.Context
	cfg         *ares_config.Config
	comp        *ares_bootstrap.Components
	chatClient  sub.ChatClient
	toolBinder  sub.ToolBinder
	store       ares_events.EventStore
	strategySrc agents.StrategySource
	expRepo     repositories.ExperienceRepositoryInterface

	kernel    *kernelHandle
	peers     []ares_config.PeerAgentConfig
	subAgents []sub.Agent

	// Cross-phase outputs that are not stored on the kernel handle.
	restoredSeq int64
	peerRouter  agentfabric.Cognition
	governance  agentfabric.Governance
}

// assembleFabric builds the task fabric, restores it from the event store, and
// returns the restored max task sequence that seeds cross-restart ID counters.
func (a *peerAssembly) assembleFabric() error {
	ctx := a.ctx
	kernel := a.kernel
	strategySrc := a.strategySrc
	store := a.store
	comp := a.comp
	// Assemble the Kernel: Task Fabric + Agent Fabric + scheduler. This
	// mirrors flipKernelToTaskFabric but runs directly at startup (no
	// legacy path to flip from).
	kernel.fabric = taskfabric.NewFabric()
	// Stamp every submitted task with the strategy that was active at
	// submission time (evolution loop closure), so runtime fitness samples
	// stay attributed to the strategy that produced them across promotes.
	// Cheap + non-blocking: one store read per Create on the submission path.
	if strategySrc != nil {
		kernel.fabric = kernel.fabric.WithStrategyStamp(func() string {
			stampCtx, stampCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer stampCancel()
			st, err := strategySrc.GetActiveStrategy(stampCtx)
			if err != nil || st == nil {
				return ""
			}
			return st.ID
		})
	}
	// The restored max counter value (0 on a fresh/empty store); feeds the
	// ID-collision seeds below. Declared here so both the restore site and
	// the syscall Kernel construction (later in this assembly) can consume it.
	var restoredSeq int64
	if store != nil {
		kernel.fabric = kernel.fabric.WithEventStore(store)
		// Rebuild in-memory tasks from the durable task.* log BEFORE the
		// scheduler starts draining: restoring after the first Acquire would
		// reset tasks created in this process lifetime. Fail-loud — silently
		// continuing would drop tasks the log says exist.
		if err := kernel.fabric.RestoreFromStore(ctx); err != nil {
			return fmt.Errorf("peer mode: restore task fabric from event store: %w", err)
		}
		// Cross-restart ID collision guard: the process-local counters reset
		// to 1 on every boot, but with a durable store the restored fabric
		// still holds the previous boot's peer-plan-N / sess-auto-N IDs. Seed
		// the sequence past the max embedded N so the next mint cannot
		// collide (grow-only: a no-op for a fresh in-memory store).
		restoredSeq = agentruntime.MaxRestoredSeq(kernel.fabric.IDs())
	}
	// Experience-derived confidence prior — recorded skill/task outcomes
	// sharpen scheduling when the same pattern recurs. Nil (skills disabled)
	// keeps declared confidences.
	if expSrc := resolveExperienceConfidence(comp); expSrc != nil {
		kernel.fabric = kernel.fabric.WithConfidenceSource(expSrc)
	}
	a.restoredSeq = restoredSeq
	return nil
}

// wireDispatchAndScheduler assembles the Task Fabric dispatcher, creates the
// shared load tracker, and configures the scheduler from YAML.
func (a *peerAssembly) wireDispatchAndScheduler() {
	kernel := a.kernel
	cfg := a.cfg
	peers := a.peers
	subAgents := a.subAgents
	store := a.store
	kernel.executors = make(map[string]CapabilityExecutor, len(subAgents))

	// Build the candidate list for the fabric dispatcher. The full declared
	// capability set (Caps) is offered to the scorer so a task matching ANY
	// capability is schedulable to the peer.
	subCaps := make([]subAgentCapability, 0, len(peers))
	for _, p := range peers {
		typ := ""
		if len(p.Capabilities) > 0 {
			typ = p.Capabilities[0]
		}
		subCaps = append(subCaps, subAgentCapability{ID: p.ID, Type: typ, Caps: append([]string(nil), p.Capabilities...)})
	}

	// Assemble the kernel dispatcher with the Task Fabric path as the active
	// path (no legacy leader track: the flag starts at PolicyTaskFabric).
	kernelDispatcher, kernelFlag := wireKernelDispatcher(subCaps)
	kernel.dual = kernelDispatcher
	kernel.flag = kernelFlag

	// One shared load tracker for the scheduler.
	tracker := newLoadTracker()
	kernel.tracker = tracker

	// Enable real Task Fabric execution (not scoring mode).
	enableKernelExecution(kernel.dual, kernel.fabric)

	// Start the scheduler.
	sched := NewKernelScheduler(kernel.fabric, kernel.executors, tracker)
	if store != nil {
		sched.WithEventStore(store)
	}
	// Honor the YAML kernel.max_concurrent (0/unset = auto). The old literal
	// WithMaxConcurrent(0) relied on the auto fallback, which stopped at
	// ExecutorCount() — empty by design in peer mode — and collapsed to 1,
	// so every drain ran ONE quantum at a time despite fabric candidates
	// existing. With the fixed fallback chain, 0 now means "parallelism =
	// live fabric candidates"; a positive value caps it explicitly.
	if cfg.Kernel.MaxConcurrent > 0 {
		sched.WithMaxConcurrent(cfg.Kernel.MaxConcurrent)
	}
	// Honor the YAML kernel.poll_interval. Previously the config field was
	// never injected — the scheduler always drained on the 500ms default.
	if d := parseKernelPollInterval(cfg.Kernel.PollInterval); d > 0 {
		sched.PollInterval = d
	}
	// Optional snappier leases for chaos/recovery demos (#panel): a dead
	// agent's tasks requeue after lease_ttl instead of the 5-minute default.
	if ttl := parseKernelLoopConfig(cfg).LeaseTTL; ttl > 0 {
		sched.WithTTL(ttl)
	}
	kernel.scheduler = sched
	kernel.flipped = true
}

// wireEvolutionFeedback records execution outcomes and pushes derived
// confidence and the zero-LLM score back into the tracker and active strategy.
func (a *peerAssembly) wireEvolutionFeedback() {
	ctx := a.ctx
	comp := a.comp
	cfg := a.cfg
	sched := a.kernel.scheduler
	tracker := a.kernel.tracker
	// Evolution feedback loop: record execution outcomes per agent +
	// capability, and periodically push the derived confidence back into the
	// tracker so the next Schedule prefers historically-successful executors.
	// The loop now also writes the zero-LLM deterministic score back
	// to the active strategy's Score field via the StrategyStore, so the
	// GA's fitness signal tracks real execution outcomes without any LLM
	// call.
	attribution := aresrecovery.NewExecutionAttribution()
	sched.WithAttribution(attribution)
	feedback := aresrecovery.NewEvolutionFeedbackAdapter(attribution, tracker)

	// Wire the zero-LLM score provider into the EvolutionScheduler so
	// task.completed/failed events feed the deterministic aggregate score
	// (from attribution) instead of the constant 1.0/0.0. The provider reads
	// the same attribution that the feedback loop writes to, so the score
	// window reflects real execution quality (latency, retries, recovery).
	if comp.Evolution != nil {
		if sched, ok := comp.Evolution.Scheduler.(*evolution.EvolutionScheduler); ok && sched != nil {
			sched.SetScoreProvider(
				aresrecovery.NewAttributionScoreProvider(attribution),
			)
		}
	}

	// Loop closure: make the "independent scorer wired" shadow gate real.
	// bootstrap_steps.go set DeterministicScorerEnabled=true so hasScorer passed
	// and the shadow gate was registered as "independent scorer wired" — but
	// buildShadowEvaluator only sets a shadow scorer when an LLM scorer exists.
	// With llmScorer==nil the evaluator's scorer stayed nil, the ShadowSampler
	// no-op'd, and the gate rejected every candidate fail-closed forever (a gate
	// that claims evidence but never gathers it).
	//
	// The scorer must DISCRIMINATE per strategy, otherwise the defect only
	// moves: one global attribution score returns the same number for the
	// candidate and the active strategy, every comparison is an exact tie
	// (ShadowWon requires shadow > active), the win rate is 0.0 and the gate
	// still rejects everything. So the evidence source is the ReplayScorer: each
	// strategy is scored by the mean of ITS OWN KindFitness records that the
	// RuntimeObserver already writes per finished task, read over a distinct
	// time window per comparison — real per-strategy evidence, zero LLM calls.
	// The attribution-derived deterministic score supplies the
	// cold-start prior for a strategy with no history in a window, so the same
	// execution quality the GA rewards also anchors the shadow comparison.
	if comp.NewEvolution != nil && comp.NewEvolution.ShadowEvaluator != nil {
		det := aresrecovery.NewDeterministicScorer()
		// The replay query limit is configurable (evolution.shadow.
		// replay_query_limit). Zero keeps the default (200) — a config that
		// never mentions it behaves exactly as before.
		replay := evolution.NewReplayScorer(comp.EvidenceStore, func() float64 {
			return det.ScoreAttribution(attribution)
		}, evolution.WithReplayQueryLimit(cfg.Evolution.Shadow.ReplayQueryLimit))
		// Without an evidence store replay degrades to prior-vs-prior, i.e.
		// the tie deadlock above. Leave the scorer unset in that case so the
		// shadow gate stays honestly fail-closed instead of judging on ties.
		if replay.HasStore() {
			comp.NewEvolution.ShadowEvaluator.SetShadowScorer(replay.Score)
		}
	}

	// Wrap the confidence-injection adapter with score write-back.
	// The strategyScoreAdapter bridges to evolution.StrategyStore without
	// creating a circular import (aresrecovery cannot import evolution).
	var scoreWriter aresrecovery.StrategyScoreWriter
	if comp.NewEvolution != nil {
		scoreWriter = newStrategyScoreAdapter(comp.NewEvolution.StrategyStore)
	}
	scoredFeedback := aresrecovery.NewScoredFeedbackAdapter(feedback, nil, scoreWriter)
	runBackground(ctx, comp, "evolution-feedback", func(loopCtx context.Context) error {
		aresrecovery.RunScoredFeedbackLoop(loopCtx, scoredFeedback, 10*time.Second)
		return nil
	})
}

// startCollabGC reclaims terminal collaboration-graph residue left by fail-fast / timeout submissions off the hot path.
func (a *peerAssembly) startCollabGC() {
	ctx := a.ctx
	comp := a.comp
	kernel := a.kernel
	// Collaboration-graph janitor: reclaim terminal residue left by fail-fast
	// / timeout submissions off the hot path (per-submission cleanup handles
	// the common case; this catches siblings that were in-flight then).
	runBackground(ctx, comp, "collab-gc", func(loopCtx context.Context) error {
		runCollabGCLoop(loopCtx, kernel.fabric, 60*time.Second)
		return nil
	})
}

// assembleAgentFabric builds the Lifecycle pillar (agent fabric) with its event sink and optional resource budget.
func (a *peerAssembly) assembleAgentFabric() {
	kernel := a.kernel
	cfg := a.cfg
	store := a.store
	// Assemble the Lifecycle pillar (agentfabric + aresrecovery).
	// Wire the agent-fabric lifecycle sink into the shared event bus (#panel
	// feedback): deaths/spawns/suspensions must reach the introspection feed
	// the moment they happen, not only via lease-expiry downstream. Mapping to
	// existing bus types keeps consumers uniform (spawned/resumed → started;
	// killed/suspended/retired → stopped with reason).
	agentBus := &fabricEventSink{store: store}
	agents := agentfabric.NewFabric().WithEventSink(agentBus)
	if len(cfg.Kernel.Resources) > 0 {
		agents = agents.WithResourceBudget(cfg.Kernel.Resources)
	}
	kernel.agents = agents
}

// assembleExecution builds the shared L2 execution core and seeds the
// submission sequence past the restored max.
func (a *peerAssembly) assembleExecution() error {
	ctx := a.ctx
	kernel := a.kernel
	cfg := a.cfg
	comp := a.comp
	chatClient := a.chatClient
	toolBinder := a.toolBinder
	strategySrc := a.strategySrc
	store := a.store
	restoredSeq := a.restoredSeq
	agents := a.kernel.agents
	// The DAG execution gate (kernel.dag_execution in config).
	// Zero/absent config = legacy ReAct behavior (chat cognition for every
	// peer, L2 machinery test-only).
	//
	// Shared L2 execution core (internal/agentruntime): session registry +
	// incremental compile coordinator + planner/router cognition + session
	// reaper. The serve and SDK entry points both build this, so "how an agent
	// runs" has a single implementation. Recovery/chaos/evolution/transport
	// stay in this package as upper layers.
	//
	// Read the L1 ToolClass DAG from the evolution components so the planner
	// can check enabled/budget/prior before growing L2 tool nodes. Nil when no
	// tools are registered (permissive).
	var l1DAG *engine.MutableDAG
	if comp.NewEvolution != nil {
		l1DAG = comp.NewEvolution.ToolClassDAG()
	}

	exec, err := agentruntime.NewExecution(agentruntime.ExecutionConfig{
		Fabric:         kernel.fabric,
		Agents:         agents,
		ChatClient:     chatClient,
		ToolBinder:     toolBinder,
		StrategySource: strategySrc,
		L1DAG:          l1DAG,
		MaxPlanDepth:   resolveMaxPlanDepth(cfg.Kernel.DAGExecution),
		ReaperGrace:    resolveReaperGrace(cfg.Kernel.DAGExecution),
		SessionIdleTTL: resolveSessionIdleTTL(cfg.Kernel.DAGExecution),
		CompileStore:   store,
		// Serve-side memory enrichment: fold conversation history into the
		// submission prompt before admission, the way the SDK folds it in
		// Agent.composePrompt. Nil when memory is disabled/unbuilt — the
		// shared core stays memory-agnostic and the SDK path (which
		// pre-enriches upstream) must never install a second hook.
		PromptEnricher: resolveServePromptEnricher(cfg, comp.Memory, slog.Default()),
		Logger:         slog.Default(),
	})
	if err != nil {
		return fmt.Errorf("peer mode: %w", err)
	}
	peerRouter := exec.Router
	sessionReg := exec.Sessions.Reg
	sessionReaper := exec.Reaper
	// The registry + compile coordinator are always wired, so the submission
	// path always admits sessions (there is no gate-off legacy mode).
	kernel.sessionReg = sessionReg
	kernel.compileCoord = exec.Compile
	kernel.submitter = exec.Submitter
	// Cross-restart ID collision guard for the submission sequence
	// (peer-plan-N / sess-auto-N): grow-only seed past the restored max (a
	// no-op for a fresh in-memory store).
	kernel.submitter.Seed(restoredSeq)

	// Terminal-task reaper for L2 session tasks. Every grown node is a fabric
	// task and the fabric never self-harvests, so without this loop the
	// in-memory task map grows monotonically across a long-lived serve. The
	// registry is the keep-set authority: a live session's tasks are its
	// readable history and are never harvested; only tasks of released
	// sessions die, after the configured grace window.
	runBackground(ctx, comp, "l2-reaper", func(loopCtx context.Context) error {
		sessionReaper.Run(loopCtx.Done(), time.Minute)
		return nil
	})
	slog.InfoContext(ctx, "peer mode: L2 session task reaper wired",
		"grace", sessionReaper.GracePeriod())
	a.peerRouter = peerRouter
	return nil
}

// wireSessionCleanup starts the idle-TTL sweeper and answer-failure release so
// abandoned sessions free their terminal tasks.
func (a *peerAssembly) wireSessionCleanup() {
	ctx := a.ctx
	comp := a.comp
	cfg := a.cfg
	store := a.store
	sessionReg := a.kernel.sessionReg
	// Session idle-TTL sweeper. The keep-set only lets a session's
	// tasks die when the session itself dies, and the only death signals
	// were "answer completed" / "admission rolled back" — an abandoned
	// session (client gone, planner loop stuck, answer quantum dying
	// before its release) pinned its terminal tasks forever. Releasing on
	// idle turns the leak bound into TTL + reaper grace; active sessions
	// are untouchable because every quantum refreshes their last-access
	// through GetSession.
	idleTTL := resolveSessionIdleTTL(cfg.Kernel.DAGExecution)
	effectiveTTL := idleTTL
	if effectiveTTL <= 0 {
		effectiveTTL = agentfabric.DefaultSessionIdleTTL
	}
	runBackground(ctx, comp, "session-idle-ttl", func(loopCtx context.Context) error {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return nil
			case <-ticker.C:
				if ids := sessionReg.SweepExpired(idleTTL); len(ids) > 0 {
					slog.InfoContext(loopCtx, "peer mode: released idle sessions past TTL",
						"count", len(ids), "ttl", effectiveTTL, "sessions", ids)
				}
			}
		}
	})
	slog.InfoContext(ctx, "peer mode: session idle-TTL sweeper wired", "ttl", effectiveTTL)

	// answer-failure session release. The answer node is the session's
	// ONLY terminal exit: when the answer task itself dies terminally
	// (retry budget exhausted by a failing executor), no successor can
	// reference the session graph again, yet nothing on that path called
	// ReleaseSession — the idle TTL (above) was the sole cleanup, pinning
	// every terminal task of the dead session for the full 30min. This
	// subscription closes the loop: a terminal task.failed whose capability
	// is ares/answer releases the session immediately, so the reaper
	// harvests after the normal grace window instead of after the TTL.
	if store != nil {
		runBackground(ctx, comp, "answer-fail-release", func(loopCtx context.Context) error {
			ch, err := store.Subscribe(loopCtx, ares_events.EventFilter{
				Types: []ares_events.EventType{ares_events.EventTaskFailed},
			})
			if err != nil {
				slog.WarnContext(loopCtx, "peer mode: answer-fail release subscription failed, idle TTL remains the backstop",
					"error", err)
				return nil
			}
			for {
				select {
				case <-loopCtx.Done():
					return nil
				case ev, ok := <-ch:
					if !ok {
						return nil
					}
					agentruntime.ReleaseOnAnswerFailure(loopCtx, sessionReg, ev)
				}
			}
		})
	}
}

// computeGovernance derives the agent cognitive-execution budget shared by configured and syscall-spawned peers.
func (a *peerAssembly) computeGovernance() {
	cfg := a.cfg
	agentGovernance := agentfabric.Governance{
		TokenBudget: cfg.Kernel.AgentBudget.Tokens,
		ToolBudget:  cfg.Kernel.AgentBudget.Tools,
	}
	if cfg.Kernel.AgentBudget.Deadline != "" {
		if d, dErr := time.ParseDuration(cfg.Kernel.AgentBudget.Deadline); dErr == nil {
			agentGovernance.Deadline = d
		}
	}
	a.governance = agentGovernance
}

// spawnPeers spawns configured sub-agents into the fabric and wires the
// recovery policy and fabric-backed candidate pool.
func (a *peerAssembly) spawnPeers() error {
	ctx := a.ctx
	cfg := a.cfg
	kernel := a.kernel
	subAgents := a.subAgents
	toolBinder := a.toolBinder
	expRepo := a.expRepo
	peerRouter := a.peerRouter
	agentGovernance := a.governance
	agents := a.kernel.agents
	sched := a.kernel.scheduler
	for _, sa := range subAgents {
		if sa == nil {
			continue
		}
		sa := sa // capture for the closure (spawn is synchronous, but keep the
		// loop-scoped binding local for the CognitionFactory below)
		if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
			Identity:     sa.ID(),
			Capabilities: peerCapabilities(toolBinder.ListTools()),
			// The execution body is always the L2 router — a fabric
			// agent is fully self-contained (LLM + tools), no sub.Agent
			// wrapper, no ReAct loop.
			CognitionFactory: func([]string) agentfabric.Cognition {
				return peerRouter
			},
			ExperiencePrior: loadExperiencePrior(ctx, expRepo, sa.ID()),
			Governance:      agentGovernance,
		}); err != nil {
			return fmt.Errorf("peer mode: spawn agent %q into fabric: %w", sa.ID(), err)
		}
	}

	policy := aresrecovery.DefaultRestartPolicy()
	if cfg.Kernel.MaxRestarts > 0 {
		policy.MaxRestarts = cfg.Kernel.MaxRestarts
	}
	kernel.recovery = aresrecovery.New(kernel.fabric, agents, policy)
	sched.WithGovernance(agents)
	// The scheduler's candidate pool includes every live, IDLE, executable
	// fabric agent — the configured peers spawned above, plus any spawned via
	// the spawn_agent syscall. Static registered executors (recovery-bound)
	// still win by skip logic in appendFabricCandidates.
	sched.WithAgentFabric(agents)
	return nil
}

// wireSyscalls binds spawn/create/ask syscalls into the ToolBinder and seeds
// the kernel ID sequence past the restored max.
func (a *peerAssembly) wireSyscalls() {
	ctx := a.ctx
	kernel := a.kernel
	agents := a.kernel.agents
	peerRouter := a.peerRouter
	toolBinder := a.toolBinder
	agentGovernance := a.governance
	restoredSeq := a.restoredSeq
	// Wire the spawn_agent / create_task syscalls into the shared ToolBinder.
	// Every agent's LLM executor sees these tools alongside the built-in
	// tools, so it can autonomously decide to spawn peers and create tasks.
	kernelSyscall := agentsyscall.NewKernel(
		agents,
		kernel.fabric,
		func(agentID, capability string) agentsyscall.Executor {
			// Syscall-spawned peers execute through the L2 router —
			// the same body as configured peers. No ReAct executor.
			return &peerExecutorAdapter{id: agentID, typ: models.AgentType(capability), cog: peerRouter}
		},
		// No scheduler registration here. The static pool is
		// skipped whenever the agent fabric is wired, so registering
		// syscall-spawned agents was a no-op for normal drains — and the
		// agentsyscall.Executor half (the factory return above, which
		// powers spawn_agent/create_task/ask_agent) is untouched.
		func(string, agentsyscall.Executor) {},
		// Plan loops started via the create_plan loop option must be
		// bounded by the serve lifetime, not the individual tool call.
		agentsyscall.WithLoopLifetime(ctx),
		// Same cognitive-execution budget as configured peers: a
		// syscall-spawned agent is bounded from birth (zero = unlimited).
		agentsyscall.WithAgentGovernance(agentGovernance),
	)
	// Same collision guard as seedPeerTaskSeq, for the Kernel's own ID
	// families (task-<capability>-N / spawned-<capability>-N /
	// plan-<origin>-N): after a durable restore the fresh counter must start
	// past the max N the previous boot already wrote into the fabric.
	kernelSyscall.SeedIDSeq(restoredSeq)
	agentsyscall.BindTools(toolBinder, kernelSyscall)
	// Retain the syscall Kernel on the kernel handle so the collaboration IPC
	// bridge (built later in setupPeerRegistry) can inject ipc.Send into
	// ask_agent (Step Y.2-ACT).
	kernel.syscalls = kernelSyscall
	log.Info("peer mode: spawn_agent / create_task / ask_agent syscalls wired into tool binder")
}

// injectPriorities injects per-peer thread priorities into the load tracker.
func (a *peerAssembly) injectPriorities() {
	peers := a.peers
	tracker := a.kernel.tracker
	// Inject agent priorities into the tracker (thread priority).
	for _, p := range peers {
		if p.Priority > 0 {
			tracker.SetPriority(p.ID, p.Priority)
		}
	}
}

// startLoops starts the plugin bus, scheduler drain, and recovery loops as
// managed background loops tied to the serve lifetime.
func (a *peerAssembly) startLoops() {
	ctx := a.ctx
	comp := a.comp
	cfg := a.cfg
	store := a.store
	sched := a.kernel.scheduler
	kernel := a.kernel
	peerRouter := a.peerRouter
	// Start the scheduler and recovery loop. The recovery loop wires a REAL
	// executor factory (newPeerExecutor — full sub.Agent with LLM + tools) and
	// binds each replacement to exactly the task it was spawned for
	// (RegisterExecutorForTask), so a dead agent's task is resumed by a real
	// cognitive process — not a canned-success stub, and never at the expense
	// of a brand-new task.
	// Runtime plugin ecosystem closure: the PluginBus hooks the scheduler's
	// quantum boundary (observer/checkpoint/tool plugins observe every
	// Schedule→Acquire→RunQuantum). The adapter lives in runtime_bridge.go —
	// the kernel stays free of any runtime import (§0.3 dependency rule).
	// The loop knobs are parsed ONCE here and shared with the recovery loop
	// below (a second parse would waste work and risk drift).
	kernelLoopCfg := parseKernelLoopConfig(cfg)
	kernel.pluginBus = startPluginBus(ctx, store, sched, kernelLoopCfg)

	// The scheduler drain loop and the recovery loop run as managed
	// background loops and hand their lifecycle to the System Runtime
	// adapter (stop = cancel, wait = join the goroutine). The loop context
	// is pre-derived from the serve ctx — NOT from the context runBackground
	// passes — so the adopt-time Stop hook owns a cancel that works
	// independently of which managed pool ended up running the goroutine.
	schedCtx, schedCancel := context.WithCancel(ctx)
	schedDone := make(chan struct{})
	runBackground(ctx, comp, sysCompScheduler, func(context.Context) error {
		defer close(schedDone)
		sched.Run(schedCtx)
		return nil
	})
	kernel.schedulerStop = schedCancel
	kernel.schedulerDone = schedDone

	recCtx, recCancel := context.WithCancel(ctx)
	recDone := make(chan struct{})
	// Bind the scheduler's stale-winner hint to this recovery loop. When a
	// leased task's winner dies with no capable replacement, the scheduler
	// releases the task and kicks a sweep here, so the replacement execution
	// body is bound within one drain instead of one full lease TTL.
	recoveryKick, recoveryHint := newRecoveryKick()
	recoveryLoopCfg := kernelLoopCfg
	recoveryLoopCfg.RecoveryKick = recoveryKick
	sched.WithRecoveryHint(recoveryHint)
	runBackground(ctx, comp, sysCompRecovery, func(context.Context) error {
		defer close(recDone)
		runKernelRecoveryLoop(recCtx, store, kernel.recovery, recoveryLoopCfg,
			func(taskID, agentID string, executor CapabilityExecutor) {
				sched.RegisterExecutorForTask(taskID, agentID, executor)
			},
			func(agentID, capability string) CapabilityExecutor {
				// Recovery-bound tasks bypass the candidate pool,
				// so dispatch per task. Every task is L2 now — the router
				// serves all of them; the newPeerExecutor fallback below
				// is wiring-error insurance only (also cognition-backed,
				// never ReAct).
				if body := selectRecoveryBody(peerRouter, capability); body != nil {
					exec, err := newCognitionExecutor(agentID, models.AgentType(capability), body)
					if err == nil {
						return exec
					}
					slog.WarnContext(ctx, "peer mode: recovery executor L2 dispatch failed, falling back",
						"agent_id", agentID, "capability", capability, "error", err)
				}
				return newPeerExecutor(agentID, models.AgentType(capability), peerRouter)
			},
			sched.HasCapableExecutor,
		)
		return nil
	})
	kernel.recoveryStop = recCancel
	kernel.recoveryDone = recDone
}

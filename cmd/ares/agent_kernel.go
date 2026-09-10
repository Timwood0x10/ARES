// agent_kernel — the peer-mode kernel assembly (createPeerAgents: Task Fabric
// + Agent Fabric + scheduler + evolution feedback + syscalls + recovery) and
// its executor/session plumbing, split out of agent.go (M-C2). Function
// bodies are moved verbatim; only the file boundary is new.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/agentsyscall"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	"github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	llm "github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/llm/output"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	ares_skills "github.com/Timwood0x10/ares/internal/runtime/protocol/skills"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// peerTaskSeq is a monotonic sequence for peer-mode task IDs (the old tracker
// counter hack is gone: the shared LoadTracker is scheduler-internal now).
var peerTaskSeq atomic.Int64

// maxRestoredTaskSeq extracts the highest counter value N embedded in any
// counter-derived task ID of a restored fabric. The ID families minted from
// process-local counters all end in "-N" before an optional "/" or "#"
// suffix:
//
//	peer-plan-N              (submitPeerTask root tasks)
//	sess/sess-auto-N/d0/t#s  (session-scoped node tasks)
//	task-<capability>-N      (agentsyscall create_task)
//	plan-<origin>-N/rR#s     (agentsyscall create_plan rounds)
//
// A generic last-dash scan intentionally covers every such family (including
// future ones) instead of enumerating prefixes: over-seeding only skips ID
// values, while a missed family would let a fresh boot mint a colliding ID.
// Non-numeric or non-positive tails (UUIDs, engine step IDs) parse to 0 and
// are ignored.
func maxRestoredTaskSeq(ids []string) int64 {
	var maxN int64
	for _, id := range ids {
		dash := strings.LastIndexByte(id, '-')
		if dash < 0 || dash+1 >= len(id) {
			continue
		}
		tail := id[dash+1:]
		if cut := strings.IndexAny(tail, "/#"); cut >= 0 {
			tail = tail[:cut]
		}
		n, err := strconv.ParseInt(tail, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		if n > maxN {
			maxN = n
		}
	}
	return maxN
}

// seedPeerTaskSeq advances the peer-mode ID sequence to at least min — the
// cross-restart collision guard for peer-plan-N / sess-auto-N (see
// maxRestoredTaskSeq for why the restored log demands it: a re-minted
// peer-plan-N fails Create with ErrTaskExists, a re-minted sess-auto-N
// silently re-admits onto a restored session's root task). Grow-only via CAS:
// a min below the current value is a no-op, so a fresh store (min 0) leaves
// the counter untouched and concurrent submissions can never move it
// backwards.
func seedPeerTaskSeq(min int64) {
	for {
		cur := peerTaskSeq.Load()
		if min <= cur || peerTaskSeq.CompareAndSwap(cur, min) {
			return
		}
	}
}

// normalizedPeers resolves the flat peer population from config. The
// agents.peers structure is the DEFAULT; when it is empty (legacy config),
// the legacy agents.sub entries are normalized into peers (each sub's single
// Type becomes its only capability). Returns an empty slice when neither is
// configured (the caller reports it as an error).
func normalizedPeers(cfg *ares_config.Config) []ares_config.PeerAgentConfig {
	if len(cfg.Agents.Peers) > 0 {
		return cfg.Agents.Peers
	}
	peers := make([]ares_config.PeerAgentConfig, 0, len(cfg.Agents.Sub))
	for _, s := range cfg.Agents.Sub {
		peers = append(peers, ares_config.PeerAgentConfig{
			ID:           s.ID,
			Capabilities: []string{s.Type},
			Priority:     s.Priority,
		})
	}
	return peers
}

// createPeerAgents builds a set of peer agents WITHOUT a Leader ("Leader OFF"
// startup mode): a group of equal agents competes for tasks via
// capability-based scheduling, with no privileged orchestrator. Each
// configured sub-agent is spawned into the Agent Fabric WITH its execution
// body (the shared L2 router cognition) and its distilled experience prior,
// so the scheduler's candidate pool — queried live from the fabric — is
// exactly the set of real, executable agents. There is no second
// registration table to keep in sync: spawn/kill take effect on the next
// scheduler drain.
//
// The spawn_agent / create_task syscalls are wired into the shared ToolBinder
// so every agent can autonomously decide to decompose work and spawn peers.
// The Kernel enforces quota/capability validation on every spawn.
//
//nolint:gocyclo // createPeerAgents is a wiring hub (like runServe): it assembles the peer-mode kernel from Task Fabric, Agent Fabric, scheduler, evolution feedback, syscalls, recovery and the lifecycle in one function. Each branch is a distinct wiring step; splitting it would spread one assembly across helpers without reducing the decisions.
func createPeerAgents(
	ctx context.Context,
	cfg *ares_config.Config,
	comp *ares_bootstrap.Components,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	store ares_events.EventStore,
	strategySrc agents.StrategySource,
	expRepo repositories.ExperienceRepositoryInterface,
) ([]sub.Agent, *kernelHandle, error) {
	kernel := &kernelHandle{}

	// The flat Peers structure is the DEFAULT agent source; the legacy
	// Sub structure remains as the fallback so older configs keep working.
	peers := normalizedPeers(cfg)

	// Build sub-agent identities from the flat peer population.
	subAgents := createPeerSubAgents(peers, store)

	// Roles have no consumer anymore (the executor role-pinning and
	// the chat body that read them are both deleted) — peers run roleless.
	if len(subAgents) == 0 {
		return nil, nil, errors.New("peer mode: no peer agents configured (agents.peers or agents.sub)")
	}

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
			return nil, nil, fmt.Errorf("peer mode: restore task fabric from event store: %w", err)
		}
		// Cross-restart ID collision guard: the process-local counters reset
		// to 1 on every boot, but with a durable store the restored fabric
		// still holds the previous boot's peer-plan-N / sess-auto-N IDs. Seed
		// the sequence past the max embedded N so the next mint cannot
		// collide (grow-only: a no-op for a fresh in-memory store).
		restoredSeq = maxRestoredTaskSeq(kernel.fabric.IDs())
		seedPeerTaskSeq(restoredSeq)
	}
	// Experience-derived confidence prior — recorded skill/task outcomes
	// sharpen scheduling when the same pattern recurs. Nil (skills disabled)
	// keeps declared confidences.
	if expSrc := resolveExperienceConfidence(comp); expSrc != nil {
		kernel.fabric = kernel.fabric.WithConfidenceSource(expSrc)
	}

	// The static sub.Agent executor pool is gone. Its entries were
	// dead in peer mode — the scheduler skips static registrations whenever
	// the agent fabric is wired (the fabric's live population is the single
	// candidate source) and recovery-bound tasks resolve through
	// RegisterExecutorForTask instead. The map stays non-nil because the
	// scheduler copies it at construction; an empty pool simply means
	// "fabric only", which the drain path was designed for.
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

	// Strategy-shadow runs replay-only. The real-execution A/B runner
	// (chat tool-loop quanta) died with ReAct; strategy judgment is
	// runtime fitness回灌 + canary metrics. The sampler's replay fallback needs
	// no feeder and no scheduler hook, so there is nothing to wire here.

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

	// Collaboration-graph janitor: reclaim terminal residue left by fail-fast
	// / timeout submissions off the hot path (per-submission cleanup handles
	// the common case; this catches siblings that were in-flight then).
	runBackground(ctx, comp, "collab-gc", func(loopCtx context.Context) error {
		runCollabGCLoop(loopCtx, kernel.fabric, 60*time.Second)
		return nil
	})

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

	// The DAG execution gate (kernel.dag_execution in config).
	// Zero/absent config = legacy ReAct behavior (chat cognition for every
	// peer, L2 machinery test-only).
	//
	// Single execution path — the router body is always built.
	// The planner needs session-scoped dependencies (registry, fabric reader)
	// that are constructed here.
	var peerRouter agentfabric.Cognition
	sessionReg := agentfabric.NewSessionRegistry()

	// Read the L1 ToolClass DAG from the evolution components so
	// the planner can check enabled/budget/prior before growing L2
	// tool nodes. Nil when no tools are registered (permissive).
	var l1DAG *engine.MutableDAG
	if comp.NewEvolution != nil {
		l1DAG = comp.NewEvolution.ToolClassDAG()
	}

	planner, err := agentfabric.NewPlannerCognition(agentfabric.PlannerDeps{
		ChatClient: chatClient, // sub.ChatClient satisfies agentfabric.ChatClient
		ToolBinder: toolBinder, // sub.ToolBinder satisfies agentfabric.ToolBinder
		Sessions:   sessionReg,
		Fabric:     kernel.fabric,
		L1DAG:      l1DAG,
		// The planner is the evolution strategy actuator after
		// ReAct — deployed prompt/params steer plan growth.
		StrategySource: strategySrc,
		// M4.3 experience-loop read side: the planner reads the EXECUTING
		// agent's cognitive Context (the spawn-time ExperiencePrior stamped
		// by loadExperiencePrior) from the agent fabric and injects it as
		// the leading context message. Same value the spawn wrote — no
		// second experience-store query, and a Recover-restored state is
		// honored mid-flight.
		AgentFabric: agents,
		// Operator-tunable growth-depth guard (0/absent = default).
		MaxDepth: resolveMaxPlanDepth(cfg.Kernel.DAGExecution),
		Logger:   slog.Default(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("peer mode: create planner cognition: %w", err)
	}
	peerRouter = agentfabric.NewRouterCognitionWithPlanner(toolBinder, planner, sessionReg, slog.Default())

	// The registry is always wired, so the submission path always
	// admits sessions. There is no gate-off legacy mode anymore.
	kernel.sessionReg = sessionReg

	// Terminal-task reaper for L2 session tasks. Every grown node is
	// a fabric task and the fabric never self-harvests, so without this
	// loop the in-memory task map grows monotonically across a long-lived
	// serve (a known named cost). The registry is the keep-set authority: a
	// live session's tasks are its readable history (decision C) and are
	// never harvested; only tasks of released sessions die, after the
	// configured grace window.
	sessionReaper := taskfabric.NewReaperWithKeep(kernel.fabric, "sess/",
		resolveReaperGrace(cfg.Kernel.DAGExecution), sessionKeepSet(sessionReg))
	runBackground(ctx, comp, "l2-reaper", func(loopCtx context.Context) error {
		sessionReaper.Run(loopCtx.Done(), time.Minute)
		return nil
	})
	slog.InfoContext(ctx, "peer mode: L2 session task reaper wired",
		"grace", sessionReaper.GracePeriod())

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
				slog.WarnContext(loopCtx, "peer mode: answer-fail release subscription failed, idle TTL remains the backstop", "error", err)
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
					releaseSessionOnAnswerFailure(loopCtx, sessionReg, ev)
				}
			}
		})
	}

	// Every peer advertises the single L2 capability set via
	// peerCapabilities below. There is no legacy partition anymore.

	// Configured sub-agents ARE the fabric's dynamic population — each is
	// spawned WITH its execution body (the shared L2 router cognition) and
	// its distilled experience prior, instead of living only in the
	// static executor
	// registry. The scheduler queries the fabric on every drain, so this
	// is the single registration point: a future kill/retire immediately
	// removes the candidate, and the recovery/chaos loops manage the SAME
	// population they recover.
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
		}); err != nil {
			return nil, nil, fmt.Errorf("peer mode: spawn agent %q into fabric: %w", sa.ID(), err)
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

	// Inject agent priorities into the tracker (thread priority).
	for _, p := range peers {
		if p.Priority > 0 {
			tracker.SetPriority(p.ID, p.Priority)
		}
	}

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
	log.Info("peer mode: peer agents registered, Kernel scheduler started (no leader)", "count", len(subAgents))
	return subAgents, kernel, nil
}

// newPeerExecutor creates the sub.Agent identity for a dynamically spawned
// peer agent. The execution body is the shared L2 router (passed in) —
// a spawned agent is a real cognitive process, not a stub, and never ReAct.
func newPeerExecutor(
	agentID string,
	capability models.AgentType,
	cog agentfabric.Cognition,
) sub.Agent {
	return sub.New(
		agentID,
		capability,
		&cognitionExecutor{id: agentID, typ: capability, cog: cog},
		&sub.SubAgentConfig{
			Config: base.Config{
				ID:   agentID,
				Type: capability,
			},
		},
	)
}

// loadExperiencePrior loads the most recent distilled experience for the
// agent and returns it as the spawn prior (memory distill onto the agent
// lifecycle — async distillation feeds an experience
// store queried at spawn time and injected as a prior).
// The prior is injected as SpawnSpec.ExperiencePrior so the agent starts with
// reusable distilled experience as its cognitive context instead of a blank
// slate. Returns nil when the repo is unavailable, the agent has no distilled
// experience yet, or the query fails — a nil prior is the zero-value
// contract, never a startup error.
func loadExperiencePrior(ctx context.Context, expRepo repositories.ExperienceRepositoryInterface, agentID string) any {
	if expRepo == nil {
		return nil
	}
	exps, err := expRepo.ListByAgent(ctx, agentID, ares_events.DefaultTenantID, 1)
	if err != nil || len(exps) == 0 {
		return nil
	}
	exp := exps[0]
	return map[string]any{
		"type":        exp.Type,
		"problem":     exp.Problem,
		"solution":    exp.Solution,
		"constraints": exp.GetConstraints(),
	}
}

// submitPeerTask creates a task directly in the Task Fabric for the peer-agent
// runtime (no leader dispatch). This is the entry point for user-submitted
// work: the task enters READY and the Kernel scheduler picks it up via the
// normal Schedule → Acquire → RunQuantum path.
//
// It is exposed as POST /api/tasks on the serve HTTP layer (actionHandler),
// closing the user-submission loop: a request reaches the fabric and the
// scheduler executes it — no leader and no autopilot involved.
//
// Single execution path. EVERY submission becomes an L2 session task:
//   - session-less payloads are auto-admitted into a fresh session (the
//     capability argument is normalized to ares/plan with a warn log);
//   - the envelope always carries SessionID, so the planner's first quantum
//     finds a live graph and no session-less legacy task can exist.
//
// There is no legacy path anymore — a submission that cannot be admitted
// fails fast instead of degrading into an unrunnable task.
// planCapability is the submission capability in the single-L2-path world
// (every submitted task is the first plan quantum of its session).
const planCapability = "ares/plan"

// answerCapability is the terminal L2 node: the session's sole exit. A
// terminal failure of an ares/answer task means no successor can reach the
// session graph (the answer-failure release key, see
// releaseSessionOnAnswerFailure).
const answerCapability = "ares/answer"

func submitPeerTask(ctx context.Context, kernel *kernelHandle, capability string, payload map[string]any) (string, error) {
	if kernel == nil || kernel.fabric == nil {
		return "", errors.New("peer mode: kernel fabric not wired")
	}
	if payload == nil {
		payload = map[string]any{}
	}
	// Normalize every submission onto the L2 session path.
	sessionID, _ := payload["session_id"].(string)
	prompt, _ := payload["input"].(string)
	if sessionID == "" {
		sessionID = fmt.Sprintf("sess-auto-%d", peerTaskSeq.Add(1))
		payload["session_id"] = sessionID
	}
	if capability != planCapability {
		slog.InfoContext(ctx, "peer mode: capability normalized to single L2 execution path",
			"from", capability, "to", planCapability, "session_id", sessionID)
		capability = planCapability
	}
	if err := ensureSessionAdmission(ctx, kernel, sessionID, prompt); err != nil {
		return "", err
	}
	taskID := fmt.Sprintf("peer-plan-%d", peerTaskSeq.Add(1))

	env := &taskfabric.CheckpointEnvelope{
		Payload: payload,
	}
	// SessionID is always stamped (auto-admitted above), so the
	// plannerCognition always finds a live per-session L2 graph.
	env.SessionID = sessionID
	task := &taskfabric.Task{
		ID:         taskID,
		Capability: capability,
		// Origin stays "" — this is a root task (user-submitted work), no
		// agent caller. Agent-created tasks get their Origin from the
		// create_task syscall's tool context (kernel.CallerID).
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
		Checkpoint:  env,
	}
	if err := kernel.fabric.Create(task); err != nil {
		return "", fmt.Errorf("peer mode: create task: %w", err)
	}
	log.Info("peer mode: submitted task → READY", "task_id", taskID, "capability", capability)
	return taskID, nil
}

// peerExecutorAdapter satisfies the agentsyscall.Executor interface over an
// agentfabric Cognition (the L2 router). It is the same field-for-field
// StepOutcome translation as cognitionExecutor, but for the syscall contract
// instead of the scheduler contract (the two StepOutcome types differ, so one
// struct cannot implement both).
// (interface defined at the consumer).
type peerExecutorAdapter struct {
	id  string
	typ models.AgentType
	cog agentfabric.Cognition
}

// ID returns the agent's ID.
func (a *peerExecutorAdapter) ID() string { return a.id }

// Type returns the agent's type.
func (a *peerExecutorAdapter) Type() models.AgentType { return a.typ }
func (a *peerExecutorAdapter) ExecuteStep(ctx context.Context, task *models.Task) (*agentsyscall.StepOutcome, error) {
	out, err := a.cog.ExecuteStep(ctx, task)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &agentsyscall.StepOutcome{}, nil
	}
	return &agentsyscall.StepOutcome{
		Done:       out.Done,
		Checkpoint: out.Checkpoint,
		Result:     out.Result,
	}, nil
}

// fabricEventSink forwards agentfabric lifecycle records onto the shared
// ares_events bus so observability consumers (introspection feed, archive)
// see agent deaths and revivals in real time.
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
	payload := map[string]any{
		"agent_id": ev.AgentID,
	}
	if reason != "" {
		payload["reason"] = reason
	}
	if ev.ParentID != "" {
		payload["parent"] = ev.ParentID
	}
	return f.store.Append(ctx, ev.AgentID, []*ares_events.Event{{
		Type:       busType,
		ModuleName: "agentfabric",
		Payload:    payload,
		Timestamp:  ev.At,
	}}, 0)
}

// resolveExperienceConfidence wires the skill catalog's experience store as
// the task fabric's confidence prior. A nil catalog (skills
// disabled) keeps the fabric's declared confidences untouched.
//
// Args:
//   - comp: the bootstrap components carrying the live skill catalog.
//
// Returns:
//   - taskfabric.ConfidenceSource: the catalog-backed prior, or nil.
func resolveExperienceConfidence(comp *ares_bootstrap.Components) taskfabric.ConfidenceSource {
	if comp == nil || comp.SkillCatalog == nil {
		return nil
	}
	return ares_skills.NewExperienceConfidenceSource(comp.SkillCatalog.Experience())
}

// cognitionExecutor adapts an agentfabric Cognition to every executor
// contract in play, so the translation lives exactly once:
//   - sub.TaskExecutor (plus subAgent's structural stepExecutor check):
//     Execute runs a single quantum and translates the outcome; completion
//     is driven by the scheduler draining quanta, never by looping here.
//     RegisterFallback is a no-op (no fallback loop exists anymore).
//   - kernel.CapabilityExecutor (recovery-bound tasks): ID/Type/ExecuteStep.
//     Done/Checkpoint/Result ride opaquely, so both chat resume checkpoints
//     and L2 planner quanta survive the boundary.
//
// A nil body fails loud: identity-only agents (peer registry shells) must
// never be driven. (The syscall contract needs its own struct — see
// peerExecutorAdapter above — because its StepOutcome type differs.)
type cognitionExecutor struct {
	id  string
	typ models.AgentType
	cog agentfabric.Cognition
}

// newCognitionExecutor builds a recovery-bound executor over the given
// execution body. A nil body is a wiring error, surfaced at construction.
func newCognitionExecutor(agentID string, capability models.AgentType, cog agentfabric.Cognition) (*cognitionExecutor, error) {
	if cog == nil {
		return nil, fmt.Errorf("peer mode: recovery executor %q has no execution body", agentID)
	}
	return &cognitionExecutor{id: agentID, typ: capability, cog: cog}, nil
}

// ID implements kernel.CapabilityExecutor.
func (e *cognitionExecutor) ID() string { return e.id }

// Type implements kernel.CapabilityExecutor.
func (e *cognitionExecutor) Type() models.AgentType { return e.typ }

// Execute implements sub.TaskExecutor: a single quantum through the wrapped
// cognition.
func (e *cognitionExecutor) Execute(ctx context.Context, task *models.Task) (*models.TaskResult, error) {
	if e.cog == nil {
		return nil, fmt.Errorf("peer mode: executor %q has no execution body (identity-only agent must not be driven)", e.id)
	}
	out, err := e.cog.ExecuteStep(ctx, task)
	if err != nil {
		return nil, err
	}
	if out != nil && out.Done && out.Result != nil {
		return out.Result, nil
	}
	// Single-quantum pass-through: not done means the scheduler resumes it.
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.Success = false
	res.Reason = "quantum yielded; resume via scheduler"
	return res, nil
}

// RegisterFallback implements sub.TaskExecutor. No-op: no fallback loop.
func (e *cognitionExecutor) RegisterFallback(models.AgentType, sub.FallbackHandler) {
}

// ExecuteStep implements the quantum path shared by subAgent's structural
// stepExecutor check and kernel.CapabilityExecutor.
func (e *cognitionExecutor) ExecuteStep(ctx context.Context, task *models.Task) (*sub.StepOutcome, error) {
	if e.cog == nil {
		return nil, fmt.Errorf("peer mode: executor %q has no execution body (identity-only agent must not be driven)", e.id)
	}
	out, err := e.cog.ExecuteStep(ctx, task)
	if err != nil {
		return nil, err
	}
	return &sub.StepOutcome{Done: out.Done, Checkpoint: out.Checkpoint, Result: out.Result}, nil
}

// createPeerSubAgents builds the sub.Agent identities for the flat peer
// population (cfg.Agents.Peers). These are identity shells for the
// peer registry/IPC — execution flows through fabric-spawned router
// cognitions, so each shell carries a body-less adapter that fails loud if
// ever driven (it never is: the static scheduler pool is gone and one-shot
// Execute has no production callers).
//
// TODO(tech-debt): peer direct messaging (the sub.Agent message queue /
// SendMessage path) was removed as dead — these shells were always built
// with a nil queue, so peer delivery never succeeded; only the
// kernel-session collaboration topics (delegate-task / pipeline-stage /
// orchestrate-worker) are live. No heartbeat monitor — the fabric owns
// scheduling and lifecycle.
func createPeerSubAgents(
	peers []ares_config.PeerAgentConfig,
	store ares_events.EventStore,
) []sub.Agent {
	agents := make([]sub.Agent, 0, len(peers))
	for _, p := range peers {
		typ := ""
		if len(p.Capabilities) > 0 {
			typ = p.Capabilities[0]
		}
		agent := sub.New(
			p.ID,
			models.AgentType(typ),
			&cognitionExecutor{id: p.ID},
			&sub.SubAgentConfig{
				Config: base.Config{
					ID:   p.ID,
					Type: models.AgentType(typ),
				},
			},
			sub.WithEventStore(store),
		)
		agents = append(agents, agent)
	}
	return agents
}

// createChatClient creates a FailoverClient from the LLM config for Chat API support.
func createChatClient(cfg *ares_config.Config) (sub.ChatClient, error) {
	configs := make([]*llm.Config, 0, 1+len(cfg.LLM.Fallbacks))
	configs = append(configs, &llm.Config{
		Provider:  cfg.LLM.Provider,
		APIKey:    cfg.LLM.APIKey,
		BaseURL:   cfg.LLM.BaseURL,
		Model:     cfg.LLM.Model,
		Timeout:   cfg.LLM.Timeout,
		MaxTokens: cfg.LLM.MaxTokens,
	})
	for _, fb := range cfg.LLM.Fallbacks {
		provider := fb.Provider
		if provider == "" {
			provider = "openai"
		}
		configs = append(configs, &llm.Config{
			Provider:  provider,
			APIKey:    fb.APIKey,
			BaseURL:   fb.BaseURL,
			Model:     fb.Model,
			Timeout:   fb.Timeout,
			MaxTokens: fb.MaxTokens,
		})
	}

	timeout := time.Duration(cfg.LLM.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	rate := cfg.LLM.ScorerAPIRate
	burst := cfg.LLM.ScorerAPIBurst
	return llm.NewFailoverClient(configs, timeout, rate, burst)
}

// resolveMaxPlanDepth maps the configured plan-depth cap onto the planner's
// MaxDepth. Zero/negative means "planner default"
// (agentfabric.DefaultMaxPlanDepth): validation rejects negatives at load
// time, and the planner itself treats non-positive as default, so an invalid
// value can never widen or remove the guard even if it reaches the resolver.
func resolveMaxPlanDepth(c ares_config.DAGExecutionConfig) int {
	if c.MaxPlanDepth <= 0 {
		return agentfabric.DefaultMaxPlanDepth
	}
	return c.MaxPlanDepth
}

// resolveReaperGrace maps the configured terminal-task reaper grace onto a
// duration. Zero/absent passes through as 0, which the reaper itself
// defaults to 30s — the single default lives in taskfabric, not here. A
// negative cannot reach the resolver (Validate rejects it at load), and the
// reaper treats non-positive as its default, so a bad value can never
// disable the grace window.
func resolveReaperGrace(c ares_config.DAGExecutionConfig) time.Duration {
	if c.ReaperGrace <= 0 {
		return 0
	}
	return c.ReaperGrace
}

// resolveSessionIdleTTL maps the configured session idle TTL onto a
// duration. Zero/absent passes through as 0, which the registry sweep
// defaults to agentfabric.DefaultSessionIdleTTL — the single default lives
// in agentfabric, not here. A negative cannot reach the resolver (Validate
// rejects it at load), and the sweep treats non-positive as the default, so
// a bad value can never disable the TTL.
func resolveSessionIdleTTL(c ares_config.DAGExecutionConfig) time.Duration {
	if c.SessionIdleTTL <= 0 {
		return 0
	}
	return c.SessionIdleTTL
}

// peerCapabilities builds one peer's advertised capability set: the
// single L2 set (ares/root, ares/plan, ares/answer, tool/<name> per bound
// tool) and deliberately NOT the primary type — there is no legacy traffic
// anymore, so every peer serves the whole L2 set and the canary partition
// is retired with the gate.
func peerCapabilities(toolNames []string) []string {
	caps := []string{"ares/root", planCapability, answerCapability}
	for _, name := range toolNames {
		if name == "" {
			continue
		}
		caps = append(caps, "tool/"+name)
	}
	return caps
}

// selectRecoveryBody picks the recovery-bound execution body for one task
// (the L2 router for L2 session tasks, nil when there is no router
// or the capability is not L2-routable (caller falls back to a freshly
// built executor — post-D also cognition-backed, never ReAct).
// Recovery-bound tasks bypass the normal candidate pool, so the dispatch
// must happen here, per task, or a rescued task would run on the wrong body.
func selectRecoveryBody(router agentfabric.Cognition, capability string) agentfabric.Cognition {
	if router == nil {
		return nil
	}
	if !agentfabric.IsL2Capability(capability) {
		return nil
	}
	return router
}

// ensureSessionAdmission admits one L2 session before its first task is
// created: register the session graph, subscribe it to the shared
// incremental compiler, and compile the root task the planner's first
// quantum falls back to.
//
// The caller is submitPeerTask, and only when the request carries a
// session_id AND the gate wired a registry (nil registry = gate off =
// legacy path, session payloads stay envelope-only). Admission is idempotent:
// resubmitting into a live session is a multi-turn continuation, not an
// error — the existing session is reused and no duplicate root is compiled.
//
// Failures are fail-fast (nothing half-created): a session the caller asked
// for but we cannot admit must not silently degrade into an unrunnable
// task. Anything InitSession registered before the failure is released
// again, so a retry starts clean.
func ensureSessionAdmission(ctx context.Context, kernel *kernelHandle, sessionID, prompt string) error {
	if kernel == nil || sessionID == "" {
		return nil
	}
	// Single execution path. A session that cannot be admitted must
	// fail fast — a session-scoped task without a live graph is unrunnable.
	// (The old gate-off silent skip is gone with the gate.)
	if kernel.sessionReg == nil {
		return fmt.Errorf("peer mode: cannot admit session %q without a session registry", sessionID)
	}
	// A session ID containing "/" breaks the reaper keep-set —
	// SessionIDFromNode reverse-parses at the first slash, so "a/b" maps
	// its tasks back to a session "a" that is not live, and the reaper
	// harvests a LIVE session's readable history once the grace window
	// passes (the exact decision-C accident, triggered by pure client
	// input). Reject at the admission boundary, same level as the empty
	// ID; the registry enforces the same contract as a backstop.
	if strings.Contains(sessionID, "/") {
		return fmt.Errorf("peer mode: session id %q must not contain a slash", sessionID)
	}
	if _, err := kernel.sessionReg.GetSession(sessionID); err == nil {
		return nil
	} else if !errors.Is(err, agentfabric.ErrSessionNotFound) {
		return fmt.Errorf("peer mode: look up session %q: %w", sessionID, err)
	}
	if kernel.compileCoord == nil || kernel.fabric == nil {
		return fmt.Errorf("peer mode: cannot admit session %q without compile coordinator and fabric", sessionID)
	}

	// The compile subscription must outlive the submission request: tying it
	// to the request context would kill the projection the moment the HTTP
	// handler returns, while the session lives on.
	liveCtx := context.WithoutCancel(ctx)
	g, err := kernel.sessionReg.InitSession(liveCtx, sessionID, prompt, nil,
		func(subCtx context.Context, dag *engine.MutableDAG) (stop func()) {
			return kernel.compileCoord.SubscribeGraphEvents(subCtx, dag)
		})
	if err != nil {
		// A concurrent admitter may have won the race between our
		// GetSession and InitSession — re-check before failing.
		if errors.Is(err, agentfabric.ErrSessionAlreadyExists) {
			if _, err2 := kernel.sessionReg.GetSession(sessionID); err2 == nil {
				return nil
			}
		}
		return fmt.Errorf("peer mode: init session %q: %w", sessionID, err)
	}

	// Compile the root task the planner's first quantum reads (or falls
	// back to the payload input when still pending). An already-compiled
	// root means a retried admission after a partial failure — adopt it,
	// but ONLY while that root is still live (see below).
	rootStep := g.DAG().StepIndex()[g.Root()]
	if _, err := kernel.fabric.CompileNode(liveCtx, planprojection.ProjectStep(rootStep)); err != nil {
		if !errors.Is(err, taskfabric.ErrTaskExists) {
			releaseSessionQuietly(kernel, sessionID)
			return fmt.Errorf("peer mode: compile session %q root: %w", sessionID, err)
		}
		// An existing TERMINAL root does not belong to a retry — it
		// belongs to a previous session that already released under this
		// same ID (the natural client "continue the chat" behavior after
		// an answer). Adopting it would hand the new turn the old prompt
		// (rootCognition wrote its input into the envelope output) and let
		// same-named node tasks resolve to old tool outputs read as fresh
		// results — silently, with the keep-set then protecting the stale
		// tasks forever. The registry just told us this session is NOT
		// live, so no planner is reading those envelopes: harvest them
		// (the reaper's job, done early) and recompile clean.
		if stale, terr := kernel.fabric.Task(g.Root()); terr == nil &&
			(stale.State == taskfabric.StateCompleted || stale.State == taskfabric.StateFailed) {
			n := harvestReleasedSession(kernel.fabric, sessionID)
			slog.InfoContext(liveCtx, "peer mode: session re-admitted after release, harvested stale tasks before recompiling root",
				"session_id", sessionID, "harvested", n)
			if _, err := kernel.fabric.CompileNode(liveCtx, planprojection.ProjectStep(rootStep)); err != nil {
				releaseSessionQuietly(kernel, sessionID)
				return fmt.Errorf("peer mode: recompile session %q root: %w", sessionID, err)
			}
		}
	}
	slog.InfoContext(liveCtx, "peer mode: admitted L2 session",
		"session_id", sessionID, "root", g.Root())
	return nil
}

// harvestReleasedSession deletes every harvestable task under a released
// session's ID prefix: terminal (COMPLETED/FAILED) and READY tasks
// go; in-flight ones (LEASED/RUNNING/SUSPENDED) are refused by Delete and
// left for the reaper — they belong to work genuinely still running.
// Returns the number of tasks removed.
func harvestReleasedSession(fabric *taskfabric.Fabric, sessionID string) int {
	prefix := agentfabric.SessionTaskPrefix(sessionID)
	removed := 0
	for _, id := range fabric.IDs() {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		if fabric.Delete(id) == nil {
			removed++
		}
	}
	return removed
}

// releaseSessionQuietly drops a half-admitted session during failure
// cleanup. The release itself is best-effort: the admission already failed,
// and a release miss only leaves a normal session behind for the reaper.
func releaseSessionQuietly(kernel *kernelHandle, sessionID string) {
	_ = kernel.sessionReg.ReleaseSession(sessionID)
}

// sessionKeepSet builds the reaper's keep predicate from the session
// registry: a task is kept while its owning session is still live.
// The registry is the single authority — an ID that parses as a session
// task but has no live session (released, or never admitted by this
// process) is harvestable once the grace window passes.
func sessionKeepSet(reg *agentfabric.SessionRegistry) func(taskID string) bool {
	return func(taskID string) bool {
		sid, ok := agentfabric.SessionIDFromNode(taskID)
		if !ok {
			return false
		}
		_, err := reg.GetSession(sid)
		return err == nil
	}
}

// releaseSessionOnAnswerFailure releases a session whose terminal answer
// task FAILED. The event payload carries the capability and the session id
// (taskfabric stamps both on must-persist events; task.failed is one), so
// the check is pure payload reading. Only the FAILED state releases: the
// requeue branch of fabric.Fail also records task.failed (state READY) while
// the retry budget still stands, and an answer that succeeds on retry must
// not lose its session. Only the answer node releases here: it is the
// session's sole terminal exit, so its terminal failure leaves the graph
// unreachable from any successor. A release miss (session already gone —
// released earlier, or reaped by the idle TTL) is logged, not an error: the
// postcondition — no live session — already holds.
func releaseSessionOnAnswerFailure(ctx context.Context, reg *agentfabric.SessionRegistry, ev *ares_events.Event) {
	if ev == nil || reg == nil {
		return
	}
	if c, _ := ev.Payload["capability"].(string); c != answerCapability {
		return
	}
	if s, _ := ev.Payload["state"].(string); taskfabric.TaskState(s) != taskfabric.StateFailed {
		return
	}
	sid, _ := ev.Payload["session_id"].(string)
	if strings.TrimSpace(sid) == "" {
		return
	}
	if err := reg.ReleaseSession(sid); err != nil {
		slog.WarnContext(ctx, "peer mode: answer-failure release found no live session",
			"session", sid, "error", err)
		return
	}
	slog.InfoContext(ctx, "peer mode: released session after terminal answer failure",
		"session", sid, "task_id", ev.StreamID)
}

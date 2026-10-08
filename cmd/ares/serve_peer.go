package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/peer"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/agentsyscall"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	"github.com/Timwood0x10/ares/internal/introspect"
	"github.com/Timwood0x10/ares/internal/runtime"
	memory "github.com/Timwood0x10/ares/internal/runtime/memory"
	"github.com/Timwood0x10/ares/internal/runtime/protocol/ahp"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// ErrAskAgentNoReply is returned when ask_agent completes without receiving
// a reply — either the request timed out or the registry has no reply path.
//
// Deprecated: use agentsyscall.ErrAskAgentNoReply instead. This var is retained
// for the default (non-evolution) branch which still returns it directly.
var ErrAskAgentNoReply = agentsyscall.ErrAskAgentNoReply

// memoryRuntimeStatser is the optional capability of a MemoryManager to
// report a live status frame. Only *memoryManager implements it (via
// memory.RuntimeStatus); a nil manager or a config-only fallback yields a
// nil source, and the panel omits the Memory section.
type memoryRuntimeStatser interface {
	RuntimeStatus() memory.RuntimeStatus
}

// memoryPanelSource adapts the runtime memory manager to the introspect
// panel's Memory source. Returns nil when memory is disabled/unbuilt or the
// manager does not expose RuntimeStatus — a nil source omits Snapshot.Memory
// and the panel renders the disabled state.
func memoryPanelSource(mgr memory.MemoryManager) func() introspect.MemoryStatus {
	if mgr == nil {
		return nil
	}
	statser, ok := mgr.(memoryRuntimeStatser)
	if !ok {
		return nil
	}
	return func() introspect.MemoryStatus {
		s := statser.RuntimeStatus()
		return introspect.MemoryStatus{
			Wired:                 true,
			Sessions:              s.Sessions,
			Tasks:                 s.Tasks,
			DistillationEngine:    s.DistillationEngineArmed,
			Retrievers:            s.Retrievers,
			Skills:                s.Skills,
			MaxHistory:            s.MaxHistory,
			SessionMaxHistory:     s.SessionMaxHistory,
			DistillationThreshold: s.DistillationThreshold,
			MaxSessions:           s.MaxSessions,
			EnableRAG:             s.EnableRAG,
			RAGTopK:               s.RAGTopK,
			RAGMinScore:           s.RAGMinScore,
			Storage:               s.Storage,
			Started:               s.Started,
		}
	}
}

// createAndServeAgents builds and registers the flat peer-agent population with
// the runtime manager. This is the ONLY production serve path (the leader is
// removed): the configured peers spawn into the Agent Fabric as
// the dynamic population, the scheduler queries the fabric for candidates,
// and the spawn_agent / create_task syscalls are wired into the tool binder for
// autonomous decomposition. The peer kernel is returned so the serve HTTP layer
// can expose the task-submission endpoint (POST /api/tasks → submitPeerTask).
func createAndServeAgents(
	ctx context.Context,
	cfg *ares_config.Config,
	internalReg *core_tools.Registry,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	comp *ares_bootstrap.Components,
	mgr *runtime.Manager,
) ([]sub.Agent, *kernelHandle, error) {
	var strategySrc agents.StrategySource
	if comp.NewEvolution != nil {
		strategySrc = ares_bootstrap.NewStrategySource(comp.NewEvolution.StrategyStore)
		if strategySrc != nil {
			log.Info("serve: evolution strategy source wired into agents (GA deploy → runtime read)")
		}
	}

	// Inject the L1 ToolClass capability graph into the evolution
	// system BEFORE creating peer agents so the plannerCognition can
	// read enabled/budget/prior at L2 growth time. The L1 graph is NOT
	// compiled into taskfabric — it is a capability catalog, not an
	// execution plan (L1 ≠ L2).
	injectToolClassDAG(comp, toolBinder)

	subAgents, peerKernel, err := createPeerAgents(
		ctx, cfg, comp, chatClient, toolBinder,
		comp.EventStore, strategySrc, comp.ExpRepo,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create peer agents: %w", err)
	}
	// Register agents with the runtime manager.
	for _, sa := range subAgents {
		factory := func() base.Agent { return sa }
		mgr.RegisterAgent(sa, factory)
	}
	log.Info("serve: peer agents registered directly to Kernel", "count", len(subAgents))

	wireLiveDAGAndCompile(ctx, cfg, comp, peerKernel, mgr)
	wireEvolutionLoops(ctx, cfg, comp, peerKernel)
	chaosStatus := wireIntrospectPanel(ctx, comp, peerKernel)
	wireChaos(ctx, comp, cfg, peerKernel, func() bool {
		if comp.NewEvolution == nil {
			return false
		}
		return comp.NewEvolution.GAGenerationActive()
	}, chaosStatus)
	if err := peerKernel.adopt(ctx, comp.SystemRuntime); err != nil {
		return nil, nil, err
	}
	return subAgents, peerKernel, nil
}

// wireLiveDAGAndCompile injects the configured agent population as the live
// workflow topology for the evolution system and the runtime manager.
//
// The agent-population DAG is TOPOLOGY, not work: projecting it into the
// task fabric compiled one READY task per peer with Capability = the peer's
// yaml capability (e.g. "worker"), which no L2 executor advertises — the
// scheduler then retried that task forever ("no capable candidate", one
// retry every few seconds). Structure patches stay observable where they
// actually act: the runtime manager's AgentDAG registry and the evolution
// executors (UpdateLiveDAG → SetGraph/SetDAG/SetGraph). The compile
// coordinator remains the projection path for REAL session/plan graphs,
// which carry L2 capabilities and session envelopes; the lifecycle's
// compile-info provider therefore counts real compiles only.
func wireLiveDAGAndCompile(
	ctx context.Context,
	cfg *ares_config.Config,
	comp *ares_bootstrap.Components,
	peerKernel *kernelHandle,
	mgr *runtime.Manager,
) {
	// Live-DAG injection (closes the evolution structure-patch loop): the
	// configured agent population IS the live workflow topology. Register it
	// on the runtime manager (under the shared live-DAG key) and swap it
	// into the evolution executors — without this, workflow/recovery patches
	// mutated the synthetic input→process→output bootstrap DAG forever and
	// "live promotion" was unobservable.
	if comp.NewEvolution != nil {
		liveDAG, dagErr := buildLiveAgentDAG(cfg)
		switch {
		case dagErr == nil:
			mgr.RegisterAgentDAG(runtime.AgentDAGLiveKey, liveDAG)
			if err := comp.NewEvolution.UpdateLiveDAG(liveDAG); err != nil {
				log.WarnContext(ctx, "serve: live DAG injection failed (evolution keeps placeholder)", "err", err)
			} else {
				log.InfoContext(ctx, "serve: live agent DAG injected into evolution executors (nodes)", "count", len(liveDAG.Steps()))
			}

			// Lifecycle attribution (/api/evolution/lifecycle generation /
			// gates / compile_id) reads the shared L2 compile coordinator —
			// the one Sessions already compiles session graphs through. The
			// agent topology is deliberately NOT compiled into the task
			// fabric (see the function comment).
			if peerKernel != nil && peerKernel.compileCoord != nil &&
				comp.NewEvolution.Lifecycle != nil {
				comp.NewEvolution.Lifecycle.SetCompileInfoProvider(
					peerKernel.compileCoord,
				)
			}
		case errors.Is(dagErr, errNoLiveAgentDAG):
			log.InfoContext(ctx, "serve: no peers configured; evolution keeps placeholder DAG")
		default:
			log.WarnContext(ctx, "serve: live agent DAG build failed (evolution keeps placeholder)", "err", dagErr)
		}
	}
}

// wireEvolutionLoops wires the evolution-aware quota, spawn-gate and population
// loops that push GA strategy decisions into the kernel ("Evolution decides;
// Kernel enforces").
func wireEvolutionLoops(
	ctx context.Context,
	cfg *ares_config.Config,
	comp *ares_bootstrap.Components,
	peerKernel *kernelHandle,
) {
	// Evolution-aware quota loop: "Evolution decides; Kernel enforces".
	// The GA strategy store publishes a
	// quota.budget param; the quota manager pushes it into the Agent Fabric's
	// resource admission budget on a fixed cadence. Without this the
	// deployed budget was consumed by nothing — the fabric kept its startup
	// config budget forever. The loop is best-effort: a nil evolution store
	// yields a nil policy source, so Apply is a no-op that leaves the
	// configured cfg.Kernel.Resources budget untouched (backward compatible).
	if peerKernel != nil && peerKernel.agents != nil && comp.NewEvolution != nil {
		quotaSrc := ares_bootstrap.NewQuotaPolicySource(comp.NewEvolution.StrategyStore, cfg.Kernel.Resources)
		if quotaSrc != nil {
			quotaMgr := aresrecovery.NewEvolutionAwareQuotaManager(peerKernel.agents, quotaSrc)
			runBackground(ctx, comp, "evolution-quota", func(loopCtx context.Context) error {
				runKernelQuotaLoop(loopCtx, quotaMgr, parseKernelLoopConfig(cfg))
				return nil
			})
			log.Info("serve: evolution quota loop wired (GA budget → fabric P5 admission)")
		}

		// Evolution-aware spawn gate: "Evolution decides; Kernel enforces". The GA strategy store publishes
		// spawn.{enabled,max_concurrent,preferred_capabilities}; the spawner
		// enforces them so every RECOVERY replacement spawn honors the evolved
		// timing gate and capability preference (the population cap is bypassed
		// for recovery — a self-healing spawn must not be stranded by quota).
		// Without this, the deployed spawn policy was consumed by nothing and
		// recovery always used the plain fabric spawn. Best-effort: a nil store
		// yields a nil source, so WithSpawner is skipped (plain spawn).
		if peerKernel.recovery != nil {
			spawnSrc := ares_bootstrap.NewSpawnPolicySource(comp.NewEvolution.StrategyStore)
			if spawnSrc != nil {
				spawner := aresrecovery.NewEvolutionAwareSpawner(peerKernel.agents, spawnSrc)
				peerKernel.recovery.WithSpawner(spawner)
				log.Info("serve: evolution spawn gate wired (GA policy → recovery spawn enforcement)")
			}
		}

		// Evolution-aware population loop (runtime adaptation).
		// "Evolution decides; Kernel enforces": the GA
		// strategy store publishes population.{spawn,retire}; the adapter
		// applies the desired delta through the Agent Fabric's spawn/retire
		// primitives on a fixed cadence (idempotent — an empty policy is a
		// no-op). This is the missing top-level growth/shrink path: the spawn
		// gate (stage-2) only shapes RECOVERY replacements, whereas this loop
		// grows or shrinks the live population per the evolved topology.
		// Best-effort: a nil store yields a nil source, so the loop is skipped.
		popSrc := ares_bootstrap.NewPopulationPolicySource(comp.NewEvolution.StrategyStore)
		if popSrc != nil {
			popAdapter := aresrecovery.NewPopulationAdapter(peerKernel.agents, popSrc)
			runBackground(ctx, comp, "evolution-population", func(loopCtx context.Context) error {
				aresrecovery.RunKernelEvolutionLoop(loopCtx, popAdapter, 0, 0)
				return nil
			})
			log.Info("serve: evolution population loop wired (GA topology → fabric spawn/retire)")
		}
	}
}

// wireIntrospectPanel wires the runtime introspection panel (pull-only
// collector + UI/JSON handler) and returns the chaos reporter shared with
// wireChaos.
func wireIntrospectPanel(
	ctx context.Context,
	comp *ares_bootstrap.Components,
	peerKernel *kernelHandle,
) *introspect.ChaosReporter {
	// Runtime introspection panel: a pull-only
	// collector refreshes the latest-wins store every 2s; actionHandler serves
	// the embedded UI at GET /introspect and JSON at /api/v1/introspect/*.
	// The chaos status source is a shared reporter the chaos loops
	// update — created here so the collector and wireChaos see the same frame.
	//
	// SECURITY: the introspect handler is unauthenticated and its eventstream
	// endpoint exposes raw event payloads (task inputs, checkpoints). Only
	// expose it on localhost/an internal network or behind an authenticating
	// reverse proxy — never bind it directly to a public address.
	chaosStatus := introspect.NewChaosReporter()
	if peerKernel.scheduler != nil && peerKernel.fabric != nil && peerKernel.agents != nil {
		store := &introspect.Store{}
		collabReporter := introspect.NewCollabReporter()
		collector := introspect.NewCollector(introspect.Sources{
			Kernel:    peerKernel.scheduler.Snapshot,
			Fabric:    peerKernel.fabric.LeaseSnapshot,
			Agents:    peerKernel.agents.AgentsView,
			Chaos:     chaosStatus.Snapshot,
			Tasks:     peerKernel.fabric.TaskSnapshot,
			Decisions: peerKernel.scheduler.DecisionsSnapshot,
			// Collab: no producer records collaboration edges today; the
			// reporter yields an empty graph. Wire a producer (e.g. the
			// spawn/collaboration IPC path) before enabling the panel tab.
			Collab: collabReporter.Snapshot,
			Memory: memoryPanelSource(comp.Memory),
		})
		peerKernel.intro = introspect.NewHandler(store).WithEventStore(comp.EventStore).
			// The panel snapshot also carries the System Runtime
			// component graph (kernel pillars + bootstrap infrastructure),
			// so a "false Ready" kernel is visible on the read surface. The
			// provider is read-only; the endpoint is read-gated like the
			// rest of the JSON feed.
			WithSystemRuntime(func() any { return comp.Snapshot() })
		sink := introspect.NewSink(store).WithCollab(collabReporter)
		comp.GoBackground(ctx, "introspect-sink", func(ctx context.Context) error {
			return sink.Run(ctx, comp.EventStore)
		})
		comp.GoBackground(ctx, "introspect-collector", func(ctx context.Context) error {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					store.Set(collector.Collect())
				}
			}
		})
		log.Info("serve: introspect panel wired (GET /introspect)")
	}
	return chaosStatus
}

// buildPeerRegistry registers the peer agents' message senders into a
// peer.Registry so agents can exchange messages directly without routing
// through a privileged orchestrator (primitive 2: peer-to-peer agent
// messaging). Agents that do not expose SendMessage (interface assertion) are
// skipped, not an error.
//
// TODO(tech-debt): no production agent exposes the SendMessage surface any
// more — it was removed with the sub.Agent message queue — so this registry
// is always empty and non-evolution ask_agent sends fail loud with "not
// registered" (equivalent to the previous always-failing nil-queue
// delivery). Kept for the discovery contract; removing it means
// restructuring the non-evolution ask_agent branch.
func buildPeerRegistry(subAgents []sub.Agent) *peer.Registry {
	reg := peer.NewRegistry()
	for _, sa := range subAgents {
		if sender, ok := sa.(interface {
			SendMessage(context.Context, *ahp.AHPMessage) error
		}); ok {
			_ = reg.Register(sa.ID(), sender.SendMessage)
		}
	}
	return reg
}

// setupPeerRegistry builds the peer-to-peer messaging registry. When the
// evolution system is wired, the peer channel is bridged through the
// evolution-aware IPC; otherwise the plain direct peer channel
// is used.
func setupPeerRegistry(
	ctx context.Context,
	g *errgroup.Group,
	subAgents []sub.Agent,
	comp *ares_bootstrap.Components,
	kernel *kernelHandle,
) (*peer.Registry, error) {
	var reg *peer.Registry
	switch {
	case comp.NewEvolution != nil:
		bridge, err := wireEvolutionIPC(subAgents, comp.NewEvolution.StrategyStore, comp.Observability.GlobalTracer, kernel)
		if err != nil {
			return nil, fmt.Errorf("wire evolution IPC: %w", err)
		}
		reg = bridge.reg
		// Dead-letter observability (RUNTIME.md #10 closure): the bus records
		// every undeliverable/failed request, but the store had no reader —
		// failures vanished at the error-return boundary. A background loop
		// surfaces the count (and a sample of recent reasons) periodically so
		// messaging failures are operator-visible. Deliberately observe-only:
		// auto-redelivery of handler-rejected messages would retry
		// non-transient failures — redelivery stays an operator decision.
		g.Go(func() error {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			last := 0
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					cur := bridge.DeadLetterCount()
					if cur > 0 && cur != last {
						dl := bridge.ipc.Bus().DeadLetters().Snapshot()
						reasons := make(map[string]int, 4)
						for _, e := range dl {
							reasons[e.Reason]++
						}
						slog.WarnContext(ctx, "peer mode: IPC dead letters retained (undeliverable/failed requests)",
							"count", cur, "reasons", reasons)
					}
					last = cur
				}
			}
		})
		// Arm the collaboration perception channel. Attaching here
		// (rather than inside wireEvolutionIPC) keeps the bridge builder free
		// of an evolution-observer parameter, and this is the only production
		// site where the bus and the recorder are both in scope. A nil recorder
		// (channel not armed — the default) leaves the bus unobserved.
		if rec := comp.NewEvolution.ChannelFeedback; rec.CollaborationArmed() {
			bridge.ipc.Bus().WithCollaborationObserver(rec)
			log.Info("serve: collaboration feedback channel armed (evolution reads collaboration receipts)")
		}
		// Wire ask_agent to ipc.Request (C-1 fix: background launch). The
		// syscall Kernel is built in peer_mode before the bridge exists, so
		// the collaboration primitive is injected here once the bridge is
		// ready.
		//
		// DEADLOCK FIX (C-1): Bus.Request MUST NOT be called inside the
		// asking quantum's goroutine. The asking quantum runs inside drain's
		// goroutine pool (drain → wg.Wait → execute → RunQuantum →
		// toolCognition.ExecuteStep → binder.CallTool → AskAgent → fn).
		// The target's handler (executeAskViaSession) submits a plan task
		// into the SAME fabric and waits for its answer — but that task can
		// only progress when drain advances, and drain is blocked in
		// wg.Wait() waiting for the asking quantum to return. Result: full
		// service deadlock.
		//
		// Fix: launch Bus.Request in a managed background goroutine
		// (runBackground, per code rules §4.1 — no bare goroutines), return
		// ErrAskAgentYielding immediately. The asking quantum completes
		// without blocking, drain's wg.Wait() unblocks, and the scheduler is
		// free to drain the target's tasks.
		//
		// The reply is currently fire-and-forget: it is logged but NOT
		// written back to the asker. The LLM receives Status="pending".
		if kernel != nil && kernel.syscalls != nil {
			ipc := bridge.ipc.Bus()
			kernel.syscalls.SetAskAgent(func(ctx context.Context, from, to, topic string, payload any) (any, error) {
				// Derive a context detached from the quantum's lifecycle so the
				// background request survives the quantum completing.
				reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), askAgentTimeout)
				runBackground(reqCtx, comp, "ask-agent-reply", func(ctx context.Context) error {
					defer cancel()
					reply, err := ipc.Request(ctx, from, to, topic, payload, askAgentTimeout)
					if err != nil {
						log.Warn("ask_agent: background request failed", "from", from, "to", to, "topic", topic, "error", err)
						return nil // nil = clean exit (GoBackground swallows non-nil as a restartable error)
					}
					if reply == nil {
						log.Warn("ask_agent: background request returned no reply", "from", from, "to", to, "topic", topic)
						return nil
					}
					log.Info("ask_agent: background reply received", "from", from, "to", to, "topic", topic)
					return nil
				})
				return nil, agentsyscall.ErrAskAgentYielding
			})
			log.Info("serve: ask_agent syscall wired to evolution-aware IPC (background launch, no drain deadlock)", "count", len(reg.IDs()))
		}
		log.Info("peer registry wired through evolution-aware IPC: agents registered", "count", len(reg.IDs()))
	default:
		reg = buildPeerRegistry(subAgents)
		// The ask_agent tool is advertised on the binder in every serve
		// path, so the default (non-evolution) branch must also wire the
		// collaboration primitive — otherwise the tool is advertised but
		// every call fails loud. Route through the plain peer registry
		// (no evolution observation here; the collaboration channel stays
		// disarmed until the evolution branch is taken).
		if kernel != nil && kernel.syscalls != nil {
			plainReg := reg
			// Left synchronous on purpose: this registry has no registered
			// sender (no production agent exposes SendMessage), so Send fails
			// immediately with "not registered" — a fast, useful diagnostic
			// the LLM should see, not a blocking wait to detach.
			kernel.syscalls.SetAskAgent(func(ctx context.Context, from, to, topic string, payload any) (any, error) {
				body := map[string]any{"topic": topic}
				if m, ok := payload.(map[string]any); ok {
					body["payload"] = m
				} else if payload != nil {
					body["payload"] = payload
				}
				msg := ahp.NewTaskMessage(from, to, "", "", body)
				if err := plainReg.Send(ctx, to, msg); err != nil {
					return nil, err
				}
				return nil, ErrAskAgentNoReply // plain registry has no reply path
			})
			log.Info("serve: ask_agent syscall wired to plain peer registry (agents)", "count", len(reg.IDs()))
		}
		log.Info("peer registry wired: agents registered", "count", len(reg.IDs()))
	}
	// Retain the registry on the kernel handle at construction time (the
	// return value was previously discarded by callers). serve.go also assigns
	// it as a defensive second write; the retention contract must not depend
	// on a single call site.
	if kernel != nil {
		kernel.peerRegistry = reg
	}
	return reg, nil
}

// injectToolClassDAG builds the L1 ToolClass capability graph from the tool
// binder's schemas and injects it into the evolution system. Called
// BEFORE peer agents are created so the plannerCognition (constructed inside
// createPeerAgents when the DAG gate is open) can read enabled/budget/prior
// at L2 growth time. The L1 graph is NOT compiled into taskfabric — it is a
// capability catalog, not an execution plan (L1 ≠ L2).
func injectToolClassDAG(comp *ares_bootstrap.Components, toolBinder sub.ToolBinder) {
	if comp.NewEvolution == nil {
		return
	}
	l1DAG, err := buildToolClassDAG(toolBinder.GetToolSchemas())
	switch {
	case err == nil:
		comp.NewEvolution.SetToolClassDAG(l1DAG)
		log.Info("serve: L1 ToolClass DAG injected into evolution (nodes)", "count", len(l1DAG.Steps()))
	case errors.Is(err, errNoToolSchemas):
		log.Info("serve: no tool schemas; L1 ToolClass DAG skipped (constraints default to permissive)")
	default:
		log.Warn("serve: L1 ToolClass DAG build failed (constraints default to permissive)", "err", err)
	}
}

// errNoLiveAgentDAG is returned when no peers are configured: the caller
// keeps the bootstrap placeholder rather than injecting an empty graph.
var errNoLiveAgentDAG = errors.New("no peer agents configured for a live DAG")

// errNoToolSchemas is returned when the tool binder exposes no tool schemas:
// the L1 capability graph would be empty, so the caller keeps the bootstrap
// placeholder instead of injecting an empty graph.
var errNoToolSchemas = errors.New("no tool schemas available for a ToolClass DAG")

// buildLiveAgentDAG materializes the configured agent population as a real
// MutableDAG: one node per peer (AgentType = primary capability), dependency
// edges from the legacy agents.sub entries' Dependencies when present.
//
// This is the live topology the evolution system's structure patches act on.
// Historically serve never called UpdateLiveDAG, so workflow/recovery
// patches mutated the synthetic input→process→output bootstrap DAG forever —
// the "live runtime" promotion affected nothing observable. The returned DAG
// is registered on the runtime manager AND injected into the evolution
// executors so graph/recovery patches land on the agent graph actually shown
// in the runtime snapshot.
//
// Returns (nil, errNoLiveAgentDAG) when no peers are configured — the caller
// matches on that sentinel and keeps the bootstrap placeholder rather than
// injecting an empty graph.
func buildLiveAgentDAG(cfg *ares_config.Config) (*engine.MutableDAG, error) {
	peers := normalizedPeers(cfg)
	if len(peers) == 0 {
		return nil, errNoLiveAgentDAG
	}

	// Legacy sub entries may declare Dependencies between agents; carry them
	// over so older configs keep their declared topology.
	legacyDeps := make(map[string][]string, len(cfg.Agents.Sub))
	for _, s := range cfg.Agents.Sub {
		if len(s.Dependencies) > 0 {
			legacyDeps[s.ID] = append([]string(nil), s.Dependencies...)
		}
	}

	steps := make([]*engine.Step, 0, len(peers))
	seen := make(map[string]bool, len(peers))
	for _, p := range peers {
		if p.ID == "" || seen[p.ID] {
			continue // defensive: NewMutableDAG rejects duplicate ids anyway
		}
		seen[p.ID] = true
		typ := ""
		if len(p.Capabilities) > 0 {
			typ = p.Capabilities[0]
		}
		step := &engine.Step{
			ID:        p.ID,
			Name:      p.ID,
			AgentType: typ,
			Input:     fmt.Sprintf("capability:%s", typ),
		}
		if deps, ok := legacyDeps[p.ID]; ok {
			step.DependsOn = deps
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, errNoLiveAgentDAG
	}

	dag, err := engine.NewMutableDAG(steps)
	if err != nil {
		return nil, fmt.Errorf("build live agent DAG: %w", err)
	}
	return dag, nil
}

// L1 metadata keys for ToolClass evolution constraints. The L1 graph's
// Metadata is string-only (engine.Step.Metadata), so budget/prior are stored
// as their string representations.
const (
	l1MetaEnabled = "enabled"
	l1MetaBudget  = "budget"
	l1MetaPrior   = "prior"
)

// buildToolClassDAG constructs the L1 capability graph: one node per
// ToolClass (toolName + "#" + argShape), with Metadata carrying the evolution
// constraints enabled/budget/prior. The argShape is the sorted set of
// parameter key names from the tool's schema — this normalizes by type
// signature, not by value, so "read_file(path=foo.txt)" and
// "read_file(path=bar.txt)" collapse into one ToolClass node.
//
// The L1 graph is the evolution system's stable action surface: genome
// patches mutate enabled/budget/prior on L1 nodes, the planner reads them
// before growing L2 tool nodes, and L2 execution
// statistics flow back as fitness. The L1 graph is NOT compiled into
// taskfabric — it is a capability catalog, not an execution plan.
//
// Returns (nil, errNoToolSchemas) when the binder exposes no tools.
func buildToolClassDAG(schemas []core_tools.ToolSchema) (*engine.MutableDAG, error) {
	if len(schemas) == 0 {
		return nil, errNoToolSchemas
	}

	steps := make([]*engine.Step, 0, len(schemas))
	seen := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		if s.Name == "" {
			continue
		}
		nodeID := core_tools.ToolClassID(s.Name, core_tools.ToolArgShape(s))
		if seen[nodeID] {
			continue // defensive: same tool+shape deduplicated
		}
		seen[nodeID] = true
		step := &engine.Step{
			ID:        nodeID,
			Name:      s.Name,
			AgentType: "tool/" + s.Name,
			Input:     s.Description,
			Metadata: map[string]string{
				l1MetaEnabled: "true",
				l1MetaBudget:  "0", // 0 = unlimited
				l1MetaPrior:   "",
			},
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, errNoToolSchemas
	}

	dag, err := engine.NewMutableDAG(steps)
	if err != nil {
		return nil, fmt.Errorf("build toolclass DAG: %w", err)
	}
	return dag, nil
}

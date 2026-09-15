// agent_kernel — the peer-mode kernel assembly (createPeerAgents: Task Fabric
// + Agent Fabric + scheduler + evolution feedback + syscalls + recovery) and
// its executor/session plumbing, split out of agent.go (M-C2). Function
// bodies are moved verbatim; only the file boundary is new.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/agentsyscall"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	kctx "github.com/Timwood0x10/ares/internal/kernel/ctx"
	llm "github.com/Timwood0x10/ares/internal/llm"
	ares_skills "github.com/Timwood0x10/ares/internal/runtime/protocol/skills"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

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
// createPeerAgents assembles the peer-mode Kernel (Task Fabric + Agent Fabric
// + scheduler + L2 execution) from the configured peer population. The heavy
// lifting lives in the peerAssembly methods (peer_assembly.go); this function
// only orders the phases and maps phase errors to the caller.
//
//nolint:gocyclo // createPeerAgents is a wiring hub (like runServe): it assembles the peer-mode kernel from Task Fabric, Agent Fabric, scheduler, evolution feedback, syscalls, recovery and the lifecycle in one function. Each branch is a distinct wiring step; splitting it would spread one assembly across helpers without reducing the decisions.
func createPeerAgents(
	ctx context.Context,
	cfg *ares_config.Config,
	comp *ares_bootstrap.Components,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	store ares_events.EventStore,
	strategySrc agents.StrategySource,
	expRepo repositories.ExperienceRepositoryInterface,
) ([]sub.Agent, *kernelHandle, error) {
	a := &peerAssembly{
		ctx:         ctx,
		cfg:         cfg,
		comp:        comp,
		chatClient:  chatClient,
		toolBinder:  toolBinder,
		store:       store,
		strategySrc: strategySrc,
		expRepo:     expRepo,
		kernel:      &kernelHandle{},
		peers:       normalizedPeers(cfg),
	}
	// Build sub-agent identities from the flat peer population. Roles have no
	// consumer anymore (executor role-pinning and the chat body that read them
	// are deleted) — peers run roleless.
	a.subAgents = createPeerSubAgents(a.peers, store)
	if len(a.subAgents) == 0 {
		return nil, nil, errors.New("peer mode: no peer agents configured (agents.peers or agents.sub)")
	}

	if err := a.assembleFabric(); err != nil {
		return nil, nil, err
	}
	a.wireDispatchAndScheduler()
	a.wireEvolutionFeedback()
	a.startCollabGC()
	a.assembleAgentFabric()
	if err := a.assembleExecution(); err != nil {
		return nil, nil, err
	}
	a.wireSessionCleanup()
	a.computeGovernance()
	if err := a.spawnPeers(); err != nil {
		return nil, nil, err
	}
	a.wireSyscalls()
	a.injectPriorities()
	a.startLoops()

	log.Info("peer mode: peer agents registered, Kernel scheduler started (no leader)", "count", len(a.subAgents))
	return a.subAgents, a.kernel, nil
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

// submitPeerTask is the thin CLI glue over agentruntime.Submitter: the
// single L2 submission path (session admission + root-task creation + the
// process-local ID sequence) lives in the shared package so the SDK drives
// the identical semantics. Exposed as POST /api/tasks on the serve HTTP
// layer (actionHandler), closing the user-submission loop: a request reaches
// the fabric and the scheduler executes it.
//
// planCapability / answerCapability alias the shared L2 capability constants
// (cmd/ares keeps the historical local names).
const (
	planCapability   = agentruntime.PlanCapability
	answerCapability = agentruntime.AnswerCapability
)

func submitPeerTask(ctx context.Context, kernel *kernelHandle, capability string, payload map[string]any) (string, error) {
	if kernel == nil || kernel.submitter == nil || kernel.fabric == nil {
		return "", errors.New("peer mode: kernel fabric not wired")
	}
	taskID, _, err := kernel.submitter.Submit(ctx, capability, payload)
	if err != nil {
		return "", err
	}
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
	// Stamp the executing agent's identity before delegating to the
	// cognition: tool nodes call the binder with this context, and the
	// Kernel syscalls (spawn_agent/create_task) enforce provenance
	// (Task.Origin/ParentID) from kctx.CallerID — without the stamp, the
	// serve path fell back to the LLM-supplied ParentID, which is
	// forgeable. The same WithCallerID stamp the pre-0.3.1 SDK engine
	// applied before Execute.
	ctx = kctx.WithCallerID(ctx, a.id)
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
// cognition. The caller identity is stamped for the same reason as
// ExecuteStep below (Kernel provenance for tool nodes).
func (e *cognitionExecutor) Execute(ctx context.Context, task *models.Task) (*models.TaskResult, error) {
	if e.cog == nil {
		return nil, fmt.Errorf("peer mode: executor %q has no execution body (identity-only agent must not be driven)", e.id)
	}
	ctx = kctx.WithCallerID(ctx, e.id)
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
// stepExecutor check and kernel.CapabilityExecutor. The caller identity is
// stamped before delegating so tool-node calls carry Kernel-enforced
// provenance (see peerExecutorAdapter.ExecuteStep).
func (e *cognitionExecutor) ExecuteStep(ctx context.Context, task *models.Task) (*sub.StepOutcome, error) {
	if e.cog == nil {
		return nil, fmt.Errorf("peer mode: executor %q has no execution body (identity-only agent must not be driven)", e.id)
	}
	ctx = kctx.WithCallerID(ctx, e.id)
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

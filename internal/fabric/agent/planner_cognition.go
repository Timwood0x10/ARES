package agentfabric

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// DefaultMaxPlanDepth is the default growth-depth upper bound for an L2
// session graph (the growth-depth upper-bound guard). It caps how many plan-tool rounds a
// session can run before the planner is forced to answer — preventing
// unbounded graph growth from a runaway LLM that keeps requesting tools.
const DefaultMaxPlanDepth = 10

// PlannerDeps carries the plannerCognition's dependencies, injected at
// construction time. The planner reads predecessor outputs from the
// fabric (by node ID = task ID join), calls the LLM once, and grows tool/plan
// nodes into the L2 graph — it does NOT execute tools (the scheduler does).
type PlannerDeps struct {
	// ChatClient sends chat messages with tool support to the LLM.
	ChatClient ChatClient
	// ToolBinder advertises tool schemas to the LLM (the planner shows the
	// LLM what tools are available so it can decide which to call; execution
	// is the scheduler's job, not the planner's).
	ToolBinder ToolBinder
	// Sessions is the per-session L2 graph registry — the planner looks up
	// the graph by the task's SessionID.
	Sessions *SessionRegistry
	// Fabric is the task fabric used to read predecessor outputs by
	// node-ID = task-ID join (the node does not store Output).
	Fabric FabricReader
	// L1DAG is the L1 ToolClass capability graph. The planner reads
	// each tool's enabled/budget/prior metadata before growing an L2 tool
	// node — enabled=false blocks growth, budget=N caps instances per
	// session. Nil = no L1 graph (constraints default to permissive).
	L1DAG *engine.MutableDAG
	// MaxDepth caps the plan-tool growth depth (0 = DefaultMaxPlanDepth).
	MaxDepth int
	// StrategySource is the optional live evolution strategy (the
	// planner is the strategy actuator after ReAct — prompt template + LLM
	// param overrides steer plan growth the way they steered the chat loop).
	// Nil = no steering (same degrade contract as the retired chat body).
	StrategySource agents.StrategySource
	// AgentFabric is the agent population the executing agents live in.
	// When set, the planner reads the EXECUTING agent's cognitive Context —
	// the distilled experience prior injected at spawn (SpawnSpec.
	// ExperiencePrior) — and injects it as the leading context message: the
	// READ side of the experience loop (M4.3). Nil = no prior injection
	// (zero-value usable: tests and degraded wiring plan without priors).
	AgentFabric *Fabric
	// Logger is the shared logger.
	Logger *slog.Logger
}

// FabricReader is the minimal fabric surface the planner needs: read a task's
// checkpoint to extract its predecessor's output. Interface at the consumer
// so the planner stays decoupled from the fabric package.
type FabricReader interface {
	// Task returns a snapshot of the task (ErrTaskNotFound when unknown).
	Task(id string) (*taskfabric.Task, error)
}

// plannerCognition grows the L2 session graph by one plan-tool round per
// quantum. It is the replacement for the ReAct loop's "chatStep":
// instead of executing tools inside a cognition, it grows tool nodes into
// the L2 graph and lets the scheduler execute them as first-class tasks.
//
// Lifecycle per quantum:
//  1. Read SessionID from the task → look up the L2Graph from the registry.
//  2. Read the predecessor's output from the fabric envelope (by node ID =
//     task ID join) to assemble the LLM context.
//  3. Call the LLM once with the available tool schemas.
//  4. If the LLM returns tool calls: AddNode each one to the L2 graph
//     (the incremental compiler projects them to fabric tasks), then grow
//     a new plan node depending on them. Done:true — the next plan quantum
//     resumes when the scheduler drains the new tasks.
//  5. If the LLM returns no tool calls: grow an answer node carrying the
//     final response. Done:true — the session terminates.
//
// The planner NEVER executes tools ("plannerCognition never executes tools,
// it only grows the graph"). Execution is the scheduler's job — tool nodes are dispatched
// to toolCognition through the same fabric → scheduler → routerCognition
// path as every other node.
type plannerCognition struct {
	chat     ChatClient
	binder   ToolBinder
	sessions *SessionRegistry
	fabric   FabricReader
	l1       *engine.MutableDAG // L1 ToolClass graph, nil = permissive
	maxDepth int
	strategy agents.StrategySource // live evolution strategy, nil = unsteered
	// agentFabric reads the EXECUTING agent's cognitive Context (the
	// spawn-time experience prior). Nil = no prior injection.
	agentFabric *Fabric
	logger      *slog.Logger
	// forcedAnswers counts quanta that hit the growth-depth guard and were
	// forced into an answer node (canary metric: the depth-exhaustion
	// rate). One shared planner serves every session, so the counter is
	// process-wide, not per-session.
	forcedAnswers atomic.Uint64
}

var _ Cognition = (*plannerCognition)(nil)

// ForcedAnswers reports how many quanta hit the growth-depth guard and were
// forced into an answer node (canary metric: the depth-exhaustion
// rate). One shared planner serves every session, so the count is
// process-wide. A rising rate under canary means the depth cap is binding
// real sessions, not just runaways.
func (c *plannerCognition) ForcedAnswers() uint64 {
	if c == nil {
		return 0
	}
	return c.forcedAnswers.Load()
}

// NewPlannerCognition constructs the L2 graph-growing Cognition.
// A nil deps.ChatClient or nil deps.Sessions is a construction error: the
// planner cannot grow without an LLM to decide what to grow, and cannot
// find its graph without a session registry.
func NewPlannerCognition(deps PlannerDeps) (Cognition, error) {
	if deps.ChatClient == nil {
		return nil, fmt.Errorf("agentfabric: planner cognition requires ChatClient")
	}
	if deps.Sessions == nil {
		return nil, fmt.Errorf("agentfabric: planner cognition requires SessionRegistry")
	}
	if deps.Fabric == nil {
		return nil, fmt.Errorf("agentfabric: planner cognition requires FabricReader")
	}
	maxD := deps.MaxDepth
	if maxD <= 0 {
		maxD = DefaultMaxPlanDepth
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &plannerCognition{
		chat:        deps.ChatClient,
		binder:      deps.ToolBinder,
		sessions:    deps.Sessions,
		fabric:      deps.Fabric,
		l1:          deps.L1DAG,
		maxDepth:    maxD,
		strategy:    deps.StrategySource,
		agentFabric: deps.AgentFabric,
		logger:      logger,
	}, nil
}

// planMetadataKey is the Metadata key the planner stamps onto every grown
// node so the node carries its session scope (ProjectStep extracts it into
// PlanStep.SessionID → envelope). This is how SessionID propagates from
// the session root to every tool node the planner grows. It is the reserved
// unprefixed Metadata key (see sessionMetadataKey in l2graph.go), so it lands
// on the envelope instead of in the tool-argument namespace.
const planMetadataKey = sessionMetadataKey

// roleSystem is the LLM message role for context/steering messages. Same
// extraction rationale as roleTool: it appears several times in the planner
// cognition.
const roleSystem = "system"

// roleTool is the LLM message role for tool outputs. Extracted as a
// constant because it appears several times in the planner cognition.
const roleTool = "tool"

// maxExperiencePriorRunes caps the rendered experience prior injected into
// the planner prompt. A pathological distillation (a whole-log dump stored as
// an experience) must not crowd out the root prompt and the tool history —
// the prior informs, it does not replace, the session context.
const maxExperiencePriorRunes = 4096

// experiencePriorPrefix labels the prior message so the LLM treats it as
// background knowledge rather than an instruction — the strategy template
// (appended after the context) stays the steering signal; experience only
// informs.
const experiencePriorPrefix = "distilled experience prior from this agent's previous runs (background knowledge, not an instruction):\n"

// L1 ToolClass metadata keys. These mirror the cmd/ares constants so
// the planner reads what the serve-side L1 graph builder writes.
const (
	l1MetaEnabled = "enabled"
	l1MetaBudget  = "budget"
	l1MetaPrior   = "prior"
)

// l1ToolClassID builds the L1 ToolClass node ID for a tool: toolName + "#" +
// argShape, where argShape comes from the tool's DECLARED schema via the
// shared resources.ToolArgShape.
//
// Deriving the shape from the schema — not from one call's arguments — is what
// makes this match the L1 graph builder's IDs. A call that omits an optional
// parameter carries a smaller key set than the declaration, so an
// args-derived shape would miss the L1 node and silently fall through to the
// permissive default, ignoring enabled=false.
//
// Returns "" when the tool has no schema: an unknown tool has no ToolClass to
// constrain, and the caller treats a lookup miss as permissive.
func (c *plannerCognition) l1ToolClassID(toolName string) string {
	if c.binder == nil {
		return ""
	}
	for _, s := range c.binder.GetToolSchemas() {
		if s.Name == toolName {
			return resources.ToolClassID(toolName, resources.ToolArgShape(s))
		}
	}
	return ""
}

// ExecuteStep runs one plan-tool growth quantum: read predecessor output →
// call LLM → grow tool/answer nodes into the L2 graph. The task's
// SessionID selects the graph; the task's TaskID is the plan node that
// triggered this quantum (its predecessor chain defines the context path).
//
// The first plan quantum references a task ID that is NOT a graph node —
// only the root exists at session start. The planner uses the root (or the
// last tool node on the predecessor chain) as the predecessor for the tool
// nodes it grows. Subsequent plan quanta reference plan nodes that were
// grown by the previous quantum's growToolNodes, so they ARE in the graph.
func (c *plannerCognition) ExecuteStep(ctx context.Context, task *models.Task) (*StepOutcome, error) {
	if task == nil {
		return nil, fmt.Errorf("agentfabric: planner cognition: nil task")
	}

	sessionID := task.SessionID
	if sessionID == "" {
		return nil, fmt.Errorf("agentfabric: planner cognition: task %q has no session id", task.TaskID)
	}

	g, err := c.sessions.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("agentfabric: planner cognition: %w", err)
	}

	// Determine the current plan-tool growth depth and check the guard
	// (the growth-depth upper-bound guard). PlanDepth counts plan nodes grown by
	// growToolNodes — the initial plan quantum has depth 0.
	depth := g.PlanDepth()
	if depth >= c.maxDepth {
		// Growth-depth upper bound reached: force an answer node so the
		// session terminates instead of growing unbounded. The guard
		// enforces TERMINATION only — the answer node is grown content-LESS
		// on purpose: answerCognition then runs M4.2 synthesis over the
		// accumulated predecessor/tool history (same bounded view the
		// planner itself has), falling back to the honest gap body when
		// synthesis is unavailable. Stamping the guard literal as content
		// made it the session answer — external clients saw COMPLETED
		// result="max plan depth reached" (REVIEW-2026-09-21 P2).
		c.forcedAnswers.Add(1)
		c.logger.Warn("planner: max plan depth reached, forcing answer",
			"session", sessionID, "depth", depth, "max", c.maxDepth)
		// The truncation fact rides the answer node so the terminal result
		// metadata carries it (answerTruncatedKey) — external clients must
		// be able to distinguish "completed" from "hit the depth bound".
		return c.growAnswerNode(ctx, g, task, "", nil, map[string]any{
			answerTruncatedKey: true,
			answerCauseKey:     answerCauseDepthCapped,
		})
	}

	// Assemble the LLM context from the predecessor path.
	prompt, err := c.assembleContext(ctx, task, g)
	if err != nil {
		return nil, fmt.Errorf("agentfabric: planner cognition: assemble context: %w", err)
	}

	// Inject L1 prior hints as a system message (prompt-only, never a
	// growth block). Deterministic order; absent when no priors are set.
	if priors := c.l1Priors(); len(priors) > 0 {
		prompt = append(prompt, &llmcore.LLMMessage{
			Role:    roleSystem,
			Content: "evolution priors (hints only, tool choice stays with you):\n- " + strings.Join(priors, "\n- "),
		})
	}

	// Steer plan growth with the live evolution strategy (the planner
	// is the strategy actuator after ReAct). Mirrors the retired chat loop's
	// renderPromptAndParams: template override rides a system message
	// (prompt-only, never a growth block), param overrides ride the LLM
	// call params. Absent/unreadable strategy = unsteered growth.
	llmParams := map[string]any{}
	if st := c.activeStrategy(ctx); st != nil {
		if strings.TrimSpace(st.Prompt) != "" {
			prompt = append(prompt, &llmcore.LLMMessage{
				Role:    roleSystem,
				Content: "evolution strategy (deployed " + st.ID + "):\n" + st.Prompt,
			})
		}
		for k, v := range st.Params {
			llmParams[k] = v
		}
	}

	// Build the tool schemas for the LLM.
	var llmTools []llmcore.Tool
	if c.binder != nil {
		schemas := c.binder.GetToolSchemas()
		llmTools = make([]llmcore.Tool, 0, len(schemas)+1)
		for _, s := range schemas {
			llmTools = append(llmTools, toCoreTool(s))
		}
		// Advertise the rationale tool to THIS cognition only — see
		// rationaleToolSchema for why it is not in the registry. Without it the model has
		// no way to state a reason on a tool-call turn, which is the measured common case
		// (empty Content), so the rationale feature would exist and never fire.
		llmTools = append(llmTools, rationaleToolSchema())
	}

	// Call the LLM once with the context and tool schemas.
	resp, err := c.chat.Chat(ctx, prompt, llmTools, llmParams)
	if err != nil {
		return nil, fmt.Errorf("agentfabric: planner cognition: chat: %w", err)
	}

	// Token accounting (the fitness cost channel, M4): this quantum's LLM
	// usage rides the StepOutcome result metadata via tokenUsageMetadata;
	// the scheduler's re-wrap path accumulates it into the checkpoint
	// envelope, so the session's total spend surfaces on the terminal
	// task.completed event for the RuntimeObserver's cost penalty. A
	// provider that reports no usage contributes zero — the observer then
	// scores on outcome and latency alone.

	// No tool calls: the LLM gave a final answer. Grow an answer node.
	if len(resp.ToolCalls) == 0 {
		// The normal ending is labelled explicitly so a consumer can tell it
		// apart from the forced ones (D1/D2) without inspecting the body.
		return c.growAnswerNode(ctx, g, task, resp.Content, resp, map[string]any{
			answerCauseKey: answerCausePlanned,
		})
	}

	// Tool calls: grow tool nodes + a new plan node depending on them.
	grown, rationales, err := c.growToolNodes(ctx, g, task, resp, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agentfabric: planner cognition: grow nodes: %w", err)
	}
	if grown == 0 && rationales > 0 {
		// The model stated WHY it was about to act and then asked for no work at all.
		// Nothing was blocked, so this MUST NOT be labelled as an L1 rejection: a
		// constraint failure in the record where a model behaviour belongs is exactly the
		// kind of mislabel this package keeps having to undo. The session ends (there is
		// nothing to execute) with the cause naming what actually happened.
		c.logger.Warn("planner: the model requested no work after stating its rationale, forcing answer",
			"session", sessionID, "rationale_calls", rationales)
		return c.growAnswerNode(ctx, g, task, resp.Content, resp, map[string]any{
			answerCauseKey: answerCauseRationaleOnly,
		})
	}
	if grown == 0 {
		// All tool calls were skipped by L1 constraints (enabled=false,
		// per-agent allowlist, or budget exhausted). Force an answer so the
		// session terminates instead of looping on a planner that can never grow
		// a tool — and label the ending (D2): the result used to be a
		// success-looking COMPLETED with no tool ever run and nothing recording
		// why. The per-tool skips are logged individually by growToolNodes; this
		// is the aggregate fact ("nothing could run") that none of them states.
		c.logger.Warn("planner: every tool call skipped by L1 constraints, forcing answer",
			"session", sessionID, "requested", len(resp.ToolCalls))
		return c.growAnswerNode(ctx, g, task, resp.Content, resp, map[string]any{
			answerCauseKey: answerCauseL1Blocked,
		})
	}

	result := models.NewTaskResult(task.TaskID, task.AgentType)
	result.SetSuccess(nil, "planner grew "+strconv.Itoa(grown)+" tool nodes")
	result.Metadata = tokenUsageMetadata(resp)
	return &StepOutcome{Done: true, Result: result}, nil
}

// activeStrategy fetches the currently-deployed evolution strategy, if any.
// Errors are logged and ignored so a missing store
// never breaks plan growth — same degrade contract as the retired chat loop.
func (c *plannerCognition) activeStrategy(ctx context.Context) *agents.ActiveStrategy {
	if c.strategy == nil {
		return nil
	}
	st, err := c.strategy.GetActiveStrategy(ctx)
	if err != nil {
		c.logger.Warn("planner: failed to read active strategy", "error", err)
		return nil
	}
	return st
}

// stableRound derives the growth round of the executing plan quantum. The
// admission-submitted plan task (not a graph node) is the round-0 trigger and
// grows round 1; a grown plan node sess/<sid>/d<N>/plan#0 has N-1 plan
// ancestors and grows round N+1. In the normal flow this equals
// PlanDepth()+1 — it only diverges on re-execution, which is exactly the
// point: every node ID this quantum grows (tools, the round's plan node, the
// answer node) is derived from the SAME stable round, so a replay after the
// graph advanced re-derives the identical IDs instead of forking the session
// into duplicate branches.
func stableRound(g *L2Graph, taskID string) int {
	if g.HasNode(taskID) {
		return g.AncestorPlanCount(taskID) + 2
	}
	return 1
}

// growToolNodes grows one tool node per LLM tool call, then grows a new plan
// node depending on all of them. Each tool node carries its args in the
// arg. namespace and the session_id in Metadata (so ProjectStep
// propagates it to the envelope).
//
// The predecessor for the first tool node is the current plan node when it
// exists in the graph (subsequent quanta), or the root node when it doesn't
// (the first plan quantum — the initial plan task ID is not a graph node,
// only the root exists at session start). Tools within one round are chained
// sequentially (the next depends on the previous).
//
// Before growing each tool node, the planner checks the L1 ToolClass
// graph's enabled/budget metadata. enabled=false skips the node
// (constraint point: "does the node get grown"). budget=N caps how many instances
// of this ToolClass can exist in the L2 graph per session. A nil L1 graph
// means no constraints (permissive default).
//
// Idempotency across quantum re-execution: the growth round is derived from
// the TASK (its ancestor plan count), not from the global PlanDepth — a
// re-executed plan quantum (fabric Fail-requeue after a partial growth, or
// a lease-expiry requeue after the step's side effects landed) computes the
// SAME round and therefore the SAME deterministic node IDs as its first
// execution. Nodes that already exist are skipped (and chained through)
// instead of erroring on the duplicate ID, and a round whose plan node
// already exists is a successful no-op.
//
// Returns the number of tool nodes the round carries (grown or already
// present), 0 when the round is genuinely empty — in which case the caller
// forces an answer node.
func (c *plannerCognition) growToolNodes(
	ctx context.Context,
	g *L2Graph,
	task *models.Task,
	resp *llmcore.GenerateResponse,
	sessionID string,
) (int, int, error) {
	// The whole response is passed, not just its tool calls, because the round's
	// plan node must also carry this quantum's rationale (see rationaleKey).
	// resp is non-nil by the caller's contract: ExecuteStep only reaches this
	// branch after reading resp.ToolCalls.
	toolCalls := resp.ToolCalls
	round := stableRound(g, task.TaskID)
	grown := 0
	rationales := 0

	// Determine the predecessor for the first tool node: the current plan
	// node when it exists, otherwise the root (first plan quantum).
	prev := task.TaskID
	if !g.HasNode(prev) {
		prev = g.Root()
	}

	for seq, tc := range toolCalls {
		toolName := tc.Function.Name

		// A rationale statement is NOT work: it was already captured by rationaleFrom,
		// which reads this call's argument. Growing a tool node for it would put a node in
		// the graph that performs nothing — one per statement, each with its own result —
		// which is noise for the projection and for anyone reading the session. Counted so
		// the caller can tell this ending apart from "L1 blocked every call".
		if toolName == RecordRationaleToolName {
			rationales++
			continue
		}

		// Check L1 constraints before growing the node.
		if !c.isToolEnabled(toolName) {
			c.logger.Warn("planner: tool disabled by L1 constraint; skipping",
				"tool", toolName, "session", sessionID)
			continue
		}
		// Per-agent tool allowlist check. The executing agent's identity rides the
		// task payload; when an allowlist is set, only listed tools are
		// permitted.
		if !c.isToolAllowedForAgent(toolName, task) {
			c.logger.Warn("planner: tool blocked by per-agent allowlist; skipping",
				"tool", toolName, "session", sessionID)
			continue
		}
		if !c.toolBudgetRemaining(g, toolName) {
			c.logger.Warn("planner: tool budget exhausted by L1 constraint; skipping",
				"tool", toolName, "session", sessionID)
			continue
		}

		nodeID := SessionNodeID(sessionID, round, toolName, seq)

		// Skip nodes an earlier execution of this quantum already grew
		// (partial-growth retry): the node exists, chains onward, and the
		// duplicate-ID error that used to fail the quantum permanently
		// never fires.
		if g.HasNode(nodeID) {
			prev = nodeID
			grown++
			continue
		}

		// Parse the tool arguments.
		args := map[string]any{}
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return grown, rationales, fmt.Errorf("parse tool %s args: %w", toolName, err)
			}
		}

		// Stamp session_id into Metadata so ProjectStep propagates it.
		metadata := args
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata[planMetadataKey] = sessionID
		// UNCONDITIONAL, exactly like session_id: an LLM-supplied "tenant_id"
		// tool argument must never survive into the grown node's envelope —
		// under a conditional stamp, a tenant-less task let the model
		// self-select the tenant its grown work executes and recalls
		// knowledge under. Overwriting with the task's own value (possibly
		// "") is the only forgery-proof form.
		metadata[tenantMetadataKey] = task.TenantID

		if err := g.AddToolNode(ctx, nodeID, toolName, metadata, prev); err != nil {
			return grown, rationales, fmt.Errorf("add tool node %s: %w", nodeID, err)
		}
		// Chain sequential tools within the same round: the next tool depends on
		// this one (data flow).
		//
		// This serial chaining is an intentional design choice, not an
		// architectural limitation. The DAG supports fan-out:
		// depsSatisfiedLocked checks every dependency, and ReadyTasks
		// returns all ready tasks at once (see TestFabricFanoutReadyTasks).
		// PlanStep.DependsOn is []string and ProjectStep copies it as-is,
		// so multi-predecessor topologies are reachable.
		//
		// The planner chains serially because LLM tool-call ordering
		// typically carries data flow (tool B reads tool A's output), and
		// the planner has no signal to distinguish "order matters" from
		// "order is arbitrary". Restructuring the topology at runtime via
		// the GA's PatchAddEdge — changing the mutation operator — is a
		// lower-risk path to parallel expression than changing the planner.
		prev = nodeID
		grown++
	}

	// The round's plan node. When every tool call was skipped by L1
	// constraints, do NOT grow a new plan node — the caller forces an answer
	// node instead (growing one here would inflate PlanDepth and shift the
	// answer node's ID, breaking the "no tools grown → answer at this
	// depth" contract). An earlier execution of this quantum may already
	// have grown the plan node (full-growth retry): skip it — the round is
	// complete, and re-adding it used to fail the retried quantum on the
	// duplicate ID.
	newPlanID := SessionNodeID(sessionID, round, "plan", 0)
	if planExists := g.HasNode(newPlanID); !planExists && grown > 0 {
		planArgs := map[string]any{planMetadataKey: sessionID}
		if task.TenantID != "" {
			planArgs[tenantMetadataKey] = task.TenantID
		}
		// This quantum's rationale rides onto the node grown for the NEXT round:
		// that node becomes the next quantum's task — and the task a replacement
		// agent resumes — so the successor reads the reasoning of the quantum
		// before it from its own args. Bounded on the write side
		// (rationaleMaxRunes); absent when the provider emitted no content, so
		// nothing is invented and the reader simply adds no message.
		if r := rationaleFrom(resp); r != "" {
			planArgs[rationaleKey] = r
		}
		if err := g.AddToolNode(ctx, newPlanID, "plan", planArgs, prev); err != nil {
			return grown, rationales, fmt.Errorf("add plan node %s: %w", newPlanID, err)
		}
	} else if planExists && grown == 0 {
		// Full-growth retry whose LLM response matched nothing: the round
		// still completed on the earlier execution — report it as grown so
		// the caller does not force a spurious answer.
		grown = 1
	}
	return grown, rationales, nil
}

// isToolEnabled reads the L1 ToolClass graph's "enabled" metadata for the
// given tool. Returns true (permissive) when the L1 graph is nil, the node
// is not found, or the metadata is missing/empty. Returns false only when
// the L1 node explicitly sets enabled="false". Reads through NodeMetadata
// (locked copy) so a concurrent evolution SetNodeMetadata cannot race.
func (c *plannerCognition) isToolEnabled(toolName string) bool {
	if c.l1 == nil {
		return true
	}
	nodeID := c.l1ToolClassID(toolName)
	md, ok := c.l1.NodeMetadata(nodeID)
	if !ok {
		return true // unknown ToolClass → permissive
	}
	val, ok := md[l1MetaEnabled]
	if !ok || val == "" {
		return true
	}
	return val != "false"
}

// isToolAllowedForAgent checks the per-agent tool allowlist.
// Returns true (permissive) when no agent fabric is wired, the task carries
// no executing-agent stamp, or the agent has no allowlist set (nil = inherit
// all). Returns false only when the agent has a non-nil allowlist that does
// not contain the tool name.
//
// ASYMMETRY WITH THE EXECUTION GUARD: growth-time rejection SKIPS the tool
// node — the quantum continues with the remaining tool calls and the model
// sees only a warn log — while execution-time rejection FAILS the task
// (l2graph toolCognition.ExecuteStep). Growth can therefore silently drop a
// tool the model asked for; the execution guard is the backstop for nodes
// that already existed when the allowlist was set (or was tightened).
func (c *plannerCognition) isToolAllowedForAgent(toolName string, task *models.Task) bool {
	if c.agentFabric == nil {
		return true
	}
	agentID, _ := task.Payload[executingAgentKey].(string)
	if agentID == "" {
		return true
	}
	agent, err := c.agentFabric.Get(agentID)
	if err != nil {
		return true // agent gone = permissive (don't block on a race)
	}
	return agent.IsToolAllowed(toolName)
}

// toolBudgetRemaining checks whether the L2 graph still has budget for one
// more instance of the given ToolClass. budget=0 (or missing) = unlimited.
// budget=N means at most N instances of this ToolClass can exist in the L2
// graph at a time. The count is by ToolClass (toolName#argShape), not by
// tool name alone, so two different argument shapes of the same tool each
// get their own budget.
func (c *plannerCognition) toolBudgetRemaining(g *L2Graph, toolName string) bool {
	if c.l1 == nil {
		return true
	}
	nodeID := c.l1ToolClassID(toolName)
	md, ok := c.l1.NodeMetadata(nodeID)
	if !ok {
		return true // unknown ToolClass → permissive
	}
	budgetStr, ok := md[l1MetaBudget]
	if !ok || budgetStr == "" || budgetStr == "0" {
		return true // unlimited
	}
	budget, err := strconv.Atoi(budgetStr)
	if err != nil || budget <= 0 {
		return true // unparseable or unlimited
	}
	count := g.CountToolClass(toolName)
	return count < budget
}

// l1ToolPrior returns the L1 ToolClass "prior" hint for the given tool.
// Empty means no hint. prior is prompt-only: it never blocks growth,
// only guides the LLM. Reads through NodeMetadata (locked copy).
func (c *plannerCognition) l1ToolPrior(toolName string) string {
	if c.l1 == nil {
		return ""
	}
	nodeID := c.l1ToolClassID(toolName)
	md, ok := c.l1.NodeMetadata(nodeID)
	if !ok {
		return ""
	}
	return strings.TrimSpace(md[l1MetaPrior])
}

// l1Priors collects the non-empty prior hints for every known tool schema,
// sorted by tool name for determinism. Used to inject evolution guidance
// into the planner prompt (the prior goes into the prompt only).
//
// The sort is load-bearing, not cosmetic: GetToolSchemas() iterates a map in
// the registry, so its order is unstable across process restarts. An unsorted
// prompt suffix would change byte-for-byte between runs, defeating the LLM
// provider's prompt-prefix (KV) cache. Each entry is "<name>: <prior>", so a
// lexical sort is a sort by tool name (ties broken deterministically by the
// prior text).
func (c *plannerCognition) l1Priors() []string {
	if c.l1 == nil || c.binder == nil {
		return nil
	}
	var priors []string
	for _, s := range c.binder.GetToolSchemas() {
		if p := c.l1ToolPrior(s.Name); p != "" {
			priors = append(priors, s.Name+": "+p)
		}
	}
	sort.Strings(priors)
	return priors
}

// growAnswerNode grows a terminal answer node carrying the final response and
// completes the quantum with Done:true. The answer node's content is read by
// answerCognition when the scheduler executes it.
//
// resp is the LLM response this quantum produced (nil on the depth-guard
// path — no LLM call happened, so there is no usage to report); its token
// usage rides the answer node's task result metadata into the checkpoint
// envelope (the fitness cost channel, M4).
//
// content is the final-turn answer text the planner's LLM produced. Empty
// means "no stamped answer" — the depth-guard path deliberately grows the
// node content-less so answerCognition synthesizes from history (or emits
// the gap body); guard/debug literals must never ride this parameter.
//
// extra carries additional args stamped onto the answer node (merged into
// the AddToolNode args map). The depth-guard path uses it to mark the
// session's final answer as truncated; nil elsewhere.
//
// The predecessor is the current plan node when it exists in the graph
// (subsequent quanta), or the root node when it doesn't (the first plan
// quantum). When the plan node IS in the graph, its predecessor (the last
// tool node) is the natural dependency for the answer.
func (c *plannerCognition) growAnswerNode(
	ctx context.Context,
	g *L2Graph,
	task *models.Task,
	content string,
	resp *llmcore.GenerateResponse,
	extra map[string]any,
) (*StepOutcome, error) {
	// The answer node is the terminal of the SAME round the quantum would
	// have grown, so its ID comes from the stable round derivation — never
	// from PlanDepth, which moves as LATER rounds grow and would hand a
	// re-executed quantum a different answer ID (duplicate answer branches).
	answerID := SessionNodeID(task.SessionID, stableRound(g, task.TaskID), "answer", 0)

	// Idempotency across quantum re-execution: the stable round makes the
	// answer node ID identical across replays, so a re-executed quantum
	// finds its own answer node. Growing it again used to fail on the
	// duplicate ID and burn the whole retry budget; a quantum whose answer
	// already exists just completes successfully.
	if !g.HasNode(answerID) {
		// Determine predecessor: the current plan node when in graph, else root.
		pred := task.TaskID
		if !g.HasNode(pred) {
			pred = g.Root()
		}

		args := map[string]any{
			"content":       content,
			planMetadataKey: task.SessionID,
		}
		for k, v := range extra {
			args[k] = v
		}
		if task.TenantID != "" {
			args[tenantMetadataKey] = task.TenantID
		}
		if err := g.AddToolNode(ctx, answerID, "answer", args, pred); err != nil {
			return nil, fmt.Errorf("agentfabric: planner cognition: add answer node: %w", err)
		}
	}

	result := models.NewTaskResult(task.TaskID, task.AgentType)
	result.SetSuccess(nil, "planner grew answer node")
	result.Metadata = tokenUsageMetadata(resp)
	return &StepOutcome{Done: true, Result: result}, nil
}

// extractOutputContent reads the textual output from a step checkpoint. The
// scheduler's buildQuantumStep wraps the StepOutcome.Result into the
// checkpoint's StepCheckpoint as a {result, items, ...} map; this helper
// extracts the first item's Content field. Returns "" when the checkpoint
// does not carry a recognizable output shape.
func extractOutputContent(sc any) string {
	if sc == nil {
		return ""
	}
	m, ok := sc.(map[string]any)
	if !ok {
		return ""
	}
	// The scheduler stores {result: "ok", items: []*RecommendItem, ...} or
	// after a JSON round-trip {items: [{content: "...", ...}]}.
	raw, ok := m["items"]
	if !ok {
		return ""
	}
	switch items := raw.(type) {
	case []*models.RecommendItem:
		if len(items) > 0 {
			return items[0].Content
		}
	case []any:
		if len(items) > 0 {
			if im, ok := items[0].(map[string]any); ok {
				if c, ok := im["content"].(string); ok {
					return c
				}
			}
		}
	}
	return ""
}

// toCoreTool converts a resources.ToolSchema to a llmcore.Tool for the LLM.
func toCoreTool(s resources.ToolSchema) llmcore.Tool {
	return resources.ToolSchemaToLLMTool(s)
}

package sdk

// l2.go wires the shared L2 execution core (agentruntime.Execution — the
// SAME core serve/start use) into the SDK runtime. This is the first stage
// of the ReAct-to-L2 convergence: the SDK gets its own session registry,
// compile coordinator, reaper, and L2 router, spawned as one fabric peer
// with the full L2 capability set (ares/root + ares/plan + ares/answer +
// tool/*), so every execution body in the process — serve, start, or SDK —
// is the same router cognition.

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// llmChatAdapter adapts the SDK's llmService (Generate-only) to the
// agentfabric.ChatClient contract the L2 cognition bodies require. The
// planner's llmParams (evolution strategy params) pass through the
// well-known keys; unknown keys are ignored (same contract as cmd/ares's
// createChatClient surface).
type llmChatAdapter struct {
	svc llmService
}

var _ agentfabric.ChatClient = (*llmChatAdapter)(nil)

// Chat sends one chat request and returns the raw GenerateResponse — the
// plannerCognition parses tool calls / content itself (shared L2 semantics,
// identical to cmd/ares's chatClient).
func (a *llmChatAdapter) Chat(ctx context.Context, messages []*llmcore.LLMMessage, tools []llmcore.Tool, params map[string]any) (*llmcore.GenerateResponse, error) {
	if a == nil || a.svc == nil {
		return nil, fmt.Errorf("sdk: llm service not configured")
	}
	req := &llmcore.GenerateRequest{
		Messages: messages,
		Tools:    tools,
	}
	if params != nil {
		if v, ok := params["model"].(string); ok && v != "" {
			req.Model = v
		}
		if v, ok := asFloat(params["temperature"]); ok {
			req.Temperature = &v
		}
		if v, ok := asInt(params["max_tokens"]); ok && v > 0 {
			req.MaxTokens = &v
		}
	}
	return a.svc.Generate(ctx, req)
}

// asFloat coerces JSON/yaml-sourced numeric params (float64 everywhere after
// a round-trip) to float64.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// asInt coerces numeric params to int (see asFloat for why).
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	}
	return 0, false
}

// l2PeerID is the SDK's L2 fabric peer identity (one peer carries the full
// L2 capability set — serve-mode parity: every execution body is the router).
const l2PeerID = "sdk-peer"

// l2PeerCapabilities is the capability set the SDK's L2 peer advertises:
// the three session-node capabilities plus one capability per registered
// tool (tool nodes dispatch by tool name).
func l2PeerCapabilities(toolNames []string) []string {
	caps := []string{agentfabric.RootCapability, agentfabric.PlanCapability, agentfabric.AnswerCapability}
	for _, name := range toolNames {
		if name == "" {
			continue
		}
		caps = append(caps, "tool/"+name)
	}
	return caps
}

// ensureL2 lazily builds the SDK's shared L2 execution core on first use
// (same once-pattern as ensureScheduler — no constructor surface change).
// It is idempotent; a build failure is recorded and reported by the returned
// nil (callers surface their own error).
//
// SNAPSHOT caveat: the advertised capability set (ares/* + one tool/* per
// registered tool) is fixed at this first build — a tool registered AFTER the
// first L2 submission is not advertised to the scheduler (see ToolRegistry).
//
// Wiring, in order:
//  1. ensureScheduler — the task fabric + scheduler + syscall tools must
//     exist first (the tool binder bridges the registry AFTER syscall
//     tools are registered, so spawn_agent/create_task are visible to the
//     planner).
//  2. agentruntime.NewExecution — session registry + compile coordinator +
//     L2 router + reaper + submitter (the B2-shared core).
//  3. Spawn one fabric peer carrying the L2 capability set; its
//     CognitionFactory returns the router — the sole execution body.
//  4. sched.WithAgentFabric — the scheduler's candidate pool now includes
//     the L2 peer, so ares/plan / tool/* / ares/answer tasks drain through
//     the router. Static per-capability executors (RegisterAgent) still win
//     by skip logic — existing SDK agent registration is unaffected.
func (r *Runtime) ensureL2() *agentruntime.Execution {
	r.l2Once.Do(func() {
		r.ensureScheduler()
		if r.llmSvc == nil {
			return // no LLM: no L2 core (Run/Submit keep their no-LLM errors)
		}
		if r.agentsFabric == nil {
			r.agentsFabric = agentfabric.NewFabric()
		}
		// Tool binder: bridge the runtime tool registry (syscall tools
		// included — wireSyscalls ran inside ensureScheduler) into the
		// shared sub.ToolBinder the L2 cognition bodies consume.
		binder := sub.NewToolBinder()
		if coreReg, err := r.toolReg.CoreRegistry(); err == nil {
			binder.BridgeFromRegistry(coreReg)
		} else {
			slog.Warn("sdk: tool registry bridge failed; L2 planner sees no tools", "error", err)
		}
		execCore, err := agentruntime.NewExecution(agentruntime.ExecutionConfig{
			Fabric:       r.sdkFabric,
			Agents:       r.agentsFabric,
			ChatClient:   &llmChatAdapter{svc: r.llmSvc},
			ToolBinder:   binder,
			CompileStore: r.eventStore,
		})
		if err != nil {
			slog.Warn("sdk: L2 execution core not wired", "error", err)
			return
		}
		if _, err := r.agentsFabric.Spawn(context.Background(), agentfabric.SpawnSpec{
			Identity:     l2PeerID,
			Capabilities: l2PeerCapabilities(binder.ListTools()),
			// Cognitive-execution budget from birth (WithAgentGovernance;
			// zero = unlimited), same as cmd/ares's configured peers.
			Governance: r.gov,
			// The execution body is always the L2 router — no ReAct
			// loop, no per-agent engine (mainline parity with cmd/ares).
			CognitionFactory: func([]string) agentfabric.Cognition {
				return execCore.Router
			},
		}); err != nil {
			slog.Warn("sdk: L2 peer spawn failed; L2 core not active", "error", err)
			return
		}
		r.l2Exec = execCore
		r.l2Binder = binder
		// The scheduler's candidate pool already carries the agent fabric in
		// hybrid mode (wired by ensureScheduler before the drain started):
		// the Spawn above joined the L2 peer to that pool, so ares/plan /
		// tool/* / ares/answer tasks now drain through the router while the
		// SDK's static ReAct executors keep winning their own capabilities.
		// Session task reaper on the runtime lifecycle (serve parity): the
		// answer node releases its session on success, and without this loop
		// the released sessions' terminal tasks grow the in-memory fabric map
		// monotonically in a long-lived SDK process. It exits on the runtime
		// ctx (cancelled in Close) and is drained by eg.Wait.
		if r.eg != nil && r.ctx != nil {
			r.eg.Go(func() error {
				execCore.Reaper.Run(r.ctx.Done(), time.Minute)
				return nil
			})
		}
	})
	return r.l2Exec
}

// resyncL2Tools brings the L2 core up to date with tools registered after the
// peer was spawned. BridgeFromRegistry is a no-op for names the binder already
// holds, and AddCapabilities skips capabilities the peer already advertises,
// so calling this on every submission is idempotent and cheap once the sets
// have converged.
//
// Without this, the binder's tool list and the peer's advertised tool/*
// capability set are both frozen at first Submit: the planner cannot see a
// late tool, and even if prompted for it the resulting tool/<name> node has no
// capable candidate and can never be scheduled.
func (r *Runtime) resyncL2Tools() {
	if r.l2Exec == nil || r.l2Binder == nil {
		return
	}
	if reg, err := r.toolReg.CoreRegistry(); err == nil {
		r.l2Binder.BridgeFromRegistry(reg)
	}
	// The peer advertises one tool/* capability per binder tool; AddCapabilities
	// is idempotent so only the genuinely new names are appended.
	if err := r.agentsFabric.AddCapabilities(l2PeerID, l2PeerCapabilities(r.l2Binder.ListTools())...); err != nil {
		slog.Warn("sdk: L2 peer capability re-sync failed", "error", err)
	}
}

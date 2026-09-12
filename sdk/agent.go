package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	tools "github.com/Timwood0x10/ares/internal/apitools"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	mcp "github.com/Timwood0x10/ares/internal/mcpclient"
	"github.com/Timwood0x10/ares/internal/tools/toolsource"
)

// Agent represents a single agent bound to a Runtime. It carries a name, an
// optional system instruction, an optional set of tools, and an optional
// human-input approval hook.
type Agent struct {
	name        string
	instruction string
	tools       []tools.Tool
	runtime     *Runtime
	humanInput  HumanInputFunc
	maxIter     int
	// maxTokens caps the cumulative prompt+completion tokens per run (<=0 =
	// unbounded). ReAct-era knob: retained for API compatibility, no longer
	// consumed by the L2 session path (bound runs with WithAgentGovernance).
	maxTokens int
	// timeout caps the total wall-clock duration per run (<=0 = the L2
	// default wait cap); passed to the L2 submission as Task.Timeout.
	timeout time.Duration
	// discovery gates runtime tool discovery (see WithToolDiscovery). When
	// false, Agent.Run is byte-for-byte identical to the legacy path.
	discovery bool
	// toolSource is the discovery source; nil means default (RegistrySource
	// over the Runtime registry). Only consulted when discovery is true.
	toolSource toolsource.ToolSource
	// selector narrows the available pool before each run; nil means
	// AllSelector. Only consulted when discovery is true.
	selector toolsource.ToolSelector
	// evolveMu guards the Evolve-mutable fields (tools, maxIter):
	// applyEvolvedParams may run concurrently with another goroutine's
	// Agent.Run reading them, so both sides go through the lock.
	evolveMu sync.Mutex
}

// snapshotTools returns the agent's current tool slice under evolveMu.
// The slice is never mutated in place (applyToolSelector builds new
// slices; applyEvolvedParams reassigns), so the header snapshot is a
// stable read.
func (a *Agent) snapshotTools() []tools.Tool {
	a.evolveMu.Lock()
	defer a.evolveMu.Unlock()
	return a.tools
}

// currentMaxIter returns the agent's current iteration budget under
// evolveMu (Evolve may replace it concurrently with Run).
func (a *Agent) currentMaxIter() int {
	a.evolveMu.Lock()
	defer a.evolveMu.Unlock()
	return a.maxIter
}

// HumanInputFunc is called when the agent needs human approval before executing
// a tool call. Return true to approve, false to skip the tool call, or an
// error to abort entirely.
type HumanInputFunc func(ctx context.Context, toolName string, args map[string]any) (approved bool, err error)

// StreamChunk represents a partial streaming result from an agent Run.
type StreamChunk struct {
	// Content is the partial text content.
	Content string
	// Done is true when the stream is complete.
	Done bool
	// Err is set when the stream encounters an error.
	Err error
	// Result is set when Done is true and no error occurred.
	Result *Result
}

// Stream runs the agent against the given input and streams results via a
// channel. The caller must read from the channel until Done is true or Err
// is non-nil — an abandoned channel does not leak the goroutine as long as
// the caller's ctx is eventually cancelled (every send also selects on
// ctx.Done).
//
// NOTE: this is NOT token-level streaming. The full agent loop runs to
// completion first and the final output is replayed in small chunks; the
// first chunk arrives only after the entire run finishes.
//
// TODO(tech-debt): plumb token-level streaming through the L2 answer
// path so Stream emits tokens as they are generated.
//
// Usage:
//
//	ch, err := agent.Stream(ctx, "hello")
//	if err != nil { return err }
//	for chunk := range ch {
//	    if chunk.Err != nil { return chunk.Err }
//	    fmt.Print(chunk.Content)
//	}
func (a *Agent) Stream(ctx context.Context, input string) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 32)

	go func() {
		defer close(ch)

		// Run the full agent logic.
		result, err := a.Run(ctx, input)
		if err != nil {
			a.sendChunk(ctx, ch, StreamChunk{Err: err, Done: true})
			return
		}

		// Simulate streaming by sending the output in chunks.
		runes := []rune(result.Output)
		chunkSize := 10
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}
			if !a.sendChunk(ctx, ch, StreamChunk{Content: string(runes[i:end])}) {
				return
			}
		}

		a.sendChunk(ctx, ch, StreamChunk{Done: true, Result: result})
	}()

	return ch, nil
}

// sendChunk delivers one chunk, abandoning the stream when the caller's ctx
// is cancelled (a caller that stopped reading is released by cancelling its
// ctx — the goroutine exits instead of blocking forever on a full channel).
// Returns false when the stream was abandoned.
func (a *Agent) sendChunk(ctx context.Context, ch chan<- StreamChunk, chunk StreamChunk) bool {
	select {
	case ch <- chunk:
		return true
	case <-ctx.Done():
		// Best-effort final error: the channel may be abandoned too, so a
		// further block is avoided by dropping the error chunk.
		select {
		case ch <- StreamChunk{Err: ctx.Err(), Done: true}:
		default:
		}
		return false
	}
}

// Result holds the outcome of a single agent Run.
type Result struct {
	Output     string        `json:"output"`
	ToolCalls  int           `json:"tool_calls"`
	MemoryUsed bool          `json:"memory_used"`
	TokenUsage TokenUsage    `json:"token_usage"`
	Duration   time.Duration `json:"duration"`
}

// TokenUsage summarises LLM token consumption.
type TokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Total  int `json:"total"`
}

// Run executes the agent against the input and returns the result. Since the
// B3 convergence the agent runs as an L2 session — through the SAME shared
// execution core serve/start use (agentruntime.Execution): the submission is
// admitted as a session, the planner cognition grows the session graph (tool
// nodes dispatch through the shared tool binder), and the session's terminal
// answer is the Result.
//
// The agent's identity survives as context, not as a private loop:
// instruction / memory context / knowledge context are composed into the
// prompt prefix (composePrompt), and Timeout bounds the session wait.
// ReAct-era options (WithHumanInput / WithMaxIterations / WithMaxTokens /
// WithToolDiscovery) no longer shape execution — the planner's loop and the
// governance budget (WithAgentGovernance) bound the run instead.
func (a *Agent) Run(ctx context.Context, input string) (*Result, error) {
	if a.runtime == nil || a.runtime.llmSvc == nil {
		return nil, errors.New("sdk: agent runtime has no LLM configured")
	}
	execCore := a.runtime.ensureL2()
	if execCore == nil {
		return nil, errors.New("sdk: L2 execution core not wired")
	}
	// Late-registered tools must be visible to the planner AND routable as
	// tool/* nodes (idempotent once converged).
	a.runtime.resyncL2Tools()
	prompt, sessionID, memUsed := a.composePrompt(ctx, input)
	res, err := a.runtime.submitThroughL2(ctx, execCore, Task{
		Capability: a.name,
		Input:      prompt,
		Timeout:    a.timeout,
	})
	if err != nil {
		return nil, err
	}
	// Persist the assistant turn so a memory-enabled agent keeps a complete
	// user/assistant trail across runs (previously done inline during agent
	// execution, now deferred to the caller here).
	if memUsed {
		_ = a.runtime.memMgr.AddMessage(ctx, sessionID, roleAssistant, res.Output)
		res.MemoryUsed = true
	}
	return res, nil
}

// ---- internal helpers ----

// composePrompt builds the LLM-facing prompt for one run: the agent's
// instruction, the memory-retrieved context and the AKF knowledge context
// (when enabled) are composed as prefix sections around the input. It also
// creates the memory session and records the user turn; the session ID is
// returned so Run can persist the assistant reply after the L2 session
// answers. memUsed reports whether a memory session backs this run.
func (a *Agent) composePrompt(ctx context.Context, input string) (prompt, sessionID string, memUsed bool) {
	sessionID = uuid.NewString()
	if a.runtime.memEnabled && a.runtime.memMgr != nil {
		if sid, err := a.runtime.memMgr.CreateSession(ctx, a.name); err == nil {
			sessionID = sid
			memUsed = true
		}
	}

	var sections []string
	if a.instruction != "" {
		sections = append(sections, a.instruction)
	}
	if memUsed {
		if ctxStr, err := a.runtime.memMgr.BuildContext(ctx, input, sessionID); err == nil && ctxStr != "" {
			sections = append(sections, ctxStr)
		}
	}

	// Inject AKF knowledge context if available.
	if a.runtime.knowledgeEnabled && a.runtime.knowledgeRT != nil {
		budget := knowledge.TokenBudget{
			MaxTokens: 3000,
			Reserved:  1000,
			ForGraph:  2000,
		}
		graph, err := a.runtime.knowledgeRT.Execute(ctx, input, budget, nil)
		if err == nil && graph != nil && len(graph.Nodes) > 0 {
			c := compiler.NewDefaultCompiler()
			compiled, cErr := c.Compile(ctx, graph, compiler.CompileConfig{
				Formats:  []compiler.Format{compiler.FormatPrompt},
				MaxNodes: 50,
				MaxEdges: 50,
			})
			if cErr == nil && compiled != nil {
				if ctxStr, ok := compiled.Formats[compiler.FormatPrompt]; ok && ctxStr != "" {
					sections = append(sections, ctxStr)
				}
			}
		}
	}

	sections = append(sections, input)
	if memUsed {
		_ = a.runtime.memMgr.AddMessage(ctx, sessionID, roleUser, input)
	}
	return strings.Join(sections, "\n\n"), sessionID, memUsed
}

func (a *Agent) toCoreTools(tt []tools.Tool) []llmcore.Tool {
	if len(tt) == 0 {
		return nil
	}
	out := make([]llmcore.Tool, 0, len(tt))
	for _, t := range tt {
		params := t.Parameters()
		if params == nil {
			params = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}
		out = append(out, llmcore.Tool{
			Type: "function",
			Function: llmcore.FunctionDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		})
	}
	return out
}

// parseArgs unmarshals a JSON arguments string into a map.
func parseArgs(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

// mcpToolAdapter wraps an MCP client tool as an SDK tool so it can be used
// with the agent tool registry.
type mcpToolAdapter struct {
	name   string
	desc   string
	client *mcp.Client
}

// a mcpToolAdapter Name returns the MCP tool name.
func (a mcpToolAdapter) Name() string { return a.name }

// a mcpToolAdapter Description returns the MCP tool description.
func (a mcpToolAdapter) Description() string { return a.desc }

// a mcpToolAdapter Parameters returns nil since MCP schemas are handled by the client.
func (a mcpToolAdapter) Parameters() map[string]any { return nil }

// a mcpToolAdapter Capabilities returns nil since MCP tools expose no capabilities.
func (a mcpToolAdapter) Capabilities() []string { return nil }

// a mcpToolAdapter Execute calls the MCP tool with the given params.
func (a mcpToolAdapter) Execute(ctx context.Context, params map[string]any) (tools.Result, error) {
	result, err := a.client.CallTool(ctx, a.name, params)
	if err != nil {
		return tools.Result{Success: false, Data: err.Error()}, nil
	}
	var sb strings.Builder
	for _, c := range result.Content {
		sb.WriteString(c.Text)
	}
	return tools.Result{Success: !result.IsError, Data: sb.String()}, nil
}

// Package ares provides the top-level, unified entry point for the ARES
// agent runtime. It wraps all internal components behind a simple,
// production-friendly API.
//
// Quick start:
//
//	import (
//	    "context"
//	    "github.com/Timwood0x10/ares"
//	    "github.com/Timwood0x10/ares/api/tools"
//	)
//
//	func main() {
//	    ctx := context.Background()
//	    rt := ares.MustNew(ares.WithOpenAI("gpt-4o-mini"))
//	    defer rt.Close()
//
//	    agent := rt.NewAgent("assistant",
//	        ares.WithInstruction("You are a helpful assistant."),
//	    )
//	    result, err := agent.Run(ctx, "Hello!")
//	}
package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/api/mcp"
	"github.com/Timwood0x10/ares/api/service/llm"
	memsvc "github.com/Timwood0x10/ares/api/service/memory"
	"github.com/Timwood0x10/ares/api/tools"
)

// ---- public types ----

// Role constants for LLM messages.
const (
	roleSystem    = "system"
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
)

// Runtime is the top-level ARES container. It owns the LLM client, tool
// registry, and — optionally — memory, MCP connections, and evolution.
// Create one with MustNew or New.
type Runtime struct {
	llmSvc     *llm.Service
	toolReg    *tools.Registry
	memSvc     *memsvc.Service
	memEnabled bool
	evoEnabled bool
	mcpClients []*mcp.Client
	trace      bool
}

// Agent is a named agent with a fixed instruction and tool set, bound to a
// Runtime. Create one via Runtime.NewAgent.
type Agent struct {
	name        string
	instruction string
	tools       []tools.Tool
	runtime     *Runtime
	humanInput  HumanInputFunc
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
// is non-nil.
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
			ch <- StreamChunk{Err: err, Done: true}
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
			select {
			case ch <- StreamChunk{Content: string(runes[i:end])}:
			case <-ctx.Done():
				ch <- StreamChunk{Err: ctx.Err(), Done: true}
				return
			}
		}

		ch <- StreamChunk{Done: true, Result: result}
	}()

	return ch, nil
}

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

// ---- constructors ----

// MustNew creates a new Runtime with the given options. It panics on error so
// it is safe for quickstart / prototyping code. Use New for production code
// that wants to handle errors gracefully.
func MustNew(opts ...Option) *Runtime {
	r, err := New(opts...)
	if err != nil {
		panic("ares: " + err.Error())
	}
	return r
}

// New creates a new Runtime. Returns an error when a required option (e.g. an
// LLM provider) cannot be initialised.
func New(opts ...Option) (*Runtime, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, fmt.Errorf("option: %w", err)
		}
	}

	// ---- LLM ----
	llmCfg := &llm.Config{
		BaseConfig: cfg.baseCfg,
		LLMConfig:  cfg.llmCfg,
	}
	llmSvc, err := llm.NewService(llmCfg)
	if err != nil {
		return nil, friendlyErr("llm", cfg.llmCfg.Provider, err)
	}

	// ---- Tools ----
	toolReg := tools.NewRegistry()

	// ---- Memory ----
	var memSvc *memsvc.Service
	if cfg.memCfg.Enabled {
		s, err := memsvc.New(nil)
		if err != nil {
			return nil, fmt.Errorf("memory: %w", err)
		}
		memSvc = s
	}

	// ---- MCP ----
	var mcpClients []*mcp.Client
	for _, conn := range cfg.mcpConns {
		client, err := mcp.ConnectStdio(context.Background(), conn.Name, conn.Command, conn.Args)
		if err != nil {
			return nil, fmt.Errorf("mcp %q: %w", conn.Name, err)
		}
		tools, listErr := client.ListTools(context.Background())
		if listErr != nil {
			return nil, fmt.Errorf("mcp %q list tools: %w", conn.Name, listErr)
		}
		for _, t := range tools {
			toolName := t.Name
			toolDesc := t.Description
			mcpClient := client
			if err := toolReg.Register(mcpToolAdapter{
				name:   toolName,
				desc:   toolDesc,
				client: mcpClient,
			}); err != nil {
				return nil, fmt.Errorf("mcp %q register %s: %w", conn.Name, toolName, err)
			}
		}
		mcpClients = append(mcpClients, client)
	}

	return &Runtime{
		llmSvc:     llmSvc,
		toolReg:    toolReg,
		memSvc:     memSvc,
		memEnabled: cfg.memCfg.Enabled,
		evoEnabled: cfg.evoCfg.Enabled,
		mcpClients: mcpClients,
		trace:      cfg.trace,
	}, nil
}

// Close releases all resources held by the Runtime (LLM connections, memory
// store, MCP connections). Call once when the Runtime is no longer needed.
func (r *Runtime) Close() {
	r.llmSvc.Close()
	if r.memSvc != nil {
		_ = r.memSvc.Stop(context.Background())
	}
	for _, c := range r.mcpClients {
		_ = c.Close()
	}
}

// ToolRegistry returns the internal tool registry. Use this to register custom
// tools before creating agents.
func (r *Runtime) ToolRegistry() *tools.Registry {
	return r.toolReg
}

// Evolve runs an evolution cycle to improve an agent's instruction. It uses the
// LLM to generate variations, evaluates them against the given task, and returns
// the best-evolved instruction.
func (r *Runtime) Evolve(ctx context.Context, agent *Agent, task string) (string, error) {
	if agent == nil {
		return "", fmt.Errorf("evolve: agent is nil")
	}
	if !r.evoEnabled {
		return "", fmt.Errorf("evolution not enabled (use WithEvolution())")
	}

	if r.trace {
		log.Printf("[ares:evolve] evolving agent %q on task: %s", agent.name, task)
	}

	// Generate a variant of the instruction.
	evolveMsg := []*core.LLMMessage{
		{Role: roleSystem, Content: "You are an evolution engine. Improve the following agent instruction to get better results."},
		{Role: roleUser, Content: fmt.Sprintf(
			`Original instruction: %s

Task to optimize for: %s

Generate an improved version of the instruction. Be specific and actionable.
Respond with ONLY the new instruction text.`, agent.instruction, task)},
	}

	resp, err := r.llmSvc.Generate(ctx, &core.GenerateRequest{
		Messages: evolveMsg,
	})
	if err != nil {
		return "", fmt.Errorf("evolve: %w", err)
	}

	if r.trace {
		log.Printf("[ares:evolve] evolved instruction: %s", resp.Content)
	}

	return resp.Content, nil
}

// NewAgent creates a new Agent bound to this Runtime. The agent carries a name,
// an optional system instruction, and an optional set of tools.
func (r *Runtime) NewAgent(name string, opts ...AgentOption) *Agent {
	ac := defaultAgentConfig()
	for _, o := range opts {
		o(ac)
	}
	return &Agent{
		name:        name,
		instruction: ac.instruction,
		tools:       ac.tools,
		runtime:     r,
		humanInput:  ac.humanInput,
	}
}

// ---- Agent ----

// Run executes the agent against the given input and returns the result.
// It runs a ReAct loop:
//
//  1. Build the message list (system instruction + memory context + input).
//  2. Call the LLM (with tool definitions).
//  3. If the LLM calls tools, execute them and feed results back.
//  4. Repeat until the LLM produces a final answer.
//  5. Store the conversation in memory (if enabled).
//  6. Return the final output and metadata.
func (a *Agent) Run(ctx context.Context, input string) (*Result, error) {
	start := time.Now()

	sessionID := uuid.NewString()
	if a.runtime.memEnabled && a.runtime.memSvc != nil {
		sid, err := a.runtime.memSvc.CreateSession(ctx, a.name)
		if err == nil {
			sessionID = sid
		}
	}

	messages := a.buildMessages(ctx, input, sessionID)

	// ---- convert tools to core.Tool format ----
	coreTools := a.toCoreTools(a.tools)
	totalInputTokens := 0
	totalOutputTokens := 0
	toolCallCount := 0
	const maxIter = 10

	for iter := 0; iter < maxIter; iter++ {
		if a.runtime.trace {
			log.Printf("[ares:trace] %s → LLM call (iter %d, %d msgs)",
				a.name, iter, len(messages))
		}

		resp, err := a.runtime.llmSvc.Generate(ctx, &core.GenerateRequest{
			Messages: messages,
			Tools:    coreTools,
		})
		if err != nil {
			return nil, friendlyErr("llm generate", a.runtime.llmSvc.GetProvider(), err)
		}

		totalInputTokens += resp.Usage.PromptTokens
		totalOutputTokens += resp.Usage.CompletionTokens

		// Store assistant message
		messages = append(messages, &core.LLMMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		if a.runtime.memEnabled && a.runtime.memSvc != nil {
			_ = a.runtime.memSvc.AddMessage(ctx, sessionID, "assistant", resp.Content)
		}

		// ---- tool calling loop ----
		if len(resp.ToolCalls) == 0 {
			// Final answer
			if a.runtime.trace {
				log.Printf("[ares:trace] %s ✓ done (%d tools, %d total tokens, %v)",
					a.name, toolCallCount, totalInputTokens+totalOutputTokens,
					time.Since(start).Round(time.Millisecond))
			}
			return &Result{
				Output:     resp.Content,
				ToolCalls:  toolCallCount,
				MemoryUsed: a.runtime.memEnabled,
				TokenUsage: TokenUsage{
					Input:  totalInputTokens,
					Output: totalOutputTokens,
					Total:  totalInputTokens + totalOutputTokens,
				},
				Duration: time.Since(start),
			}, nil
		}

		// Execute each tool call
		for _, tc := range resp.ToolCalls {
			args := parseArgs(tc.Function.Arguments)

			// Human-in-the-loop check.
			if a.humanInput != nil {
				approved, err := a.humanInput(ctx, tc.Function.Name, args)
				if err != nil {
					return nil, fmt.Errorf("human input: %w", err)
				}
				if !approved {
					if a.runtime.trace {
						log.Printf("[ares:trace] %s → tool call REJECTED by human: %s",
							a.name, tc.Function.Name)
					}
					messages = append(messages, &core.LLMMessage{
						Role:       roleTool,
						ToolCallID: tc.ID,
						Content:    fmt.Sprintf("Tool call %s was rejected by human operator", tc.Function.Name),
					})
					continue
				}
			}

			toolCallCount++
			if a.runtime.trace {
				log.Printf("[ares:trace] %s → tool call: %s(%s)",
					a.name, tc.Function.Name, tc.Function.Arguments)
			}

			result, err := a.runtime.toolReg.Execute(ctx, tc.Function.Name, args)
			resultContent := ""
			if err != nil {
				resultContent = fmt.Sprintf("Error: %v", err)
			} else {
				resultContent = fmt.Sprintf("%v", result.Data)
			}

			messages = append(messages, &core.LLMMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    resultContent,
			})
		}

		// Continue loop — the LLM will either call more tools or produce a final answer
	}

	if a.runtime.trace {
		log.Printf("[ares:trace] %s ⚠ max iterations reached (%d)", a.name, maxIter)
	}
	return &Result{
		Output:     "max iterations reached",
		ToolCalls:  toolCallCount,
		MemoryUsed: a.runtime.memEnabled,
		TokenUsage: TokenUsage{
			Input:  totalInputTokens,
			Output: totalOutputTokens,
			Total:  totalInputTokens + totalOutputTokens,
		},
		Duration: time.Since(start),
	}, nil
}

// ---- internal helpers ----

func (a *Agent) buildMessages(ctx context.Context, input, sessionID string) []*core.LLMMessage {
	var msgs []*core.LLMMessage

	if a.instruction != "" {
		msgs = append(msgs, &core.LLMMessage{
			Role:    roleSystem,
			Content: a.instruction,
		})
	}

	// Inject memory context if available
	if a.runtime.memEnabled && a.runtime.memSvc != nil {
		ctxStr, err := a.runtime.memSvc.BuildContext(ctx, input, sessionID)
		if err == nil && ctxStr != "" {
			msgs = append(msgs, &core.LLMMessage{
				Role:    roleSystem,
				Content: ctxStr,
			})
		}
	}

	msgs = append(msgs, &core.LLMMessage{
		Role:    roleUser,
		Content: input,
	})

	if a.runtime.memEnabled && a.runtime.memSvc != nil {
		_ = a.runtime.memSvc.AddMessage(ctx, sessionID, roleUser, input)
	}

	return msgs
}

func (a *Agent) toCoreTools(tt []tools.Tool) []core.Tool {
	if len(tt) == 0 {
		return nil
	}
	out := make([]core.Tool, 0, len(tt))
	for _, t := range tt {
		out = append(out, core.Tool{
			Type: "function",
			Function: core.FunctionDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
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

func (a mcpToolAdapter) Name() string           { return a.name }
func (a mcpToolAdapter) Description() string    { return a.desc }
func (a mcpToolAdapter) Capabilities() []string { return nil }
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

// friendlyErr wraps an LLM error with an actionable hint based on the provider.
func friendlyErr(scope string, provider core.LLMProvider, origErr error) error {
	hints := map[core.LLMProvider]string{
		core.LLMProviderOpenAI:     "→ Set OPENAI_API_KEY or check https://platform.openai.com/account/api-keys",
		core.LLMProviderAnthropic:  "→ Set ANTHROPIC_API_KEY or check https://console.anthropic.com/",
		core.LLMProviderOpenRouter: "→ Set OPENROUTER_API_KEY or check https://openrouter.ai/keys",
		core.LLMProviderOllama:     "→ Run: ollama run llama3.2  (Ollama may not be running)",
	}
	msg := fmt.Sprintf("%s: %v", scope, origErr)
	if hint, ok := hints[provider]; ok {
		msg += "\n  " + hint
	}
	return fmt.Errorf("%s", msg)
}

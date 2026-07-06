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
	"time"

	"github.com/google/uuid"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/api/service/llm"
	memsvc "github.com/Timwood0x10/ares/api/service/memory"
	"github.com/Timwood0x10/ares/api/tools"
)

// ---- public types ----

// Runtime is the top-level ARES container. It owns the LLM client, tool
// registry, and — optionally — memory and MCP connections. Create one with
// MustNew or New.
type Runtime struct {
	llmSvc     *llm.Service
	toolReg    *tools.Registry
	memSvc     *memsvc.Service
	memEnabled bool
	trace      bool
}

// Agent is a named agent with a fixed instruction and tool set, bound to a
// Runtime. Create one via Runtime.NewAgent.
type Agent struct {
	name        string
	instruction string
	tools       []tools.Tool
	runtime     *Runtime
}

// Result captures the outcome of a single Run call.
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
		return nil, fmt.Errorf("llm: %w", err)
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

	return &Runtime{
		llmSvc:     llmSvc,
		toolReg:    toolReg,
		memSvc:     memSvc,
		memEnabled: cfg.memCfg.Enabled,
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
}

// ToolRegistry returns the internal tool registry. Use this to register custom
// tools before creating agents.
func (r *Runtime) ToolRegistry() *tools.Registry {
	return r.toolReg
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
			return nil, fmt.Errorf("llm generate: %w", err)
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
			toolCallCount++
			if a.runtime.trace {
				log.Printf("[ares:trace] %s → tool call: %s(%s)",
					a.name, tc.Function.Name, tc.Function.Arguments)
			}

			result, err := a.runtime.toolReg.Execute(ctx, tc.Function.Name, parseArgs(tc.Function.Arguments))
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
			Role:    "system",
			Content: a.instruction,
		})
	}

	// Inject memory context if available
	if a.runtime.memEnabled && a.runtime.memSvc != nil {
		ctxStr, err := a.runtime.memSvc.BuildContext(ctx, input, sessionID)
		if err == nil && ctxStr != "" {
			msgs = append(msgs, &core.LLMMessage{
				Role:    "system",
				Content: ctxStr,
			})
		}
	}

	msgs = append(msgs, &core.LLMMessage{
		Role:    "user",
		Content: input,
	})

	if a.runtime.memEnabled && a.runtime.memSvc != nil {
		_ = a.runtime.memSvc.AddMessage(ctx, sessionID, "user", input)
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

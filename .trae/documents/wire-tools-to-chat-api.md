# Wire Tools from Registry to LLM Chat API

## Context

The Chat API now supports all 4 providers (OpenAI/OpenRouter/Anthropic/Ollama), but **no production code passes tools to it**. The agent executor uses `output.LLMAdapter.Generate(prompt)` — a text-only interface. The `ToolBinder` has `CallTool()` but it's never invoked as part of an LLM-driven tool loop. Tools are registered but invisible to the LLM.

**The gap**: No code converts `resources/core.Tool` (registry interface) → `api/core.Tool` (LLM wire format), and no code handles LLM-returned `tool_calls` → execute tool → send result back.

## Plan

### Step 1: Add `ToolSchemaToLLMTool` converter

Create a function in `internal/tools/resources/core/convert.go` that converts the registry's `ToolSchema` → `api/core.Tool` (the LLM wire format). This bridges the two type systems.

```go
// ToolSchemaToLLMTool converts a ToolSchema from the registry to api/core.Tool
// for passing to the LLM Chat API.
func ToolSchemaToLLMTool(schema ToolSchema) core.Tool {
    return core.Tool{
        Type: "function",
        Function: core.FunctionDefinition{
            Name:        schema.Name,
            Description: schema.Description,
            Parameters:  ParameterSchemaToMap(schema.Parameters),
        },
    }
}

// ParameterSchemaToMap converts *ParameterSchema to map[string]interface{}
// for the JSON Schema format expected by api/core.FunctionDefinition.Parameters.
func ParameterSchemaToMap(schema *ParameterSchema) map[string]interface{} { ... }
```

Also add `Registry.GetLLMTools() []core.Tool` as a convenience method on the registry.

### Step 2: Add `ToolBinder.GetToolSchemas() []ToolSchema` to the ToolBinder interface

Extend `sub.ToolBinder` interface and `toolBinder` implementation to expose tool schemas, not just names. The `toolBinder` already has a `*core.Registry` reference — use it to get schemas.

```go
// In sub/agent.go ToolBinder interface:
GetToolSchemas() []core.ToolSchema

// In sub/tools.go toolBinder implementation:
func (b *toolBinder) GetToolSchemas() []core.ToolSchema { ... }
```

### Step 3: Add Chat capability to the executor

Add a new `ChatCapable` interface that the executor can use alongside `LLMAdapter`:

```go
// ChatClient sends chat messages with tool support.
type ChatClient interface {
    Chat(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool) (*core.GenerateResponse, error)
}
```

Add it as an optional field on `taskExecutor`. When set AND tools are available, use Chat API instead of plain Generate.

### Step 4: Add tool-dispatch loop to executor

In `executeWithLLMSingle`, after the LLM call, check if the response contains `tool_calls`. If so:

1. Execute each tool call via `toolBinder.CallTool()`
2. Build new messages: original messages + assistant message with tool_calls + tool result messages
3. Call Chat again with the updated messages
4. Repeat until LLM returns no tool_calls (or max iterations reached)

This implements the agentic loop: LLM → tool_calls → execute → result → LLM → final answer.

Add a `maxToolRounds` field (default 5) to prevent infinite loops.

### Step 5: Wire ChatClient through the api_impl layer

In `api_impl/service.go`, create both an `output.LLMAdapter` (for the dashboard orchestrator) AND an `llm.Client` (for the executor's Chat path). Pass the `llm.Client` to the executor constructor.

### Step 6: Tests

- Test `ToolSchemaToLLMTool` conversion
- Test `ParameterSchemaToMap` conversion
- Test tool-dispatch loop with mock ChatClient (returns tool_calls first, then text)
- Test max iterations limit

## Files Changed

| File | Action |
|------|--------|
| `internal/tools/resources/core/convert.go` | **CREATE** — ToolSchema→api/core.Tool converter |
| `internal/tools/resources/core/registry.go` | **MODIFY** — add `GetLLMTools()` convenience method |
| `internal/agents/sub/agent.go` | **MODIFY** — add `GetToolSchemas()` to ToolBinder interface |
| `internal/agents/sub/tools.go` | **MODIFY** — implement `GetToolSchemas()` |
| `internal/agents/sub/executor.go` | **MODIFY** — add ChatClient field, tool-dispatch loop |
| `internal/api_impl/service.go` | **MODIFY** — create llm.Client, wire to executor |
| `internal/tools/resources/core/convert_test.go` | **CREATE** — conversion tests |

## Verification

1. `go build ./...` — all packages compile
2. `go test ./internal/agents/sub/... ./internal/tools/resources/core/... ./internal/api_impl/...` — tests pass
3. Manual: configure an Ollama provider, register tools, verify tool_calls are returned and dispatched

# Fix Tool Discovery and Calling — Wire Tools Through to LLM Chat API

## Summary

Tools are registered in the system but never reach the LLM. The executor always falls through to `executeWithLLMTextOnly()` because:
1. `chatClient` is nil (never wired)
2. `GetToolSchemas()` returns nil (wrong adapter)
3. The internal `core.Registry` with schemas is disconnected from the `registryToolBinder`

## Current State Analysis

### The Broken Chain

```
main.go
  → newToolRegistry()         [api_tools.Registry: tools registered ✅]
  → setupMCP()                [MCP tools → internal core.Registry, then bridge to api_tools.Registry ✅]
  → registryToolBinder{}      [wraps api_tools.Registry, GetToolSchemas() returns nil ❌]
  → createExecutor()          [NO WithChatClient passed ❌]
     → e.chatClient == nil    [❌ Chat path never entered]
     → e.toolBinder.GetToolSchemas() → nil [❌ No schemas]
     → Falls through to executeWithLLMTextOnly() [❌ Tools never reach LLM]
```

### 6 Breakpoints Identified

| # | Location | What Breaks | Severity |
|---|----------|-------------|----------|
| 1 | `cmd/monitor-live/tools.go:49` | `GetToolSchemas()` returns nil — no schemas reach executor | CRITICAL |
| 2 | `cmd/monitor-live/agents.go:162-178` | `WithChatClient` never called — `chatClient` is nil | CRITICAL |
| 3 | `internal/llm/output/adapter.go` | `LLMAdapter` lacks `Chat()` — can't serve as `ChatClient` | STRUCTURAL |
| 4 | `cmd/monitor-live/main.go:273-309` | No `BridgeFromRegistry` call — internal `core.Registry` never linked | WIRING |
| 5 | `api/tools/tools.go` | Public `Registry` has no schema support — `GetSchemas()` doesn't exist | API GAP |
| 6 | `internal/agents/sub/executor.go:416-420` | `llmTools = nil` after round 0 — multi-round tool use impossible | DESIGN |

## Proposed Changes

### Step 1: Use internal `toolBinder` instead of `registryToolBinder`

**File**: `cmd/monitor-live/tools.go`

Replace `registryToolBinder` (which wraps `api_tools.Registry` and returns nil schemas) with the internal `sub.NewToolBinder()` which supports `BridgeFromRegistry()` and `GetToolSchemas()`.

Changes:
- Remove `registryToolBinder` struct and all its methods
- Add `newToolBinder()` function that creates a `sub.ToolBinder` and bridges from the internal `core.Registry`

```go
func newToolBinder(internalReg *core.Registry) sub.ToolBinder {
    binder := sub.NewToolBinder()
    binder.BridgeFromRegistry(internalReg)
    return binder
}
```

### Step 2: Keep `api_tools.Registry` for MCP/dashboard compatibility

**File**: `cmd/monitor-live/main.go`

The `api_tools.Registry` is still needed for the MCP adapter and monitoring dashboard. Keep creating it, but also return the internal `core.Registry` from `setupMCP()` so it can be used for tool schemas.

Changes:
- Modify `setupMCP()` signature to return `*core.Registry`: `func setupMCP(ctx context.Context, cfg *ares_config.Config, registry *api_tools.Registry) *core.Registry`
- The internal `core.Registry` already exists in `setupMCP()` (line 280), just return it
- In `main()`, capture the returned `core.Registry` and pass it to `newToolBinder()`

### Step 3: Create `llm.Client`/`FailoverClient` and pass as `ChatClient`

**File**: `cmd/monitor-live/agents.go`

The executor's `ChatClient` interface is:
```go
Chat(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool) (*core.GenerateResponse, error)
```

Both `*llm.Client` and `*llm.FailoverClient` satisfy this interface. We need to create one from the same config and pass it via `WithChatClient()`.

Changes:
- Add `createChatClient()` function that creates an `llm.FailoverClient` (or single `llm.Client`) from `ares_config.Config`
- Update `createExecutor()` to accept a `sub.ChatClient` and pass it via `sub.WithChatClient()`
- Update `createAgents()`, `createLeaderAgent()`, `createSubAgents()` to propagate the `ChatClient`

```go
func createChatClient(cfg *ares_config.Config) (sub.ChatClient, error) {
    configs := []*llm.Config{
        {Provider: cfg.LLM.Provider, APIKey: cfg.LLM.APIKey, BaseURL: cfg.LLM.BaseURL, Model: cfg.LLM.Model, Timeout: cfg.LLM.Timeout, MaxTokens: cfg.LLM.MaxTokens},
    }
    for _, fb := range cfg.LLM.Fallbacks {
        provider := fb.Provider
        if provider == "" {
            provider = "openai"
        }
        configs = append(configs, &llm.Config{Provider: provider, APIKey: fb.APIKey, BaseURL: fb.BaseURL, Model: fb.Model, Timeout: fb.Timeout, MaxTokens: fb.MaxTokens})
    }
    timeout := time.Duration(cfg.LLM.Timeout) * time.Second
    if timeout <= 0 {
        timeout = 60 * time.Second
    }
    return llm.NewFailoverClient(configs, timeout, 0, 0)
}
```

### Step 4: Fix multi-round tool calling

**File**: `internal/agents/sub/executor.go`

Currently line 416-420 sets `llmTools = nil` after round 0, which prevents the LLM from making additional tool calls in subsequent rounds. This is overly restrictive — the LLM should be able to make tool calls in any round until it produces a final text answer.

Change:
```go
// Remove or change: if round == 0 { llmTools = nil }
// Instead: always pass tools so the LLM can choose to call them or produce a final answer.
// The loop naturally terminates when resp.ToolCalls is empty (LLM gives final text).
```

Actually, keeping tools available in later rounds is the standard agentic pattern. The LLM decides when to stop calling tools and give a final answer. Remove the `if round == 0` block entirely and always pass `llmTools`.

### Step 5: Wire everything in `main.go`

**File**: `cmd/monitor-live/main.go`

Update the main wiring sequence:

```go
// Before (broken):
toolBinder := &registryToolBinder{registry: registry}

// After (fixed):
internalReg := setupMCP(ctx, cfg, registry)  // returns *core.Registry
toolBinder := newToolBinder(internalReg)       // uses sub.NewToolBinder with BridgeFromRegistry
chatClient, err := createChatClient(cfg)       // creates llm.FailoverClient
if err != nil {
    log.Fatalf("create chat client: %v", err)
}
```

Then pass `chatClient` through `createAgents()`.

## Files to Modify

| File | Change |
|------|--------|
| `cmd/monitor-live/tools.go` | Replace `registryToolBinder` with `newToolBinder()` that uses internal `sub.ToolBinder` + `BridgeFromRegistry` |
| `cmd/monitor-live/agents.go` | Add `createChatClient()`, update `createExecutor()` with `WithChatClient`, propagate through `createAgents`/`createLeaderAgent`/`createSubAgents` |
| `cmd/monitor-live/main.go` | Capture `core.Registry` from `setupMCP()`, create `ChatClient`, pass to agent creation |
| `internal/agents/sub/executor.go` | Remove `if round == 0 { llmTools = nil }` to allow multi-round tool calling |

## Verification

1. **Build**: `go build ./cmd/monitor-live/`
2. **Unit test**: Create `convert_test.go` in `internal/tools/resources/core/` to verify `ToolSchemaToLLMTool` conversion
3. **Integration check**: Run the service and verify in logs that:
   - `GetToolSchemas()` returns non-empty list
   - `executeWithChatAndTools` is entered (not `executeWithLLMTextOnly`)
   - LLM receives tool definitions and can return `tool_calls`
   - Tool calls are executed and results sent back to LLM

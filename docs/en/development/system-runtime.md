# System Runtime Architecture

> Date: 2026-08-04
> Status: Stage 0-2 complete; Stage 3 partial (B01 done; F04 live-DAG binding moved pre-Start, currently no-op with explicit warning — no live DAG registered yet, Track C deferred)

## Overview

The System Runtime (`internal/system_runtime/`) is the system-level control plane that unifies component assembly, dependency resolution, lifecycle orchestration, and shutdown coordination. It is distinct from `ares_runtime.Manager`, which remains the Agent lifecycle subsystem.

## Key Concepts

### Component Lifecycle

Every managed component implements one or more of these interfaces:

```go
type Component interface {
    Name() string
    Dependencies() []string
}

type Binder interface {
    Bind(ctx context.Context, deps Resolver) error
}

type Starter interface {
    Start(ctx context.Context) error
}

type ReadinessChecker interface {
    Ready(ctx context.Context) error
}

type Stopper interface {
    Stop(ctx context.Context) error
}

type Waiter interface {
    Wait() error
}
```

State machine:
```
Declared → Constructed → Bound → Started → Ready
                         ↘ Failed
Ready → Degraded / Failed
Ready|Degraded → Stopping → Stopped
```

### Component Modes

- **Required**: Must reach Ready for system Ready. Failure = system failure.
- **Optional**: Not constructed when disabled. When enabled, behaves as Required.
- **Degraded**: May operate with reduced capability. Must report missing capability.

### Config Gates (Stage 2)

The following config flags now control whether components are constructed:

| Config Flag | Default | Component | Behavior when false |
|---|---|---|---|
| `memory.enabled` | false | MemoryManager | Not constructed, no goroutines |
| `evolution.enabled` | false | NewEvolution + GA ticker | Not constructed, no tickers |
| `knowledge.retrieval_enabled` | false | AKG loop | Read-only or skipped |
| `embedding.enabled` | false | EmbeddingClient | Not constructed |
| `storage.enabled` | false | PostgreSQL pool | Not constructed |

**Important**: Tests that call `Bootstrap()` must explicitly set `memory.enabled: true` if they expect Memory to be constructed.

### EventStore Wiring (Stage 3)

EventStore is now wired into MemoryManager during Bootstrap construction, not post-Bootstrap in `serve.go`. This eliminates the B01 bypass.

Before (bypass):
```go
// serve.go — old bypass
comp, _ := ares_bootstrap.Bootstrap(ctx, cfg, deps)
comp.Memory.SetEventStore(store, "memory") // ← bypass
```

After (fixed):
```go
// bootstrap.go — EventStore wired during construction
if cfg.Memory.Enabled {
    mem, _ := ProvideMemory(memCfg)
    comp.Memory = mem
    if comp.EventStore != nil {
        comp.Memory.SetEventStore(comp.EventStore, "memory") // ← in Bootstrap
    }
}
```

### EvidenceStore (always available)

`comp.EvidenceStore` is now always set, even when evolution is disabled. When evolution is enabled, it's the NewEvolution EvidenceStore. When disabled, it's a standalone store for the flight recorder.

## Usage

### Basic Bootstrap (Memory enabled)

```go
cfg := &ares_config.Config{
    LLM:    ares_config.LLMConfig{Provider: "ollama", Model: "llama3.2"},
    Memory: ares_config.MemoryConfig{Enabled: true},
}
comp, err := ares_bootstrap.Bootstrap(ctx, cfg, nil)
```

### Full Configuration

```go
cfg := &ares_config.Config{
    LLM:       ares_config.LLMConfig{Provider: "ollama", Model: "llama3.2"},
    Memory:    ares_config.MemoryConfig{
        Enabled:          true,
        EnableRAG:        true,
        EnableDistillation: true,
    },
    Evolution: ares_config.EvolutionConfig{Enabled: true},
    Knowledge: ares_config.KnowledgeConfig{RetrievalEnabled: true},
    Storage:   ares_config.StorageConfig{Enabled: true, Type: "postgres", Host: "localhost"},
    Embedding: ares_config.EmbeddingConfig{Enabled: true},
}
comp, err := ares_bootstrap.Bootstrap(ctx, cfg, nil)
```

### Minimal Configuration (no Memory, no Evolution)

```go
cfg := &ares_config.Config{
    LLM: ares_config.LLMConfig{Provider: "ollama", Model: "llama3.2"},
    // Memory and Evolution are disabled by default
}
comp, err := ares_bootstrap.Bootstrap(ctx, cfg, nil)
// comp.Memory == nil, comp.NewEvolution == nil
// comp.EvidenceStore is still set (standalone)
// comp.FlightRecorder is still started
```

## System Runtime Package

The `internal/system_runtime/` package provides:

- `Component` interface and lifecycle interfaces (Binder, Starter, etc.)
- `Registry` for component registration and dependency resolution
- `Orchestrator` for lifecycle management (Construct → Bind → Start → Ready, Stop → Wait)
- `Snapshot` API for component status reporting
- `Mode` (Required, Optional, Degraded) for component failure semantics

### Orchestrator Usage

```go
reg := system_runtime.NewRegistry()
reg.Register(myComponent, system_runtime.ModeRequired)

orch := system_runtime.NewOrchestrator(reg, ctx)
if err := orch.Start(ctx); err != nil {
    // rollback is automatic
    return err
}
defer orch.Shutdown(ctx)

// Check readiness
snap := reg.Snapshot()
if !reg.IsReady() {
    return fmt.Errorf("system not ready")
}
```

## Closure Tests

Closure tests are behind the `closure` build tag:

```bash
go test -tags closure ./internal/ares_bootstrap/...
```

All 20 closure tests run green (16 PASS, 4 SKIP). The F03 (knowledge retrieval
not-Ready with missing write deps), F04 (live-DAG binding), and PatchRegistry
identity checks are explicitly skipped: their hard assertions need the registry
to report a Degraded state or an entry-level test, and they are no longer PASS
entries that merely log known gaps (R09).

## Files Changed

| File | Change |
|---|---|
| `internal/system_runtime/component.go` | NEW: lifecycle interfaces |
| `internal/system_runtime/state.go` | NEW: state machine |
| `internal/system_runtime/registry.go` | NEW: component registry + topological sort |
| `internal/system_runtime/orchestrator.go` | NEW: lifecycle orchestrator |
| `internal/system_runtime/snapshot.go` | NEW: status snapshot API |
| `internal/ares_bootstrap/bootstrap.go` | Config gates (F01, F02), EventStore wiring (B01), EvidenceStore field |
| `internal/ares_bootstrap/retriever_wiring.go` | nil-interface-trap fix |
| `cmd/ares/serve.go` | Removed SetEventStore bypass (B01) |
| `api/bootstrap/bootstrap.go` | Use comp.EvidenceStore instead of comp.NewEvolution.EvidenceStore |
| `internal/ares_bootstrap/closure_contract_test.go` | 4 contract tests |
| `internal/ares_bootstrap/closure_shared_instance_test.go` | 6 shared instance tests |
| `internal/ares_bootstrap/closure_lifecycle_test.go` | 8 lifecycle tests |

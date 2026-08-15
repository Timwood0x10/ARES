# ares Architecture Deep Dive (XXVII): Context Management — Three Lines of Defense for the Token Budget

> Note: This article is grounded in the actual code (`internal/ares_memory/context/cleaner.go`, `internal/ares_memory/manager_impl.go`, `internal/knowledge/skills`) — the dedicated context-management article in the docs series.

## 1. Why Context Management Is the Agent's Lifeline

The LLM context window is a hard constraint: agents accumulate history every turn, and tool results (especially `tool_result`) can run hundreds or thousands of tokens — a few dozen turns easily blow through the window.

ARES controls the token budget with **three lines of defense**:

| Defense | Mechanism | Location | Solves |
|---------|-----------|----------|--------|
| 1. Turn grouping | `TurnID` links session messages | `internal/ares_memory/context` | Makes "one conversation turn" a processable unit |
| 2. Differential compression | Role-aware `ContextCleaner` | `internal/ares_memory/context/cleaner.go` | Token reduction for tool noise and repetition |
| 3. Progressive disclosure | skills Level-0 resident + body on demand | `internal/knowledge/skills` + memoryManager | 100 skills ≠ 100 full instruction bodies |

## 2. ContextCleaner: Role-Aware Differential Compression

`ContextCleaner` in `internal/ares_memory/context/cleaner.go` is the core of the second line of defense. Its design insight: **different message roles have different token-value densities**, so compression must be differentiated by role:

```go
// ContextCleaner intelligently cleans conversation context before LLM calls.
// It applies differential compression based on message role:
//   - tool_call / tool_result → aggressively compressed to first sentence
//   - assistant with ToolCalls → treated as tool-like content
//   - pure assistant reasoning → code blocks compressed, content truncated
//   - user / system → straightforward truncation
type ContextCleaner struct {
    mu          sync.Mutex
    stats       CleanerStats          // tool-call count + bytes-saved statistics
    codePattern *regexp.Regexp        // ```...``` code-block detection
}
```

**Four roles, four strategies**:

| Message Role | Compression Strategy | Rationale |
|--------------|----------------------|-----------|
| `tool_call` / `tool_result` | **Aggressively compressed to the first sentence** | Tool round-trips are the biggest noise source: ~90% of a result is pagination/repetition; the first sentence suffices |
| `assistant` + ToolCalls | Treated as tool-like content | Reasoning carrying tool calls usually need not be fully retained |
| Pure `assistant` reasoning | **Code blocks compressed + content truncated** | Reasoning steps can be truncated; code blocks are detected via regex and compressed |
| `user` / `system` | Straightforward truncation | User input is the semantic anchor; conservative strategy |

Key implementation details:
- **All fields preserved**: `Clean` returns a new slice retaining `Time`, `TurnID`, etc. — content is compressed, structure is not
- **Original slice immutable**: `Returns a new slice with compressed content; original slice is not modified` — zero side effects, safe to retry
- **Observable statistics**: `CleanerStats` (tool-call count + bytes saved) tracked internally — compression effectiveness is measurable

## 3. Turn Grouping: Making "One Turn" a Whole

The granularity of context compression is not a single message but a **turn** (one round of interaction). The `internal/ares_memory/context` layer uses `TurnID` to link one interaction (user input + agent reasoning + tool round-trips + final reply) into a group:

- **Structured messages**: `AddStructuredMessage` writes `TurnID`/`ToolCallID`/`ToolCalls` metadata into the session, preserving the full turn structure (for turn-aware cleaning)
- **Cleaning happens per turn**: compression/truncation respects turn boundaries — the intermediate states of one turn are never shredded
- **Distillation reuse**: `buildCleanedDistillationMessages` in `manager_impl.go` also cleans before distillation — the same turn-grouping semantics run across both "cleaning" and "distillation" consumers

## 4. Progressive Disclosure: 100 Skills ≠ 100 Full Instruction Bodies

The third line of defense lives in the knowledge layer: `internal/knowledge/skills.Registry` keeps only **name + one-line description** resident (Level-0); the full SKILL.md instruction body (Level-1) loads on demand.

The memoryManager mount (`manager_impl.go`):

```go
// SetSkillsRegistry attaches a skills registry for progressive disclosure.
// When set, BuildContext prepends a resident "Available skills" block listing
// each skill's name and description only; full skill details are fetched on
// demand by ID via the registry.
func (m *memoryManager) SetSkillsRegistry(reg *skills.Registry) {
    m.skillsRegistry = reg
}
```

- **Resident block**: `BuildContext` injects "Available skills" at the top (~100 tokens of name+description per skill)
- **On-demand fetch**: `LoadDetail` returns the full body only when the agent explicitly needs it
- **Capability Fabric upgrade** (0.3.0): `internal/ares_skills`'s `SeedRegistry` pours the skill-directory index into the same registry, and the `skill_load` tool fetches bodies on demand — the three-stage progressive disclosure (metadata → SKILL.md → resources) closes the loop in 0.3.0

## 5. Production Wiring

`ContextCleaner` is injected on two production paths:

```go
// manager_impl.go
ctxCleaner: memctx.NewContextCleaner()   // classic memoryManager

// production_manager.go
ctxCleaner: memctx.NewContextCleaner()   // production variant
```

`BuildContext` assembly: resident skills block (if seeded) → session history compressed by `ContextCleaner.Clean` → final prompt. Compression runs **before LLM calls**, so every request consumes the smallest possible context.

## 6. Summary

| Defense | Mechanism | Metric |
|---------|-----------|--------|
| Turn grouping | `TurnID` structured messages | One turn, one whole |
| Differential compression | Role-aware `ContextCleaner` | `CleanerStats` bytes saved |
| Progressive disclosure | skills Level-0 resident | 100 skills ≈ 100 × ~100 tokens |

**Design line: context is not "shoved into the window" but "budget-managed".** Each of the three lines handles one slice — grouping manages structure, compression manages noise, disclosure manages knowledge — together letting an agent fit "history + tool round-trips + skill knowledge" into a finite window. This is also the context-layer echo of the Capability Fabric principle "never stuff all skill content into the LLM".

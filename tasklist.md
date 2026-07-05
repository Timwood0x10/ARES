# Tool Intelligence Layer Task List

## Scope

This task list tracks the work required to evolve the current tool system from name-based tool
calling into a capability-driven planning layer.

Implementation work must follow `plan/rules/code_rules.md`:

- Keep files under 1000 lines.
- Keep functions under 100 lines.
- Use constructor-based dependency injection.
- Depend on interfaces for business components.
- Avoid global state for data transfer.
- Do not use `reflect` or `unsafe`.
- Wrap and propagate errors with context.
- Add meaningful tests for success, failure, and edge cases.
- Run the required checks before merge:
  - `goimports`
  - `go vet ./...`
  - `staticcheck ./...`
  - `golangci-lint run`
  - `go test -race -cover ./...`
- Do not commit from the agent.

## Phase 1: Design and Interfaces

- [x] Rewrite `tools.md` as an engineering design document.
- [x] Define the core planning objects:
  - `Intent`
  - `CapabilityRequirement`
  - `ToolCandidate`
  - `ToolScore`
  - `ExecutionPlan`
  - `ExecutionStep`
  - `ToolEvidence`
- [x] Document that `Registry` remains the runtime catalog.
- [x] Decide package placement for the planner.
  - Final path: `internal/tools/planner`.
- [x] Define narrow interfaces for:
  - `SemanticAnalyzer`
  - `CapabilityPlanner`
  - `ToolResolver`
  - `ToolScorer`
  - `ExecutionPlanner`
  - `EvidenceStore`
- [x] Define constructor names and dependency injection boundaries.
- [x] Write interface-level tests or compile-time assertions for the package boundary.

Acceptance criteria:

- The design separates planning from execution.
- The registry is not responsible for intent detection or scoring.
- Every planned Go component has a clear input and output.

## Phase 2: Single-step Planning

- [x] Implement `Intent` and `CapabilityRequirement` models.
- [x] Implement a rule-based `SemanticAnalyzer`.
- [x] Implement a deterministic `CapabilityPlanner` for simple tasks.
- [x] Implement `ToolResolver` using the existing `Registry` and `CapabilityEngine`.
- [x] Implement `ToolScorer` with static metadata and neutral evidence defaults.
- [x] Implement `ExecutionPlanner` that returns a single-step `ExecutionPlan`.
- [x] Add tests for:
  - empty request
  - unknown capability
  - multiple candidate tools
  - no candidate tools
  - deterministic ranking
  - unsafe side-effect penalty

Acceptance criteria:

- A simple request can produce an explainable single-step execution plan.
- Planner behavior is deterministic for identical inputs.
- Existing direct registry execution remains unchanged.

## Phase 3: Agent Integration

- [x] Add a compatibility integration path before changing default execution behavior.
- [x] Keep direct LLM tool-name execution as the default path until planner tests are stable.
- [x] Add planner fallback for:
  - tool not found
  - low-confidence tool selection
  - requests with no LLM tool call
- [x] Add structured logs with trace identifiers where available.
- [x] Add integration tests around `executeToolCall` or the nearest safe boundary.

Acceptance criteria:

- Existing tool calls still work.
- Planner fallback can select a tool without relying on a tool name from the LLM.
- Errors include enough context to locate the failing component.

## Phase 4: Evidence-aware Scoring

- [x] Define `ToolEvidence` model in Go.
- [x] Connect runtime results to the existing tool call experience collector where appropriate.
- [x] Add an evidence aggregation interface.
- [x] Compute success rate, latency, retry count, and failure class per tool and capability.
- [x] Feed aggregated evidence into `ToolScore`.
- [x] Add tests for:
  - no evidence
  - successful evidence improves score
  - repeated failures reduce score
  - high latency reduces score
  - non-idempotent tool is not retried

Acceptance criteria:

- Planner can rank tools with or without evidence.
- Evidence affects ranking in a predictable and testable way.
- Failed executions are recorded with normalized error classes.

## Phase 5: Multi-step Planning

- [x] Extend `ExecutionPlan` with dependency edges.
- [x] Validate input and output compatibility between steps.
- [x] Add fallback handling per step.
- [x] Decide whether multi-step execution belongs in the agent executor or workflow engine.
- [x] Implement DAG validation before DAG execution.
- [x] Add tests for:
  - missing dependency
  - cycle detection
  - incompatible output/input types
  - fallback selection
  - partial failure evidence

Acceptance criteria:

- Planner can produce a valid DAG without executing it.
- Invalid DAGs fail before runtime execution.
- Runtime evidence remains tied to `PlanID` and `StepID`.

## Phase 6: Capability Model Evolution

- [x] Split broad capabilities into precise capabilities.
- [x] Add aliases for natural-language matching.
- [x] Add stable input and output type declarations.
- [x] Add capability versioning if needed.
- [x] Add migration path for existing tools.
- [x] Add tests to ensure every built-in tool exposes at least one capability.

Acceptance criteria:

- Capability matching no longer depends mainly on tool descriptions.
- Tool capability metadata is specific enough for planning.
- Existing built-in tools remain discoverable.

## Review Checklist

- [x] Every new public type and method has an English comment.
- [x] Every function that can fail returns an error.
- [x] Every error is wrapped with component context.
- [x] No planner component executes a tool.
- [x] No runtime component performs semantic planning.
- [x] No global mutable state is used to pass planner data.
- [x] Tests include failure paths and boundary cases.
- [x] Lint, vet, static analysis, and race tests are run before merge.

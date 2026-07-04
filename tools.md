# Tool Intelligence Layer Design

## 1. Purpose

The Tool Intelligence Layer turns a user request into a high-quality tool execution plan.

The current tool system already has a useful runtime foundation:

- `Tool` defines executable behavior.
- `Registry` stores tools by name.
- `Capability` describes broad tool abilities.
- `CapabilityEngine` can map capabilities to registered tools.
- `ToolCallExperienceCollector` can persist execution feedback.

This design keeps those foundations and adds a planning layer above them. The goal is not to
replace the registry. The registry remains the runtime catalog and execution substrate. The new
layer decides which capabilities are required, which tools are candidates, how candidates are
scored, and what execution plan should be sent to the runtime.

## 2. Design Principles

1. Plan by capability, execute by tool.
2. Use the LLM only where semantic understanding is required.
3. Keep candidate resolution, scoring, validation, and execution planning deterministic.
4. Make every selected tool explainable.
5. Record execution evidence so future planning can improve from real outcomes.
6. Preserve the existing `Registry` API as the low-level runtime catalog.
7. Add narrow interfaces and constructor-based dependency injection for all new components.
8. Follow `plan/rules/code_rules.md` for all later Go implementation work.

## 3. Architecture

```text
Request
  |
  v
Semantic Analyzer
  |
  v
Intent
  |
  v
Capability Planner
  |
  v
Capability Requirements
  |
  v
Tool Resolver
  |
  v
Tool Candidates
  |
  v
Tool Scorer
  |
  v
Execution Planner
  |
  v
Execution Plan
  |
  v
Tool Runtime
  |
  v
Tool Evidence Collector
```

The registry is used by the Tool Resolver and Tool Runtime. It is intentionally not responsible
for understanding user intent or choosing the best tool.

## 4. Runtime Boundary

### 4.1 Registry

`Registry` remains a runtime catalog.

Responsibilities:

- Register tools.
- Return tools by name.
- List tools.
- Export schemas for LLM tool calling.
- Execute a named tool with validated parameters.

Non-responsibilities:

- It does not infer user intent.
- It does not rank tools.
- It does not generate execution plans.
- It does not own historical success metrics.

### 4.2 Capability Engine

`CapabilityEngine` remains the first deterministic resolver.

Responsibilities:

- Detect coarse capabilities from a request.
- Map capabilities to registered tools.
- Return candidate tools for a capability.

Planned evolution:

- Move from broad capabilities such as `math` or `text` toward more specific capabilities such as
  `Arithmetic`, `Summation`, `RegexExtract`, `PDFTextExtraction`, and `HTTPFetch`.
- Keep capability definitions stable and testable.

## 5. Core Objects

### 5.1 Intent

`Intent` is the normalized meaning of the user request. It must not contain tool names.

Fields:

- `Goal`: The concrete user objective.
- `Operation`: The main operation to perform.
- `Domain`: The domain or task family.
- `Inputs`: Structured input references or inline values.
- `Constraints`: User or system constraints.
- `RequiredCapabilities`: Capability names inferred from the request.
- `Complexity`: A coarse estimate such as `simple`, `multi_step`, or `workflow`.
- `Confidence`: Confidence in the semantic analysis.

Example:

```yaml
goal: calculate the sum from 1 to 1000000
operation: summation
domain: mathematical_computation
inputs:
  range_start: 1
  range_end: 1000000
required_capabilities:
  - Arithmetic
  - Summation
complexity: simple
confidence: 0.95
```

### 5.2 CapabilityRequirement

`CapabilityRequirement` describes one required ability in a plan.

Fields:

- `Name`: Stable capability name.
- `InputTypes`: Accepted input types.
- `OutputTypes`: Required output types.
- `Required`: Whether the capability is mandatory.
- `Constraints`: Capability-level constraints.
- `DependsOn`: Other capabilities that must run first.

Example:

```yaml
name: PDFTextExtraction
input_types:
  - application/pdf
output_types:
  - text/plain
required: true
depends_on:
  - HTTPFetch
```

### 5.3 ToolCandidate

`ToolCandidate` is a registered tool that can satisfy a capability requirement.

Fields:

- `ToolName`: Registered tool name.
- `ToolVersion`: Optional tool version.
- `Capability`: Matched capability.
- `InputTypes`: Supported input types.
- `OutputTypes`: Produced output types.
- `Constraints`: Tool constraints.
- `Cost`: Estimated cost unit.
- `Latency`: Estimated latency.
- `Deterministic`: Whether identical inputs should produce identical outputs.
- `Composable`: Whether output can feed another plan step.
- `Idempotent`: Whether retrying is safe.

Candidate discovery must be deterministic and must not depend only on free-form descriptions.

### 5.4 ToolScore

`ToolScore` explains why a candidate was selected or rejected.

Fields:

- `ToolName`: Candidate tool name.
- `Capability`: Matched capability.
- `Total`: Final score.
- `CapabilityMatch`: Match score between requirement and candidate.
- `Reliability`: Historical success score.
- `Latency`: Normalized latency score.
- `Cost`: Normalized cost score.
- `Safety`: Safety and side-effect score.
- `Recency`: Recent outcome adjustment.
- `Reasons`: Human-readable score explanations.

Initial scoring can use static metadata. Later scoring should include evidence from successful and
failed tool executions.

### 5.5 ExecutionPlan

`ExecutionPlan` is the planner output consumed by the runtime.

Fields:

- `PlanID`: Unique plan identifier.
- `Intent`: Normalized request intent.
- `Steps`: Ordered or graph-based execution steps.
- `Edges`: Dependencies between steps.
- `Inputs`: Plan-level inputs.
- `ExpectedOutput`: Expected final output type.
- `SelectedTools`: Chosen tools with scores.
- `Fallbacks`: Optional fallback tools.
- `Validation`: Preconditions and postconditions.

The first implementation should support a single-step plan. Multi-step DAG execution can be added
after candidate scoring and evidence collection are stable.

### 5.6 ExecutionStep

`ExecutionStep` represents one tool invocation in a plan.

Fields:

- `StepID`: Stable step identifier.
- `Capability`: Required capability.
- `ToolName`: Selected tool.
- `Arguments`: Runtime arguments.
- `DependsOn`: Previous step identifiers.
- `RetryPolicy`: Retry behavior based on idempotency.
- `Timeout`: Step-level timeout.
- `ExpectedOutput`: Expected output schema or type.

### 5.7 ToolEvidence

`ToolEvidence` records what happened after execution.

Fields:

- `PlanID`: Plan identifier.
- `StepID`: Step identifier.
- `ToolName`: Executed tool.
- `Capability`: Capability the tool was selected for.
- `Success`: Whether execution succeeded.
- `ErrorClass`: Normalized error category.
- `Latency`: Observed latency.
- `Cost`: Observed cost unit.
- `InputType`: Observed input type.
- `OutputType`: Observed output type.
- `RetryCount`: Number of retries.
- `Timestamp`: Execution time.

Evidence is the bridge from runtime behavior back to future planning. It must be structured enough
for scoring and debugging.

## 6. Component Responsibilities

### 6.1 Semantic Analyzer

Input:

- User request.
- Optional conversation context.

Output:

- `Intent`.

Rules:

- It may use an LLM for semantic normalization.
- It must not select tools.
- It must output capabilities, not tool names.
- It must expose confidence and ambiguity.

### 6.2 Capability Planner

Input:

- `Intent`.

Output:

- Ordered `CapabilityRequirement` values.

Rules:

- It decomposes goals into required capabilities.
- It handles simple tasks as one requirement.
- It can produce dependencies for multi-step tasks.
- It must be deterministic for the same intent.

### 6.3 Tool Resolver

Input:

- `CapabilityRequirement`.
- `Registry`.
- `CapabilityEngine`.

Output:

- `ToolCandidate` values.

Rules:

- It resolves by capability metadata first.
- It may use tags and schemas as secondary filters.
- It must reject tools with incompatible input or output types.
- It must not execute tools.

### 6.4 Tool Scorer

Input:

- `CapabilityRequirement`.
- `ToolCandidate` values.
- Historical `ToolEvidence`.

Output:

- Ranked `ToolScore` values.

Rules:

- It must produce explainable scores.
- It must work without historical evidence.
- It must prefer deterministic and idempotent tools for pure computation.
- It must penalize unsafe side effects unless required by the task.

### 6.5 Execution Planner

Input:

- `Intent`.
- `CapabilityRequirement` values.
- Ranked tool candidates.

Output:

- `ExecutionPlan`.

Rules:

- It must validate that every required capability has a selected tool.
- It must include fallbacks when multiple valid candidates exist.
- It must produce a single-step plan before DAG support is implemented.
- It must not execute the plan.

### 6.6 Tool Runtime

Input:

- `ExecutionPlan`.

Output:

- Tool result.
- `ToolEvidence`.

Rules:

- It executes selected tools through the existing registry or tool binder.
- It validates arguments before execution.
- It observes timeouts and cancellation.
- It records structured evidence for every step.

## 7. Scoring Model

The initial scoring model should be simple and deterministic.

```text
total =
  capability_match * 0.40 +
  reliability      * 0.25 +
  latency          * 0.15 +
  cost             * 0.10 +
  safety           * 0.10
```

Default values:

- `CapabilityMatch`: Based on exact capability and type compatibility.
- `Reliability`: Neutral when no evidence exists.
- `Latency`: Static metadata or neutral default.
- `Cost`: Static metadata or neutral default.
- `Safety`: Based on idempotency, side effects, and state mutation.

The weights should be constants in the first implementation. They can become configuration only
after there is evidence that tuning is needed.

## 8. Evidence Feedback Loop

Tool execution feedback should flow into future scoring.

```text
ExecutionPlan
  |
  v
Tool Runtime
  |
  v
ToolEvidence
  |
  v
Experience Store
  |
  v
Tool Scorer
```

Evidence aggregation should answer:

- Which tool succeeds most often for a capability?
- Which tool fails for specific input types?
- Which tool is fastest for a task class?
- Which tool is safe to retry?
- Which fallback works when the primary tool fails?

## 9. Integration Strategy

### Phase 1: Documentation and interfaces

- Define the planning objects.
- Document component boundaries.
- Keep all existing runtime behavior unchanged.

### Phase 2: Single-step planner

- Convert request to `Intent`.
- Resolve one or more capabilities.
- Rank candidates.
- Produce a single-step `ExecutionPlan`.
- Keep direct name-based execution as a compatibility path.

### Phase 3: Evidence-aware scoring

- Persist `ToolEvidence`.
- Aggregate success rate, latency, and failure classes.
- Feed evidence into `ToolScore`.

### Phase 4: Multi-step execution plans

- Add dependency edges between steps.
- Validate input and output compatibility between steps.
- Execute DAG plans through the workflow runtime.

## 10. Non-goals

- Do not replace `Registry` in the first implementation.
- Do not require the LLM to rank tools.
- Do not implement multi-step DAG execution before single-step planning is stable.
- Do not add speculative configuration knobs before scoring behavior is measurable.
- Do not make capability definitions depend on long natural-language descriptions.

## 11. Open Questions

1. Should capability names remain Go constants or move to a versioned manifest?
2. Should external MCP tools expose capabilities through tags, schemas, or a separate adapter?
3. Where should aggregated evidence live: memory, evolution experience store, or a tool-specific store?
4. What is the minimum confidence threshold before the planner falls back to LLM tool calling?
5. Which runtime should execute multi-step plans: the agent executor or workflow engine?

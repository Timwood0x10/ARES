# Causal Context Compression Plan

## Goal

Replace the old "skip tool calls" cleanup idea with causal compression:

- Keep the causal chain of each agent turn.
- Compress each event's content.
- Preserve enough structured evidence for replay, debugging, and distillation.
- Avoid storing or prompting with raw oversized tool output unless it is still active protocol state.

The key change is:

```text
Do not drop tool calls.
Keep tool-call causality, compress tool-call payloads.
```

This is a hard requirement. Tool-call causality is not optional cleanup data; it is the execution proof chain. The implementation may truncate, summarize, externalize, or hash tool payloads, but it must not remove the causal event from the prompt trace or distillation trace.

## Retrofit Existing Code

Some cleanup and structured-message work already exists. Do not delete it. Re-route it:

- Keep existing structured fields such as `TurnID`, `ToolCallID`, and `ToolCalls`.
- Treat them as causal linkage fields, not just provider protocol fields.
- Change completed-turn cleanup from "drop tool result" to "replace raw tool result with structured observation".
- Change distillation input from `user + assistant` only to `user intent + tool actions + tool observations + final answer`.
- Keep raw tool output through artifact references when large.

Current code paths that must be adjusted:

- `internal/memory/context/cleaner.go`
  - Replace completed-turn tool-message dropping with causal event compression.
  - Preserve `TurnID`, `ToolCallID`, and `ToolCalls` in every cleaned/converted message.
- `internal/memory/manager_impl.go`
  - `BuildPromptMessages` and `BuildContext` should render causal traces for completed turns.
  - `StoreDistilledTask` should build a distillation trace that includes compressed tool observations.
- `internal/memory/production_manager.go`
  - Store causal metadata in conversation metadata JSONB.
  - Do not use raw payload dumps as the only memory content.
- `internal/memory/distillation`
  - Extend extraction to consume causal evidence, not just adjacent user/assistant text.

The intended retrofit is additive: preserve existing raw history and structured message APIs, then add causal views on top.

## Why The Old Direction Is Wrong

Dropping tool calls saves context, but it breaks causality:

- User request explains the intent.
- Assistant tool call explains the action chosen.
- Tool result explains the observed world state.
- Assistant answer explains the decision based on that observation.

If the tool call/result is skipped, later replay or distillation may know the final answer but not why it was correct.

Correct behavior:

```text
raw tool result -> structured observation summary + artifact reference
```

Incorrect behavior:

```text
raw tool result -> deleted
```

The second form is forbidden because it destroys the evidence chain.

## Causal Judgment From Structured Output

Structured output makes causal judgment possible. The system should not infer causality from text. It should infer causality from exact fields:

- `turn_id` groups events into one ReAct turn.
- `tool_call_id` links assistant action to tool observation.
- `parent_id` links an event to the event it depends on.
- `event_kind` tells whether the event is intent, action, observation, decision, or outcome.
- `tool_name` identifies the action source.
- `summary` carries compressed content.
- `artifact_ref` points to raw evidence when needed.

If these fields exist, the system can answer:

- What was the user's intent?
- What action did the assistant choose?
- What did the tool observe?
- What decision did the assistant make from that observation?
- What raw artifact can be fetched if the summary is insufficient?

If these fields do not exist, the event should be marked `orphan` rather than guessed by regex or message order.

## Target Shape

Each ReAct turn should become a compact causal trace:

```text
Turn
- user_intent
- assistant_action
- tool_observation
- assistant_decision
- final_outcome
```

Raw content is compressed, but event order and IDs remain intact.

## Canonical Structures

### 1. Add `CausalEvent`

- What to do:
  - Introduce a structured event representation for prompt building and distillation.
- How to do it:
  - Add a type near `internal/memory/context`, for example:

```go
type CausalEvent struct {
    TurnID     string
    EventID    string
    ParentID   string
    Role       string
    Kind       string
    ToolCallID string
    ToolName   string
    Summary    string
    Evidence   map[string]any
    ArtifactRef string
    RawRef      string // compatibility alias if needed during migration
    Timestamp  time.Time
}
```

- Acceptance:
  - Every compressed event has enough IDs to reconstruct order and dependency.
  - Raw content is referenced, not copied, when it is too large.
  - Tool action and tool observation events are never removed from completed turns.

### 2. Add `CausalTurn`

- What to do:
  - Group events by explicit `turn_id`.
- How to do it:
  - Add:

```go
type CausalTurn struct {
    TurnID string
    Events []CausalEvent
    Outcome string
}
```

  - Group by `TurnID` first.
  - If `TurnID` is missing, use exact `tool_call_id` linkage only.
  - If exact linkage is impossible, mark event as `orphan`, do not guess.
- Acceptance:
  - No regex, substring, or role-adjacency heuristic is required to build causality.

## Compression Rules

### 3. Compress Tool Calls Into Actions

- What to do:
  - Keep the action, remove oversized arguments.
- How to do it:
  - Convert assistant `tool_calls` into event summaries:

```text
Action: call {tool_name}
Args: {selected safe fields}
Reason: {assistant content summary if present}
```

  - Preserve:
    - `tool_call_id`
    - `tool_name`
    - argument keys
    - important argument values such as path, command, URL, query
  - Do not preserve:
    - huge inline payloads
    - raw file contents
    - secrets
- Acceptance:
  - Tool action remains visible in causal chain.
  - Tool arguments cannot dominate prompt size.
  - Every tool action has an event whose `tool_call_id` can be matched to a later observation.

### 4. Compress Tool Results Into Observations

- What to do:
  - Preserve what the agent learned from the tool.
- How to do it:
  - Build per-tool observation summaries from structured fields.
  - Recommended fields:
    - `tool_call_id`
    - `tool_name`
    - `status`
    - `exit_code`
    - `read_paths`
    - `changed_paths`
    - `artifact_refs`
    - `important_lines`
    - `error_summary`
    - `bytes_in`
    - `bytes_out`
  - For file reads:

```text
Observed file {path}: {summary}, lines {range}, hash {hash}
```

  - For writes:

```text
Changed file {path}: {operation}, {summary}
```

  - For commands:

```text
Ran {command}: exit={code}, key output={summary}
```

- Acceptance:
  - The observation explains what changed or what was learned.
  - Raw output is stored as reference/artifact, not copied into prompt by default.
  - Every observation keeps `tool_call_id` and `parent_id`.
  - A missing/failed tool result is represented as an observation with status/error, not dropped.

### 5. Preserve Active Tool Protocol Exactly

- What to do:
  - Do not compress messages still required by the current provider tool loop.
- How to do it:
  - If a tool call has not yet received its matching result, preserve raw provider-required messages.
  - Once the turn is complete, convert them to causal events.
- Acceptance:
  - OpenAI-style tool continuation still works.
  - Completed turns are compressed.

### 5.1 Keep Completed Tool Causality

- What to do:
  - Completed tool events must be retained as compressed causal events.
- How to do it:
  - For completed turns:
    - raw assistant tool-call message -> `assistant_action`
    - raw tool result message -> `tool_observation`
    - final assistant message -> `assistant_decision`
  - Remove or externalize large raw fields only after producing the causal event.
- Acceptance:
  - Prompt trace still shows the tool step occurred.
  - Distillation trace still has evidence for the final answer.
  - Replay can reconstruct the action-observation-decision chain without raw blobs.

## Distillation Changes

### 6. Distill From Causal Trace, Not Raw Chat

- What to do:
  - Change distillation input from raw messages to compressed causal turns.
- How to do it:
  - Add a builder:

```go
BuildDistillationTrace(sessionID, taskID) []CausalTurn
```

  - Feed the distiller a compact text or structured DTO derived from:
    - user intent
    - action summaries
    - observations
    - final answer
    - task outcome
- Acceptance:
  - Distillation preserves the reason chain.
  - Distillation does not need raw tool output.
  - Distillation still includes tool actions and tool observations as compressed evidence.

### 7. Update Experience Extraction

- What to do:
  - Extract problem/solution/evidence from causal turns.
- How to do it:
  - Extend the distillation extractor to understand:

```text
Problem: user_intent
Actions: assistant_action events
Evidence: tool_observation events
Solution: assistant_decision/final_outcome
```

  - Store evidence summaries in metadata:
    - `causal_turn_id`
    - `tool_names`
    - `observations`
    - `changed_paths`
    - `artifact_refs`
- Acceptance:
  - Stored memories can explain why the solution was valid.
  - Replays can inspect the causal evidence without loading huge raw outputs.
  - If two final answers differ only because their tool observations differ, the distilled memories remain distinguishable.

## Prompt Context Changes

### 8. Build Prompt Context From Causal Trace

- What to do:
  - Use causal compression when building prompt history.
- How to do it:
  - Replace old role-only cleaner behavior with:

```go
BuildPromptTrace(sessionID) []CausalTurn
RenderPromptTrace(turns []CausalTurn) []Message
```

  - Recent active turn:
    - preserve raw protocol state if needed
  - Completed turns:
    - render compact causal trace
- Acceptance:
  - Prompt includes cause/effect sequence.
  - Prompt does not include raw stale tool dumps.
  - Prompt does not hide that a tool was called.

### 9. Separate Raw History, Prompt Trace, And Distillation Trace

- What to do:
  - Keep three views with different purposes.
- How to do it:
  - Raw history:
    - full audit log
    - not directly used for long prompts
  - Prompt trace:
    - compact causal view for next LLM call
  - Distillation trace:
    - compact causal view with evidence metadata
- Acceptance:
  - Cleaning never mutates raw history.
  - Distillation and prompt building can evolve independently.

## Storage Changes

### 10. Raw Tool Output Storage Is Not Vector Storage

- What to do:
  - Store raw tool output in a durable artifact store, not in pgvector.
  - Store only causal summaries and references in prompt, distillation, and vector memory paths.
- How to do it:
  - Route data by purpose:
    - `conversations/messages`: causal event, compact summary, metadata, `artifact_ref`.
    - `tool_artifacts`: raw tool output or object-store pointer.
    - `experiences_1024` / pgvector: distilled semantic memory and causal summary only.
  - Persist artifacts when a tool result is ingested, before prompt cleanup or distillation.
  - Replace oversized tool-result content with:

```json
{
  "event_kind": "tool_observation",
  "tool_call_id": "call_x",
  "tool_name": "read_file",
  "summary": "Observed docs/a.md lines 1-80; file describes checkpoint recovery.",
  "artifact_ref": {
    "artifact_id": "uuid",
    "content_hash": "sha256:...",
    "content_size": 182394,
    "storage_type": "postgres_bytea"
  }
}
```

- Acceptance:
  - Raw tool payload is fetchable for audit/replay.
  - Prompt and distillation can proceed from summaries plus `artifact_ref`.
  - pgvector never receives raw tool dumps, command logs, file contents, or provider payload blobs.

Recommended PostgreSQL table:

```sql
CREATE TABLE tool_artifacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    turn_id TEXT,
    message_id TEXT,
    tool_call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,

    content_hash TEXT NOT NULL,
    content_size BIGINT NOT NULL,
    mime_type TEXT,
    compression TEXT,

    storage_type TEXT NOT NULL,
    storage_uri TEXT,
    content BYTEA,

    summary TEXT,
    metadata JSONB DEFAULT '{}',

    created_at TIMESTAMPTZ DEFAULT NOW(),
    expires_at TIMESTAMPTZ
);

CREATE INDEX idx_tool_artifacts_session
ON tool_artifacts (tenant_id, session_id, turn_id);

CREATE INDEX idx_tool_artifacts_tool_call
ON tool_artifacts (tenant_id, tool_call_id);

CREATE UNIQUE INDEX idx_tool_artifacts_hash
ON tool_artifacts (tenant_id, content_hash);
```

Storage rules:

- `storage_type = 'postgres_bytea'`: use `content` for small/medium artifacts that should stay in PostgreSQL.
- `storage_type = 'object_store'`: use `storage_uri` for large artifacts stored outside PostgreSQL.
- `content_hash` is required for deduplication and replay verification.
- `summary` is the observation text used by prompt and distillation.
- `metadata` stores tool-specific structured fields such as `exit_code`, `changed_paths`, `read_paths`, `line_ranges`, `stderr_digest`, and `provider_request_id`.

### 11. Store Raw Artifacts By Reference

- What to do:
  - Keep raw tool output available without putting it into prompt/memory text.
- How to do it:
  - Add artifact references to message metadata:
    - `artifact_id`
    - `content_hash`
    - `storage_path`
    - `mime_type`
    - `size`
    - `tool_call_id`
    - `turn_id`
  - For large tool results, store raw output as artifact and keep only summary in message content.
- Acceptance:
  - Replay can fetch raw evidence if needed.
  - Normal prompt/memory paths stay compact.
  - Artifact references are linked to the causal event that produced them.

### 12. Persist Causal Metadata

- What to do:
  - Make causal chain queryable.
- How to do it:
  - Extend message metadata with:
    - `turn_id`
    - `event_id`
    - `parent_id`
    - `tool_call_id`
    - `event_kind`
    - `summary`
    - `evidence`
    - `artifact_ref`
  - For PostgreSQL conversations, store these in metadata JSONB first.
  - Later, promote hot fields to columns if query performance requires it.
- Acceptance:
  - A turn can be reconstructed from stored messages exactly.
  - Cause/effect can be reconstructed without reading raw message content.

## Embedding Integration

### 13. Embed Causal Memory, Not Raw Tool Text

- What to do:
  - Align with `docs/embedding-lifecycle-unification-plan.md`.
- How to do it:
  - Canonical memory text should include causal evidence:

```text
MemoryType: {type}
Problem: {user_intent}
Actions: {action summaries}
Observations: {tool observation summaries}
Solution: {final answer}
```

  - Do not embed raw tool output.
  - Include causal metadata in experience metadata.
- Acceptance:
  - Embedding keeps semantic cause/effect.
  - Embedding does not drift due to raw output formatting.
  - Embedding input is built after artifact externalization, from canonical causal summaries only.

## Implementation Order

### Phase 1: Data Model

1. Add `CausalEvent` and `CausalTurn`.
2. Add metadata fields to existing message model.
3. Add exact grouping by `turn_id` and `tool_call_id`.
4. Add orphan handling for unlinked events.
5. Add `tool_artifacts` migration and artifact repository/service.

### Phase 2: Compression

1. Add tool-call action summarizers.
2. Add tool-result observation summarizers.
3. Persist raw tool results to `tool_artifacts` during tool-result ingestion.
4. Add artifact reference support for large raw outputs.
5. Add tests for file/read/write/command/network/search observations.
6. Replace any completed-turn tool dropping with observation compression.

### Phase 3: Prompt View

1. Add `BuildPromptTrace`.
2. Add `RenderPromptTrace`.
3. Preserve active provider tool protocol exactly.
4. Replace completed-turn raw tool messages with causal summaries.

### Phase 4: Distillation View

1. Add `BuildDistillationTrace`.
2. Update extractor to consume causal traces.
3. Store evidence metadata in memory candidates.
4. Add tests proving tool observations affect extracted memories.

### Phase 5: Embedding

1. Update canonical memory embedding text to include causal summaries.
2. Ensure `EmbeddingSpec` receives causal fields, not raw payload dumps.
3. Add drift tests for equivalent causal traces.

## Tests To Add

### Causal Preservation Tests

- Given user -> assistant tool call -> tool result -> assistant final answer.
- Assert compressed trace keeps all four causal steps.
- Assert no completed tool event disappears from the trace.

### No Raw Tool Dump Tests

- Given a large tool result.
- Assert prompt trace contains summary and artifact reference, not full content.

### Replay Adequacy Tests

- Given compressed causal trace.
- Assert replay can identify:
  - what tool was called
  - why it was called
  - what it observed
  - what final answer used that observation

### Distillation Evidence Tests

- Given two similar user problems with different tool observations.
- Assert distilled memories keep different evidence metadata.

## Non-Goals

- Do not drop completed tool calls entirely.
- Do not embed raw tool output by default.
- Do not infer causality from free-form text.
- Do not mutate raw stored history during compression.
- Do not block active tool-call protocol continuation.
- Do not treat tool output as disposable noise; only raw payload size is noise, not the causal event.

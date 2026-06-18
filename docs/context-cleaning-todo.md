# Context Cleaning TODO

code rules: plan/rules/code_rules.md skills.md uber_go_style.md 

## Goal

Add a runtime context cleaning layer before each LLM call, while keeping the current distillation layer for long-term memory. The short-term layer should remove stale tool-call detail and repeated reasoning from prompt input. The distillation layer should keep only reusable facts, decisions, preferences, and lessons.

## Current State

- `internal/memory/context/cleaner.go` already has a role-aware cleaner.
- `internal/memory/context/session.go` and `internal/llm/output/toolcall.go` already define structured tool-call fields.
- `internal/memory/distillation` already provides long-term memory distillation.
- The main gap is that the runtime path still collapses history into plain text in `BuildContext`, and public memory APIs mostly store only `role + content`.

## TODO

### 1. Define One Canonical Message Model

- What to do:
  - Create or choose one canonical internal message type for runtime memory.
  - It must represent user, assistant, system, tool call, and tool result messages.
  - It must carry `tool_calls`, `tool_call_id`, `turn_id`, timestamps, and metadata.
- How to do it:
  - Use `internal/memory/context.Message` as the likely base.
  - Avoid duplicating similar structs across `internal/memory`, `api/core`, and `internal/llm/output`.
  - Add explicit conversion helpers only at package boundaries.
  - Match messages by explicit fields, not by regex or fuzzy text scanning.
- Acceptance:
  - Tool call metadata can survive memory storage and retrieval.
  - No main runtime path needs to encode tool calls inside plain text.

### 2. Extend Memory APIs For Structured Messages

- What to do:
  - Add structured message APIs beside the current `AddMessage(sessionID, role, content)` path.
  - Keep the old API for compatibility.
- How to do it:
  - Add something like `AddStructuredMessage(ctx, sessionID, msg)` to the internal memory interface.
  - Extend `api/core.Message` or its metadata contract to preserve tool call fields.
  - Update in-memory and PostgreSQL-backed implementations consistently.
- Acceptance:
  - Assistant tool calls and tool results can be stored and retrieved as structured fields.
  - Existing callers using plain role/content still compile.

### 3. Make ContextCleaner Turn-Aware

- What to do:
  - Change cleaning from message-only truncation to turn-aware cleanup.
  - A turn should include assistant tool calls, tool results, and the final assistant answer.
- How to do it:
  - Group messages by `turn_id` first.
  - If `turn_id` is absent, use exact structural linkage via `tool_call_id` and stored message role transitions.
  - Do not infer turns from regex, substring search, or free-form text heuristics.
  - For completed turns, replace tool-call detail with a compact summary.
  - For the active turn, preserve tool-call protocol fields required by the provider.
- Acceptance:
  - Old completed tool results do not re-enter prompts as full text.
  - The active tool-call loop still has enough data to send valid provider messages.
  - No cleanup step depends on regex matching of prompt text.

### 4. Split Prompt Context From Stored History

- What to do:
  - Keep full structured history in memory/storage.
  - Generate a separate cleaned view only when building the next LLM request.
- How to do it:
  - Introduce a function like `BuildPromptMessages(sessionID, input)` that returns structured prompt messages.
  - Keep `BuildContext` as a legacy text wrapper or make it call the new function and render to text.
  - Do not mutate stored messages during prompt cleaning.
- Acceptance:
  - Stored history remains auditable.
  - LLM prompt input is smaller than raw history.
  - Cleaning decisions are deterministic and testable.

### 5. Add Tool Result Summaries

- What to do:
  - Summarize tool results before they leave the current ReAct round.
  - Preserve only stable facts needed by future reasoning.
- How to do it:
  - Add a `ToolResultSummary` field in metadata or a structured summary message.
  - For file reads, keep path, version clue if available, and short conclusion.
  - For writes, keep changed path and operation summary.
  - For commands, keep command, exit code, and important output lines.
  - Derive summaries from explicit tool result schema, not from regex parsing of raw text blobs.
- Acceptance:
  - A long `read`, `grep`, test output, or tool JSON response can be reduced to a small summary.
  - Follow-up prompts do not include stale raw tool output by default.
  - Summary generation is field-driven and deterministic.

### 6. Add Policy Controls

- What to do:
  - Make cleanup behavior configurable.
- How to do it:
  - Add options for max raw tool result length, max summarized turns, and whether to keep raw tool details in prompt.
  - Default to aggressive cleanup for completed turns.
  - Preserve raw details only for the active tool protocol step.
- Acceptance:
  - Conservative mode can keep more history for debugging.
  - Default mode improves prompt size without breaking tool-call continuation.

### 7. Connect Distillation To The Cleaned Runtime Layer

- What to do:
  - Feed distillation with cleaned, semantically meaningful task history instead of raw noisy tool logs.
- How to do it:
  - After task completion, pass final user request, final answer, compact tool summaries, and task output to the distiller.
  - Do not distill raw command output, large file reads, stack traces, or repeated reasoning unless they are the lesson.
- Acceptance:
  - Distilled memories contain reusable experience, not transient execution noise.
  - Distillation still has enough context to explain why the answer was produced.

### 8. Add Tests For Cache-Friendly Cleanup

- What to do:
  - Add tests that model repeated ReAct turns and verify old tool noise is dropped before the next LLM input.
- How to do it:
  - Create fixture conversations like `[user, assistant, tool_result, assistant]`.
  - Assert that completed tool detail is summarized or removed in the next prompt view.
  - Assert that stable earlier prefix messages remain unchanged when a new user turn is appended.
- Acceptance:
  - Tests cover old tool result removal, active tool-call preservation, and summary rendering.
  - Tests prove the prompt view is smaller than raw history.

### 9. Add Observability

- What to do:
  - Track whether the cleanup layer is actually saving context.
- How to do it:
  - Extend cleaner stats with raw bytes, cleaned bytes, dropped tool messages, summarized tool messages, and active preserved messages.
  - Log per-session cleanup metrics at debug level.
- Acceptance:
  - Developers can see prompt-size savings and cleanup behavior in logs.

### 10. Roll Out In Compatibility Mode

- What to do:
  - Ship structured cleanup without breaking current users.
- How to do it:
  - Keep old `BuildContext` behavior available.
  - Add the new structured prompt builder behind config.
  - Convert one internal caller first, then expand.
- Acceptance:
  - Existing examples still run.
  - New structured path has focused tests.
  - Config can switch between legacy text context and structured cleaned context.

## Recommended Order

1. Canonical message model.
2. Structured memory APIs.
3. Prompt-view builder that does not mutate stored history.
4. Turn-aware cleaner.
5. Tool result summaries.
6. Distillation input cleanup.
7. Tests and observability.
8. Config-gated rollout.

## Non-Goals

- Do not replace the distillation engine.
- Do not delete raw stored history by default.
- Do not optimize vector retrieval until runtime prompt cleanup is working.
- Do not introduce provider-specific logic into the memory package; keep provider adapters at the boundary.

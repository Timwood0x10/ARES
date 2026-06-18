# Embedding Lifecycle Unification Plan

## Goal

Unify embedding generation across memory distillation, storage, and retrieval so the system does not generate incompatible vectors for the same semantic object. The target is one canonical embedding input per object type, one embedding pipeline, explicit model/version metadata, and predictable re-embedding behavior.

## Current Code Reality

- `internal/memory/distillation.Distiller` embeds `problem -> solution` with prefix `memory:` during `DistillConversation`.
- `internal/memory/manager_impl.go` later stores only `Problem`, `Solution`, and confidence through `expRepo.Create`; it does not pass `Memory.Vector` to the repository.
- `internal/memory/production_manager.go` stores distilled payloads through `WriteBuffer` using `fmt.Sprintf("%v", distilled.Payload)` as content.
- `internal/storage/postgres/write_buffer.go` inserts a placeholder vector and queues async embedding tasks.
- `internal/memory/manager_impl.go` searches with `EmbedWithPrefix(query, "query:")`.
- `internal/storage/postgres/services/retrieval_service.go` searches with `Embed(query)`.
- `internal/storage/postgres/services/simple_retrieval_service.go` searches with `EmbedWithPrefix(query, QueryPrefix)`.

This means the system currently has multiple embedding inputs and prefixes for related data.

## Design Rule

Embedding generation must not be owned by distillation, storage, or retrieval separately. These layers must call one shared pipeline:

- Distillation decides what memory is worth storing.
- Embedding pipeline decides exactly how memory/query text is vectorized.
- Storage persists the vector and the embedding spec.
- Retrieval builds query vectors from the same spec rules.

## Canonical Types

### 1. Add `EmbeddingKind`

- What to do:
  - Define explicit embedding kinds.
- How to do it:
  - Add a new package or file, likely `internal/memory/embedding/spec.go`.
  - Start with:
    - `memory_experience`
    - `memory_query`
    - `knowledge_chunk`
    - `tool_result_summary`
    - `task_result`
- Acceptance:
  - Callers cannot pass arbitrary prefixes directly in memory/retrieval code.

### 2. Add `EmbeddingSpec`

- What to do:
  - Represent the exact embedding contract as data.
- How to do it:
  - Add:

```go
type EmbeddingSpec struct {
    Kind      EmbeddingKind
    Text      string
    Prefix    string
    Model     string
    Version   int
    Dim       int
    Hash      string
}
```

  - `Hash` must be computed from `kind + prefix + text + model + version + dim`.
- Acceptance:
  - The same object always produces the same spec hash.
  - Model/version changes produce a different hash.

### 3. Add Canonical Text Builders

- What to do:
  - Define one canonical text format per object type.
- How to do it:
  - Add builders like:

```go
BuildMemoryExperienceSpec(memory MemoryCandidate) EmbeddingSpec
BuildMemoryQuerySpec(query string) EmbeddingSpec
BuildKnowledgeChunkSpec(chunk KnowledgeChunk) EmbeddingSpec
```

  - For memory experience, use a stable field-driven format:

```text
MemoryType: {type}
Problem: {problem}
Solution: {solution}
```

  - Do not use `fmt.Sprintf("%v", map)` or raw payload dumps.
- Acceptance:
  - Two equivalent memories with reordered map fields generate identical canonical text.
  - No embedding input is generated from Go map string formatting.

## Integration With Current Context Cleaning Work

### 4. Feed Distillation From Cleaned Structured Context

- What to do:
  - Distillation should consume stable structured data, not raw tool logs.
- How to do it:
  - Use the planned structured message path from `docs/context-cleaning-todo.md`.
  - `BuildPromptMessages` remains for LLM prompt construction.
  - Add a separate distillation view builder, e.g. `BuildDistillationMessages(sessionID, taskID)`.
  - Include:
    - final user request
    - final assistant answer
    - compact tool summaries
    - task input/output
  - Exclude:
    - raw tool result blobs
    - repeated reasoning
    - raw command output unless it is the lesson
- Acceptance:
  - Distiller input is deterministic and field-driven.
  - Distiller never embeds raw `tool_result.Content` directly.

### 5. Keep Distiller Focused On Extraction

- What to do:
  - Stop treating `Distiller` as the owner of persistent embeddings.
- How to do it:
  - Change `DistillConversation` to return memory candidates without final storage vectors, or keep vector generation only behind a conflict-check interface.
  - Prefer this split:
    - `DistillConversation` returns `[]MemoryCandidate`.
    - `EmbeddingPipeline` produces vectors for candidates.
    - `ExperienceRepository` stores candidates with vectors.
- Acceptance:
  - There is one final storage vector per stored memory.
  - Conflict detection and storage use the same `EmbeddingSpec`.

## Pipeline Changes

### 6. Introduce `EmbeddingPipeline`

- What to do:
  - Centralize embedding generation and spec metadata.
- How to do it:
  - Add:

```go
type EmbeddingPipeline interface {
    BuildSpec(kind EmbeddingKind, payload any) (EmbeddingSpec, error)
    Embed(ctx context.Context, spec EmbeddingSpec) ([]float64, error)
}
```

  - Internally call the existing `embedding.EmbeddingService`.
  - Only this pipeline should call `Embed` or `EmbedWithPrefix` for memory/retrieval paths.
- Acceptance:
  - `Distiller`, `ProductionMemoryManager`, `RetrievalService`, and `SimpleRetrievalService` do not choose prefixes directly.

### 7. Use The Same Spec For Conflict Detection

- What to do:
  - Conflict detection should compare vectors generated from the final memory storage spec.
- How to do it:
  - Replace direct call:

```go
EmbedWithPrefix(ctx, embeddingText, "memory:")
```

  - with:

```go
spec := pipeline.BuildSpec(memory_experience, candidate)
vector := pipeline.Embed(ctx, spec)
```

  - Pass `spec.Hash`, `spec.Model`, and `spec.Version` through metadata.
- Acceptance:
  - Conflict detection vector equals the vector that will be stored.

### 8. Update `StoreDistilledTask`

- What to do:
  - Store canonical memory experiences, not raw payload string dumps.
- How to do it:
  - In non-production `memoryManager.StoreDistilledTask`:
    - preserve candidate vector/spec when calling `expRepo.Create`
    - extend `distillation.Experience` or add a storage-specific DTO with `Vector`, `EmbeddingSpec`, `TenantID`, and `MemoryType`
  - In `ProductionMemoryManager.StoreDistilledTask`:
    - stop using `fmt.Sprintf("%v", distilled.Payload)`
    - convert `distilled.Payload` into a canonical memory candidate
    - enqueue embedding using `EmbeddingSpec.Text`, not payload dump
- Acceptance:
  - Stored experience input/output are `problem` and `solution`.
  - The queued embedding content is canonical text.

### 9. Update `WriteBuffer` And `EmbeddingQueue`

- What to do:
  - Queue embedding specs, not loose content/model/version triples.
- How to do it:
  - Extend `EmbeddingTask` with:
    - `kind`
    - `prefix`
    - `text_hash`
    - `dim`
  - Generate dedupe key from `EmbeddingSpec.Hash`.
  - Insert pending row with spec metadata.
- Acceptance:
  - Queue idempotency is based on canonical spec hash.
  - Reordered metadata does not create duplicate embedding tasks.

### 10. Update Retrieval Query Embedding

- What to do:
  - Use one query embedding path.
- How to do it:
  - Replace direct calls:
    - `Embed(ctx, query)`
    - `EmbedWithPrefix(ctx, query, "query:")`
    - `EmbedWithPrefix(ctx, query, QueryPrefix)`
  - with:

```go
spec := pipeline.BuildSpec(memory_query, query)
queryVector := pipeline.Embed(ctx, spec)
```

  - Use this in:
    - `memoryManager.SearchSimilarTasks`
    - `ProductionMemoryManager.SearchSimilarTasks` retrieval service path
    - `RetrievalService.getEmbedding`
    - `SimpleRetrievalService.Search`
- Acceptance:
  - All memory experience queries use the same prefix, model, version, and dimension.

## Storage Changes

### 11. Persist Embedding Spec Metadata

- What to do:
  - Make stored vectors auditable and reproducible.
- How to do it:
  - Add or populate fields in `experiences_1024` metadata:
    - `embedding_kind`
    - `embedding_prefix`
    - `embedding_text_hash`
    - `embedding_model`
    - `embedding_version`
    - `embedding_dim`
    - `canonical_text_version`
  - If schema changes are acceptable, prefer explicit columns for `embedding_kind` and `embedding_text_hash`.
- Acceptance:
  - A stored row tells exactly how its vector was generated.
  - Drift can be detected without recomputing text.

### 12. Add Re-Embedding Policy

- What to do:
  - Make model/prefix/canonical format upgrades explicit.
- How to do it:
  - Add a background reconciliation job:
    - find rows where stored `embedding_model/version/dim/text_hash` does not match current spec
    - enqueue re-embedding
    - update vector and metadata atomically
  - Do not silently mix old and new vectors in the same retrieval path unless retrieval filters by compatible spec.
- Acceptance:
  - Model upgrades are observable.
  - Mixed embedding spaces can be isolated or reindexed.

## Tests

### 13. Spec Determinism Tests

- What to test:
  - Same memory candidate produces same canonical text and hash.
  - Reordered metadata does not change hash.
  - Different model/version changes hash.

### 14. No Direct Embedding Calls In Memory Paths

- What to test:
  - Search code calls pipeline, not embedding client directly.
  - Distiller does not call `EmbedWithPrefix` for final storage vectors.

### 15. End-To-End Memory Search Test

- What to test:
  - Distill task.
  - Store memory.
  - Confirm stored row has spec metadata.
  - Search query uses compatible query spec.
  - Result is returned from `experiences_1024`.

### 16. Drift Detection Test

- What to test:
  - Insert an experience with old `embedding_version`.
  - Run reconciliation.
  - Assert a new embedding task is queued.

## Rollout Order

1. Add `EmbeddingKind`, `EmbeddingSpec`, and canonical builders.
2. Add `EmbeddingPipeline` wrapper around existing `EmbeddingService`.
3. Update memory query embedding paths to use pipeline.
4. Update distillation conflict detection to use memory experience specs.
5. Update non-production `StoreDistilledTask` to preserve vector/spec.
6. Update production `StoreDistilledTask` to stop using payload string dumps.
7. Extend `WriteBuffer` and `EmbeddingQueue` to carry spec metadata.
8. Persist spec metadata in `experiences_1024`.
9. Add drift detection/re-embedding job.
10. Add tests for determinism, no direct embedding calls, and E2E search.

## Non-Goals

- Do not redesign the embedding model service.
- Do not remove async embedding from production storage.
- Do not make distillation responsible for database writes.
- Do not use raw prompt text or raw tool result text as embedding input unless a canonical builder explicitly chooses it.

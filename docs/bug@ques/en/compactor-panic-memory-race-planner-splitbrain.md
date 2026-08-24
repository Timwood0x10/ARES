# Compactor Panic on Negative KeepRecent; Memory Config Data Race; Planner Evidence Split-Brain

Date: 2026-08-24
Scope: internal/ares_events, internal/ares_memory, api/tools + internal/tools/planner

## 1. Compactor: negative KeepRecent crashed the process

`NewCompactor` sanitized only `Threshold`. With `KeepRecent < 0` and an empty
stream, `compactStream` computed `candidates := allEvents[:total-KeepRecent]`
→ slice out of range → panic inside the store's errgroup worker (`errgroup.Go`
does not recover) → whole process down. A YAML-provided `CompactionConfig`
reaches this path directly via `ForceCompact`.

Fix: constructor sanitizes `KeepRecent < 0` to defaults; `compactStream`
additionally guards the negative case defensively.

Tests: `TestCompactStream_NegativeKeepRecent_NoPanic` (empty stream, config
bypassing the constructor) and `..._WithEvents`.

## 2. Memory manager: config patches raced hot-path reads

`MemoryPatchExecutor.Apply` mutates `MaxHistory`/`MaxTasks`/`CleanOptions`
under the write lock, but `BuildPromptMessages`, `BuildContext`, the
distillation path and `ProductionMemoryManager` reads (`SessionTTL`,
`MaxHistory`) read those fields with NO lock — a data race whenever an
evolution patch landed during live traffic (`runRetrieval` already did it
correctly; the others were inconsistent).

Fix: snapshot the tuned fields under `RLock` in every hot path;
`snapshotTuning()` helper for ProductionMemoryManager.

Test: `TestMemoryConfigPatch_RacesWithHotPaths` — concurrent patch-writer vs
prompt/context readers, fails under `-race` on the old code.

## 3. Planner evidence store split-brain

`api/tools.NewPlanner` created evidence store A (the scorer queries it);
`api/tools.NewBridge` created a SECOND store B (the bridge writes outcomes
there). Evidence never reached scoring — tool selection could not adapt to
real failures/latency through the public API.

Fix: `Planner.EvidenceStore()` accessor; NewBridge wires the planner's own
store so write and read paths share one instance.

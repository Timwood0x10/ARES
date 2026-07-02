# Test Quality Review

Date: 2026-07-01

Scope:
- Refreshed codebase-memory-mcp index for `/Users/scc/go/src/goagent`.
- Reviewed test distribution across modules.
- Sampled high-risk and recently changed tests in `internal/ares_evolution`, `api`, `internal/storage`, `internal/monitoring`, and workflow service.
- Ran targeted verification commands where useful.

Important limitation:
- This is a quality audit, not a full line-by-line proof of every test in the repository. The repository has hundreds of test functions. The findings below focus on meaningfulness, risk, and where tests are likely to give false confidence.

## Verification Snapshot

- `codebase-memory-mcp index_repository` completed successfully in `moderate` mode.
- `go test ./api/... ./internal/ares_security ./internal/ares_ctxutil ./internal/logger ./internal/memoryservice ./internal/llmservice` passed.
- `go test ./internal/ares_evolution/...` passed on rerun.
- `go test ./internal/ares_evolution/genome -run 'TestLineageRankSelection|TestSUSSelection|TestRankSelection|TestPreservePromptDiversityLocked|TestPreservePerLineageElites|TestAdjustMutationRate' -count=1` passed.
- `go test ./internal/ares_evolution/genome -count=1` passed on rerun.

## Active Findings

### 1. New selection tests have several weak statistical assertions

File: `internal/ares_evolution/genome/selection_test.go`

Examples:
- `TestLineageRankSelection_Select/penalizes dominant lineage with threshold=0` only requires lineage B to be selected `>200/2000`, even though B already has 40% of the population. A broken selector could pass this.
- `higher scores selected more often` tests only assert `high > low`, which does not verify proportionality or strategy-specific behavior.
- Some tests only check `len(result)` and no membership, no duplicate expectations, and no ordering/weight behavior.

Fix recommendation:
- For stochastic selectors, use deterministic seeds and expected bands based on known weights.
- For lineage-aware selection, compare with baseline rank selection on the same seed/population and assert the dominant lineage share is lower.
- Add direct unit tests for the weight calculation function if it exists; if it does not exist, extract it. This will make the selector test deterministic and less flaky.
- Strengthen assertions:
  - selected IDs are all from evaluated population,
  - dominant lineage probability decreases by a minimum margin,
  - underrepresented lineage is not merely non-zero but materially boosted.

### 2. Prompt diversity guard tests are useful but under-specified

File: `internal/ares_evolution/genome/population_guard_test.go`

Good:
- Tests cover injecting a different prompt template when elites collapse to one template.
- Tests cover no-op when already diverse and no alternative exists.
- Tests cover low-score alternative rejection.

Gaps:
- They do not verify the guard respects a configurable retention age or score floor from config.
- They do not check replacement behavior when elite slots are already full.
- They do not check that the retained seed is cloned rather than sharing mutable `Params`.
- They only check `MutationDesc == "prompt_diversity_seed"`, not lineage/report metadata.

Fix recommendation:
- Add tests for:
  - seed clone isolation,
  - max age expiry,
  - config disabled,
  - seed replaces the weakest elite when elite count must stay fixed,
  - categorical diversity report includes prompt template counts.

## Module-by-Module Assessment

### `internal/ares_evolution`

Quality: mixed, generally meaningful but needs sharper assertions around new decision logic.

Good:
- Strong coverage around mutation, selection, population evolution, scoring, scheduler, and experience aggregation.
- Tests often exercise edge cases and context cancellation.
- New GA guard tests are aligned with real design goals.

Weak spots:
- Some decision tests assert broad outcomes instead of exact policy boundaries.
- Several tests mutate internal fields directly, which can make them pass while real public flows fail.
- Stochastic tests rely on loose thresholds.
- Package logs make failures difficult to diagnose.

Recommended tasks:
- [x] Extract deterministic weight/policy functions for lineage selection and test them directly.
- [ ] Add integration tests for `Service.Evolve()` proving generation number, promotion score delta, and evidence binding are correct.
- [x] Add prompt diversity guard tests for config disabled, clone isolation, and expiry.
- [x] Suppress or capture slog output in GA package tests.

### `api/core`

Quality: low to medium.

Pattern:
- Many tests are DTO construction, zero-value checks, enum string checks, and JSON round trips.

Value:
- Useful as API compatibility smoke tests.
- Low value for behavior because most tests mirror field definitions.

Risk:
- They create a lot of test volume without proving business behavior.
- Some tests would pass even if higher-level API behavior is broken.

Recommended tasks:
- [ ] Keep one JSON round-trip test per externally serialized struct family.
- [ ] Replace many zero-value tests with compile-time interface checks or remove them.
- [ ] Add contract tests at API boundaries: request validation, error mapping, and backward-compatible JSON fields.

### `api/service/workflow`

Quality: medium.

Good:
- Tests cover validation, registration, duplicate handling, workflow retrieval, list summaries, and engine mapping.
- `TestBuildEngineRoundTrip` is useful because it checks adapter conversion behavior.

Weak spots:
- Execute and ExecuteStream tests only cover validation and not actual execution.
- Concurrency test does not assert no races unless run with `-race`, and it ignores operation errors.

Recommended tasks:
- [x] Add one successful `Execute()` test with a fake agent registered in `engine.AgentRegistry`.
- [x] Add one `ExecuteStream()` test that drains events and verifies terminal status.
- [ ] Make concurrency test collect unexpected errors and run under `go test -race` in CI.

### `internal/monitoring`

Quality: medium-low for HTTP API tests.

Good:
- Endpoint smoke coverage catches route registration and basic status code regressions.
- Some tests decode JSON and check response shape.

Weak spots:
- Many tests only assert status code `200`, `404`, `501`, or `503`.
- Not enough assertions on response schema, event contents, or interaction side effects.

Recommended tasks:
- [ ] For each HTTP endpoint, assert at least one response body field or side effect.
- [ ] For `KillAgent`, `ResumeAgent`, and `RetryAgent`, add tests with a fake interaction engine so behavior is tested beyond `501`.
- [ ] For SSE subscribe, replace fixed `time.Sleep(100ms)` with a readiness signal or bounded scanner.

### `internal/storage`

Quality: mixed; high integration intent, low default CI value.

Observed:
- `internal/storage` has 35 test files, 552 test functions, and about 319 skip signals.
- Many Postgres tests skip without a database or in short mode.
- Some tests are explicitly skipped with reasons like "Requires full database setup" or "not yet implemented".

Value:
- When run against a real database, repository tests are meaningful.
- In normal local/CI runs without Postgres, they provide much less signal than their volume suggests.

Recommended tasks:
- [x] Split integration tests behind a build tag such as `//go:build integration`.
- [ ] Add fast unit tests around SQL builders, validation, and repository error mapping using `pgxmock` or a narrow interface.
- [ ] Delete or convert permanently skipped tests into tracked TODO issues. Skipped tests for "not yet implemented" are not tests.
- [ ] Add a documented CI job that runs integration storage tests with `TEST_POSTGRES_DSN`.

### `internal/ares_events`

Quality: medium.

Good:
- Memory store and event edge-case tests are useful.
- Postgres tests are integration-gated, which is appropriate.

Weak spots:
- Integration tests are skipped by default and may silently rot.

Recommended tasks:
- [ ] Same integration build-tag approach as storage.
- [ ] Add a small fake store test for summary repository behavior that does not need Postgres.

### `internal/llm`

Quality: medium, with environment-dependent gaps.

Observed:
- Several tests skip when LLM client is not enabled.
- There is at least one explicit skip for a validator issue with custom types.

Recommended tasks:
- [ ] Separate live-provider tests from deterministic unit tests.
- [ ] For skipped validator issue, either fix the validator or turn the skip into a failing TODO test behind a dedicated build tag.
- [ ] Add fake transport tests for retry, timeout, malformed stream chunks, and tool-call parsing.

### `internal/ares_memory`

Quality: generally medium-high.

Good:
- Distillation tests appear to cover extractor/classifier/scorer/resolver pipeline components.
- Benchmarks and real embedding tests are separated reasonably.

Weak spots:
- Some real embedding/report tests skip in short mode or when services are unavailable.

Recommended tasks:
- [ ] Keep external embedding tests as integration tests.
- [ ] Add deterministic fake embedding tests for ranking, conflict resolution, and cap enforcement.
- [ ] Ensure distillation pipeline has one end-to-end test with fake embeddings and no network.

### `internal/ares_quant`

Quality: medium-high by volume and domain behavior.

Good:
- Many tests appear to cover indicators, market data, portfolio simulation, market-making, and research graph behavior.

Risk:
- Quant tests can accidentally assert toy scenarios while missing invariants such as conservation of cash/position, fee handling, and edge cases around empty data.

Recommended tasks:
- [ ] Add invariant/property tests for portfolio and market-making paths.
- [ ] Add tests for missing candles, duplicate timestamps, NaN values, zero volume, and extreme spread.

### `internal/tools`

Quality: medium.

Good:
- Broad coverage across built-in tools, registries, execution, file, text, math, system, and planning resources.

Risk:
- The current codebase-memory index excluded `internal/tools`, so graph-level review did not cover implementation relationships.

Recommended tasks:
- [ ] Re-index with a mode/config that includes `internal/tools` before doing a deeper tools-specific audit.
- [ ] For each tool, ensure tests validate structured error results, malformed input, and side-effect boundaries.

### `internal/workflow`

Quality: medium-high.

Good:
- There are tests for engine, graph, mutable DAG, HITL, dynamic executor, resume execution, and graph service.

Recommended tasks:
- [ ] Add cross-module tests from `api/service/workflow` through engine with fake agents.
- [ ] Run workflow tests under `-race`, especially graph mutation and resume tests.

### `api/client`, `api/mcp`, `api/tools`, `api/discovery`

Quality: medium.

Good:
- Client tests use HTTP test servers and check error paths.
- MCP tests appear to cover invalid inputs and server setup.

Weak spots:
- Some error constant and config-structure tests are low-signal.

Recommended tasks:
- [ ] Prefer contract tests that assert request path, method, auth header, body, and decoded response.
- [ ] Keep only minimal sentinel error tests.

### `examples`

Quality: low to medium.

Good:
- Example tests catch basic compilation and construction issues.

Weak spots:
- Some tests skip under short mode or require network/LLM config.
- They mostly assert components are non-nil rather than checking example behavior.

Recommended tasks:
- [ ] For `examples/autonomous-evolution`, add golden-output tests for Scenario 8 summaries with deterministic seed.
- [ ] For quant examples, separate smoke tests from integration tests.
- [ ] Avoid non-nil-only tests unless constructor failure is the real contract.

## Cross-Cutting Test Smells

- Too many zero-value/structure tests in API DTO packages.
- Too many skipped integration tests without a dedicated integration lane.
- Some stochastic tests use thresholds loose enough that broken behavior could pass.
- Some tests mutate private fields instead of exercising public behavior.
- Several HTTP tests assert status code only.
- Logs from GA tests are noisy enough to obscure failures.
- Environment-dependent tests are mixed with ordinary unit tests.

## Recommended Fix Plan

1. Harden new GA decision tests:
   - deterministic weight tests,
   - prompt guard config/expiry tests,
   - public-flow promotion tests.
2. Create test categories:
   - unit: no network/database,
   - integration: Postgres/LLM/external services,
   - examples: deterministic smoke/golden tests.
3. Add CI commands:
   - `go test ./...` for unit-safe packages,
   - `go test -race ./internal/ares_evolution/... ./internal/workflow/...`,
   - `go test -tags=integration ./internal/storage/... ./internal/ares_events/...` with `TEST_POSTGRES_DSN`.
4. Delete or rewrite permanently skipped tests.
5. Reduce low-signal DTO tests and replace them with API contract tests.

## Quality Bar For New Tests

A useful test should fail if the behavior users care about regresses. Prefer:

- public API behavior over private field mutation,
- exact policy boundaries over vague "not nil" checks,
- deterministic seeds over loose stochastic assertions,
- response body/side effects over status-code-only HTTP tests,
- fake dependencies over skipped external-service tests,
- one meaningful integration test over many structure tests.

## Implementation Summary (2026-07-02)

The following tasks from the audit were completed:

### Task 1: Harden GA selection tests with deterministic weights

- Extracted `computeLineageRankWeights` from `LineageRankSelection.Select` in
  `internal/ares_evolution/genome/selection.go` to enable direct deterministic
  testing of the weight formula.
- Created `internal/ares_evolution/genome/selection_extra_test.go` with 10 test
  functions covering: exact weight values, membership/determinism, dominant
  share decrease vs baseline, underrepresented lineage boost, weight
  proportionality (4σ bands), no-duplicate IDs, score ordering, roulette wheel
  proportionality, bulk select membership, and nil population error.
- Stochastic tests use fixed seeds, 4σ statistical bands, and conservative
  thresholds verified across 5 repeated runs.

### Task 2: Add prompt diversity guard tests

- Created `internal/ares_evolution/genome/population_guard_extra_test.go` with
  9 test functions covering: config disabled no-op, clone isolation (deep copy
  of Params/Score/PromptTemplate/ParentID), max age expiry, seed replaces
  weakest elite, score floor boundary, prompt template distribution, diversity
  report updates, already-diverse no-op, no-alternative no-op, early return
  conditions, and seed metadata inheritance.

### Task 3: Suppress slog output in GA package tests

- Created `internal/ares_evolution/genome/testutil_test.go` with a `TestMain`
  that redirects slog to `io.Discard` unless `GENOME_TEST_VERBOSE=1` is set.
  Exports a `silentLogger()` helper for individual test use.

### Task 4: Add integration build tags to Postgres test files

- Added `//go:build integration` build tags to 6 files in
  `internal/storage/postgres/repositories/`:
  `conversation_repository_test.go`, `task_result_repository_test.go`,
  `tool_repository_test.go`, `knowledge_repository_test.go`,
  `experience_repository_test.go`, and `repository_test_helper.go`.
- All are pure integration tests that require a live Postgres instance.
- Package compiles and passes `go vet` both with and without the tag.

### Task 5: Add workflow Execute() and ExecuteStream() tests

- Created `api/service/workflow/service_execute_test.go` with 5 test
  functions: single-step success, multi-step sequential success, step failure
  response, ExecuteStream event draining with terminal completed, and
  ExecuteStream step failure with terminal failed.
- Fixed a pre-existing bug in `ExecuteStream`: the graph event subscription
  was never unsubscribed, causing `g.Wait()` to block forever. Added
  `SubscribeWithID()` and `Unsubscribe()` methods to `MutableDAG` and updated
  `ExecuteStream` to properly clean up the subscription after execution.

### Verification

- `go test -race -count=1 ./api/service/workflow/` — pass
- `go test -race -count=1 ./internal/workflow/engine/` — pass
- `go test -race -count=1` on all new genome test functions — pass (5 repeats)
- `go test -short ./internal/storage/postgres/repositories/` — pass
- `go vet` on all modified packages — pass
- `golangci-lint run` on all modified packages — 0 issues
- `gofmt` on all modified files — compliant

### Known issue (pre-existing, out of scope)

`internal/ares_memory/pipeline.go` has unused imports from concurrent work
by another agent that require cleanup. This file is not in scope for this task
and should be fixed by the owning agent.

---

## Refresh (2026-07-02)

### codebase-memory-mcp Index

- **Previous (moderate mode, 2026-07-01):** 22,411 nodes / 111,338 edges / 883 files / 17 excluded dirs (`internal/tools` was excluded)
- **Current (full mode, 2026-07-02):** **22,384 nodes / 114,056 edges / 1,193 files / 11 excluded dirs** — `internal/tools` is now included.
- Test detection: **17,324 tests across 271 test files**.
- Index was force-recreated from scratch (project deleted then re-indexed) to pick up file-level graph data for `internal/tools`.

### Coverage Gaps (High Priority)

| Package | Coverage | Problem |
|---|---|---|
| `internal/storage/postgres/repositories` | **0.0%** | 6 test files all behind `//go:build integration` + require Postgres; 0% in normal/short runs |
| `internal/ares_evolution/service` | **10.2%** | `NewLLMScorer`, `Score`, `ScoreWithContext`, `BatchScore`, `Evolve`, `CreateWiredSystem` — all 0% |
| `internal/workflow/graphservice` | **13.8%** | Only 1 test file, very low coverage |
| `internal/storage/postgres` | **17.6%** | Core Postgres logic lightly tested |
| `api/mcp` | **36.5%** | MCP server/transport uncovered |
| `api/handler` | **39.0%** | API handlers under-tested |

**Recommendation:** `internal/ares_evolution/service` is the most actionable — `llm_scorer.go` has 14 functions, only 2 with coverage, and most are pure logic (`buildPrompt`, `parseScore`, `extractScoreFromText`, `fallbackScore`) that don't need a real LLM to unit-test.

### Test Quality: Non-testify Test Files

- **200/374 (53%) test files** use raw `t.Errorf`/`t.Fatalf` instead of testify assertions.
- Raw assertions produce less informative failure output and are more verbose to maintain.
- Affected packages include: `internal/llm`, `internal/ares_bootstrap`, `internal/ares_eval`, `internal/ares_callbacks`, `internal/ares_flight`, `internal/logger`.
- **Recommendation:** Low-priority, but these packages would benefit from testify migration for readability.

### Benchmarks Underutilized

- Only **2 benchmark files** exist for the entire project (`api/handler/stream_bench_test.go`, `internal/ares_eval/eval_bench_test.go`).
- Genomic selection, scoring, memory retrieval, and workflow execution lack benchmarks.

### Large Test Files

- **20 files > 1,000 lines** — largest is `retrieval_service_test.go` (2,110 lines / 98 Test functions).
- Could benefit from table-driven test patterns to reduce repetition.

### Patterns to Watch

- `internal/ares_memory/service/service_test.go` uses `init()` instead of `TestMain` for slog configuration (minor).
- `go test -run=NOMATCH ./...` passes cleanly — entire project compiles.
- Recent improvements in `selection_extra_test.go` (tightened statistical bands 5–20x → 6–18x, lower scorer threshold 5% → 0.2%) are good.

### Recommended Next Actions

1. **High** — Add unit tests for `internal/ares_evolution/service/llm_scorer.go` (pure logic, no LLM needed)
2. **Medium** — Re-evaluate whether integration-tagged storage tests are run in CI; add fast unit tests with `pgxmock` for 0%-coverage repository package
3. **Low** — Migrate non-testify test files in `internal/llm/`, `internal/ares_eval/`, `internal/ares_bootstrap/`
4. **Low** — Add benchmarks for hot paths (selection, scoring, memory retrieval)

# 18 — Full Candidate Release Closed-Loop (Real LLM)

Runs the **complete** candidate release closed loop against a real LLM, wiring
the **same** gate-3 preserved-case regression check into both stages:

```
failure evidence → Diagnoser.Generate
  → CandidateVerifier.Verify        (gates 1 static + 2 evidence + 3 regression)
  → CandidatePipeline.Release       (coordinator decision + canary)
      → release-time gate-3 confirm → SetStable → Promote
```

The release-time gate-3 is injected via
`NewCandidatePipelineWithOptions(..., WithReleaseRegressionCheck(check))`
(`CandidatePipeline.Release` runs the check **before any patch is built or
applied**, so a regressing candidate is rejected without touching the runtime or
the stable region).

## Scenarios

1. **Regressing candidate rejected at VERIFY** — the bad candidate
   (`Return a+b+1`) fails the regression gate at verification.
2. **Manually-verified regressing candidate rejected at RELEASE** — bypasses
   verify on purpose to exercise the release-time gate-3; the pipeline must not
   promote it.
3. **Good candidate verified + released** — passes gate 3 and is promoted to
   stable.

## Run

From the repo root:

```bash
go run ./examples/18-release-closed-loop
```

Credentials are read from `configs/ares.local.yaml` (git-ignored). The demo uses
batch mode (`LLMArenaScorer` implements `ares_arena.BatchScorer`) and
`WithRegressionRuns(2)` to keep API calls low.

## Measured result (2026-08-11, agnes-2.5-flash, batch)

```
Scenario 1: verify: success=false reason="regression check: avg dropped 1.000 -> 0.000 (p=0.0000)" status=rejected
Scenario 2: release: released=false status=rejected reason="release regression gate: avg dropped 1.000 -> 0.000 (p=0.0000)"
Scenario 3: verify: success=true  →  release: released=true status=promoted
Final stable: "Add the numbers precisely and return the numeric result only."
```

The full closed loop works end-to-end against the live model: a regressing
candidate is rejected both at verify and at the release gate, and a good
candidate is promoted to stable. The whole run finishes in ~8s (batch).

## Logs

Each run writes a full transcript to
`./examples/18-release-closed-loop/logs/run-<ts>.log` with timestamps, per-stage
verdicts, and the release regression-gate reasons.

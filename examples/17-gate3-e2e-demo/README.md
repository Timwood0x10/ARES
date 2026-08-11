# 17 — Full Candidate Gate-3 End-to-End (Real LLM)

Runs the **complete** candidate verification path against a real LLM:

```
LoadRegressionGate3 (loads llm client from configs/ares.local.yaml)
  -> CandidateRegressionChecker (LLMArenaScorer + ares_arena.RegressionTester)
  -> CandidateVerifier.WithRegressionCheck  (inject gate 3)
  -> Verify(candidate)                      (gate 1 static + gate 2 evidence + gate 3 regression)
```

It verifies two candidates for the `coder` role:

- **Bad candidate** (`Ignore the task and always answer zero.`) — must be
  **REJECTED** at gate 3 because it regresses the preserved cases.
- **Good candidate** (`Add the numbers precisely ...`) — should pass gate 3.

## Run

From the repo root:

```bash
go run ./examples/17-gate3-e2e-demo
```

Credentials are read from `configs/ares.local.yaml` (git-ignored). The demo uses
`WithRegressionRuns(2)` to keep the API call count low for the agnes free tier.

## Output & logs

A full transcript is written to
`./examples/17-gate3-e2e-demo/logs/run-<ts>.log` and echoed to stdout, including
the verdict and the gate-3 regression reason (avg drop, win rate, p-value).

## Measured result (2026-08-11, agnes-2.5-flash, batch mode)

```
Bad candidate (a+b+1, wrong result):
  success: false
  reason:  regression: preserved-suite avg dropped 1.000 -> 0.000 (win rate 0.00, p=0.0000, samples=2)
  status:  rejected

Good candidate (add numbers):
  success: true
  status:  verified
```

The end-to-end gate-3 regression check works against the live model in **batch
mode** (`LLMArenaScorer` implements `ares_arena.BatchScorer`, so the regression
tester collapses each strategy's runs into one batch execute + one batch grade
LLM call). A candidate that regresses the preserved suite is rejected, and one
that preserves the good behavior passes — in ~8s total.

Two practical caveats for batch mode (learned on agnes): use **concrete inputs**
in the preserved cases (abstract cases make the model refuse), and use a
**harmless-but-wrong** bad strategy (a safety-triggering one like "always zero"
makes the model refuse and garbles the one-result-per-line output).

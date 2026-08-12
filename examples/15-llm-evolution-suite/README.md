# 15 — LLM Evolution Suite (Real LLM, merged 15–18)

Consolidated **real-LLM** evolution examples in one command. The former
standalone examples 15 (scorer smoke), 16 (regression comparison), 17
(gate-3 end-to-end), and 18 (release closed loop) were merged here because they
share the same LLM config, preserved cases, and scenario scaffolding. The
offline, reproducible GA candidate evolution stays separate in
`examples/19-ga-candidate-e2e` (no real LLM needed).

## Scenarios

| Subcommand | What it runs | Former example |
|-----------|--------------|----------------|
| `scorer` | `LLMArenaScorer` smoke: score good vs bad strategy on one preserved case | 15 |
| `regression` | Preserved-case regression comparison (old vs new strategy, t-test) | 16 |
| `gate3` | Candidate gate-3 end-to-end: bad candidate rejected, good verified | 17 |
| `release` | Candidate release closed loop: verify + release-time gate-3, promote to stable | 18 |

## Run

All scenarios make **real API calls** against the LLM configured in
`configs/ares.local.yaml` (git-ignored) and may incur usage cost.

```bash
go run ./examples/15-llm-evolution-suite scorer
go run ./examples/15-llm-evolution-suite regression
go run ./examples/15-llm-evolution-suite gate3
go run ./examples/15-llm-evolution-suite release
```

`scorer` honors `LLM_SMOKE_EXPECT_REGRESSION=1` to fail unless the bad strategy
scores lower.

## Output & logs

A full transcript of each run is written to
`./examples/15-llm-evolution-suite/logs/run-<ts>.log` and echoed to stdout.

## Shared scaffolding

- `configs/ares.local.yaml` — real LLM credentials (provider/model/base_url).
- `preservedCases` — concrete-input preserved cases (abstract cases make the
  model refuse and garble the batch one-result-per-line output).
- `goodStrategy` / `badStrategy` — the harmless-but-wrong bad strategy avoids
  triggering the model's safety refusal.
- `WithRegressionRuns(2)` keeps the API call count low for rate-limited
  providers.

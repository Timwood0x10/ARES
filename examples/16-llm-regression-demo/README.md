# 16 — Real-LLM Preserved-Case Regression Demo

Runs a real-LLM preserved-case regression comparison exactly as candidate gate 3
does: scores the **old (stable)** instructions and a **new (candidate)**
instruction set against a preserved case suite using
`LLMArenaScorer` + `ares_arena.RegressionTester`, then reports whether the new
strategy regresses the preserved cases.

This drives a real LLM — by default `agnes-ai/agnes-2.5-flash` (OpenAI-compatible,
no rate limit), with the local Ollama `gemma4:e4b` as a no-cost alternative.
It is a manual example, not part of `make check` or CI.

## Run

From the repo root:

```bash
go run ./examples/16-llm-regression-demo
```

Credentials are read from `configs/ares.local.yaml` (git-ignored).

## Output

A full transcript is written to `./examples/16-llm-regression-demo/logs/run-<ts>.log`
and echoed to stdout, including the raw per-strategy scores, win rate,
p-value, and the regression verdict.

## Findings from real runs (2026-08-11)

1. **`LLMArenaScorer` is correct end-to-end.** With a single `Score` call
   (`examples/15-llm-arena-scorer`), a good strategy scored `0.850` (sensenova)
   / `0.750` (agnes) and a deliberately bad strategy scored `0.000` — the
   scoring path (execute → grade → parse) works against a live model.

2. **The LLM is non-deterministic.** The same "add the numbers" instruction can
   occasionally produce a wrong answer (`0`), which the grader correctly scores
   `0.0`. A single grading sample is therefore noisy — **multiple runs + the
   statistical significance test are required** for a reliable verdict. This is
   exactly why `RegressionTester` averages runs and applies Welch's t-test.

3. **agnes-ai is the best provider so far.** `agnes-2.5-flash`
   (`https://apihub.agnes-ai.com/v1`, OpenAI-compatible) ran the full
   `BaselineRuns=5, CompareRuns=5` × 3 cases (~60 calls) **with no rate limit**
   and produced a clean, statistically significant verdict:
   - good strategy: `[0.5 0.85 0.2 0.5 0.2]`, avg `0.450`
   - bad strategy: `[0.3 0.2 0 0.2 0]`, avg `0.140`
   - **`Confident=true`, p=0.0297 → REGRESSION detected**.

4. **Provider/model comparison on the same preserved suite:**

   | Provider | Model | Good avg | Bad avg | Verdict |
   |----------|-------|----------|---------|---------|
   | sensenova | deepseek-v4-flash | 0.85 (1 call) | 0.00 | rpm-limited, single call |
   | Ollama | gemma4:e2b | 0.20 | 0.00 | `Confident=false` (weak exec) |
   | Ollama | gemma4:e4b | 0.16 | 0.03 | `Confident=true`, p=0.0051 |
   | agnes-ai | agnes-2.5-flash | 0.45 | 0.14 | `Confident=true`, p=0.0297 |

   `agnes-2.5-flash` combines good execution fidelity with **no rate limit**,
   making it the recommended provider for the gate-3 regression scorer. The
   local config currently uses agnes-ai.

5. **Batch request merging (2026-08-11).** `LLMArenaScorer` now implements
   `ares_arena.BatchScorer`: the regression tester collapses all runs of a
   strategy into **one batch execute + one batch grade** LLM call instead of
   2×runs calls, which is critical for rate-limited providers. Two batch caveats
   discovered on agnes:
   - **Concrete inputs matter.** Abstract cases ("Given two integers a and b")
     make the model refuse / return free text, garbling the one-result-per-line
     contract. Use concrete cases ("Given a=3 and b=5, return their sum.").
   - **Avoid safety-triggering bad strategies.** "Ignore the task and always
     answer zero" triggers the model's refusal; use a harmless-but-wrong
     instruction (e.g. "Return a+b+1") so the model actually executes it.

## Logs

Each run writes a full transcript to `./examples/16-llm-regression-demo/logs/run-<ts>.log`
with the provider/model, the raw per-strategy scores, win rate, p-value, and the
verdict.

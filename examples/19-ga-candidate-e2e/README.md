# 19 — Real GA Evolution Closed Loop (Offline, Reproducible)

Runs a **real multi-generation GA evolution** loop and turns the evolved
champion into a candidate for the evolution pipeline:

```
failure evidence cluster (>= MinFailureClusterSize = 2)
  -> base strategy (stable instructions)
  -> Population (init: size 16, elite 3, mutation 0.25, tournament size 3)
  -> 6 generations of { ScoreAgents (fitness heuristic) → Evolve
       (mutation + crossover + tournament selection + elitism) }
  -> BestStrategy (champion)
  -> Candidate (champion diff) → CandidateVerifier.Verify (gates 1/2)
```

This is the "real GA" counterpart of a deterministic single-shot mutation: it
shows the full Darwinian loop — **population, fitness scoring, selection,
crossover, mutation, elite survival** — with complete per-generation logs
(best/avg/worst fitness, population size, best prompt of the generation).

Unlike 17 and 18, this demo needs **no real LLM**: the mutator, crossover, and
population are all seeded (`seed 42`) and fitness is a deterministic heuristic
over the strategy's prompt template and parameters, so every run converges to
the **same champion** — fully reproducible offline.

## Run

From the repo root:

```bash
go run ./examples/19-ga-candidate-e2e
```

## What it demonstrates

- **Real GA loop**: `Population.ScoreAgents` + `Population.Evolve` over 6
  generations, with `mutation.Mutator` (prompt pool + params) and
  `genome.NewCrossover` driving variation, tournament selection and elitism
  driving survival.
- **GA is a first-class candidate source**: the evolved champion's
  `PromptTemplate` becomes the candidate `Diff`, carrying the failure cluster's
  evidence IDs — the same candidate contract as the human-confirmed path.
- **Same verification pipeline**: the candidate goes through the standard
  `CandidateVerifier` (gates 1/2). Gate 3 (LLM regression) is intentionally not
  attached here — see `examples/17-gate3-e2e-demo` and
  `examples/18-release-closed-loop` for the real-LLM gate-3 path.
- **Determinism**: a second same-seed GA run must converge to the same
  champion, verified at the end of the run.

## Output & logs

A full transcript is written to
`./examples/19-ga-candidate-e2e/logs/run-<ts>.log` and echoed to stdout,
including the population init, every generation's fitness stats and best
prompt, the champion strategy, the candidate verdict, and the reproducibility
check result.

## Wiring shown

```go
mutator, _ := mutation.NewMutator(
    mutation.WithPromptPool(promptPool),
    mutation.WithSeed(42),
)
crosser, _ := ares_genome.NewCrossover(ares_genome.WithSeed(42))
population, _ := ares_genome.NewPopulation(ctx, base, mutator,
    ares_genome.WithPopulationSize(16),
    ares_genome.WithEliteCount(3),
    ares_genome.WithMutationRate(0.25),
    ares_genome.WithSurvivalRate(0.6),
    ares_genome.WithTournamentSelection(3),
    ares_genome.WithPopulationSeed(42),
)
for gen := 1; gen <= 6; gen++ {
    population.ScoreAgents(fitnessScore)
    population.Evolve(ctx, mutator, crosser)
}
champion := population.BestStrategy()
```

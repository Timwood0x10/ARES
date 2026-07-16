# GA Evolution System Deep Dive — When Strategies Learn to Mate

> This is the full introduction to the GA evolution system — not a snippet about a specific bug fix. Subtitle: "From 1 strategy to a population of 20, from 1 operator to 7 selection strategies, 3 crossover types, and 4 mutation types — how GA grew from a toy into a production-grade engine." I'll walk through my experience rewriting the GA system twice, sharing the architectural reasoning along the way.

---

## 1. A Naive Idea: Single-Parent Breeding Is Enough

When I first wrote the GA, I thought it was simple.

The evolution system (DreamCycle) already had a Mutator — mutate a parent into several children, pick the best one, replace the parent. Straightforward:

```
Parent → Mutate → [Child A, Child B, Child C] → Arena PK → Best Child → Replace Parent
```

Keep one optimal solution at a time. Simple and efficient. My argument against having a population was: "What's the point of a population? Only one strategy is deployed at a time — keeping suboptimal ones around just wastes memory."

After a few days of running, the problem surfaced.

First evolution: temperature went from 0.7 to 0.3 (it won). Second evolution: temperature could only mutate from 0.3 onward. What if 0.3 was actually a local optimum? You've already lost the 0.7 allele — you can never get it back.

This is classic **genetic drift** — small population + strong selection pressure = rapid gene pool shrinkage. In biology, once the population drops below a threshold, alleles get lost to random sampling. My system had a population of 1. Allele loss was guaranteed.

So I decided to rewrite — from "single-parent breeding" to "population + mating." Keep a group of survivors, let them mate to produce offspring, and let good genes flow between individuals so they aren't permanently lost due to a single generation's bad luck.

That was **Upgrade One**: introducing Population, Crossover, and Selection.

---

## 2. Core Insight: It's Not About Complexity — It's About Diversity

The trigger for Upgrade Two was more subtle.

After the first upgrade, GA was running. Population of 20, elite preservation, tournament selection, uniform crossover — everything looked normal. But after hundreds of generations, I noticed another problem:

The population wasn't losing genes anymore, but it was converging too fast.

Gen 1-5: diversity dropped from 35% to 12%. Gen 10+: stable around 8%. All individuals looked the same — parameters converged, prompts converged, tool selection converged. Evolution had become local fine-tuning.

This isn't a GA bug; it's GA's nature: **the stronger the selection pressure, the faster the convergence.** But fast convergence isn't necessarily good — that convergence point might just be a local optimum.

My first reaction was to tweak parameters: increase mutation rate, lower survival rate, increase elite count — but results were limited. Until I realized the problem wasn't in the parameters, it was in the **mechanisms**:

- **Selection operators**: only tournament. Different scenarios need different selection pressures.
- **Crossover methods**: only uniform. Sometimes you need to preserve gene blocks (two-point), sometimes you need large segment swaps (segment).
- **No diversity preservation**: no fitness sharing, no crowding distance — none of the classic mechanisms.

So **Upgrade Two** wasn't about adding more config knobs. It was about building a **pluggable operator architecture** where evolution strategies can be composed per scenario.

The current GA engine has **7 selection operators, 3 crossover types (with 3 prompt inheritance modes), 4 mutation types (with adaptive distribution), and multi-objective NSGA-II optimization** — all swappable strategies, not just configuration parameters.

---

## 3. System Architecture Overview

The GA evolution system has three layers with clear boundaries:

```
┌─────────────────────────────────────────────────────────────────────┐
│                     api/evolution/ (Public API)                      │
│    Population, DreamCycle, Mutator, Promoter interfaces + adapters   │
│    External modules and AI assistants must NOT import internal/       │
└──────────────────────┬──────────────────────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────────────────────┐
│              internal/ares_evolution/ (Core GA Engine)               │
│                                                                      │
│  ┌──────────┐   ┌──────────────┐   ┌────────────┐   ┌───────────┐  │
│  │ mutation │──▶│ genome       │──▶│ scheduler  │──▶│ experience │  │
│  │ .Strategy │   │ .Population  │   │ .Scheduler  │   │ .Evidence  │  │
│  │ .Mutator  │   │ .Selection[] │   │ .DreamCycle │   │ .Store     │  │
│  │ .Types    │   │ .Crossover[] │   │             │   │ .Guidance  │  │
│  └──────────┘   └──────────────┘   └────────────┘   └───────────┘  │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │  WiredEvolutionSystem (Factory + Adapter)                       │  │
│  │  Wires Scheduler + DreamCycle + Population + Scoring + Genealogy│  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────┬──────────────────────────────────────────────┘
                       │ Phase 6 bridge
┌──────────────────────▼──────────────────────────────────────────────┐
│              internal/evolution/ (Runtime Evolution Engine)          │
│                                                                      │
│  genome/Registry  ──▶  diff/Registry  ──▶  coordinator/Coordinator  │
│  (Genome interface)    (Differ interface)    (Patch decisions)       │
│  MemoryGenome          DiffAll()             Apply/Reject/Delay     │
│  PlannerGenome                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

**Design principle: the API layer exposes only interfaces, never implementations.** External consumers interact with GA through `api/evolution.Population` — they don't need to know there are 7 selection operators inside. AI assistants are only allowed to reference packages under `api/evolution`, never `internal/`.

---

## 4. Population: The Skeleton

`internal/ares_evolution/genome/population.go` defines the core data structure:

```go
type Population struct {
    Agents     []*mutation.Strategy  // current individuals
    Size       int                   // target size (constant across generations)
    Generation int                   // current generation
    cfg        PopulationConfig      // configuration snapshot
    rng        *rand.Rand            // deterministic random source

    bestScore  float64               // all-time high score
    bestEver   *mutation.Strategy    // all-time best individual
    paretoFront []*mutation.Strategy // NSGA-II Pareto front
    stagnantGens int                 // stagnation counter
    currentMutationRate float64      // adaptive mutation rate
}
```

Key design decisions:

**Read-write lock for concurrency**: `Best()` and `Stats()` use a read lock; `doEvolve()` uses a write lock. The evolution frequency is much lower than query frequency, so read-write separation is appropriate.

**Configuration is an immutable snapshot**: all options are set once in `NewPopulation()` and can't be changed afterward. Evolution parameters shouldn't be tampered with mid-run.

**Deterministic random source**: seeded with `time.Now().UnixNano()`. The comment marks it `#nosec G404` — GA doesn't need cryptographically secure random numbers. Fixed seeds allow experiment reproducibility.

### Default Configuration

```go
func DefaultPopulationConfig() PopulationConfig {
    return PopulationConfig{
        Size:              20,       // population size
        EliteCount:        3,        // elite count
        MutationRate:      0.2,      // mutation rate
        SurvivalRate:      0.6,      // survival rate per generation
        SelectionStrategy: "tournament",
        BreedingPoolRatio: 0.3,      // breeding pool ratio
    }
}
```

20 is an empirical value in the GA field — too small (<10) causes genetic drift, too large (>50) converges too slowly. Combined with a 0.6 survival rate, 12 individuals survive per generation and 8 new offspring are created.

### Evolution Pipeline

```go
func (p *Population) doEvolve(ctx context.Context, mut MutatorInterface, cross CrossoverInterface) error {
    // 1. Sort (descending by Score)
    // 2. Select (e.g., tournament: shuffle → pick k → best)
    // 3. Preserve elites
    // 4. Crossover (uniform/two-point/segment + prompt inheritance mode)
    // 5. Mutate (at mutation rate)
    // 6. Assemble new population
    // 7. Increment generation
}
```

Additional mechanisms:

- **Steady-state GA** (`EvolveSteadyState`): replaces only a portion of the population per generation (controlled by `replaceRate`), suitable for online production. `replaceRate` is clamped to [0.1, 0.5] to prevent population oscillation.
- **Stagnation recovery**: when Best stays unchanged for consecutive generations, automatically increases mutation rate, injects fresh mutants, and evicts over-aged agents.
- **Fitness sharing**: penalizes individuals in crowded regions via `SelectionScore`, Sigma=0.3, NicheRadius=0.15. Elite individuals are exempt.

---

## 5. Selection Operators: 7 Strategies, Each With Its Domain

`internal/ares_evolution/genome/selection.go` (876 lines) implements 7 selection strategies. This was the core deliverable of Upgrade Two — not "adding a couple of options," but building a complete strategy enum + factory architecture.

| Operator | Mechanism | Selection Pressure | When to Use |
|----------|-----------|-------------------|-------------|
| **Tournament** | Fisher-Yates shuffle → pick k random → best of k | Medium (higher k = higher pressure) | **Default recommendation.** Balances diversity and convergence. k=3 is empirical. |
| **Rank** | Linear rank weighting, best=N, worst=1 | Low | Early exploration stage, don't want premature convergence. Insensitive to outliers. |
| **SUS** (Stochastic Universal Sampling) | Uniformly spaced sampling, N pointers evenly distributed | Medium | When minimizing sampling bias. Lower variance than roulette wheel (no individual selected repeatedly). |
| **RouletteWheel** | Proportional selection, probability proportional to score | High | When large score gaps exist. Amplifies advantages quickly. But prone to premature convergence. |
| **Truncation** | Keep only top N% | Highest | Deterministic scenarios where top region is known to be good. But worst diversity. |
| **LineageRank** | Lineage diversity penalty, `penaltyThreshold` + `penaltyStrength` | Adaptive | Wired mode's signature operator. Penalizes same-lineage individuals, encourages exploring different bloodlines. |
| **NondominatedSorting** | NSGA-II non-dominated sort + crowding distance | Multi-objective | **Dedicated to multi-objective optimization.** See Section 7. |

### Why Tournament Selection Is the Default

The implementation is remarkably concise:

```go
func (s *Selection) TournamentSelection(population []*mutation.Strategy, numToSelect int) ([]*mutation.Strategy, error) {
    shuffled := make([]*mutation.Strategy, len(population))
    copy(shuffled, population)
    s.rng.Shuffle(len(shuffled), func(i, j int) {
        shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
    })

    selected := make([]*mutation.Strategy, 0, numToSelect)
    for len(selected) < numToSelect {
        best := shuffled[0]
        tournamentSize := s.tournamentSize
        for i := 1; i < tournamentSize && i < len(shuffled); i++ {
            if shuffled[i].Score > best.Score {
                best = shuffled[i]
            }
        }
        selected = append(selected, best)
    }
    return selected, nil
}
```

Why tournament over more sophisticated methods? Three reasons:

1. **Computation is simple**: O(n·k), where k is the tournament size. No sorting, no global comparisons.
2. **Parallel-friendly**: each tournament round is independent, naturally suited for concurrent execution.
3. **Controllable selection pressure**: larger k means higher probability of picking high-score individuals. k=3 is moderate pressure; k=10 is equivalent to truncation selection.

### LineageRank: Wired Mode's Signature Operator

This is Wired mode's exclusive selection strategy. Core idea: **if two individuals share the same lineage, penalize them to reduce the probability of both being selected simultaneously.**

```go
penalty := 1.0 - (lineageSimilarity * penaltyStrength)
effectiveScore := score * penalty
```

This maintains population diversity — in Wired mode, each thread independently evolves its own lineage. If one lineage becomes particularly dominant (producing many high-scoring individuals), it suppresses other lineages' exploration space. LineageRank balances selection pressure by penalizing same-lineage individuals.

---

## 6. Crossover and Mutation: Engineering Genetic Recombination

### Three Crossover Types

GA crossover operators determine "how offspring inherit genes from parents":

```go
CrossoverUniform   // Each parameter independently inherits from either parent (50/50)
CrossoverTwoPoint  // Two cut points, swap the middle segment
CrossoverSegment   // Take a contiguous block from parent B, rest from parent A
```

**Uniform** is the default because parameters don't have a natural order (temperature and top_k are independent). Uniform random inheritance is the most sensible choice.

**TwoPoint** is suitable when parameters have implicit ordering. For example, `[search_depth, temperature, top_k, batch_size]` — if search_depth and batch_size are both performance-related, two-point crossover can preserve this "performance block" while swapping the "behavior block" (temperature, top_k).

**Segment** is suitable when certain parameters are known to have dependencies. For instance, `tool_selector` and `batch_size` may interact in real-world scenarios — segment crossover swaps them as a block.

### Prompt Inheritance: Three Modes

Prompt crossover is more nuanced than parameter crossover — it's not structured data, it's natural language. Three inheritance modes:

```go
PromptInherit    // Inherit complete prompt from higher-scoring parent (conservative)
PromptHalfSplit  // rune-aware front half / back half split (Chinese-capable!)
PromptUniform    // Randomly from either parent (highest diversity)
```

**Why does PromptHalfSplit need to be rune-aware?** In Chinese scenarios, byte-level splitting cuts a character in half. `PromptHalfSplit` uses `utf8.RuneCountInString` to calculate length, ensuring split points land on character boundaries.

### Four Mutation Types

```go
MutationParameter  // Parameter value mutation (temperature, top_k, etc.)
MutationPrompt     // PromptTemplate mutation
MutationTool       // Tool configuration mutation
MutationCrossover  // Crossover-produced strategy (marks origin)
MutationRoot       // Initial strategy
```

The core of mutation operators is **AdaptiveDistribution** — it dynamically adjusts mutation type probabilities based on historical results:

```go
type AdaptiveDistribution struct {
    params  map[MutationType]float64   // current probabilities
    history map[MutationType][]float64 // historical success rates
    window  int                        // sliding window size
}
```

If Prompt mutations have been producing higher average scores in recent generations, the `MutationPrompt` probability auto-increases. Conversely, if Parameter mutations have been underperforming, their weight gradually decreases.

### Mutator in Code

Exposed to external consumers via `api/evolution/mutation` sub-package:

```go
mutator, err := pubmutation.NewMutator(pubmutation.MutatorConfig{
    ParamRanges: map[string][]any{
        "temperature":   {0.1, 0.3, 0.5, 0.7, 0.9},
        "top_k":         {10, 20, 40, 60, 80, 100},
        "max_tokens":    {1024, 2048, 4096, 8192},
        "tool_selector": {"auto", "manual", "priority"},
        "search_depth":  {1, 2, 3, 4, 5},
        "batch_size":    {1, 3, 5, 10},
    },
    PromptPool: []string{
        "You are a helpful assistant. Complete the task efficiently.",
        "You are an expert programmer. Write clean, efficient code.",
        "You are a data analyst. Analyze data thoroughly and report findings.",
        "You are a system architect. Design robust and scalable solutions.",
    },
    ToolPool:           []string{"search", "read", "write", "exec"},
    ParamMutationProb:  0.4,
    PromptMutationProb: 0.2,
})
```

---

## 7. Multi-Objective Optimization: There's No Single "Best"

The most practical feature added in Upgrade Two — **NSGA-II multi-objective optimization**.

### The Problem

GA's default mode is single-objective maximization: higher Score = better. But in real-world scenarios, strategy quality isn't unidimensional:

- **Higher success rate** is better, but may be more expensive
- **Higher output quality** is better, but may be slower
- **Lower cost** is better, but may sacrifice quality
- **Lower latency** is better, but may limit complex processing

Single-objective optimization requires manually weighting these dimensions into one score — but how do you determine the weights? Different task types may need different weights.

### The NSGA-II Solution

`internal/ares_evolution/genome/multi_objective.go` implements non-dominated sorting + crowding distance:

```go
// Pareto dominance: a strictly dominates b in >=1 dimensions, and is no worse
// in any dimension
func ParetoDominance(a, b *mutation.Strategy) bool {
    betterInAny := false
    for _, dim := range DimensionOrder {
        aVal := a.DimensionScores[dim]
        bVal := b.DimensionScores[dim]
        dir := DimensionDirection[dim] // maximize / minimize
        if dir == Maximize && aVal > bVal { betterInAny = true }
        if dir == Maximize && aVal < bVal { return false }
        if dir == Minimize && aVal < bVal { betterInAny = true }
        if dir == Minimize && aVal > bVal { return false }
    }
    return betterInAny
}
```

The four optimization dimensions with default weights:

| Dimension | Direction | Weight | Meaning |
|-----------|-----------|--------|---------|
| `success_rate` | maximize | 0.40 | higher success rate is better |
| `quality` | maximize | 0.25 | higher output quality is better |
| `cost` | minimize | 0.20 | lower API cost is better |
| `latency` | minimize | 0.15 | lower response time is better |

The selection pipeline: **non-dominated sort → assign Pareto rank (0=best front) → sort within front by crowding distance → boundary points get infinite distance for guaranteed preservation**

Activate by passing `"nsga2"` or `"nondominated"` as the selection strategy string. Weights are only used when a single scalar score is needed for reporting; the selection process itself is based on the full Pareto ranking and is weight-agnostic.

### Real-World Experience

The biggest difference between running NSGA-II and single-objective GA: **there's no longer such a thing as "Best Score."** Each individual has scores across multiple dimensions — what you get is a Pareto front. All strategies on the front are "optimal" — just optimal in different dimensions.

This might be disastrous for product managers ("which one should I use?"), but it's honest engineering — real-world strategy quality is inherently multi-dimensional. Force-compressing it into one dimension is just hiding complexity.

---

## 8. WiredEvolutionSystem: The Central Factory

Upgrades One and Two completed the pluggable operator architecture. But one engineering problem remained: **who wires all these components together?**

Population, Scheduler, DreamCycle, Genealogy, StrategyStore, Scorer, Experience, Diff engine, Coordinator — 10+ components, each with its own lifecycle and dependencies. If every GA usage required manually instantiating all these, nobody would use it.

So `NewWiredEvolutionSystem` was born — a central factory function:

```go
type WiredEvolutionSystem struct {
    Scheduler     *EvolutionScheduler
    DreamCycle    *DreamCycle
    PopAdapter    *GenomePopulationAdapter
    Population    *genome.Population
    Genealogy     *PopulationGenealogyRecorder
    StrategyStore StrategyStore
    ActiveStrategyManager *ActiveStrategyManager
    ShadowEvaluator       *ShadowEvaluator
    FeedbackRecorder      *FeedbackRecorder
    TieredScorer          *scoring.TieredScorer
    ScoreCache            *scoring.ScoreCache
    Metrics               *ares_observability.PrometheusMetrics
    Reflector             *genome.LLMReflector
    HypothesisGen         *genome.HypothesisGenerator
    MetaCtrl              *genome.MetaController
    DiffReg               *diff.Registry
    Coordinator           *coordinator.EvolutionCoordinator
    GenomeReg             *evogenome.Registry
    AfterGeneration       func(ctx, gen int, system) error  // post-generation hook
    AfterRun              func(ctx, system) error            // post-run hook
}
```

The `RunIdleEvolution` method runs a complete N-generation evolution loop:

```
Scoring → Evolution → Genealogy Recording → LLM Reflection →
Meta Control → Diff Engine → Coordinator → Post-Generation Hooks
```

The scoring pipeline is layered, assembled by `GenomePopulationAdapter`:

```
1. ScoreCache hit check (avoid repeated scoring)
2. TieredScorer → LLM budget gating
3. Heuristic fallback (when LLM is unavailable)
4. MemoryAwareScorer (adjust scores based on experiential evidence)
5. BatchScorer batch pre-fill
```

The benefit of this design: **each layer has a distinct concern with low coupling.** ScoreCache only handles caching, not scoring logic; MemoryAwareScorer only handles experience adjustment, not how LLMs are called. Modifying one layer doesn't affect the others.

---

## 9. Experience System: From Data to Evolution Guidance

GA mutations need direction, otherwise it's just random search. The experience system converts historical observation data into evolution guidance signals.

Pipeline:

```
ToolCallRecord → ToolCallExperienceCollector → Normalizer → MemoryExperienceStore → AggregateEvidence → EvolutionHint
```

### ToolCallRecord

Captures detailed metrics for each tool invocation:

```go
type ToolCallRecord struct {
    StrategyID      string
    TaskType        string
    ToolName        string
    LatencyMs       int
    Success         bool
    RetryCount      int
    ResultSizeBytes int
    ErrorCode       string
    CalledAt        time.Time
}
```

### Confidence Calculation

Experience quality depends on sample size:

- Samples < 10: `confidence = count/10 * 0.5` (max 0.5)
- Samples 10-1000: `confidence = 0.5 + (count-10)/(1000-10) * 0.5`
- Samples >= 1000: `confidence = 1.0`

Only experiences with confidence >= 0.7 are used to guide evolution.

### EvolutionHint

From experience to evolution guidance:

```go
type EvolutionHint struct {
    TaskType   string
    Problem    string
    Solution   string
    ToolHint   string
    ParamHints map[string]any
    PromptHint string
    Confidence float64
}
```

```go
evHint := guidance.HintsForTask("data")
// returns: {"tool": "calculate", "confidence": 0.91}
// effect: GA boosts calculate tool's weight by +0.91 during tool mutations
```

In practice, if a strategy consistently fails on a specific task type (e.g., database schema migrations), the experience system captures the failure pattern → normalizes it into a hint ("DDL operations should use explicit transaction blocks") → provides it to GA's mutation operators → future mutations avoid known failure patterns.

---

## 10. The Two Upgrades Story

Let's review what each upgrade accomplished.

### Upgrade One: From "Single Parent" to "Population"

| Aspect | Before | After | Why |
|--------|--------|-------|-----|
| Individual count | 1 | 20 | Prevent genetic drift |
| Mating | None (mutation only) | Crossover + mutation | Gene flow |
| Selection | None (sort only) | Tournament/Rank/Truncation | Controllable selection pressure |
| Deployment | Direct replacement | Lineage recording + strategy store | Traceability |

### Upgrade Two: From "Single Operator" to "Pluggable Operator Architecture"

| Aspect | Before | After | Why |
|--------|--------|-------|-----|
| Selection operators | Only tournament | 7 types | Different scenarios need different pressures |
| Crossover types | Only uniform | 3 types + 3 prompt modes | Parameter structures differ |
| Mutation types | Only parameter | 4 types + adaptive distribution | Mutation types need dynamic adjustment |
| Optimization objective | Single-objective | Multi-objective NSGA-II | Real-world tradeoffs are multi-dimensional |
| Scoring | Hardcoded | Layered pipeline | Cache/LLM/heuristics decoupled |
| Runtime scope | Strategy params only | Memory/Planner genomes | GA isn't just for strategy tuning |

---

## 11. Public API: A Safety Boundary for AI Assistants

The GA system's public API lives under `api/evolution/`:

```go
// Population — GA core
type Population interface {
    Agents() []Agent
    Size() int
    CurrentGeneration() int
    BestScore() float64
    BestStrategy() *Strategy
    ScoreAgents(scorer ScorerFunc)
    Evolve(ctx context.Context) error
}

// Mutator — mutation operator (in mutation sub-package)
// Configured via MutatorConfig: param ranges, prompt pool, tool pool

// Promoter — promotion/demotion system
type Promoter interface {
    Evaluate(ctx context.Context, strategyID string, successRate, confidence float64) (string, error)
    Promote(ctx context.Context, strategyID string) error
    Demote(ctx context.Context, strategyID string) error
}

// DreamCycle — evolution scheduler wrapper
type DreamCycle interface {
    Run(ctx context.Context, data CallbackData) error
    SetEnabled(enabled bool)
    IsEnabled() bool
    TaskCount() int64
}
```

The complete usage flow is in `examples/10-ga-full-evolution/main.go`, demonstrating:

1. **Tool selection strategy evolution**: 6 parameter ranges (temperature, top_k, max_tokens, tool_selector, search_depth, batch_size)
2. **Memory-guided mutation**: historical experience biases scoring (`hintProvider.confidenceForStrategy`)
3. **Multi-objective fitness**: `quality*0.6 - cost*0.25 - latency*0.15`
4. **5 generations of evolution**: tournament selection, population size 20, elite count 3
5. **Promoter evaluation**: champion decisions (promote/demote)

Core flow code (~60 lines of actual logic after stripping mock and display logic):

```go
// 1. Create base strategy
base := &pubmutation.Strategy{
    ID: "root", Params: map[string]any{
        "temperature": 0.7, "top_k": 40, "max_tokens": 4096,
        "tool_selector": "auto", "search_depth": 3, "batch_size": 5,
    },
}

// 2. Create mutator
mutator, _ := pubmutation.NewMutator(cfg) // configure param ranges + prompt pool + tool pool

// 3. Create population
population, _ := pubevolution.NewPopulation(base, popCfg) // 20 individuals, tournament selection

// 4. Evolution loop
for gen := 0; gen < 5; gen++ {
    population.ScoreAgents(multiObjectiveScorer) // multi-objective scoring
    population.Evolve(ctx)                       // one generation of evolution
}

// 5. Evaluate champion
promoter.Evaluate(ctx, best.ID, bestScore, confidence)
```

---

## 12. Phase 6: Runtime Evolution Engine

The third upgrade (technically Phase 6's bridge) extends GA to the runtime component level.

Beyond tuning strategy parameters, GA can now evolve:

- **MemoryGenome**: memory parameters (MaxHistory [3,50], MaxSessions [20,500], MaxDistilledTasks [500,20000], UseStructuredCleaning)
- **PlannerGenome**: planning parameters (Strategy "balanced"/"architecture-first"/"memory-first", MaxSources [3,30], MinRelevance [0.1,0.9])

The diff engine converts genome differences into deployable patches:

```go
Genome (old) ──┐
                ├──→ Diff Engine ──→ []RuntimePatch
Genome (new) ──┘
```

The coordinator decides the fate of patches:

- Fitness >= 60.0 → Apply (deploy immediately)
- Fitness < 30.0 → Reject (discard)
- Between → Delay (wait for more evidence)

---

## 13. Final Thoughts

The GA evolution system has come a long way — from "one strategy mutating over and over" to "7 selections × 3 crossovers × 4 mutations × multi-objective optimization." My biggest takeaway isn't technical — all the technical pieces are established algorithms.

The biggest takeaway is: **a flexible pluggable architecture matters far more than "guessing which config is best."**

During Upgrade One, I firmly believed Tournament Selection was the best selection strategy. By Upgrade Two, I realized different strategies excel in different scenarios. If I had hardcoded tournament selection, the current GA engine wouldn't be adaptable to diverse evolution scenarios.

This reflects a design philosophy that runs throughout the ares system: **don't make choices for the user — give them the tools to make choices.**

You won't find a "best configuration" preset in the GA engine. You'll find 7 selection strategies, 3 crossover types, customizable parameter ranges, and pluggable scoring functions — combined, they can handle nearly any evolution scenario from "fast convergence" to "broad exploration" to "multi-objective tradeoffs."

GA is no longer a "parameter tuning tool." It's a **strategy generator** — one that discovers parameter combinations humans would never think of, continuously adapts to changing task distributions, and optimizes its own mutation direction based on historical experience.

---

## Appendix

[A] Code locations covered in this article:

| Module | Path |
|--------|------|
| Public API | `api/evolution/evolution.go`, `api/evolution/mutation/mutator.go` |
| Core engine | `internal/ares_evolution/genome/population.go` |
| Selection operators | `internal/ares_evolution/genome/selection.go` |
| Crossover operators | `internal/ares_evolution/genome/crossover.go` |
| Multi-objective optimization | `internal/ares_evolution/genome/multi_objective.go` |
| Mutation operators | `internal/ares_evolution/mutation/mutator.go` |
| Wired system | `internal/ares_evolution/genome_wiring_system.go` |
| Wired adapter | `internal/ares_evolution/genome_wiring.go` |
| Experience system | `internal/ares_evolution/experience/` |
| Scheduler | `internal/ares_evolution/scheduler.go` |
| Runtime evolution | `internal/evolution/genome/`, `internal/evolution/diff/`, `internal/evolution/coordinator/` |
| Full example | `examples/10-ga-full-evolution/main.go` |

[B] To try GA evolution manually:

```bash
go run examples/10-ga-full-evolution/main.go
```

[C] Topics not covered in this article:
- TieredScorer's LLM budget management (might be covered in a separate scorer article)
- Promoter system's champion/challenger details (already in the promoter sub-system)
- Genealogy recording and family tree implementation (can be expanded in a genealogy article)
- Benchmark performance comparison for each selection operator (could be a standalone evaluation report)

If these topics interest you, they can be covered in follow-up articles.
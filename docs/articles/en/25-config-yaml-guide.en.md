# ARES config.yaml Guide (English) (0.3.x)

> Version: 0.2.9 · Last updated: 2026-08-05
> 中文版见 [Chinese Version](./25-config-yaml-guide.zh.md)

This guide explains how to write `ares.yaml` (or any `<name>.yaml`) to configure the ARES Runtime.
Configuration is **YAML + strongly typed validation**: every field has a sensible default — set only what you need to override (zero-value philosophy).

**Minimal working config** (LLM only is enough to start):

```yaml
llm:
  provider: ollama        # ollama | openai | anthropic | openrouter
  model: llama3.2
  api_key: ""             # empty for local providers; required for cloud (or via env var)
  base_url: ""            # custom endpoint (optional)
```

**Bootstrap options** (pick one):

```go
rt := sdk.NewRuntime(sdk.WithConfig("ares.yaml"))    // assemble from file (recommended)
// or
cfg, _ := sdk.LoadConfigFile("ares.yaml")             // load config
opts, _ := cfg.ToOptions()                            // config → Options
rt := sdk.NewRuntime(opts...)
// or pure code: sdk.NewRuntime(sdk.WithOllama("llama3.2"), sdk.WithDefaultMemory())
```

---

## 1. LLM (Model Provider)

```yaml
llm:
  provider: openai                 # required: ollama | openai | anthropic | openrouter
  model: gpt-4o-mini               # model name
  api_key: ""                      # API key (or env var such as OPENAI_API_KEY)
  base_url: ""                     # custom base URL (proxy / private deployment)
  timeout: 30                      # request timeout in seconds, 0 = default 30s
  max_prompt_length: 8192          # max prompt chars, 0 = default 8192
```

| provider | Notes | Local default model |
|---|---|---|
| `ollama` | Local Ollama | `llama3.2` |
| `openai` | OpenAI / compatible | `gpt-4o-mini` |
| `anthropic` | Claude | `claude-sonnet-4-5` |
| `openrouter` | OpenRouter aggregation | set `model` explicitly |

---

## 2. Distillation (Memory Distillation)

Distillation turns task experiences into long-term memory and needs an embedding client
(for vector recall). Lives under the `memory` block.

```yaml
memory:
  enabled: true                    # enable memory system (prerequisite for distillation)
  max_history: 10                  # closed-loop context turns, 0 = default 10
  enable_distillation: true        # enable task distillation (since v0.2.4)
  distillation_threshold: 3        # fire distillation every N rounds, 0 = default 3
  enable_rag: false                # retrieval-augmented generation (opt-in)
  rag_top_k: 5                     # snippets injected by RAG, 0 = default 5
  session:
    max_history: 20                # session store window (independent of max_history)
    ttl_seconds: 3600              # session expiry
  user_profile:
    enabled: true                  # long-term user profile
  task_distillation:
    provider: llm                  # distillation mode: llm | rule
    max_tasks: 100                 # max distilled tasks
```

> Persistence: configure the `database` block to persist distillation results and evidence
> to PostgreSQL (see below).

---

## 3. Genetic Evolution (GA)

GA evolves strategies (prompts, params, scheduling, knowledge retrieval, …) via
mutation/crossover/selection. Must be explicitly enabled.
Genomes: workflow / scheduler / knowledge / recovery / memory / prompt.

```yaml
evolution:
  enabled: true                    # required: default false — whole pipeline skipped
  population_size: 20              # agents per generation; larger = more diverse, slower
  elite_count: 2                   # top agents preserved unchanged per generation
  survival_rate: 0.6               # survival fraction [0,1]; higher = more diversity
  mutation_rate: 0.2               # base gene mutation probability
  min_mutation_rate: 0.05          # floor for adaptive mutation decay
  max_mutation_rate: 0.5           # ceiling for adaptive mutation bursts
  generations: 15                  # max generations, 0 = unlimited
  breeding_pool_ratio: 0.5         # fraction used as crossover parents
  min_interval: "5m"               # min interval between scheduler runs (Go duration)
  selection_strategy: "tournament" # tournament | roulette | rank
  tournament_size: 3               # tournament selection size
  crossover_type: "uniform"        # crossover scheme
  target_fitness: 0                # stop early when reached; 0 = disabled
  steady_state: false              # steady-state evolution (replace, not whole generation)
  steady_state_replace_rate: 0.3   # steady-state replacement rate
```

> Optional LLM-backed scorer: when enabled, GA scores strategies via LLM calls
> (instead of the constant baseline 50.0). Requires a working LLM client.

**Knowledge retrieval params** (`knowledge` block):

```yaml
knowledge:
  retrieval_enabled: true          # enable AKG retrieval, default false
  top_k: 5                         # max snippets per retrieval, 0 = default 5
  min_score: 0.4                   # min similarity threshold, 0 = default 0.4
```

> The GA knowledge-genome params (`max_results` / `reducer_strategy` / `planner_strategy` /
> `summarizer_type`) are genome-internal and are NOT exposed directly via the
> `knowledge` block of `config.yaml`.

---

## 4. Chaos (Fault Injection / Resilience)

> **Note**: Chaos fault injection has **no dedicated YAML block** — it is injected via
> runtime APIs (`manager.SetChaosConfig` / `InjectChaos`, …) or driven by the arena
> fault-injection scenarios, not by the config file. The resilience-related switch at
> the config level is:

```yaml
# Resilience / fault-tolerance related (at config or runtime level)
chaos:
  enabled: false                   # set true if your build exposes this switch
  # individual injections (delay/timeout/partition/crash) are runtime-driven, not YAML
```

For a hands-on example see `examples/06-chaos-resilience/`.

---

## 5. Other Blocks

```yaml
database:                          # PostgreSQL (distillation persistence / evidence persistence)
  host: localhost
  port: 5432
  user: ares
  password: ""
  database: ares
  ssl_mode: disable                # empty = disable (local dev)

embedding:                         # Embedding service (needed by distillation/RAG)
  service_url: "http://localhost:8000"
  dimension: 1536                  # vector dimension, 0 = model default

tools:
  builtin: true                    # enable built-in toolset
  mcp:
    - null-server                  # enabled MCP server names (must be registered separately)

reflection:
  enabled: false                   # self-reflection toggle
```

---

## Full Example (all features)

```yaml
llm:
  provider: ollama
  model: llama3.2

memory:
  enabled: true
  enable_distillation: true
  distillation_threshold: 3
  enable_rag: true
  rag_top_k: 5

knowledge:
  retrieval_enabled: true
  top_k: 5
  min_score: 0.4

evolution:
  enabled: true
  population_size: 20
  elite_count: 2
  survival_rate: 0.6
  mutation_rate: 0.2
  min_interval: "5m"
  selection_strategy: "tournament"
  tournament_size: 3

tools:
  builtin: true

reflection:
  enabled: false

database:
  host: localhost
  port: 5432
  user: ares
  password: ""
  database: ares
  ssl_mode: disable
```

---

## Config Sources & Precedence

Config may come from **twelve sources**, merged into one `ares_config.Config`:

1. Defaults → 2. `ares.yaml` file → 3. environment variables (`ARES_*`, `OPENAI_API_KEY`, …) → 4. programmatic Options (`sdk.WithXxx`)
   Later sources override earlier ones. Zero-value fields fall back to component defaults and are never clobbered.

## Related Docs

- Config system deep dive: `docs/articles/en/22-config-system.md`
- Full examples: `examples/01-quickstart/`, `examples/12-yaml-driven-flags/`

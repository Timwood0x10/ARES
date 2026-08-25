# ARES config.yaml 配置指南（中文版）

> 版本：0.2.9 · 最后更新：2026-08-05
> 英文版见 [English Version](./25-config-yaml-guide.en.md)

本指南说明如何编写 `ares.yaml`（或任意 `<name>.yaml`）来配置 ARES Runtime。
配置采用 **YAML + 强类型校验**：所有字段都有合理默认值，只设置你需要覆盖的项即可（零值哲学）。

**最小可用配置**（只需 LLM 即可启动）：

```yaml
llm:
  provider: ollama        # ollama | openai | anthropic | openrouter
  model: llama3.2
  api_key: ""             # 本地 provider 可留空；云端 provider 必填（也可用环境变量）
  base_url: ""            # 自定义 endpoint（可选）
```

**启动方式**（三选一）：

```go
rt := sdk.NewRuntime(sdk.WithConfig("ares.yaml"))    // 从文件装配（推荐）
// 或
cfg, _ := sdk.LoadConfigFile("ares.yaml")             // 读配置
opts, _ := cfg.ToOptions()                            // 配置 → Options
rt := sdk.NewRuntime(opts...)
// 或纯代码：sdk.NewRuntime(sdk.WithOllama("llama3.2"), sdk.WithDefaultMemory())
```

---

## 1. LLM（大模型接入）

```yaml
llm:
  provider: openai                 # 必填：ollama | openai | anthropic | openrouter
  model: gpt-4o-mini               # 模型名
  api_key: ""                      # API Key（或走环境变量 OPENAI_API_KEY 等）
  base_url: ""                     # 自定义 base URL（代理/私有部署）
  timeout: 30                      # 请求超时（秒），0 = 默认 30s
  max_prompt_length: 8192          # 最大提示词字符数，0 = 默认 8192
```

| provider | 说明 | 本地模型默认 |
|---|---|---|
| `ollama` | 本地 Ollama | `llama3.2` |
| `openai` | OpenAI / 兼容服务 | `gpt-4o-mini` |
| `anthropic` | Claude | `claude-sonnet-4-5` |
| `openrouter` | OpenRouter 聚合 | 需在 `model` 指定 |

---

## 2. 蒸馏（Memory Distillation）

蒸馏把任务经验沉淀为长期记忆，需要 embedding client（用于向量召回）。属于 `memory` 配置块。

```yaml
memory:
  enabled: true                    # 启用记忆系统（蒸馏的前置开关）
  max_history: 10                  # 闭环上下文保留轮数，0 = 默认 10
  enable_distillation: true        # 启用任务蒸馏（v0.2.4 起）
  distillation_threshold: 3        # 每 N 轮对话触发一次蒸馏，0 = 默认 3
  enable_rag: false                # 启用检索增强（蒸馏记忆注入 prompt），默认关闭
  rag_top_k: 5                     # RAG 注入片段数，0 = 默认 5
  session:
    max_history: 20                # 会话存储窗口（独立于 max_history）
    ttl_seconds: 3600              # 会话过期时间
  user_profile:
    enabled: true                  # 用户画像（长期记忆）
  task_distillation:
    provider: llm                  # 蒸馏方式：llm | rule
    max_tasks: 100                 # 蒸馏任务上限
```

> 蒸馏 + 持久化：配置 `database` 块后，蒸馏结果与 evidence 可持久化到 PostgreSQL（见下文）。

---

## 3. 遗传进化（GA Evolution）

GA 通过变异/交叉/选择进化策略（提示词、参数、调度、知识检索等），需要显式启用。
对应模块：workflow / scheduler / knowledge / recovery / memory / prompt 六个 genome。

```yaml
evolution:
  enabled: true                    # 必填：默认 false，不启用则整个进化管线跳过
  population_size: 20              # 每代种群大小，越大搜索越多样、每代越慢
  elite_count: 2                   # 每代保留的精英数（防止最优解丢失）
  survival_rate: 0.6               # 存活比例 [0,1]，越高多样性越强、收敛越慢
  mutation_rate: 0.2               # 基因变异基础概率，越高探索越强
  min_mutation_rate: 0.05          # 自适应变异率下限
  max_mutation_rate: 0.5           # 自适应变异率上限
  generations: 15                  # 最大代数，0 = 无限（直到手动停止）
  breeding_pool_ratio: 0.5         # 作为交叉亲本的种群比例
  min_interval: "5m"               # 进化调度最小间隔（Go duration 格式）
  selection_strategy: "tournament" # tournament | roulette | rank
  tournament_size: 3               # 锦标赛选择规模
  crossover_type: "uniform"        # 交叉方式
  target_fitness: 0                # 目标适应度（达到即提前停止），0 = 不启用
  steady_state: false              # 稳态进化（持续替换而非整代更替）
  steady_state_replace_rate: 0.3   # 稳态替换率
```

> 可选的 LLM 打分器：启用后 GA 用 LLM 打分每个策略（替代常量基线 50.0），需要可用 LLM。

**知识检索参数**（`knowledge` 块）：

```yaml
knowledge:
  retrieval_enabled: true          # 启用 AKG 知识检索，默认 false
  top_k: 5                         # 每次检索最大片段数，0 = 默认 5
  min_score: 0.4                   # 最小相似度阈值，低于则过滤，0 = 默认 0.4
```

> GA 的 knowledge genome 进化参数（`max_results` / `reducer_strategy` / `planner_strategy` /
> `summarizer_type`）是基因组内部配置，不由 `config.yaml` 的 `knowledge` 块直接暴露。

---

## 4. Chaos（故障注入 / 韧性测试）

> **注意**：Chaos 故障注入**没有独立 YAML 配置块**——它由运行时 API 注入
> （`manager.SetChaosConfig` / `InjectChaos` 等）或 arena 故障注入场景驱动，
> 而非配置文件控制。配置文件层面的韧性相关开关是：

```yaml
# 韧性/容错相关（在 config 或运行时启用）
chaos:
  enabled: false                   # 若你的构建暴露该开关，置 true 启用故障注入
  # 具体注入项（延迟/超时/网络分区/崩溃）由 arena / 运行时 API 控制，非 YAML
```

如需在示例中体验 chaos，参见 `examples/06-chaos-resilience/`。

---

## 5. 其他配置块

```yaml
database:                          # PostgreSQL（蒸馏持久化 / evidence 持久化）
  host: localhost
  port: 5432
  user: ares
  password: ""
  database: ares
  ssl_mode: disable                # 留空 = disable（本地开发）

embedding:                         # Embedding 服务（蒸馏/RAG 需要）
  service_url: "http://localhost:8000"
  dimension: 1536                  # 向量维度，0 = 模型默认

tools:
  builtin: true                    # 启用内置工具集
  mcp:
    - null-server                  # 启用的 MCP 服务器名（需另行注册）

reflection:
  enabled: false                   # 反思（self-reflection）开关
```

---

## 完整示例（features 全覆盖）

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

## 配置来源与优先级

配置可来自 **十二个来源**，统一合并进 `ares_config.Config`：

1. 默认值 → 2. `ares.yaml` 文件 → 3. 环境变量（`ARES_*` / `OPENAI_API_KEY` 等）→ 4. 程序化 Options（`sdk.WithXxx`）
   后者覆盖前者。零值字段回退组件默认值，不会误覆盖。

## 相关文档

- 配置系统深入：`docs/articles/zh/22-config-system.md`
- 完整示例：`examples/01-quickstart/`、`examples/12-yaml-driven-flags/`

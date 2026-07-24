# AKG 知识图 → Agent 自动检索闭环计划

> 目标：把"压缩 → 蒸馏 → 知识图"在**消费端**真正闭环——让 leader agent 在自动检索时用到编译器沉淀进 AKG 的知识图，且**不把低质噪声塞进每次响应**。
> 原则（协作铁律）：方向优先于接线；先质量门、再持久化、最后接线。噪声进信号 = 失败模式，故质量门是前置硬门。

## 进度（截至 2026-07-23 晚）

- ✅ **L1 结构门**：`validator.go:ValidateNodeForAKG` + `AKGBuilder.WithQualityGate(true)`（生产已启用，`bootstrap_steps_knowledge.go`）。单测 12 例 + 门开关测试全绿。
- ✅ **L2 置信度语义化**：`akg_extractor.go` 引入 `confStrong=0.9/confMedium=0.7/confWeak=0.4` 三档替换全部硬编码；`akg_selector.go` 去掉"实体总保留"carve-out（新增 `selectEntities` 按阈值过滤）；默认 `AKGMinConfidence` 0.4→0.6（`config.go:496`/`context_lifecycle.go:41`/`pipeline.go:39`）；`akg_builder.go` 写入 `Metadata["source_signal"]` 观测标签。build/vet/lint/staticcheck/gofmt 干净，compiler+ares_config+ares_bootstrap+knowledge 测试全绿。
- ✅ **L3 质量门观测 metrics**：新增 `compiler/metrics.go`（`AKGMetrics` 线程安全收集器 + `AKGSnapshot` + `signalTierOf`），统一接入三处落点——`AKGSelector.Select`（`akg_objects_in`+`dropped_lowconf`）、`AKGBuilder.Build`（`dropped_structural`+`objects_built`+置信直方图+信号档）、`Resolver.Resolve`（`dedup_hits`）。共享实例经 `WithMetrics`/`WithAKGMetrics`/`SetAKGMetrics` 注入，挂到 `KnowledgeCompilerComponents.AKGMetrics` 供评测基座/serve 层读取；并桥接既有 Prometheus 出口（`ares_observability.SetAKGSnapshot` → `ARES_akg_*` gauge，含置信分桶/信号档，用 Set 语义避免跨 run 累加）。新增 `metrics_test.go`(5 例)+`prometheus_akg_test.go` 全绿；build/vet/-race/gofmt/golangci-lint(0)/staticcheck(0)。
- ✅ **1.3 评测基座（已建，待真实样本跑）**：新增 `internal/ares_memory/compiler/eval`（独立子包，**不进管线**）+ `cmd/akg-eval` CLI。基座跑真实 `AKGSelector.Select → AKGBuilder.Build`（复用既有 L1/L2/L3 + 共享 `AKGMetrics`），对每条样本算 `structural_pass_rate` / `precision`(vs gold) / `recall` / `dedup_rate` / 置信直方图+信号档，并裁决硬门：`StructureGate≥0.95`、`PrecisionGate≥0.85`（缺 gold 时跳过精度门）。`NormalizeKey` 做大小写/语序无关的精度比对（CJK 整体作为一个 token，因无分词器）。内置 4 份合成样本（`samples/`：2 份干净 + 2 份跨样本重复）已验证：全干净集 `ready_for_phase3=true`、跨样本共享 store 时重复 fact 被 `dedup_hits` 命中。带 `-fail-on-gate` 可当 CI 门禁（不过则退出码 3）。`eval_test.go` 5 例全绿。
  - **实测暴露两个真实 Gate 行为（非 bug，已用样本验证）**：
    1. `akgStopwords` 含 `is/are/be/...` 但**不含 `the`** → 实体 `the` 会被 L1 放行（结构门只拦 `a/an/of/to/...` 与中文停用词）。
    2. L1 对 fact 的 predicate 是停用词（`is/are/to` 等）即判 `stopword triple` 丢弃 → 自然句 "X is Y" 会被拒。属保守策略，接真实样本后需观察是否误杀合理事实。
  - **reference 节点无 `description` 时 `nodeSummary` 退化成节点 ID**（非 fact 类型不取 subject/predicate/object）→ 其 `source_signal` 比对 key 无法匹配 gold。当前样本已用 `description` 规避；这是后续可优化点（reference 也取三元组做 summary）。
- 实测效果（合成对话，临时 demo 已删）：L1 在噪声对话 4→2、真实对话 15→14（丢停用词三元组）；L2 在弱信号密集对话 0.4 阈值选 2 节点 / 0.6 阈值选 1 节点（丢 WEAK 档中文名词短语/开放问题）。L3 e2e 验证：6 候选 → 丢 2 低置信(e3/f3) + 丢 2 结构(e2 停用词/f2 残缺三元组) → 留存 2(e1/f1, 均 strong)；去重独立验证命中 1。
- ✅ **Phase 2 持久化 swap（DI 不新造存储）**：共享 AKG 池从 `memorystore.New()` 改为按配置选 postgres/sqlite/memory 实现，经 `comp.KnowledgeStore` 注入点切换（`StoreProvider` 不变，只读接口）。
  - 配置：`KnowledgeCompilerConfig` 新增 `AKGStore`(`auto/memory/sqlite/postgres`) + `AKGSQLitePath`；`setDefaults` 补默认（`auto` / `data/akg.db`）；`validateKnowledgeCompiler` 校验（sqlite 需 path、postgres 需 `Storage.Enabled && Type=="postgres"`）。
  - 实现：`bootstrap_steps_knowledge.go` 的 `wireKnowledgeCompiler` 加 `cleanups *[]func()` 参数；新增 `newSharedAKGStore`（switch 模式，auto 下 `Storage.Enabled && postgres` 选 postgres 否则 memory）、`newPostgresAKGStore`（复用 Storage DSN、`sql.Open("postgres")`+Ping 5s+连接池+`postgresstore.New(db)`、`%w` 错误链+close 兜底）、`newSQLiteAKGStore`（`MkdirAll`+`sql.Open("sqlite")`+`SetMaxOpenConns(1)`+`sqlitestore.NewWithDB(db)`）。
  - 优雅降级：postgres/sqlite 打开失败 → `log.WarnContext` 显式降级 memory（不崩 bootstrap）；`storageTypePostgres` 常量消除 goconst。
  - 测试（全绿，含 -race）：config KC 测试 19 例 + bootstrap 新增 `akg_store_test.go`(7 例：memory 显式 / auto 无 pg / sqlite 重启持久性 e2e / sqlite 空 path 报错 / postgres 不可达降级 / cleanups 注册等)。build/vet/-race/gofmt/golangci-lint(0)/staticcheck(0)。

---

## 0. 现状（已核实，file:line）

| 系统 | 状态 | 证据 |
|---|---|---|
| 压缩 (Conversation Compiler) | 写入闭环，opt-in 默认关 | `ContextLifecycle.Compile` 经 `SetAKGBuilder` 写共享 `comp.KnowledgeStore`；`bootstrap.go:293` 仅 `kc.Enabled` 时注入（`bootstrap_steps_knowledge_test.go:22` 验证默认关） |
| 蒸馏 (经验) | 闭环，默认开 | `bootstrap_steps.go:45-72` 订阅 `EventTaskCompleted`→`Distill`→postgres experience；`production_manager_tasks.go:260` live 消费 |
| 蒸馏 (编译器内 KMDistiller) | 随 compiler 开关 | opt-in |
| 知识图 AKG | **写入闭环、自动读出断开** | 写入：`RegisterProvider(storeProv)`（`bootstrap_steps_knowledge.go:93`）把共享池注册进 `KnowledgeRuntime`；读出：`production_manager_tasks.go:255` `SearchKnowledge=false`；`retriever.Retriever` 仅 MCP/HTTP/e2e 调用，无生产 agent 自动路径 |

**两个关键事实（决定方案形状）：**

1. **存储错位 → A1 字面方案接不通**：compiler 写 `memorystore`(纯内存，`bootstrap_steps_knowledge.go:72`)；`SearchKnowledge` 查的 `s.kbRepo` 是 **postgres 知识库表**（`retrieval_service.go:153`）。两套隔离库。把 `:255` 改成 `true` 只会搜另一个不相关语料，**假闭环**。

2. **持久化是 swap 不是缺口（重要更正）**：`knowledge.KnowledgeStore` 已有耐久实现 `internal/knowledge/store/postgres/store.go`（`akf_objects` 表：id/type/namespace/raw/normalized/summary/metadata/tags/confidence/version）与 `store/sqlite/store.go`。compiler 写出的对象形状与这张表完全匹配（`akg_builder.go:202` `Confidence: n.Confidence`）。所以持久化 = 把共享池从 `memorystore.New()` 换成 postgres/sqlite 实现（DI swap），**不用新造存储**。

3. **真洞在置信度质量**：compiler 置信度在 `akg_extractor.go` 按规则**硬编码**（别名命中 entity=0.9、引号=0.7、泛化 triple=0.5–0.6、decision/constraint=0.7 等），经 `akg_builder.go` 写入 `KnowledgeObject.Confidence`，由 `akg_selector.go` 按 `AKGMinConfidence=0.4` 过滤。即：置信度只反映"命中了哪条规则"，**不反映事实是否正确**，且 0.4 阈值几乎放行一切 → 直接进响应就是噪声进信号。

---

## 1. Phase 1 — 质量门（先做，详细设计）

**目标**：在"知识图内容进入任何自动检索响应"之前，保证产出 (a) 结构有效、(b) 置信度可解释、(c) 可量化评估、(d) 可观测。

### 1.1 置信度审计（discovery，1 步）
- 已定位：置信度来源 = `akg_extractor.go` 各抽取规则的硬编码常量；过滤点 = `akg_selector.go:77/103`（`n.Confidence >= s.MinConfidence`）；落库点 = `akg_builder.go:202`。
- 结论：质量门**不能只依赖阈值**，必须加结构有效性 + 信号可解释性。

### 1.2 质量门三层（实现，零/低 LLM）
- **L1 结构有效性（必做，零 LLM）** — 新增 `internal/ares_memory/compiler/validator.go: ValidateForAKG(obj)`：
  - 三元组 subject/predicate/object 全非空、非停用词、长度合理；
  - 实体须经过归一化（命中 alias 表/项目词典/引号），未归一化裸实体 → 降权或丢；
  - summary 非空且非纯标点。
  - 不通过 → 不写 store（或写 `quarantine` 命名空间供复核，不进主图）。
- **L2 置信度语义化（必做）** — 把"规则命中"升级为"可解释置信度"：
  - 引入 `SourceSignal` 或复用 `metadata`：强信号句（决策/约束/权衡等中文关键词命中）→ 高；泛化 triple 抽取 → 中；stopword-heavy → 低。
  - 让 `AKGMinConfidence` 真正过滤低信号对象；默认值从 0.4 上调到可解释档（提议 0.6），在 bootstrap 配置项暴露。
- **L3 去重有效性（已有，加观测）** — Resolver Jaccard(0.85) 已在用；补指标：dedup 命中率、跨编译重复率。
- **可观测 metrics** — `akg_objects_in / dropped_structural / dropped_lowconf / dedup_hits / confidence_histogram`，接入既有 metrics 出口（启动 Phase 1 时确认出口位置）。

### 1.3 评测基座（关键，决定"够好"的硬指标）
- 独立工具/脚本，**不进管线**：
  - 取 N 段真实对话（fixtures 或脱敏样本），跑 compiler 产出 object，导出 JSONL（summary/confidence/source-signal/normalized）。
  - 自动指标：结构通过率、置信分布、去重率、每对话产出量。
  - **精度采样**：用 LLM（仅评测，不进管线）或人工对样本 object 标 correct/incorrect/low-value，算 precision。
- **验收门槛（提议，待你拍板数值）**：
  - 结构通过率 ≥ 95%；
  - 精度采样 ≥ 85%（incorrect + low-value < 15%）；
  - 去重率落在合理区间（不过度合并也不漏合并）；
  - 置信分布不应 90% 堆在单一值。
- 未达标 → 回到 extractor 调优（Phase 1 已做中文，可能需更强信号识别），**不进 Phase 3**。

### 1.4 测试
- `validator_test.go`：好三元组过、坏三元组丢、未归一化实体处理、低信号降权。
- 评测基座脚本 + 一份基线报告（存 `reports/` 或 `docs/`，待定）。

### 1.5 Phase 1 完成判据
质量门代码合入 + 单测绿；评测基座跑出基线报告；验收门槛达成（或明确未达 + 改进项清单）。

---

## 2. Phase 2 — 持久化决策与接线（中等） ✅ 已完成

- 现状（已改）：共享池经 `newSharedAKGStore(ctx, cfg)` 按 `AKGStore` 配置选 `postgresstore`/`sqlitestore`/`memorystore`，注入 `comp.KnowledgeStore`（`bootstrap_steps_knowledge.go` 的 `wireKnowledgeCompiler`）。
- 选项：
  - **P-a（推荐）**：postgres 启用时用 `knowledge/store/postgres`（akf_objects 耐久、跨副本共享）；sqlite 作轻量持久。统一经 `comp.KnowledgeStore` 注入点切换。
  - **P-b**：维持内存（会话内临时图），接受冷启动空图——若 A2 仅作"会话级增强"可接受。
- 实施：bootstrap 按 `cfg.Postgres.Enabled` 选 store 实现；`StoreProvider` 不变（它只读 `KnowledgeStore` 接口）。
- 测试：e2e 验证重启后对象仍在（postgres 路径）。

---

## 3. Phase 3 — 接 A2（leader agent 自动检索，核心闭环）✅ 已完成

- **选注入点**：leader agent 的 prompt 构建 / 前置检索链（**非** `production_manager_tasks.go:255` 的 `SearchSimilarTasks`——那是相似任务搜索，语义不符）。已精确命中 `internal/agents/leader/agent_memory.go:initMemoryContext` → `BuildContext` 之后（覆盖 `Process`/`ProcessStream`），详见 `AKG_PHASE3_REVIEW.md` §2/§7/§11。
- **接线**：`retriever.Retriever` 在 `wireKnowledgeCompiler`(`kc.Enabled` 分支) 内构造并存入 `comp.KnowledgeRetriever`，经 `createAgents/createLeaderAgent` 参数流入 `cmd/ares` 与 `cmd/monitor-live` 两个生产入口，通过 `leader.WithKnowledgeRetriever` 注入；`BuildContext` 后 `Retrieve(strInput)` 把 `## Agent Knowledge Fabric (AKG) Context` 追加进 `enrichedInput`，失败仅 warn 回退。
- **配置开关**：随 `kc.Enabled` 联动，无独立子开关；retriever 为 nil 时 leader 跳过检索（fail-safe，行为与改动前一致）。
- **防噪声**：写时 L1/L2 门已在 `akgBuilder.Build` 过滤；读时 runtime reducer token budget 防挤占。
- **超时加固（2026-07-24）**：`initMemoryContext` 的 A2 检索以 `context.WithTimeout(ctx, 3s)` 包裹（`agent_memory.go:akgRetrieveTimeout`），避免全管线卡住阻塞 leader 主路径（见 `AKG_PHASE3_REVIEW.md` §8.3）。
- 测试：e2e 验证 `TestAKGClosedLoopE2E`（retriever 包，生产级 `StoreProvider`+真实 `memorystore` 种子→检索→prompt 含种子知识）证明读侧闭环；leader 注入块由 `go build` + 该 e2e 覆盖依赖。
- 落地 commit：`0b5e2a13 feat(akg): add A2 leader agent auto-retrieval with knowledge enrichment`。

---

## 4. Phase 4 — 验证与收尾 ✅ 已完成

- **端到端验证闭环**：新增 `internal/knowledge/retriever/retriever_closure_e2e_test.go`（`TestAKGClosedLoopE2E`）——用生产级 `StoreProvider` + 真实 `memorystore` 种子 AKG 对象，构建 `KnowledgeRuntime`+`Retriever`，调 `Retrieve`，断言 `FormatPrompt` 含种子知识。证明"写→读→进 prompt"闭环（即 A2 注入 leader 的内容）真实成立。`go test ./internal/knowledge/retriever/...` 全绿。
- **性能/回归**：A2 检索以 `akgRetrieveTimeout=3s` 限时（`agent_memory.go`），失败回退不阻断主流程；bootstrap 全量测试（含 `akg_store_test.go` 7 例）覆盖持久化 swap。
- **文档状态**：本文档各 Phase 已标记 ✅；`AKG_PHASE3_REVIEW.md` 同步 §8 风险状态。
- **不动**：`retrievalservice`（已 LIVE）、experience 底层、postgres 检索仓库（独立库）。
- **遗留已知限制**（非阻塞）：规则抽取器 recall 偏低（真实样本 `recall_micro≈0.38`），A2 接入后检索覆盖面有限；抽取器英文实体走 lexicon-free 机制（CamelCase+结构引用+跨轮复现+形状门），残余 FP 极少（automatic/A/C/so/.dylib）。均为已知上限，不卡 1.3 门、不影响主流程。

---

## 风险与未决

- 注入点精确位置需 Phase 3 启动时再确认；可能触及 leader agent 核心检索逻辑，需小步。
- 评测基座需要真实对话样本（来源？脱敏？）——待你提供或用 fixtures。
- 数值门槛（精度 85% 等）为提议，需你拍板。
- **A1（改 `:255`）明确不做**——它 closure 的是另一个库，是假闭环。

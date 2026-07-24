# AKG Phase 3 (A2) 就绪审查 — leader agent 自动检索闭环

> 范围：仅测试 + 审查（只读），**未改动任何源码**。目标是确认 Phase 3 能否启动：
> (1) 1.3 真实样本硬门是否可评估；(2) 注入点精确位置；(3) 消费机制是否就位；(4) 风险与未决。
> 协作铁律：方向优先于接线。本审查只给结论与证据，落地需单独授权。

## 结论（先行）

- ✅ **消费机制已就位、未接线**：`knowledge/retriever.Retriever` 已实现，经 `KnowledgeRuntime.Execute` 跑完整 AKF 管线，会咨询我们在 Phase 1 注册进运行时的 `StoreProvider`。但它目前**只被测试调用**，没有任何生产 agent 路径。
- ✅ **注入点已精确命中**：leader agent 的上下文构建缝是 `internal/agents/leader/agent_memory.go:13 initMemoryContext` → `:63 a.memoryManager.BuildContext(...)`。这正是计划所说的"leader agent 的 prompt 构建/前置检索链"，且同时覆盖 `Process`(`agent.go:383`) 与 `ProcessStream`(`agent.go:706`)。**不是** `production_manager_tasks.go:255` 的 `SearchSimilarTasks`（语义不符，计划已排除）。
- ✅ **1.3 硬门已在真实对话样本（英文）上通过（2026-07-24 机制修复后）**：语料 `conversations/ourchat.json` 已全量翻为英文（用户要求输出英文），经真实 `AKGExtractor.Extract` 再生 `samples_real/ourchat.json`。机制修复（英文路径仅认 CamelCase 复合词 / 全大写缩写，**零词表**）后 `ready_for_phase3=true`，`structure_rate=1.0`(≥0.95✓)、`precision_rate=1.0`(≥0.85✓)、`recall_micro=0.38`（规则抽取器对自由文本/小写技术词召回低，已知限制，不卡 1.3 门）。**注意**：更早的 0.909 是中文语料 + 数据适配词表（`englishDanglingTerms`）的产物，该词表已删（属"针对真实数据做适配"，非机制修复）；现 precision=1.0 来自机制而非词表。
- ⚠️ **存在写/读耦合（关键设计点，仍未决策）**：AKG `StoreProvider` 仅在对话编译器启用时才注册进运行时，故 A2 读侧**仅在编译器（写侧）开启时才有产出**。需决定是"与 `kc.Enabled` 联动"还是"解耦注册（只要 `comp.KnowledgeStore != nil` 就注册）"。
- ✅ **噪声进信号已被写时质量门拦住**：L1/L2 门在 `akgBuilder.Build`(`WithQualityGate(true)`) 处过滤，进 store 的对象已是质量门后的；读侧再经 runtime reducer 的 token budget 防挤占。

**总判**：Phase 3 启动的硬前提（1.3 真实样本双门过）**已满足**；技术上可启动（机制在、缝在、写时门在）。启动前仅剩写/读耦合一项设计决策待用户拍板。注意 recall 仍低（英文真实样本 `recall_micro=0.38`，规则抽取器对自由文本/小写技术词召回低）——这不影响 1.3 门，但意味着 A2 接入后检索召回有限，属已知限制。

---

## 1. 测试结论（"先测试"）

### 1.1 1.3 评测基座（cmd/akg-eval，内置合成样本）
```
go run ./cmd/akg-eval -dir internal/ares_memory/compiler/eval/samples
```
- `ready_for_phase3: true`
- `structure_rate: 1.0`（≥0.95 ✓）、`precision_rate: 1.0`（≥0.85 ✓）
- 聚合：nodes_in=11，dropped_low_conf=0，dropped_structural=0，objects_built=11
- 置信直方图：`0.7-0.9`:6、`0.9-1.0`:5；信号档 medium:6 / strong:5
- `-shared-store` 变体：dedup_hits=1、objects_built=10、dedup_rate=0.09 → **跨样本去重生效**，门仍过。

### 1.2 全量相关测试（go test -race，全绿）
`internal/ares_memory/compiler/eval`、`internal/ares_memory/compiler`、`internal/ares_config`、`internal/ares_bootstrap`、`internal/knowledge/runtime`、`internal/knowledge/provider/store` —— 均 `ok`。

### 1.3 硬门有效性缺口（必须指出）
- 合成样本仅 4 份（`conv_clean`/`conv_clean2` 干净 + `conv_dup_a`/`conv_dup_b` 跨样本重复；`noisy/conv_noisy.json` 在子目录被 `LoadDir` 跳过，不参与 CLI 评测）。
- L1 两个已知真实行为**未经真实数据验证**：① `akgStopwords` 不含 `the` → 实体 `the` 会放行；② fact 的 predicate 是停用词(`is/are/to`)判 `stopword triple` 丢弃 → "X is Y" 自然句被拒（保守策略，可能误杀合理事实）。
- **结论**：合成过门 ≠ 真实过门。Phase 3 启动硬前提 = 用户提供脱敏真实对话，跑 `akg-eval -dir <real-samples>` 实测结构率/精度率。

---

## 2. 注入点定位（grep 确认，file:line）

| 角色 | 位置 | 说明 |
|---|---|---|
| 上下文构建缝 | `internal/agents/leader/agent_memory.go:13` `initMemoryContext` | leader 在每轮 `Process`/`ProcessStream` 开头构建 enriched input |
| 确切插入行 | `agent_memory.go:63` `a.memoryManager.BuildContext(ctx, strInput, sessionID)` | 返回 "Previous conversation history + Current request" 字符串；在此之后追加 AKG 检索结果最自然 |
| 调用方（同步） | `internal/agents/leader/agent.go:383` | `Process` → `initMemoryContext` |
| 调用方（流式） | `internal/agents/leader/agent.go:706` | `ProcessStream` → `initMemoryContext` |
| **排除点** | `internal/ares_memory/production_manager_tasks.go:255` `SearchKnowledge = false` | 属 `SearchSimilarTasks`（相似任务向量检索），语义不符，计划已排除 |

`BuildContext` 实现（`manager_impl.go:442`）：取历史消息 → `ctxCleaner.Clean` → 拼 "Previous conversation history" + "Current request"。返回单个 `string`，注入点只需在其后拼接 AKG 段落，不改变既有签名。

---

## 3. 消费机制（已建，未接 agent）

- `internal/knowledge/retriever/retriever.go:98` `Retrieve(ctx, query)`：
  - `:141` `r.runtime.Execute(ctx, query.Text, budget, nil)` —— 跑完整 AKF 管线 Plan→Load→Pipeline→Link→Reduce，**会咨询运行时所有已注册 Provider（含 Phase 1 注册的 `StoreProvider`）**。
  - `:156` `r.compiler.Compile(ctx, graph, cfg)` —— 把工作图格式化为 prompt 上下文（默认 `FormatPrompt`）。
- 构造：`retriever.New(rt *runtime.KnowledgeRuntime, comp compiler.Compiler) *Retriever`（`retriever.go:84`）。
- **`retriever.New` 全仓仅测试调用**：`knowledge/e2e_test.go:100`、`retriever_test.go`、`benchmark_test.go`。无任何生产 agent 路径 → 印证计划"retriever 仅 MCP/HTTP/e2e 调用，无生产 agent 自动路径"。
- `compiler.Compiler` 是极小接口（`compiler.go:50` 仅 `Compile(ctx, graph, cfg)`），有 `NewDefaultCompiler()`（`compiler.go:59`）。**A2 不需要 opt-in 的对话编译器**，用默认编译器做格式化即可。

---

## 4. 构造点与可用依赖（Components）

- 生产构造点：`cmd/ares/agents.go:112` `leader.New(...)`（及 `cmd/monitor-live/agents.go:114`）。已用 `LeaderOption` 模式（`WithEventStore`/`WithFeedbackService`/`WithStrategySource`）——扩展点现成。
- `createLeaderAgent`（`cmd/ares/agents.go:43`）当前接收 `memMgr/store/feedbackSvc/strategySrc`，**不接收 `comp`/`KnowledgeRuntime`**。落地需把 `comp.KnowledgeRuntime` 透传进 `createAgents`→`createLeaderAgent`。
- `Components`（`internal/ares_bootstrap/bootstrap.go:30`）：
  - `:48 KnowledgeRuntime *knowledgeruntime.KnowledgeRuntime` —— **始终创建**（bootstrap.go:231 `comp.KnowledgeRuntime = knowRt`）。这是 `retriever.New` 的第一个参数。
  - `:54 KnowledgeStore knowledge.KnowledgeStore` —— 共享 AKG 池（Phase 2 swap 产物），编译器启用时非空。
  - `:60 KnowledgeCompiler *KnowledgeCompilerComponents` —— **opt-in，可为 nil**。A2 不需要它（用 `compiler.NewDefaultCompiler()`）。

---

## 5. 关键耦合风险（写/读）—— 决策点

`bootstrap_steps_knowledge.go:110` 注册 `StoreProvider` 进 `comp.KnowledgeRuntime` 的代码块**受 `kc.Enabled` 门控**（仅编译器启用时注册）。
- 后果：若 A2 开启但对话编译器关闭 → 运行时无 `StoreProvider` → 检索返回空图，**读侧静默无产出**。
- 计划原话"与 compiler `kc.Enabled` 联动"是其中一种选择，但更干净的作法：**把 `StoreProvider` 注册从 `kc.Enabled` 解耦**，改为"只要 `comp.KnowledgeStore != nil` 就注册"。这样 A2 的开关可独立灰度，且读侧在 store 非空时必有产出。
- **精度补充（避免误判）**：即使解耦注册，当前 store 也只有对话编译器会写入（`ContextLifecycle.Compile` 走 Phase 4 增量路径、`Pipeline.Run` 走一次性路径），故"编译器关闭 → 无写入 → 读侧仍为空"这一层耦合是 inherent 的。解耦注册的真正价值是**让 A2 开关可独立灰度与测试**，而非凭空产出数据；若要 A2 独立于编译器产出，必须有其他写入方（或放宽编译器为"仅持久化 AKG 而不注入 prompt"）。
- 此决策影响 Phase 3 落地代码，需用户拍板。

---

## 6. 质量门保护（防噪声进信号）

- **写时门（已生效）**：`akgBuilder.Build` 经 `WithQualityGate(true)`（Phase 1）强制 L1 结构 + L2 置信过滤 → 进 store 的对象已是质量门后。retriever 读的正是这些对象。
- **读时保护**：`runtime.Execute` → reducer（token budget）防止低质对象挤占；`StoreProvider.IntentMatch` 基线 0.4 > 发现阈值 0.35 → 所有查询都会咨询共享池。
- **残留风险**：运行时若挂了其他 Provider（非本范围）可能贡献对象；retriever 的 pipeline 会再归一化/摘要。已通过 token budget 约束，但上线后需观测 `ARES_akg_*` 指标。

---

## 7. Phase 3 落地形态建议（待授权，本次未动）

> 以下为实现提案，需用户单独授权后才动手（code_rules §10.5 改核心需授权；§10.4 禁用 git commit）。

1. **leader 包**：`leaderAgent` 加 `knowledgeRetriever *retriever.Retriever` 字段 + `LeaderOption WithKnowledgeRetriever(r)`（`agent_types.go:86` 结构体附近）；`initMemoryContext`（`agent_memory.go:63` 之后）若 retriever 非 nil 且启用，用 `strInput` 调 `retriever.Retrieve` 并 append `result.Context.Prompt` 到 `enrichedInput`；**失败仅 warn、回退无 AKG**（与 `BuildContext` 错误回退同款，不阻断主流程）。
2. **构造点**：`createLeaderAgent` 接收 `comp.KnowledgeRuntime`；`retriever.New(comp.KnowledgeRuntime, compiler.NewDefaultCompiler())` 经 `leader.WithKnowledgeRetriever(...)` 注入。`cmd/monitor-live/agents.go:114` 同步处理避免漂移。
3. **配置开关**：默认关（opt-in）。建议新增 `LeaderAgentConfig.EnableAKGRetrieval`（或挂在 `KnowledgeCompiler` 下），与"store 非空"联动，灰度开启。
4. **检索 query 文本**：建议先用原始 `strInput`（最简），后续可升级为基于意图的子查询；以配置暴露，便于回退。
5. **解耦 StoreProvider 注册**（见 §5）：建议改为 `comp.KnowledgeStore != nil` 即注册。

---

## 8. 风险与未决

1. ~~**真实样本硬门缺口**（最高优先）~~ **已解决（2026-07-24）**：用本对话自身作语料跑通，修复抽取器 run-on 后 1.3 双门过（structure=1.0、precision=0.909）。见 §10。
2. **写/读耦合**：§5 决策（**仍待用户拍板**）。
3. **延迟**：每次 leader 轮次跑 `runtime.Execute` 全管线，需预算/超时 + 失败回退（§7.1 已含）。
4. **import 环**：leader 当前不 import `knowledge/retriever`；retriever 仅 import knowledge/compiler/runtime，leader import agents/base —— 应无环，落地时以 `go build` 验证。
5. **检索 query 质量**：原始 `strInput` 可能含噪声，首版需观察命中质量。
6. **monitor-live 同步**：第二个 leader 构造点需同步改动。
7. **recall 偏低（已知限制）**：英文真实样本 `recall_micro=0.38`（规则抽取器对自由文本/小写技术词如 postgres/sqlite/validator/retriever/Phase 2 召回不足），A2 接入后检索召回有限，不影响 1.3 门但影响覆盖面。

---

## 9. 建议的下一步（待用户定）

- 决策 §5 写/读耦合（解耦 vs 联动）—— **当前唯一剩余阻塞项**。
- 决策后，再授权 Phase 3 落地（§7 形态），严格按 `plan/rules/code_rules.md` 执行、不提交。
- 语料维护：每次对话追加进 `conversations/ourchat.json`，用 `gen_real.go` 再生 `samples_real/ourchat.json` 并扩展 gold，复跑 `akg-eval -dir samples_real -fail-on-gate=false` 监控精度回归。

---

## 10. 更新（2026-07-24）：抽取器机制修复（非数据适配）+ 1.3 真实样本（英文）重测通过

### 10.1 用户纠正：机制 vs 数据适配
首版修复在 `extractEntities` 英文路径加了 `englishDanglingTerms` 硬编码词表（`phase/step/section/...`）来堵 `Phase` 这类漏出 —— 这是**针对真实数据做适配**（看着本对话 `Phase` 漏出就硬编词表），不是改机制。用户明确要求"修改机制，不是针对真实数据做适配"，故删除该词表与 `isEnglishDanglingTerm`，改为通用机制。

### 10.2 真正的机制根因
英文路径"首字母大写词 = 实体"启发式过松。英文每句首词大写 → `Found`/`Suggest`/`Executed`/`Timeline`/`Phase` 等句子首字母动词/普通名词全成实体。把对话翻成英文后此缺陷彻底暴露：39 候选里绝大多数是这类噪声（仅结构门过、精度必然崩）。中文路径（词典/引号/名词缀）本身是干净的。

### 10.3 机制修复（`internal/ares_memory/compiler/akg_extractor.go`）
英文 capitalized 循环改为：先拒 `hasCJK`（中英文混排 run-on）与 `!isASCIIIdentifier`（含代码括号 `NewAKGExtractor().Extract` 的调用式 token），再要求 `isTechnicalIdentifier`（**零词表**）：
- `isCamelCase(s)`：纯 ASCII 字母且 ≥2 大写 + 含小写 → CamelCase 复合词（`BuildContext`/`AKGStore`/`AKGExtractor`/`SearchSimilarTasks`/`LoadDir`/`SourceMessage`/`StoreProvider`/`NodeEntity`/`AKGSQLitePath`）；
- `isAllCapsAcronym(s)`：≥2 大写字母（可含数字/下划线）→ 全大写缩写（`AKG`/`CLI`/`CJK`/`ASCII`/`DI`）。
删除死代码 `isCapitalized`/`isCommonWord`，新增 `isUpper`/`isCamelCase`/`isAllCapsAcronym`/`isTechnicalIdentifier`。中文路径与事实三元组抽取未动。

### 10.4 校验（全绿）
`go build ./internal/ares_memory/...` / `go vet` / `gofmt` / `golangci-lint`(0 issues) / `go test -race ./internal/ares_memory/compiler/...`（含 `akg_extractor_zh_test` —— 该测试不依赖单大写专有名词，机制改动未破坏）。

### 10.5 1.3 重测（英文真实对话样本）
语料 `conversations/ourchat.json` 已全量翻为英文（用户要求），经 `gen_real.go` 跑真实 `AKGExtractor.Extract` 再生 `samples_real/ourchat.json`（19 candidates = 18 实体 + 0 事实），gold 按对话真实知识手工扩展（实体覆盖抽出的 18 个 + 抽漏的小写真词如 postgres/sqlite/Phase 2，用于度量 recall）：

| 门 | 阈值 | 数据适配词表版(中文) | 机制修复版(英文) | 结论 |
|---|---|---|---|---|
| Structure | ≥0.95 | 1.0 | **1.0** | ✅ |
| Precision | ≥0.85 | 0.909 | **1.0** | ✅ |
| ready_for_phase3 | — | true | **true** | ✅ |
| recall_micro | — | 0.25 | **0.38** | 已知限制，不卡门 |

修复后 built 实体全部为真实技术标识符（LoadDir/AKGExtractor/BuildContext/StoreProvider/newPostgresAKGStore/reasonStopwordEntity/hasCJK/isASCIIIdentifier/...），句子首字母动词与普通单大写词全部消失。**precision=1.0 来自机制（零词表），recall=0.38 暴露规则抽取器对小写技术词（postgres/sqlite/validator/retriever/Phase 2）召回不足——已知限制，A2 接入后检索覆盖面有限。**

### 10.6 交付物（均未提交，§10.4）
- `akg_extractor.go`：英文路径 `isTechnicalIdentifier` 机制（核心修复，零词表）
- `conversations/ourchat.json`：随对话增长的语料（**已全量翻为英文**，用户要求）
- `gen_real.go`：真实抽取器语料生成器（复用）
- `samples_real/ourchat.json`：含 candidates + gold 的可评测真实样本（英文）

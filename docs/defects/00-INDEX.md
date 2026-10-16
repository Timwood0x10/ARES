# 缺陷总索引（P0 / P1 / P2）

> 这是 `docs/defects/` 的**速查总表**。细节在各分文件（带源码锚点 + 取舍/已做），
> 这里只列"要修的 + 按优先级排"，给"只能先修哪几个"用。
> 性质：`缺陷`=建议修 / `隐患`=规模/并发/时间下才暴露 / `取舍`=设计权衡，知边界即可（**不在本表排优先级**）。
>
> 分文件：
> - `dynamic-dag-and-quantum.md`（Q 量子 / D 动态 DAG）
> - `genetic-algorithm.md`（G）
> - `disposable-agent.md`（A 可丢弃/复活）
> - `collaboration-and-factories.md`（I 协作 / T 任务工厂 / A agent 工厂）
> - `recovery-and-fault-tolerance.md`（T-F task 失败 / A-F agent 失败）
> - `retrieval-and-storage.md`（R 检索 / S-st 存储 / D embedding 死信）
> - `service-loading-and-mcp.md`（S-mcp 服务发现/MCP 加载/工具注册）
> - `cognition-persistence.md`（P0-4 / A / P0-1 认知持久化专项：存什么 / 如何存，Rationale 载体 + 摘要分档 + 为何不上 Merkle）
>
> **注意**：`A`（disposable 复活）与 `A`（agent 工厂）共用前缀，看文件名区分；
> `S` 也撞：`S-mcp`=service-loading 的 S1-S6，`S-st`=retrieval-and-storage 的存储侧 S1-S3。
> 下标一律带文件前缀（如 `A-dispo`、`A-factory`、`S-mcp-S5`、`S-st-S2`）。

---

## P0 · 最致命（方向性 / 静默卡死，生产先咬人）

| # | 缺陷 | 一句话 | 共因 |
|---|---|---|---|
| P0-1 | **G1** GA 无"做对没"信号 〔证据链路已通 2026-10-13；**选育侧已接入**（同上日）；**golden 判据待拍板**〕 | fitness 只奖励"成功+快省"，没 accuracy → **越跑越会糊弄** | 认知/正确性没持久化 |
| P0-2 | **T1+T2** LLM `create_task` 不校验 **〔已修 2026-10-12〕** | `create_task` 依赖不存在/成环 → **任务静默永不 READY**（`create_plan` 走 `CompilePlan`、operator 走自带 `findCycle`，都安全；**只有 `create_task` 旁路漏**） | 引用/边界没校验 |
| P0-3 | **D1+D2** 提前交差不够可见 **〔已修 2026-10-12〕** | maxPlanDepth 撞墙 / L1 全拦 → 强制出 answer；**不标记成因**（D1 已有 Warn+计数，但无面板/告警规则） | 静默早停 |
| P0-4 | **Q3+A-dispo1+A-F4** 认知有损 **〔B 已动工 · A 与 P0-1 合并立项 2026-10-13〕** | checkpoint/复活只带"进度"不带"思路" → 换人/续跑**丢推理链**；A 的"存什么/如何存"见 `cognition-persistence.md` | 认知/正确性没持久化 |

## P1 · 该修（高频咬人 / 一处改受益一片）

| # | 缺陷 | 一句话 |
|---|---|---|
| P1-1 | **T-F1** `classifyFailure` 默认可重试 **〔已修 2026-10-15〕** | 忘 `MarkPermanent` 的**永久错白烧满重试预算**（+ backoff）。修：已知永久错表（窄表，`errors.Is`）+ 未分类原因重试/耗尽时 Warn |
| P1-2 | **R2** 检索纯余弦无精排 **〔已修 2026-10-15〕** | 假近进 top-K。修：生产路径**早就在宽召回（TopK*5）却没精排**，补确定性精排（相似度+词面覆盖+新鲜度）。**不是 cross-encoder**（无模型服务），如实标注 |
| P1-3 | **R1** ivfflat `lists=100` 写死 **〔已修 2026-10-15〕** | 真因是"没人再看它一次"：单一 DDL 源 + lists 按行数自适应 + 启动时 2 倍门限复核重建 + 检索期 probes 可配 + HNSW 选项 |
| P1-4 | **D(retrieval)** embedding 死信 **〔已修 2026-10-15〕** | 修：可行动性拆分告警（pending/given-up）+ **每条内容只自动重放一次**的原子重放（`auto_replayed` 标记随死信搬运，防无限循环）+ tick 顺序改为重放→清理→报告 |
| P1-5 | **I1** `ask_agent` 不回写 **〔已修 2026-10-15〕** | 修：回信作为**任务**投递给 asker（`(asker,peer,topic)` 哈希 id 幂等；无能力/不存在则响亮失败）+ 工具描述同步改口 |
| P1-6 | **A-factory3** agent quota 泄漏 **〔已修 2026-10-13〕** | claim/release 漏释放 = **agent 池假死**（`ErrResourceQuotaExceeded`） |
| P1-7 | **S-mcp-S2** MCP 挂了工具静默消失 〔已修 2026-10-13〕 | server 连不上/断连 → 它的工具没进/留在 Registry，LLM **不知道自己少了什么**，会白调不存在的工具 |

## P2 · 打磨 / 可观测 / 规模化（不急，但要知道）

| # | 缺陷 | 一句话 |
|---|---|---|
| P2-1 | **G2** 三道门盲区 | 门只防"回归"，不防"整体往糊弄漂"（**依赖 P0-1 修好才有意义**） |
| P2-2 | **D3** Reconcile 全量拿锁 | 漏事件时 O(节点)+持锁+RUNNING 短暂 desync（规模化才痛） |
| P2-3 | **D6** stableRound 并发前提 | 并发改同 session 图时"稳定重放"可能失效 |
| P2-4 | **A-F3** requeue ≠ 恢复 | 全 agent 超预算时任务**静默坐着**（requeue 只管回队） |
| P2-5 | **T-F3** 两个预算互不知晓 | agent 复活预算 / task 重试预算各报各的，无联合视图 |
| P2-6 | **A-dispo2/3/4** 复活预算·快照·schema | lifetime 累积误拒长寿命 agent / 快照 1024 静默退化 / 滚动升级挡复活 |
| P2-7 | **A-factory4** 孤儿 agent 累积 | Retire 不杀子 agent，反复 spawn/retire 堆孤儿占 quota |
| P2-8 | **I4** dead-letter 非持久 | 进程内存死信，重启即失，协作失败不可审计 |
| P2-9 | **S-st-S1/S-st-S2** 维度写死·共实例 | 1024 锁死（换模型=重嵌）；向量与事件账本共 PG 无隔离 |
| P2-10 | **R4/R6** RAG 默认关·阈值 0.4 | 记忆复用不显式开就不走；高维余弦 0.4 偏弱（知边界） |
| P2-11 | **S-mcp-S1** 工具名撞车 first-wins | 两个 server 同名工具只有先连的活，后到的 skip（**已有 warn，但 `winner` 标签误导**） |
| P2-12 | **S-mcp-S4** health 标了没人消费 〔已修 2026-10-13；**复核后补端点 join**〕 | discovery 标 `Healthy=false` 但 MCP Manager 不摘工具 → 半死 server 工具仍可调 |
| P2-13 | **S-mcp-S5** transport 自动断连不摘工具 〔已修 2026-10-13〕 | 断连后工具留 Registry 变"僵尸"（execute 才报 connection refused） |
| P2-14 | **S-mcp-S3/S6** discovery 默认关·工具规模 | 工具集启动时定死（不随网络发现）；工具越多 LLM 选择越稀释 |

---

## 三个共因（修"一类"胜过修"一条"）

```
① 认知 / 正确性没被持久化     → G1 · Q3 · A-dispo1 · A-F4
   （系统存的是"状态/进度"，不是"理解和做对没"）
② 引用 / 边界没校验 + 静默    → T1 · T2 · D1 · D2 · I1
   （任务/协作"看着没事其实卡住/早停/拿不到答"，且无告警）
③ 资源 / 预算静默耗尽         → T-F1 · A-factory3 · A-F3 · T-F3
   （回收/接住/重试 没有兜底告警，失败是静默的）
④ 外部依赖断联静默不可见      → S-mcp-S2 · S-mcp-S4 · S-mcp-S5
   （MCP 服务器挂了/半死/断连，工具静默消失或变僵尸，LLM 无感知）
```

---

## 只能先修 5 个（按"影响最大 + 一处改受益一片 + 可修"）

1. **G1** — 给 GA 加"正确率信号"（最低成本：golden 任务回放打分，权重高于 cost/latency；
   并把 gap/truncated 答案在 observer 侧记成 degraded 而非 success）。**方向性的，先修它否则进化越跑越偏。**
2. **D1+D2** — answer 出结果时**统一带"成因标签"**（正常 / 深度掐断 / L1 拦截 / 降级 / 有损续跑）
   + 对非正常成因**打告警**。**一处改，把 P0-3 整类"静默早停"变"可见"。**
3. **T1+T2** — 给 LLM 的 **`create_task`** 补上 `create_plan`/operator 已有的那类校验
   （复用 `CompilePlan` 的 `resolveDependencies` + `detectPlanCycle`，或等价增量校验）。
   **注意：`create_plan` 已经走 `CompilePlan`、operator 已有自带 `findCycle`，要补的只有 `create_task`。** **消除 P0-2 静默卡死。**
4. **T-F1** — `classifyFailure` 加**已知永久错识别**（4xx / 引用不存在 / 特定 4xx 类）
   + "重试 N 次仍未好"自动告警。**别白烧预算。**
5. **A1/Q3/A-F4** — **认知结构化**：把关键推理/工具中间态写进 `CognitiveState`（别留 opaque any）
   与 `Checkpoint`。**让"可丢弃"真能"可恢复思路"。**

> 这 5 个都是"缺陷级 + 修一个受益一片"。P1 其余（检索精排 / embedding 死信 / 协作回写 /
> quota 泄漏）和 P2（规模化/可观测）排在其后。

---

## 已做（确认非缺陷，别误当 bug 排进优先级）

- 唯一写者（lease + epoch fencing）、requeue 保 checkpoint、复活预算原子预留防风暴
- sweep 顺序（deadline→approval→lease）、单次 sweep 30s 超时、stale-winner 即时 reclaim
- operator 建图**已有**依赖存在性 + 环检测（**自带** `findCycle`，`agent_routes_tasks.go:611-617/:738`）+ `create_plan` 走 `CompilePlan`（`resolveDependencies`+`detectPlanCycle`）——**真正没校验的只有 `create_task`**
- **R3 hybrid 融合**（0.7 向量 / 0.3 lexical，已接生产 RAG）、**G1 的 `DimensionEval` 维度**（默认权重 0.25，只是运行时 observer 不产出）、**T-F1 的 `ErrInvalidArgument` 自动永久化**（`markPermanentStepCause`）、**S-mcp-S1 的撞名 warn 日志**（`manager.go:455`）——见各分文件"更正"段
- PG/pgvector 分工（向量表 vs 关系表，不混一张表）
- G3 样本门槛/冷启动中性（`MinTasksBeforeEvolve` 在 `dream_cycle.go`，非 `dream_cycle_ga.go`）、G6 进化改 L1 不在跑任务无感
- **G7** 说明项（非缺陷）：空闲 GA 零 LLM 调用——热路径（planner 量子）产生 ExecutionAttribution 成绩，冷路径（`ReplayScorer` 查 evidence store + 纯算术选育）事后消费，数据/评测解耦；可选 LLM Judge（`evolution.llm_scoring`）默认关且受 `MaxLLMCallsPerGeneration` 预算限制。详见 `genetic-algorithm.md` G7 + `system-internals.zh.md` §14.1
- **MCP/工具注册**：多 server 连接隔离（一个挂不连一个）、手动注册不被 discovery 误删（#51）、热重连先删旧再注新、Registry 拒绝重复名、discovery 默认关

---

## 修订记录

**2026-10-15 · 五问收口 ③④⑤ + ①②（生产 rerank / 4xx 映射）**：

1. **① 生产路径的 recency 项（P1-2 的真正闭合，含一处更正）**：上批把精排修在了
   `SimpleRetrievalService` —— **该类的非测试引用只有 examples** ⇒ **修在无人路径上**（已如实改台账）。
   生产真路径 `store_adapter → HybridSearch → ScoreHybrid` **本来就有宽召回 + 加权重排**，**缺的只有 recency**。
   已落：recency 第三项 + **相对权重按可用项归一** + 复用 `EffectiveObservedAt()` 的年龄口径 +
   **默认权重 0**（SQL 窗口已按 updated_at 偏置 ⇒ 分数里再来一次是重复计入，开不开要实测）+
   **`postgresstore.WithDefaultWeights` 一个装配点覆盖三条检索路径** + 配置 `knowledge.recency_weight`
   + 两份示例配置同步；**dedup 显式用纯相关权重**（阈值本就向量化，但候选窗口会受影响）。
   **反证**：默认权重塞 recency → `want legacy 0.85` 失败；忽略存储默认权重 → `store default was not applied`。
2. **② T-F1 的生产端映射**：工具层把 HTTP 状态映射进既有 9 个哨兵（`core.HTTPStatusError`，一处定义），
   `failure.go` **一行未改**；三处接线（`HTTPError.Unwrap` / `Result.Err`（`json:"-"`，协议不变）/
   `bridge.resultFailureCause` 优先用 cause）；**未映射状态一律 `nil`（可重试）**，靠既有"未分类第 2 次起 Warn"兜底；
   `http_request.go` **刻意不映射**（404 是观察结果）。**反证**：Unwrap 不给类 → 404/503 双双失败；
   bridge 丢 cause → 只剩消息。顺手修掉 `%!w(<nil>)` 伪影。
3. **③ arena 门吃到确定性锚**：用例带上 `ExpectedOutput`/`Checks`（原先装配即丢），`ArenaCase.String()`
   **只渲染 Input ⇒ 提示词一字未改**（代码里有"收紧 rubric 会毁显著性"的实测），压分在 **LLM 之外**做，
   **单条 + 批量两条路径都接**。**反证**：去掉压分 → 错答案拿满分。
4. **④ 两个 30s 单源化** + `Duplicate` 带 `PendingSince`/`PendingUntil`（派生，且标明是估算）+ 修掉两处过时注释。
   **反证**：忽略注入 → `the injected value was ignored`。
5. **⑤ 跨 method 切换 + `hnsw.ef_search`**：目录查询扩到 `pg_am.amname`；不一致 → **一次性、响亮、BLOCKING**
   重建（保留名字与 partial 谓词）；**空期望 = 不评判**（迁移命令没有配置可比）；probes/ef_search 合并为
   一个 `VectorSearchTuning`，走**同一条事务路径**。**反证**：判定恒 false → 不一致用例失败。
6. **本批自查（比功能更该记）**：① 我一度写出 **7 个参数**的函数（§1 上限 5）而**门禁不查这个**；
   ② `TestHTTPError_UnwrapExposesTheClass` 写在错包 ⇒ **从未编译**，而我说"绿"时只看了 `ok` 行
   ——**第三次"验证了过滤器而不是事实"**；③ 留下一个未使用的 `pendingAskExists`，被 `make check` 抓到后删除。
7. **未做**：**⑥ `eval_suite` 默认指 golden** —— 前置是"默认路径不存在会让 boot 失败"（`bootstrap_evolution.go`
   对非 `errEvalGateNotConfigured` 的错误直接 return）且默认 suite 会**静默武装 arena 门**（与其自称 opt-in 矛盾），
   **等拍板**；两个零生产引用的 service **待清**（独立一步）；`genome` 包 `TestSUSSelection_UniformDistribution`
   **既有 flaky**（连跑 3 次通过、本批未动该包）。

**2026-10-15 · 二轮复核（H1/M1/M2）+ P0-1(ii) 最后一跳的接线闭合**：

1. **H1（高）· 探针"接了不生效"——与 INDEX 的"已修"不符**。初版只在 `VectorSearcher.Search` 裸发
   `SET LOCAL ivfflat.probes`，而 **SET LOCAL 只在事务块内生效**（注释自己都写了）⇒ Postgres **静默忽略**；
   更关键：**真正跑 RAG 热检索的 `KnowledgeRepository.SearchByVector` 当时连这条逻辑都没有**。
   ⇒ **配了探针的部署两条路径都没生效**，而台账写"已修"。**修法**：`postgres.WithVectorSearchProbes`
   **按句柄形态建事务**（`*sql.Tx` 直接用 / `*Pool`、`*sql.DB` 各自开 / 都不匹配则**响亮 Warn**
   "已配但无法生效"），接到**两条路径**（热路径抽出 `searchByVectorQuery`，顺带把该函数压回 §1 的 100 行内），
   探针值经 `repositories.WithSearchProbes` 由装配点注入。
   **测试**：sqlmock 断言 **BEGIN → SET LOCAL → 查询 → COMMIT**、失败必回滚、未配置不开事务、
   已是事务的句柄**不嵌套开事务**、不可用句柄仍执行且告警。**反证**：去掉建事务分支 → 事务用例失败。
2. **M1（中）· 静默面从"不回写"平移到"回写任务挂了没人知道"**。`DeliverAgentReply` 只要 `Create` 成功就
   返回 nil，回信任务后续在 fabric 里跑挂无人知晓；且幂等 id 让同一任务**永远不能再建** ⇒ "投递失败"
   变"答案永久丢失"。**修法**：① 回信标记常量**定义在 `taskfabric`**（agentsyscall 已依赖它，反向成环；
   单一词表），经 `DecodeCheckpoint` 识别；② `failureVisibilityReason` 增加回信分支——**即使本会保持安静的
   `retryable=false` 也 Warn**"答案永远到不了 asker"；③ `redeliverReplyTask`：已存在任务为 `FAILED` 时
   **先删（带墓碑）再重建**并 Warn，活着的状态视为"在路上/已读"。
   **测试**：规则表 +3 行、识别函数单测、ask_reply 侧"失败后可重投"契约（真跑到 FAILED 再投递，断言回 READY）。
   **反证**：去掉发声分支 → 规则表失败；重投直接 return → `state = FAILED, want READY`。
3. **M2（中）· 新鲜度项的回归面**：`created_at` 是本批才随 metadata 带出的，**存量候选没有它**。
   已锁 `TestRerankCandidates_UnknownAgeDropsOnlyTheRecencyTerm`（无时间戳的候选**仍被返回**、
   分数等于"关掉新鲜度项"的值、不高于同文本新候选）。**反证**：把未知时间当"很旧" → 用例失败。
4. **P0-1(ii) 最后一跳（回放接选育）的诚实口径**：**"对所有候选同一组题"这个可比性条件本来就满足**——
   G3 门就是"用配置的 suite 对每个候选跑一遍"。缺的是两件可见性，现已落：
   - **语料的锚定率在装配期读得出来**：`eval.CountAnchoredCases` + `buildEvalGate` 日志——
     一条语料声明了确定性检查就 Info（带 anchored/cases），**一条都没有就 Warn**
     （"门的准确率只是判官意见"）；
   - **判官的一致率运行期真的被读**：`eval.NewAgreementReportingEvaluator` 装饰器在
     **计数变化时**记录（`anchored/lenient/harsh/broken/leniency_rate`），并**由装配守卫测试**锁住
     "注册的必须是装饰器"（否则读数永不运行）。
   **仍未做（明确）**：把 `evolution.gates.eval_suite` 默认指向 `test/eval/golden.yaml`——
   那是运维配置决定，代码不该静默覆盖；以及**per-candidate 的独立运行时回放**（要为每个 genome 实例化
   运行时，属架构方向，需拍板）。
5. **自记（测试比代码更该记）**：装饰器那条"计数器不动就安静"的用例最初走的是**全零分支**，
   被"还没有锚不是新闻"的守卫短路，**根本没覆盖去重**——反证时才发现（改回不去重它照样绿）。
   已改为"先锚定、再不动"；现在反证能准确失败（4 条 vs 1 条）。

**2026-10-15 · code review 自审：修掉我自己引入的 9 条（其中 2 条推翻上一批的结论）**：

> 结论性的一条：**"功能已实现"与"功能会生效"是两件事**，本批有 2 条属于后者没做到。

1. **回信模块（P1-5）写了错的 payload 键** ⇒ L2 cognition 只读 `task.Payload["input"]`
   （`planner_context.go`），初版只写 `ask_reply` ⇒ **答案进得了 fabric、模型读不到**。
   已同时写 `input`（人可读一行，含来源与主题）；测试补上对 `input` 的断言——**初版只断言信封非空，
   所以缺陷下测试仍然是绿的**，这是比缺陷本身更该记的一条。**反证**：不写 `input` → 用例失败。
2. **回信模块的幂等 id 只含问题不含答案** ⇒ 同一主题的**第二次合法提问**（30s 去重窗口过后）会被当
   "已投递"**静默丢弃**。已把回信内容并入指纹（同内容仍幂等）。**反证**：去掉内容 → `task count = 1, want 2`。
3. **索引复核（P1-3）按猜的名字查索引** ⇒ 迁移建的是 `idx_<table>_embedding`，而代码查
   `<table>_embedding_idx` ⇒ 每次启动都跳过，**整个能力是死代码**。已改为按 (表, 列) 查目录，
   并保留索引自身的名字与 partial 谓词。**反证**：改回按名查 → 守卫用例失败。
4. **`lists` 自适应规则会让小表变慢**：`rows/1000` 假定 probes 被调大，默认 `probes=1` 时 500 行会得
   1 个列表、扫全表。已改为取 `rows/1000` 与 `sqrt(rows)` 的较大者（单次探针约 `sqrt(rows)` 行）。
   **反证**：只用 `rows/1000` → `ivfflatListsForRows(500)=1, want 22`。
5. **`numeric` 检查要求整串是数** ⇒ "The answer is 56088." 会被判错、准确率归 0（把信号做**错**，比薄更糟）。
   已改为在答案中扫描数字，并如实记录接受的宽松方向（提到正确数字但反驳它也会通过——下限不是测谎仪）。
   **反证**：改回整串比较 → 三条正常答法用例失败。
6. **词面项对中文失效**：中文无空格 ⇒ 整句一个 token ⇒ 词面项在**判官提示词与语料都是中文**的场景里≈0。
   已加无空格脚本的 bigram 切分。**反证**：关掉 CJK 分支 → 中文用例失败（0 ≠ 0.875）。
7. **死信告警每 tick 一条**（一条修不好的内容 ≈144 条/天，淹没真告警）。已改为**按状态变化记录**。
   **反证**：去掉抑制 → 同状态被重复报出。
8. **回放 `Mean` 单独看会美化坏掉的运行**。已加 `Scored/Total` 与 `Coverage()`；并记录判官一致率计数
   **有状态**（比较不同候选应各建实例）。
9. **启动期 `count(*)`**（千万行表上每次启动固定成本）。已改为优先读 `pg_class.reltuples`，不可用时兜底。

**未修、已记录的小项（刻意留待下一批，避免churn）**：`RerankScore` 每候选算两次；
`rerankCandidates` 用 `map[int]float64`（切片足矣）；`len([]rune(...))` 的命名。

**2026-10-15 · P1 全部收口（P1-1…P1-5）+ golden 回放驱动落地**：

1. **P1-1 T-F1**（`fabric/task/failure.go`）：已知永久错**窄表**（`ErrNotFound` 族 / 非法参数 / 配置 /
   权限 / 已存在 / 环 / 超过重试上限；`errors.Is` 匹配）+ **未分类原因重试到第 2 次起或预算耗尽时
   Warn**。超时/限流/配额/熔断/`ErrInvalidState`/密钥过期**仍可重试**（表刻意窄）。**反证**：停用识别 →
   not-found 各行失败。
2. **P1-2 R2**（`services/simple_rerank.go`）：真因是**生产路径早就在宽召回（TopK*5）却没有精排**，
   那一跳补上：相似度 + 词面覆盖 + 新鲜度（缺失项剔除并归一）。**不是 cross-encoder**（仓库无模型），
   如实标注。另发现并补上一个"接了不生效"的事实：`SearchByVector` 没把 `created_at` 放进 metadata。
   同时记录：三个旧 evaluator（`ExactMatch`/`ToolUsage`/`KeywordPresence`）是**生产死代码**。
3. **P1-3 R1**（`postgres/vector_index.go`）：真因是"没人再看 lists 一次"。单一 DDL 源（三处写死改为
   一处）+ lists 按行数自适应 + `MigrateStorage` 收尾做 2 倍门限复核重建 + 检索期 `SET LOCAL
   ivfflat.probes` 可配 + **HNSW 选项**。未做：存量换 method 需重建（数据面决定）、`hnsw.ef_search` 未接。
4. **P1-4 D1**（`postgres/embedding_queue.go` + `ares_bootstrap/embedding_worker.go`）：可行动性拆分
   告警（pending / given-up）+ **每条内容只自动重放一次**的原子重放（`auto_replayed` 标记**随死信搬运**，
   否则重放再失败会每 tick 循环）+ tick 顺序改为**重放 → 清理 → 报告**（否则保留期会静默决定内容
   不可恢复）。`RequeueDeadLetter` 此前零生产调用。
5. **P1-5 I1**（`agentsyscall/ask_reply.go` + `serve_peer.go`）：回信**作为任务**投递给 asker
   （`(asker,peer,topic)` 哈希 id → 幂等；无 L2 能力/不存在 → 响亮失败且不排队）+ 工具描述同步改口。
6. **golden 回放驱动**（`eval/replay.go`）：语料**真跑起来**了 —— 顺序执行、逐案记录、
   报告 `AnchoredShare` 与判官↔事实一致率；`replay_test.go` 有一条**对仓库里真实 `golden.yaml` 的
   接线守卫**（语料与驱动不一致时直接失败）。至此 P0-1(ii) 的"判据 + 语料 + 体检 + 回放"四件齐了，
   **仍未做**：把回放接到 evolution 前的那一步（定时触发、跨候选共享子集的编排）。

**2026-10-15 · P0-1(ii) 第二步开工：给准确率信号加"确定性锚"（分层 oracle + 语料 + 一致率）**：

1. **oracle 形态定案 = 分层**（新增 `internal/runtime/eval/ground_truth.go`）：底层是**确定性检查**（零 LLM：
   `exact`/`contains`/`numeric`/`tools`/`no_error`），上层仍是四维 rubric 判官。**判 Fail → 准确率归 0、
   判官无翻盘权**（`dimension_judge.go` 的 ground-truth arm），且**排在 provenance floor 之后** —— 反证显示
   若不压，"答错 + 截断"会拿 0.5，**高于"什么都没产出"的 0**。判 Pass **只做下限、不做上限**（不送分）。
   - **复用而非新造**：执行者是既有的 `ResultVerifier`（Ch.8 底层校验器，never calls an LLM、
     cannot prove success → Uncertain）—— 它此前**生产零调用**（只有构造器自身），又是一处"写了没接线"。
   - 语料**逐条声明**检查种类（`TestCase.Checks`）；**未声明即不改行为**（不做隐式回填，否则一条旧
     `expected_output` 就会把存量套件改成 0）。顺带修掉 `types.go` 那句与实现不符的注释
     （原文称 `ExpectedOutput` "for exact match evaluation"，实现里根本没有 exact-match 路径）。
   - **实机反证**：停用该 floor → 核心用例报 `must floor the score to 0, got 1`（判官给错答案满分）与
     `got 0.5`（错答案高于 gap），恢复即绿。
2. **语料第一批已落**：`test/eval/golden.yaml` 35 条 + 存量加锚（`basic.yaml` tc-001 → `contains`；
   `tools.yaml` 4 条 → `tools`，tool-001 另加 `numeric`）= **40 条带确定性检查**，按
   `axis:factual/prose/compute/tools/adversarial/format/safety` 分层，**每条声明自己的检查种类**。
   `axis:adversarial`（自信的错答案最便宜处）是必须保住的一格。
3. **触发方式：已落离线/CI 一半**（`corpus_test.go` 语料体检：声明的检查必须可解析、种类必须已知、锚定数
   不低于 `minAnchoredCorpusCases`）；**evolution 前确定性回放未做**（可比信号必须"对所有候选同一组题"，
   活流量的不重叠时间窗 + 低流量退回 0 都做不到）；**定时触发未做**。
4. **一致率可读数（新增）**：`LLMJudgeEvaluator.GroundTruthAgreement()` →
   `anchored/lenient/harsh/broken` + `LeniencyRate`，lenient（判官给确定性判错的答案以分数）会打 Warn 日志。
   **这是"有多少准确率信号是判官主观"的唯一量化**；没有它，语料在长、判官在变宽，信号全程"看着合理"。
5. **诚实边界**：本批的准确表述是"**准确率信号里有一部分已可复核**"，**不是**"GA 已经会做对了"。
   锚的覆盖面 = 声明了检查的语料面（40 条，且多为单事实/单算术）；无锚用例仍只是判官意见，占比可由
   `GroundTruthAgreement` 读出。

**2026-10-14 · 复核（上一批加固的自审 + 运维通告）**：

1. **`OutOfRange` 两处漏计已修**：① 维度通道的累加曾在 `if dimCount > 0` 内 → **整通道越界**时计数为 0、
   而告警照发（数字与日志互相打脸）；② 越界自增曾在**作用域过滤之前** → `WindowToolStepAt` 把他人的坏值
   算进本工具步。两条各有回归测试。
2. **`tieBreakJitter` 表述更正**：`JudgeScaleCeiling / 5000` 是"单一来源"，**不是自适应**（那个常量仍需人工维护）。
3. **⚠️ 运维通告（破坏性，包外）**：记忆打分器不再读裸 `cost`/`latency`/`regression`（那是多目标**维度名**，
   界 {0,100}），改读 `cost_units`/`latency_seconds`/`regression_units`。仓库内无生产者写旧键；**包外若写过，
   惩罚会静默归零**（不会报错）——请改名。
4. **`ChildScore` 收口（第 5 条的后续）**：追到底——`scoreImprovement = CandidateScore − BaselineScore` 是
   **0–100 的"点"**，所以 `ChildScore = scoreImprovement + parent.Score` **同量纲，没问题**（上一轮把它列为
   "未闭合"是把增量误当 [0,1] 比值，**此处更正**）。但**阈值是真错**：默认 `0.01` 被拿去比"点"为单位的增量
   = 百分之一**点**，与该字段"低于此阈值视为噪声"的 doc 恰好相反 → `ImprovementSignificant` 与 raw/significant
   计数**几乎恒真**。已修：`defaultMinLineageImprovement = 0.01 × JudgeScaleCeiling`（= 1 点 = 1%，从量纲推导），
   两处比较点收敛为唯一阈值解释 + 唯一规则，删掉重复字面量兜底；测试 + 反证
   （退回 0.01 → `improvementIsSignificant(0.01, 0.01) = true, want false`）。
   **⚠️ 迁移指令（存量配置会静默翻转，必须照做）**：`min_lineage_improvement` 是**用户可配置**项，旧版本
   默认 `0.01` 且语义是 **[0,1] 比率**；本批之后同一字面值 `0.01` 的含义变为 **0.01 个点**（≈噪声 →
   显著性标记几乎恒真）。**升级前请复核任何显式写的 `min_lineage_improvement`，按 0–100 尺度改写
   （旧 `0.01` → 新写 `1`）**。为免"升级后 flag 全亮却无从查起"，代码在**唯一解释点**对"看起来像旧比率"
   的值（`0 < v < 0.1`，即不足 0.1 点、任何 scorer 都无法分辨）**一次性告警**，同时给出两种读法的换算
   （`configured × 100`）；该启发式是纯函数、有测试，且只报一次。
5. **⚠️ 运维通告（可见变化）**：`Strategy.Score` 统一到 GA 的 0–100 量纲。此前 `cmd/ares` 的
   `WriteActiveScore`（强制输入 [0,1]）与 dream-cycle 的胜率都**原样写入**该字段，而字段本身是 0–100（每条 scorer
   如此，且被 `population.go`/`multi_objective.go` 按数值比较）→ 面板上的活跃策略分数会**比之前大 100 倍**
   （这才是正确值）。公开面新增机器可读标注 `ScoreMax` / `score_max`（= `JudgeScaleCeiling`），负值 = 未评估哨兵。

**2026-10-14 · 后续（跨量纲接缝扫描与加固）**：

1. **可观测性**：`WindowResult.OutOfRange` 报告被**跳过**的越界证据条数（无 value 的记录不计），并对每个
   (source, kind) **限频告警一次**（上限 32 + 抑制通告）。此前"指标里少了样本"完全哑静默 —— 与
   `dimension_eval` 权重静默空转同一失败模式。
2. **夹具审计**：抽查 promoter/experience/聚合器/eval/coordinator/记忆打分器 六处标量来源，**没有第二处
   错量纲夹具**（唯一错的就是已修的选育混合）；新增守卫测试锁"混合结果恒在 judge 量纲 + 兜底 50 是契约"。
   **剩余 UI 级接缝（未动）**：`evoapi` 暴露的 `Strategy.Score` 是 0–100，与 [0,1] 的窗口均值同面共存，
   建议展示层命名区分。
3. **常量/单位统一**：回归测试器的平局抖动改为从 `judgeScaleCeiling` 推导（+ 意图测试：平局须给出接近随机的
   胜率、1 分之差不可被抖动翻盘）；记忆打分器的惩罚参数键改单位自明（`cost_units`/`latency_seconds`/
   `regression_units`），不再与多目标维度名（`cost`/`latency`，0–100 界）共键，碰撞有测试钉死。

**2026-10-14 · 复核修复（量纲 P0、溯源配对、可观测与跨模块断言）**：

1. **P0（真 bug，已修）：选育混合的量纲混用**。`withMeasuredEvidence` 把聚合器的 **[0,1]** `Mean` 与
   **[0,100]** 的判官（`service.DeterministicScore` base 50、clamp [5,100]，LLM 提示词 "0 to 100"，
   兜底 `ConstantScorer(50)`）直接相加，且注释**断定"两边都是 [0,1]"**。实测后果：0.95（优）与 0.2（差）
   只差 ~1 分（都落在 ~13），信号被淹没且**证据越多分越低**。修法：`judgeScaleCeiling = 100` 把实测抬到
   judge 量纲再混（结果仍留 judge 量纲）；更正 `scaleUpFitnessToCoordinator` 的错误声明（它把
   `Strategy.Score` 算进 [0,1] 那侧，是温床）。测试改用**真实 0–100 judge**（旧测试用 0–1 假 judge，
   正是它掩盖了 bug）+ 新增 `TestRunScorer_MeasuredEvidenceIsNotDrowned`。反证：去掉提升 →
   0.95 实测 = **23.21**、0.2 实测 = **22.65**（不可区分），恢复后 93.75 / 37.5。
2. **🟠（已修）：body 与 provenance 可能来自两轮**。两个读函数各自选择答案（一个"最新可读"、一个"绝对最新"），
   在"最新答案 body 不可读"时会把 A 轮文字配 B 轮溯源，污染确定性 floor。修法：抽 `adoptedAnswerTask`
   作为**唯一选择**（最新可读优先，全不可读时回退最新并如实报"无 body"），两个公开函数共用它。
   测试：`TestSessionAnswer_AndProvenanceDescribeTheSameTurn`（反证：退回独立选择即失败）。
3. **🟡（已补）**：回收日志打印 `id(STATE)`（`State` 字段真被下游消费，避免把"暂停回收"读成"空闲回收"）；
   状态表补"SUSPENDED 且 busy → 保留"一行（防御"暂停只在量子边界"的不变量，并写明唯一触发者是未守卫的
   `SetBusy`）；新增**跨模块集成测试** `suspended_handoff_test.go`：回收挂起 agent → 其任务经
   `CheckExpiredLeases` 回 READY（checkpoint 完整）→ 另一 agent 接手，并顺带钉住**顺序依赖**
   （`LeaseSnapshot` 不过滤过期租约，所以必须"先 requeue 后回收"，即 `runSweepChain` 的 3→4）。
4. **文档口径**：G1 明确"floor + 溯源**实质缓解** reward hacking，但不替代正确率锚（golden 回放仍未做）"。

**2026-10-13 · 复核修复（回收闭环的第三态与边；GA 选育读实测）**：

1. **agent 回收：SUSPENDED 出口**（`collaboration-and-factories.md` A3「第四轮」D）。SUSPENDED 此前是
   三个出口全不通的格子（不被调度、不被回收、无 `Resume` 生产调用方）→ 被挂起的 agent 永久占额度 +
   Cognition。新增策略位 `SuspendedTTL`（默认 30m，零值禁用），复用**同一条** `retireLocked` 出口，状态覆盖
   收敛到 `reclaimTTLFor` 一处；安全性靠既有租约机制（挂起任务保留租约 → 过期 requeue → drain 拾回）。
2. **agent 回收：`f.children` 随回收清理**（同上 E）：边随记录一起删（含空列表删键），消除悬空引用并把
   这个只增不减的结构**有界**到"注册表仍持有的 agent"。
3. **GA：选育读实测 fitness**（`genetic-algorithm.md` G1 新增段）。此前生产证据只进晋升/回滚门，
   选育仍只看判官 → 同一策略两套结论。现由 `popAdapter.evidence = agg`（**同一个**聚合器）接入，
   打分按收缩估计 `(n·measured + k·judge)/(n+k)` 混合（k=3，无证据时与判官完全一致）。

**2026-10-13 · 复核修复（H1/M1/L1/L2：truncated 真跑、provenance 确定性、文档对齐）**：

1. **H1（高，真缺陷 + 真跑）**：`truncated` 档此前**没有真跑 e2e**，`TestAdversarialE2E_*` 是 `t.Skip`。
   追查后**根因不是"SDK 到不了 planner 的 growth bound"，而是一个生产缺陷**：深度守卫确实触发
   （planner 日志 `max plan depth reached, depth=1 max=1`），但答案节点的 arg 在 engine 里是
   **字符串化**存储的（`argsMetadata` 对每个值 `json.Marshal`），而 `answerCognition` 的读取处断言
   `.(bool)` —— 标记被每个撞墙会话写入、被**所有**读取方读丢（`answer_cause` 是字符串，所以只有它没暴露）。
   后果：P0-1(i) 的"`truncated=true` → score=0.5"与 eval floor 的 truncated 档**在生产侧从未生效**。
   **修**：`answerArgTruncated` 按 wire 形状读取（字符串 `"true"`/内存 bool 都可，非规范拼写不得当 true）；
   `answer_truncation_test.go` 改为**走 `argsMetadata` 的真实 wire 形状**（旧测试传真 bool，正是它掩盖了缺陷）
   并加 bool 形态与"非规范拼写不算 true"两条。**H1 的 e2e 现已真跑通过**（`t.Skip` 删除，脚本按实测调用序
   给两次响应：planner 第 1 轮要工具 → 答案合成）。**实机反证**：停用该读取 → 单测报
   `truncated marker must ride the result metadata`、e2e 报 `the depth guard must have forced the answer`
   （正是当初 skip 的理由），恢复即绿。
2. **M1（中）**：`SessionAnswer` / `SessionAnswerProvenance` 改为**确定性取"最新 round 的答案"**
   （`sessionAnswerTasks`，按 `sess/<sid>/d<N>/…` 的 N 降序、同轮按 id），取代原先
   `fabric.IDs()`（map 随机序）的"第一个匹配"。多轮会话（重入会话的上一轮 + 本轮）此前可能两次运行
   取到不同的答案，且 body 与 provenance 可能来自不同轮次。**实机反证**：退回 map 序 → 新测试报
   `iteration 0 must read the newest round's facts, not the map's luck`（重复 25 次迭代是刻意的，
   单次断言对旧实现有一半概率假绿）。
3. **L1（低）**：`test/eval/adversarial.yaml` 的 `adv-gap-001.runtime_requirement` 原文称 gap 由
   "per-agent 空工具白名单"触发，与 e2e 实际路径（**空模型**：每轮无 content 无工具调用）不符。已改为如实描述，
   并写明**决定 gap 的是"没有 content"而非"工具被跳过"**（L1 拦下工具但模型给了 content 时，终态是
   `supplied` + `answer_cause=l1_blocked`，属另一档更高分的结局）。`adv-trunc-001` 补上 e2e 验证指针与
   实测调用序约束。
4. **L2（低）**：`eval.ProvenanceReporter` 的 doc 明确"**仅限串行 runner**"：该报告是 last-Execute 语义，
   并发 runner 下每个 case 都会被判成"最后完成的那个 case"的事实，且**静默**（事实对任何 case 都像真的）；
   需要并发就得上按 case id 键控的报告，而不是给它加锁。

**2026-10-13 · P0-1(ii) 第一步：golden → `dimension_eval` 证据链路打通**：

- **核实**：`dimension_eval` 的机制三件都在（四维判决器 / `DimensionJudgeBridge` / 权重 0.25），断的是
  **两处字符串契约**——桥写 Source `"dimension_judge"`（`Evidence.Source` 契约=verifier 名），而 aggregator
  按 Source `"dimension_eval"`（kind 名）查 → **全仓无此生产者，查询永远 0 命中**；且 payload 无 `"value"`
  数值键。**生产 `setupEvaluators` 还根本没接桥**。于是 0.25 的权重一直**静默空转**，GA 只吃 outcome+快省。
- **修**：`Evidence.Value`（payload `{"value":0..1}`，与门分共用 `normalizedDimensionMean` 一条规则）、
  `DimensionJudgeSource` 导出、aggregator 按 **kind** 查询、payload 解析改 `*float64`
  （**无标量不再被当成 0.0 计入**，避免把均值拉向 0 的静默错误）、`setupEvaluators` 开
  `WithDimensionAveraging()` + `WithEvidenceStore`（`ProvideEvolution` 新增变参 `WithEvalEvidenceStore`）。
- **实机反证（§7.1）**：查询退回修复前形态 → 端到端测试失败；恢复即过。
- **代价已记录**：门的评分由"一个总评分"变为"四维 rubric 均值"。同属 [0,1]，`min_score` 刻度不变，
  但**测量的量变了**。
- **未做（待拍板，勿当已完成）**：oracle 形态 / golden 语料规模 / 触发方式 —— 故
  **"gap 交差在 golden 回放里必须低分"的合同测试尚未落地**；通了的是**证据链路**，不是**判据**。

**2026-10-13 · P1 修复（agent 资源回收闭环 + MCP 可用性）**：

1. **P1-6 的三处残余全部处理**（详见 `collaboration-and-factories.md` A3「第三轮」）：
   - **A 墓碑回收器**：`Fabric.ReapRetired` + 策略位 `RetiredAgentRetention`（默认 1h）+ sweep 第五步；
     状态转移表进 doc（§11），年龄基准 = 退休时刻，未盖章的墓碑永不回收。
   - **B 死亡快照**：先核实**本来就有 count cap（1024）+ 已有测试锁定**（**更正我上轮"无 TTL"的说法**），
     再补**年龄上限 1h**（cap 只限条数不限字节）；并修正 `TestSnapshotStoreBounded` 的时间间隔，
     否则它会被年龄裁剪接管、悄悄不再测 cap。
   - **C 派发窗口**：置 busy 改**守卫取用** `Agent.TryBeginQuantum`（与 Retire 单赢家）+ 调度器准入闸
     `Scheduler.admitWinner`（抽函数压回圈复杂度门禁），配合活租约过滤，窗口结构性关闭。
2. **P1-7 / P2-12 / P2-13 已修**（详见 `service-loading-and-mcp.md` 的 S2/S4/S5「修复」段）：
   新增**每 server 一个可用性单元** `ServerHealth`（单源真相）：transport 自行死亡 / 连接失败 /
   discovery 健康判决三个写入方，工具执行与工具描述两个读取方 → 工具不再"静默消失"、不再"变僵尸"，
   且 **S4 的"标了没人消费"有了真消费者**（先核实 `discovery/health.go` 确有 `MCPHealthChecker`，
   事件名即 server 名，是真路径而非死接线）。
3. **未做（如实记录）**：不往 LLM 上下文注入系统提示（要动 planner 的 prompt 组装，属另一条路径）；
   `Kill` 快照的"字节量"上限靠年龄（1h）而非按字节裁剪；`f.children` 的 provenance 边故意保留。
4. **复核修正（review 抓到，均已修；原"已修"标记属超前/漏改）**：
   - **S4 的 join key 断在命名空间**（真断裂，且**被我自己的手工对齐测试掩盖**）：discovery 用
     **端点派生身份**、manager 用**运维手填 config name**，中间无映射层 → 自动发现的 MCP server 的健康
     判决一律 `ErrServerNotManaged` 被**静默丢弃**，只有名字恰好等于归一 endpoint 才生效。已导出
     `discovery.ServiceKey`（复用同一归一规则）+ `MCPManager.ServerEndpoints()`（纯数据）+
     `mcpHealthJoin.resolve` **端点 join**，并保留**直接同名**这条；未管理服务改为**每身份一次、
     上限 32 条**上报（超限自身也明说），不再静默。**实机反证**：禁用端点 join → 测试以
     `the health verdict must reach the managed server …; got ""` 失败。端到端测试断言走公开
     `ListServers().HealthNote`（运维/模型实际读的那份），不再手工对齐名字。
   - `collaboration-and-factories.md` 中"扫描与退休**不嵌锁**"与同文件 F2 行冲突（第二轮漏改）→ 已改为
     "收敛在同一 `f.mu` 临界区"。
   - `ServerHealth.since` 由 wall clock 改为**可注入时钟**（与 fabric `WithClock` 纪律一致），并加
     确定性断言（原先只断言"非零"，测不出时长）。

**2026-10-12 · P0 修复进展（逐条先核实再修）**：

1. **P0-2（T1+T2）已修**：核对确认 `Create`/`createLocked` 对 `Dependencies` **零校验**，且
   `depsSatisfiedLocked` 把"依赖不存在"当**永久不满足**（`ReferencedDependencies` 注释原文承认此机制）。
   修法 = `Fabric.Create` 新增**显式严格选项** `WithResolvedDependencies()`（与插入同一临界区校验，
   新哨兵 `ErrDependencyUnresolved`），`create_task` 启用；**默认保持宽松**，因为 `runCollabGraph`
   依赖图内前向引用（提交顺序建节点）。**拒绝在模型边界转成可读结果**（`CreateTaskResult.Rejected`
   + `MissingDependencies`，`errors.As(*DependencyResolutionError)` 识别）而非工具错误 ——
   否则会判量子失败并经 `cascadeFailureLocked` 炸掉整条下游子图。
2. **T2 未加独立环检测（结构性覆盖）**：存在性校验后"创建序即拓扑序"，独立环遍历在 `create_task`
   路径不可达（自引用形态已显式拒绝）—— 加它会是被规范禁止的空壳机制。真正需要环检测的是
   `SetDependencies`（残留，未修，其宽松性供 L2 增长且已被测试锁死）。
   详见 `collaboration-and-factories.md` 的 T1/T2「修复」段。
3. **P0-3（D1+D2）已修**：核实结论 = D1 **机制已在**（Warn + canary 计数 + `truncated`），缺的是
   **外部可见性**；D2 **确实完全无标记**。修法 = 答案节点两把**正交**键进终态 metadata：
   `answer_cause`（`planned`/`depth_capped`/`l1_blocked`/`unlabeled`）与
   `answer_body_source`（`supplied`/`synthesized`/`gap`），外加 D2 的聚合告警。
   **未自造 metrics 设施**（仓库无 metrics 包），复用既有的"终态 metadata + 日志"机制。
   **未做**：outcome 层降级（属 G1/P0-1，`answer_body_source` 正是它要读的信号）。
4. **P0-4（Q3/A1/A-F4）已核实，未修 —— 待拍板**：机制成立，但**边界更正**：生产恢复是
   `runKernelRecoveryLoop` 的 **requeue（零认知转移）**，`RecoverTaskCheckpoint` 是
   **TEST/CHAOS-ONLY**（`cmd/` 无调用方）；`CheckpointEnvelope` 逐字段确认**无 reasoning 字段**；
   L2 图上"先 A 后 B"**可由图结构推出**，真正丢的是模型**自述理由**。
   方案 **A**（envelope 加"有界 Decision/Rationale" + schema 升版 + 喂给下一量子）vs 方案 **B**
   （承认边界 + 让两条路径可辨识）。**A 动 schema 与 planner 的 prompt 语义，按规范 §0.4 未擅自实施**；
   详见 `disposable-agent.md` A1「核实 / 修复方案」段。
5. **2026-10-13 拍板并动工**：采纳"**两步走**"——B（摘静默，立即做）+ A（Rationale）与 **P0-1 合并为同一个"认知持久化"专项**。B 的代码已落地（见下节「未做 / 待做」清单），A 与 P0-1 未动工。用户两条技术指示：epoch 用**每任务单调、局部解决**（实测发现 `f.epoch` 本就是**全局**计数、当"被交接"信号无判别力，遂改用 task 侧回队事实，不动栅栏令牌语义）；`RestoreFromStore` 折叠（已按此实现）。

---

## P0 专项：未做 / 待做（2026-10-13，"认知持久化"专项的完整清单）

> 状态：**B 的代码已进树且 B-4 专项锁定测试已写、B-5 台账已回填。**
> P0-1(i) observer 侧接线已落地（gap/truncated 答案记 degraded 而非 success）。
> **A（Rationale）档 0 已落地**（写/读两侧 + 4 条合同测试；**载体更正**——原
> `res.Metadata → StepCheckpoint` 通道对"第一次交接"不成立，改用 plan 节点 args mailbox，
> 详见 `cognition-persistence.md` §0.5）；P0-1(ii) golden 任务回放仍未动工。全部改动未提交。

### 一、B · 续跑路径可辨识（A6）——代码已落地，测试已写，台账已回填 ✅

**已落地**：

| 件 | 位置 | 说明 |
|---|---|---|
| agent 复活位 | `internal/fabric/agent/agent.go`（`Agent.revived`，mu 保护）+ `lifecycle.go` 的 `Recover` 置位 | `Recover` 是"把被保存认知装进 agent"的**唯一入口**（`RestartAgent` 走它），所以复活语义精确；`EventAgentRecovered` 事件新增 `revived: true`；**只置位不清除**（复活体终生是复活体） |
| 执行期戳记 | `executor.go`：`Agent.ExecuteStep` 读位 → `withExecutingAgent(payload, agentID, revived)` 盖 `executor_revived`（**false 也盖**） | "在场且 false"必须能区别于"缺席"——没走这层包装的量子根本没回答过这个问题。按既有设计**不落盘**（quantum-scoped，注释载明落盘会把某量子的执行者误归到后续量子的历史坑） |
| task 回队事实 | `internal/fabric/task/task.go` 新增 `Task.Requeued bool`；`fabric_lifecycle.go` 的 `CheckExpiredLeases` 与 `Fail` 的重试回队分支置位 | 两个置位点 = 租约过期（所有权人死亡）+ 重试回队，覆盖"工作被退回池子"的全部来源 |
| 折叠（耐久） | `restore.go`：`EventTaskExpired` 无条件置位；`EventTaskFailed` 且 `retryable=true` 置位 | 唯一过近似：最后一次可重试失败（该任务终态、永不再被调度、标志永不被读） |
| 派发期可见 | `internal/kernel/scheduler_quantum.go` 的 `ToModelTask`：`tk.Requeued` → payload `requeued`（键 = `taskfabric.PayloadKeyRequeued`，与 `PayloadKeyDegradedInputs` 同一跨包键契约） | 仅 true 时写 → **缺席即"首次运行"**；从 task 戳而非镜像进 envelope（payload 与耐久 envelope 共享，见 `copyPayloadMap`） |
| 终态暴露 | `internal/fabric/agent/l2graph.go` 的 `answerCognition.ExecuteStep`：`executor_revived` + `requeued` 进终态 metadata（与 `answer_cause`/`answer_body_source` 并列） | 从**原始 payload** 读（两键都是无前缀 envelope 命名空间；`argsFromPayload` 只暴露 `arg.` 命名空间） |

**已完成收尾**：

1. **B-4 专项锁定测试 ✅**：`revived_test.go`（agent 位 + 事件 + 不清除）、`answer_continuation_test.go`（终态 metadata 四场景）、`requeued_test.go`（lease 过期 + 重试 + 恢复折叠 + 已知过近似）。
2. **B-5 台账回填 ✅**：`disposable-agent.md` A6 段补"已修"记录 + A1 核实段补"B 已按正交两位落地"。

**已明确的取舍（已写进代码注释，勿推翻）**：

- **不用 `Lease.Epoch` 判"被交接"**：`f.epoch` 是 **fabric 全局**单调计数（`f.epoch++` 后赋给 `Lease.Epoch`），不是每任务单调 → `Epoch > 1` 对单任务无判别力；把它改成每任务单调等于改**栅栏令牌**的语义（安全机制），不做。
- **两个正交位，不合成 `requeue_fresh | snapshot_revive` 枚举**：合成枚举在"被交接 + 完成者也是复活体"的组合下无位可放——与 P0-3"成因/来源压成一个标签会丢信息"的实测教训同源。派生规则：`executor_revived=true` → **带脑续跑**；`requeued=true && !executor_revived` → **白盘续跑**；`!requeued` → 首次运行。
- `Requeued=true` **不证明**换了人：fabric **故意**不持久化前任 owner（`withExecutingAgent` 的注释载明落盘的历史坑），它只断言"被退回过池子"。

### 二、A · Rationale（认知持久化，方案 A）——**档 0 已落地（2026-10-13）**

> 落点、载体的**更正**（原 `res.Metadata → StepCheckpoint` 通道对**第一次交接**不成立，改用
> plan 节点的 args mailbox）、测试与"仍未做"（`record_rationale` 元工具），详见
> `cognition-persistence.md` §0.5「已落地与载体更正」。以下为原始设计记录，与 §0.5 冲突处以 §0.5 为准。

### 二·原始设计记录 · Rationale（认知持久化，方案 A）

> 详细设计（存什么 / 如何存、摘要分档、可靠性上限、Merkle 取舍、合同测试清单）见
> `cognition-persistence.md`。本节是清单，该文件是设计展开。

**载体决策（与原设想不同，暂不升 v6）**。证据链（全部已核实）：

```
planner 写 Result.Metadata[rationale]
  → scheduler_quantum.go 终态分支：outMap["metadata"] = res.Metadata
  → 该 map 作为 StepCheckpoint 进 envelope（StepCheckpoint 是 any，无版本门）
  → 下一量子 readNodeOutput：fabric.Task(nodeID) → DecodeCheckpoint → StepCheckpoint
```

- `extractOutputContent` 与 `PlanLoop.collectOutput` 只读特定键 → **加键不破坏既有消费者**；
- `readNodeOutput` 的注释原文："the envelope is the only place output lives (decision C)" → 下一量子**本来就从 envelope 读前驱输出**；
- `res.Metadata` 已是**既定的跨包键契约**（token 通道 `tokenUsageFromResult`、P0-3 的 `answer_cause` 同走此路）；
- **滑窗不需要**：L2 每轮是独立 task/节点（`SessionNodeID(sid, round, "plan", 0)`），每个节点的 envelope 只写一次 → 天然有界；yield 路径 `StepCheckpoint` 每量子**覆写** → 只留最新一份，不随 session 线性膨胀（规避 A3 那类"上限静默退化"坑的方式是形状选对，不是加淘汰）；
- **动手前待验证一件事**：yield（非终态）分支走 `out.Checkpoint`（与终态的 `outMap` 不同源）→ planner 在同一会话多次 yield 的场景下 rationale 是否也随 `out.Checkpoint` 携带，需确认或补齐；
- 若将来出现**外部契约**消费者，再把字段提升为 typed v6（forward-only、rollback 需 drain）——那时升版是自然演进，不是现在的预付成本。

**写侧（A2，用户已倾向；A1 不采纳）**：同一次 LLM call 带出"本轮决定+理由"。**机制坑（已识别，动手前必须定）**：工具调用回合 `resp.Content` 通常为空（planner 正是以 `len(resp.ToolCalls) == 0` 判定最终答案）→ 需显式契约，三选一：
1. `record_rationale` 专用工具（**倾向**：可校验、可截断、可测）；
2. 首个工具调用 args 带 `rationale` 字段（模型易漏）；
3. content 约定"要调工具必先写理由"（最便宜，但各 provider 对 tool-call 回合的 content 处理不一致，脆）。

**读侧**：`assembleContext` 现在只走 `tool/` 节点（`!strings.HasPrefix(step.AgentType, "tool/")` 直接 skip）→ 需显式把**前驱 plan 节点**的 rationale 读进来（一条 message，标注"上一量子的决定与理由"），这样同 agent 续跑与换人**都**能读到"为什么"。

**有界**：生产者侧硬上限（512~1024 char）+ 截断；per-node 覆写 → 无累积膨胀。

**可靠性上限（必须写进设计）**：rationale 是**模型自述**，是 reward hacking 的喂食点（"理由写得漂亮" ≠ "做对了"）→ 只能当判官**输入之一**，须与真实工具结果对照判定，**不得**单独作为高权重 correctness 通道。

**测试（合同测试，动手时写）**：换人后下一量子确实注入前驱 rationale（断言 `assembleContext` 的 message 序列含它）；有界截断；per-node 不累积；旧 envelope（无该键）空值兼容。

### 三、P0-1（G1 · 正确率信号）——未动，两半拆开接

| 半 | 现状 | 待做 |
|---|---|---|
| (i) 停止把 gap / 被迫交差 记为 success **〔已修 2026-10-13；同日补修 truncated 半场，见修订记录〕** | ✅ observer 侧已接线：`answer_body_source=gap` → score=0 且 Success=false；`truncated=true` → score=0.5 且 Success=false。**注**：`truncated` 这半场此前**在生产侧从未生效**（标记在节点 arg 的字符串化 wire 上被读丢），已于同日修复并真跑验证 | 信号从 checkpoint envelope 经 `answerQualityFromCheckpoint` 提取 → `task.completed` 事件 payload → `degradedAnswerPenalty` 读取 |
| (ii) 真正的 accuracy 维度 | **分层锚已落（2026-10-15）**：确定性检查（`ground_truth.go`）判 Fail → 准确率 0、**判官不可翻盘**；语料第一批 40 条带锚（`golden.yaml` 35 + 存量 5）；CI 语料体检已落 | **evolution 前确定性回放**（对所有候选同一组题）**未做**；**定时触发未做**；**语料扩容未做**（40 条只是下限）。全量细节见 `genetic-algorithm.md` G1 段 |

**专项内顺序**：(i) 接线（信号现成，即得）→ (ii) golden（可复核的对错 oracle）→ A（Rationale）作为判官输入 + 续跑思路放最后。A 排后的理由：其价值一半在 G1，而 G1 的主信号来自**可复核的 golden**，不来自自述理由；先有 golden 这个"地"，rationale 才有"用"。

### 四、其余未做（前几轮已记录的残留，集中在此防丢）

| 项 | 状态 | 出处 |
|---|---|---|
| `SetDependencies` 无环/存在性校验 | 残留（宽松性供 L2 增长且已被测试锁死；但它能给**已存在**任务改边、可闭合环） | T1/T2「修复」段 |
| `SearchSimilarTasks` 无 user 维度 | 残留（作用域已改按请求 `tenantctx`；user 隔离靠查询词"软"约束，硬隔离需加 scope 参数） | M5 旁路清点 |
| `StoreExporter` 无人实现 | 残留（`ExportStore`/`ImportStore` 永远走 `Query`/`Save` 回退；接口只是扩展点） | plan 文档 §8.5 |
| M2.5 关系门 / M4 `BuildReport` / M6 `TieredRetriever` 接生产 | 未排期（`TieredRetriever` 须先解其定义处自陈的两点：整店入内存 per query、需 namespace 来源） | plan 文档 §8.2 |
| `create_plan`/operator 建图路径 | **无需动**（一个走 `CompilePlan`、一个自带 `findCycle`）——列出防误判 | T1 路径表 |

**2026-10-12 · 源码比对修订（对 dev 分支逐条核对源码）**：对 7 个分文件全部缺陷 ID 逐条比对代码后，
修正以下内容（各分文件内已就地标注"更正/实测"）：

1. **`collaboration-and-factories.md` T1/T2（事实性更正）**：`create_plan` 实际走 `k.fabric.CompilePlan`
   （有 `resolveDependencies`+`detectPlanCycle`）；operator `POST /api/graphs` 走 `runCollabGraph` 的**裸
   `Create`** 但**自带**边存在性检查 + `findCycle`（**不经过 `CompilePlan`**）。真·无校验的**只有 `create_task`**。
2. **`genetic-algorithm.md` G1**：`FitnessWeights` 实含 `DimensionEval`（默认 0.25）；缺的是运行时
   observe 路径不产出该通道。G3 锚点 `dream_cycle_ga.go` → `dream_cycle.go`。
3. **`retrieval-and-storage.md` R3**：hybrid 融合**已实现**（`knowledge/hybrid.go:156` 0.7 向量/0.3 lexical，
   已接生产 RAG），原"未坐实"改判"已做"。R2/S1/S2 补边界澄清。
4. **`recovery-and-fault-tolerance.md` T-F1**：已有 `markPermanentStepCause` 把 `ErrInvalidArgument`
   自动永久化。A-F2 `handleStaleWinner` 在 `scheduler_execute.go`。A-F4 checkpoint envelope 不止
   `StepCheckpoint`。
5. **`service-loading-and-mcp.md` S1/S2/S4/S5**：S1 撞名**已有 warn 日志**（`manager.go:455`）；
   S2 无 `continue`（循环自然继续）；S4 事件名 `EventHealthChanged`；S5 `:312` 是 `RefreshTools` 非 `DisconnectServer`。
6. **`disposable-agent.md` A1/A3/A5/A6**：`CognitiveState` 字段补全；快照淘汰按 `DiedAt` 最旧（非 FIFO）；
   `StatePaused` 线值 `"SUSPENDED"`；换人路的 `RecoverTaskCheckpoint` 是 TEST/CHAOS-ONLY。补 `recovery.go` 路径说明。
7. **`dynamic-dag-and-quantum.md` D1**：撞墙已有 `logger.Warn` + canary 计数，缺的是面板/告警规则。

> 其余 ID（Q1-Q4、D2-D6、G2/G4/G5/G6、A2/A4、I1-I4、T3/T4、A1/A3/A4、T-F2/T-F3、A-F1/A-F3、R1/R4/R5/R6、S1/S2/S3、S3/S4/S6 等）
> 经核对**方向与机制成立**，仅有个别行号漂移（已在分文件标注），不影响结论。

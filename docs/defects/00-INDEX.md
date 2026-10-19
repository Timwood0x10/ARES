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
| P0-1 | **G1** GA 无"做对没"信号 〔证据链路已通 2026-10-13；选育侧已接入；**判据四项已落地**：oracle 分层 / **语料 73 条带锚（下限 65）** / 默认 suite / arena 确定性锚；**per-candidate 真实回放已端到端完成 2026-10-17**〕 | fitness 只奖励"成功+快省"，没 accuracy → **越跑越会糊弄**。**回放四步全落**：① 候选**本体（含 `Params`）抵达 scorer**（门不再只传 `PromptTemplate`；`instructionsOf` 同时接受 string 与 `*mutation.Strategy`）；② **执行步可换**（`ArenaExecutor` 缝 + `NewRuntimeBackedArenaScorer`；判分与 `groundTruthFloor` **共用**，运行时路径**逐题**执行）；③ **运行时执行器**（`RuntimeArenaExecutor`：每候选一运行时、**有界保留 2**（A/B 只需两个；"每候选一个且永不释放"在滚动部署里是无界增长）、provider **懒解析**（实测启动顺序：`comp.Evolution`@`serve.go:108` 早于 `toolBinder`@`:141`，值传递必然是 nil）、`Close` 收口、候选 `Params` 直通沙箱）；④ **接线**（`regression_runtime_replay` **默认关**、缺 provider **失败关闭并指名缺件**、**成本/边界启动日志**）。**测试 10 条**（门契约 1 + 运行时 scorer 4 + 执行器 5）；**反证 2 条**（只传模板 → `the strategy must travel as itself`；丢 `Params` → 候选拿到**全部 15 个工具**而非它申请的 1 个）。**定时触发（2026-10-17）**：① **零成本那半已落**——`evolution.gates.eval_probe_interval`（**默认关**）驱动的**语料探针**：定时重读 suite，报**指纹变化**（任何编辑可见，含不改计数的那种）与**锚定归零**（`Warn`：此后每个分都只是判官意见），**零模型调用**，随 ctx 退出；`InspectCorpus` 4 条测试 + 反证（把锚定数当案例数 → `expected 1, actual 2`）。② **定时跑真实评测未做**——每 case 一次模型调用，属**成本决策**（与 `regression_runtime_replay` 同一理由：默认开=让默认去乘账单），需你点头后接 | 认知/正确性没持久化 |
| P0-2 | **T1+T2** LLM `create_task` 不校验 **〔已修 2026-10-12〕** | `create_task` 依赖不存在/成环 → **任务静默永不 READY**（`create_plan` 走 `CompilePlan`、operator 走自带 `findCycle`，都安全；**只有 `create_task` 旁路漏**） | 引用/边界没校验 |
| P0-3 | **D1+D2** 提前交差不够可见 **〔已修 2026-10-12〕** | maxPlanDepth 撞墙 / L1 全拦 → 强制出 answer；**不标记成因**（D1 已有 Warn+计数，但无面板/告警规则） | 静默早停 |
| P0-4 | **Q3+A-dispo1+A-F4** 认知有损 **〔B 已动工 · A 与 P0-1 合并立项 2026-10-13〕** | checkpoint/复活只带"进度"不带"思路" → 换人/续跑**丢推理链**；A 的"存什么/如何存"见 `cognition-persistence.md` | 认知/正确性没持久化 |

## P1 · 该修（高频咬人 / 一处改受益一片）

| # | 缺陷 | 一句话 |
|---|---|---|
| P1-1 | **T-F1** `classifyFailure` 默认可重试 **〔已修 2026-10-15〕** | 忘 `MarkPermanent` 的**永久错白烧满重试预算**（+ backoff）。修：已知永久错表（窄表，`errors.Is`）+ 未分类原因重试/耗尽时 Warn |
| P1-2 | **R2** 检索纯余弦无精排 **〔已修 2026-10-15〕** | 假近进 top-K。修：生产路径**早就在宽召回（TopK*5）却没精排**，补确定性精排（相似度+词面覆盖+新鲜度）。**不是 cross-encoder**（无模型服务），如实标注 |
| P1-3 | **R1** ivfflat `lists=100` 写死 **〔已修 2026-10-15；`ef_search` / 跨 method 重建 2026-10-16 补齐〕** | 真因是"没人再看它一次"：单一 DDL 源 + lists 按行数自适应（`max(rows/1000, √rows)`）+ 启动复核重建 + 检索期旋钮。**补齐**：`hnsw.ef_search` 与 `probes` 合并为 `VectorSearchTuning`、走**同一条事务路径**（`SET LOCAL` 无事务会被静默忽略）；目录查询扩到 `pg_am.amname`，配置与索引 method 不一致时**一次性响亮重建**（保留名字与 partial 谓词；空期望=不评判）。**注：该包（`internal/storage/postgres/services`）已随死代码删除，本节其余内容见 R2 段** |
| P1-4 | **D(retrieval)** embedding 死信 **〔已修 2026-10-15〕** | 修：可行动性拆分告警（pending/given-up）+ **每条内容只自动重放一次**的原子重放（`auto_replayed` 标记随死信搬运，防无限循环）+ tick 顺序改为重放→清理→报告 |
| P1-5 | **I1** `ask_agent` 不回写 **〔已修 2026-10-15〕** | 修：回信作为**任务**投递给 asker（`(asker,peer,topic)` 哈希 id 幂等；无能力/不存在则响亮失败）+ 工具描述同步改口 |
| P1-6 | **A-factory3** agent quota 泄漏 **〔已修 2026-10-13〕** | claim/release 漏释放 = **agent 池假死**（`ErrResourceQuotaExceeded`） |
| P1-7 | **S-mcp-S2** MCP 挂了工具静默消失 〔已修 2026-10-13〕 | server 连不上/断连 → 它的工具没进/留在 Registry，LLM **不知道自己少了什么**，会白调不存在的工具 |

## P2 · 打磨 / 可观测 / 规模化（不急，但要知道）

| # | 缺陷 | 一句话 |
|---|---|---|
| P2-1 | **G2** 三道门盲区 **〔已核 2026-10-17：绝对闸门早就在〕** | "门只防回归、不防整体漂移"的**前提不成立于"绝对闸门缺失"**：`evolution.gates.eval_min_score` 早已接进门（`>0` 时覆盖，否则用门自身 **0.7**，`gate_eval.go:45`）⇒ 整片漂移有绝对线兜着。**已补的只是可见性**：启动日志打 `min_score=<有效值> source=<配置/门默认>`（此前数值从不露面，运维分不清读到的是自己的策略还是门的内置下限）。**剩余**：阈值本身（0.7）与口径属政策，**待拍板**；另 P0-1 的确定性 floor 已堵住"两候选都答错、判官都给好评"那一种漂移 |
| P2-2 | **D3** Reconcile 全量扫 〔**已关闭 2026-10-17**：措辞更正 + 可见性已在〕 | ① **措辞更正（2026-10-16）**：不是"全量拿锁"——只在快照 ID 集时持读锁，重活在锁外；② **"漏事件静默"不成立**：漏事件那条路径**已打** `log.Error("reconcile failed", reason="dropped events")`，重扫完成打 `log.Warn("reconcile complete", created/removed/…)` ⇒ 补一次重导是**可观测**的；③ 残留（非本缺陷）：O(节点) 重扫成本与"瞬态 desync 到下次 tick 收敛"是**规模化属性**，不是静默 |
| P2-3 | **D6** stableRound 并发前提 **〔已关闭 2026-10-17：前提在当前设计下成立〕** | 该函数按"同一 stable round 重导出同一批 ID"工作，隐含**单写者**。实测：同一 session 的图由**单写者**推进（调度器按 session 串行 lease 任务，一个 session 同时只有一个 quantum 在改图）⇒ 前提成立。**留痕**：若将来引入"并发改同一 session 图"，必须重新评估此前提（届时失败形态是**分叉出重复分支**，而非报错） |
| P2-4 | **A-F3** requeue ≠ 恢复 〔**已关闭 2026-10-16**：静默半已消除〕 | requeue 只管回队（设计如此）；但"全 agent 超预算 ⇒ 没人能接"**不再静默**：`Scheduler.logFailure` 对 `ErrNoAffordableCandidate` / `ErrNoCapableCandidate` **各占一个节流槽、每 5s 一条 Error（带 task_id）**，READY 每 500ms 重轮询 ⇒ 持续可见且可区分成因。剩余（非本缺陷）：等待计数做面板指标 / durable 事件 |
| P2-5 | **T-F3** 两个预算互不知晓 **〔已落 2026-10-16 · 唯一可行落点〕** | 为什么"联合视图"只能落在**调用方**：task fabric **不知道 agent**，而恢复策略只拿到一个死 agent 的 id（`RestartAgent(ctx, deadAgentID, cognitive, capabilities)`）⇒ 两个预算**结构上互不可见**。**修法**：把任务自己的预算并进恢复快照（`RecoveryTask.Attempts/MaxRetries`，**原样拷贝、不推断**——0/0 读作"没有重试预算"而不是"无限"），在唯一同时持有两者的 `cmd/ares/kernel_loop.go` 那一行**同时给出两个预算**；**两者皆尽时升为 `log.Warn` 并明说"没有东西可试了"**（有余额时仍是 Info：替换执行体还会再跑一次，此刻不重要）。测试 2 条 + 反证（去掉填充 → `the snapshot must carry the task's spent attempts verbatim`） |
| P2-6 | **A-dispo2/3/4** 复活预算·快照·schema | **②「快照 1024」已关闭 2026-10-17（且我更正自己上轮的"未能定位"）**：它就是 `internal/fabric/agent/snapshot.go` 的 `maxDeathSnapshots = 1024` —— 死亡快照**条数上限**，且有**第二重时间上限**（`defaultDeathSnapshotRetention = 1h`）；注释写明"按 `DiedAt` 淘汰最旧、恢复只取最新、丢的是恢复环用不到的尾部"。判定：**有意的、有理由的边界，不是缺陷**（唯一可挑的是淘汰无日志，但按该理由无处可诉）。**① 已修 2026-10-17**：`RestartPolicy.RestartWindow`（**默认 1h**，"1 小时内 5 次"= 崩溃环预算；**零值仍是 lifetime 语义** ⇒ 零值策略行为不变）。计数器由 `map[string]int` 改为**时间戳切片**，gate 读取时**就地剪掉窗口外的戳**（切片长度正比于预算而非 agent 寿命），`RestartCount` 报"**计入预算的次数**"（与 gate 看到的一致）。**测试 2 条**（窗口给回预算；零窗口保持旧语义）+ **反证**（忽略窗口 → `restarts older than the window must stop counting, got 5`）。**③ 滚动升级挡复活（A4）已核 2026-10-17**：**当前不会触发**（`CognitiveStateSchemaVersion = 1`，只有一个版本 ⇒ 不存在"旧节点读到新快照"）。它是**前瞻性隐患**：一旦升版，新节点写的 v(N+1) 死亡快照会被仍在运行的旧节点**拒读** ⇒ 该 agent 的**原地复活退化为换人**（恰好在原地复活存在的意义处丢认知）。**已落**：把**滚动升级规则**写进版本常量旁（升版必须**同时**交付"向后兼容读（新字段 additive 且缺失是合法态）"或"升级前清空/迁移待复活快照"，**二者择一**），并让 `ErrCognitiveStateSchemaVersion` 的文本**说出部署后果**（"written by a newer build… cannot revive it in place"，否则读日志的人会当成损坏而重试）。测试 2 条（拒读语义 + 文本合同；正常/遗留版本仍可读）。**说明**：这条是文档+文案改动，**测试就是验证**（不给它做变异反证——变异只会把字符串改回去，不增加信息） |
| P2-7 | **A-factory4** 孤儿 agent 累积 | `Retire` 不杀子 agent（`Agent.Parent` 注释明说 **PROVENANCE ONLY，非生命周期关系** ⇒ 级联**需拍板**）。**已落（可见性半边 2026-10-16）**：`retireLocked` 在状态变更**前**读孤儿（新增 `orphanedSpawnsLocked`，持 `f.mu`），两个调用方（`Retire` / `ReclaimIdleAgents`）在**锁外**把孤儿挂进 `agent.retired` 事件（新 payload key `orphaned_spawns`，仅在有孤儿时带；该包**用事件不用日志**，记录做 I/O 不能进锁）。测试 3 条（契约 / 无孤儿不带键 / helper 边界：只报**直接**且**未退役**的子）+ **反证**（去掉读取 → `retiring a parent must name the agents it left live`）。**仍是"可见"不是"回收"**：级联方向 1（显式策略）/ 方向 2（对账指标）待拍板 |
| P2-8 | **I4** dead-letter 非持久 | 进程内存死信，重启即失，协作失败不可审计 |
| P2-9 | **S-st-S1/S-st-S2** 维度写死·共实例 | **① 维度写死一半已修 2026-10-17**：`1024` 原本在**两处**各写一遍（`storage/postgres` 的列类型 DDL + `ares_bootstrap` 的向量 provider 配置）⇒ 已**单源化**（导出 `postgres.DefaultVectorDimension`，bootstrap 引用它；静态验证：生产侧不再有第二个维度字面量，注释里写明"一个数两个读者"，并点名历史上 1536/1024 那次插入失败）。**仍开**：表名本身把维度烘进去（`knowledge_chunks_1024`/`experiences_1024`）⇒ **换嵌入模型仍要重嵌+改表名**。**② 共实例**：属**部署属性**（同一 PG 实例存向量与事件账本，无资源隔离=吵闹邻居），要独立实例才能隔离，未改 |
| P2-10 | **R4/R6** RAG 默认关·阈值 0.4 | 记忆复用不显式开就不走；高维余弦 0.4 偏弱（知边界） |
| P2-11 | **S-mcp-S1** 工具名撞车 first-wins 〔**已关闭 2026-10-17**〕 | 策略本身**有意**（既存注册优先；内置先注册，所以内置赢）。缺陷是**它的报告说谎**：`winner="builtin"` 在两个 MCP server 同名时会指名一个**根本没参与的**赢家——而那一行日志的唯一用途就是报告赢家。已改为 `kept="pre-existing registration"` + `skipped_server=…`，并**不再臆断归属**（注册表只告诉"已存在"，不说持有者是谁）⇒ 冲突可观测、可归因 |
| P2-12 | **S-mcp-S4** health 标了没人消费 〔已修 2026-10-13；**复核后补端点 join**〕 | discovery 标 `Healthy=false` 但 MCP Manager 不摘工具 → 半死 server 工具仍可调 |
| P2-13 | **S-mcp-S5** transport 自动断连不摘工具 〔已修 2026-10-13〕 | 断连后工具留 Registry 变"僵尸"（execute 才报 connection refused） |
| P2-14 | **S-mcp-S3/S6** discovery 默认关·工具规模 | **① discovery 默认关是有意的、非缺陷**（`config_defaults.go`：opt-in via `Enabled`，注释自称 "**the single security boundary**"——只允许列出的服务被发现）。**② 工具规模仍开**：演化的工具白名单有界（`evolution.MaxToolsEnabled`），但**启动时注册的工具总数无上限**（MCP server × 其工具）⇒ "工具越多、LLM 选择越被稀释"仍成立。属**规模化项**（需要什么形态的上限要产品判断），未做 |

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

**2026-10-19 · 收敛 C（散文断言，§21–§25）**

| 断言 | 源码证据 | 判定 |
|---|---|---|
| `maxPlanDepth` 默认 **10** | `planner_cognition.go:26`（`DefaultMaxPlanDepth = 10`） | ✅ |
| RAG 默认 `TopK=5 / MinScore=0.4` | `memory/manager.go:448-449` | ✅（原引用 429-430 指向函数开头，已改到实际赋值行） |
| checkpoint `SchemaVersion` = **v5** | `checkpoint_schema.go:37`（`CurrentCheckpointSchemaVersion = 5`） | ✅ |
| 重启预算 **5 次 / `RestartWindow`=1h** | `aresrecovery/recovery.go:124/131`（`MaxRestarts: 5`、`RestartWindow: time.Hour`） | ✅ |
| 退避 `1s,2s,4s … 30s cap` | `recovery.go:145`（`Backoff: 1s`）＋测试里的 `MaxBackoff: 30s` | ✅ |
| 租户/`StrategyID` 不被 LLM 覆盖、只写一次 | `syscall.go:119` 的 caller 派生、注释"stamped once" | ✅（与 §6–§8 同源） |


**2026-10-19 · 收敛 C（散文断言，§17–§20）**

| 断言 | 源码证据 | 判定 |
|---|---|---|
| `MemoryRetriever` 默认 `MinScore=0.4` / `TopK=5` | `memory_retriever.go`（`DefaultMinScore` 等） | ✅ |
| 蒸馏由 **30s** 上限约束 | `ares_bootstrap/knowledge_akg.go:43`（`akgBridgeTimeout`） | ✅ |
| MCP 服务发现每 **5min** | `discovery/engine.go:306` | ✅ |
| `Discovery.Enabled` 默认 **false** | `ares_config/config_defaults.go:290` | ✅ |
| MCP 客户端重启退避 `1s/2s/4s…30s` | `aresrecovery/recovery.go:145` 确有 `Backoff: 1s` / `MaxBackoff: 30s` 的策略；**该策略是否就是 MCP 客户端所用未确认** | ✅（主体成立，归属待确认） |


**2026-10-19 · 收敛 C（散文断言，§13–§16）**

| 断言 | 源码证据 | 判定 |
|---|---|---|
| Shadow A/B 只有 **delta ≥ 5%** 才通过 | `deployment.go:49`（`PromotionThreshold: 0.05`） | ✅ |
| 回归 **> 10%** 自动回滚 | `deployment.go:50`（`RollbackThreshold: 0.10`） | ✅ |
| 部署后 `MonitorAndRollback` 监控 | `deployment.go:313`；30s 是**评估超时**（`EvaluationTimeout`），文档措辞"monitor 30s"与实现基本对应但不算精确 | ✅（措辞偏松） |
| `events_retention_days` 默认永不删除 | `bootstrap_builder.go`（未设置则不清理） | ✅ |
| `regression_runtime_replay` 默认关，且启用时必须有 provider | `bootstrap/regression_gate_wiring.go:139`（缺 provider 直接报错） | ✅ |
| `defaultPlanner` 用 `containsAny` 关键词 + 权重 | `internal/knowledge/planner/default.go:112` | ✅ |


**2026-10-19 · 收敛 C（散文断言，§9–§12）**

| 断言 | 源码证据 | 判定 |
|---|---|---|
| `ask_agent` 的回复**只记日志、不回写** | `internal/agentsyscall/syscall.go:119`（"FIRE-AND-FORGET"） | ✅ |
| 相同提问 30s 内去重 / `Request` 30s 超时 | `syscall.go:624`、`agentipc/primitives.go:20` | ✅ |
| `KeepSet`：活会话的任务永不被回收 | `internal/agentruntime/session.go:311`，用于 `NewReaperWithKeep(..., "sess/", ...)` | ✅ |
| 跨会话任务 ID 前缀 `` `sess-N-*` `` | 实际为 **`sess/<sessionID>/dN/...`**（`agentruntime/submit.go:105`、`await.go:67`） | ❌ **已修** |


**2026-10-19 · 收敛 C（散文断言，§6–§8）**

| 断言 | 源码证据 | 判定 |
|---|---|---|
| `create_task` 的 `Origin` 取自 `kctx.CallerID(ctx)`，非 LLM 提供 | `internal/agentsyscall/plan.go:128` | ✅ |
| 依赖不存在时**当场失败、不创建任务**（不会留下永远无法满足的依赖） | `internal/fabric/task/fabric_deps.go:86`（"nothing would ever satisfy it"） | ✅ |
| 相同提问在 **30s** 内去重 | `internal/agentsyscall/syscall.go:624`（`defaultAskDedupeWindow`） | ✅ |
| `Request` = ask + wait，**30s** 超时 | `internal/agentipc/primitives.go:20/146`（`defaultRequestTimeout`） | ✅ 但原引用只写 `primitives.go:146`，易被误认为 `agentsyscall/` 下的文件——已补全路径 |
| spawn 额外过配额 | `internal/fabric/agent/fabric.go:27`（`resourceBudget`） | ✅ |

§6–§8 无错误断言，仅一处**路径歧义**已修。


**2026-10-19 · 收敛 C（散文断言，§1–§5）**

| 断言 | 源码证据 | 判定 |
|---|---|---|
| `?wait=` 超过 300s 拒绝 400，默认 60s | `cmd/ares/agent_routes_tasks.go:39/42`（`taskWaitDefaultDuration`/`taskWaitMaxDuration`） | ✅ 准确 |
| 会话存储上限 500 条 | `internal/runtime/memory/context/session.go:25`（`defaultMaxSessionMessages`） | ✅ 准确 |
| drain 事件驱动 + **500ms** 轮询兜底 | `internal/kernel/scheduler.go`（`PollInterval: 500 * time.Millisecond`） | ✅ 准确 |
| "The **28** sections below" | 实际 **30** 个 `## N.` 章节（§29 HITL、§30 版本状态是后加的） | ❌ **已修为 30** |

§1–§5 只做了**抽样**（4 条）；§6–§30 未开始。


**2026-10-19 · 收敛 C（收尾）：24 条锚点逐条过完，剩 17 条经阅读判定正确**

逐条定位后修正 **6 处**（中英各 6）：

| 原锚点 | 真实位置 | 依据 |
|---|---|---|
| `memory_retriever.go:65` | **:93** | `type MemoryRetriever struct` |
| `planner_cognition.go:186` | **:209** | `const maxExperiencePriorRunes = 4096` |
| `manager.go:102/116/125` | **:113/123/132** | `DistillationThreshold` / `MaxDistilledTasks` / `DistilledTaskTTL` |
| `migrate.go:107` | **:123** | `ALTER INDEX … idx_agent_checkpoints_status` |
| `scheduler_execute.go:159` | **:264** | `ErrNoAffordableCandidate` 的返回点 |
| `scheduler_quantum.go:258` | **:309** | `s.attribution.RecordWithMetrics(...)`（文档原断言"kernel 内联写入"是**对的**，只是行号漂移） |

**剩余 17 条**逐条读过，属**检查器假阳性**而非文档错误，例如：
- `backoff.go:19` 的候选是 `BackoffBase`/`BackoffMax`，但该行是 `func retryBackoff` —— 句子讲的是字段语义，锚点指实现函数，**正确**；
- `coordinator.go:384/695` 在 `internal/fabric/planprojection/coordinator.go` 里确实存在（检查器在同名文件里找）；
- `scheduler_execute.go:146` 是该行 `StopUnfit.String()` 的**使用点**（字符串 `capability_unfit` 定义在 `stopreason.go`）。

⇒ 锚点这件事到此为止：**234 条中 140 条原本正确、76 条已修、17 条经人工确认正确**（剩余无一条是"指错"）。


**2026-10-19 · 收敛 C（第二半）：散文断言抽样核对**

抽 6 条"默认值"类断言回源码核对：`RestartWindow`=1h ✓、`events_retention_days` 默认不清理 ✓、`MemoryRetriever.DefaultMinScore` 存在 ✓、discovery 默认 5min ✓、`Discovery.Enabled` 默认 false ✓（配置层），**`defaultServeHost` 的行号漂移**（文档 327 → 实际 `cmd/ares/serve.go:353`，已修，中英各一处）。

**如实说明**：两份文档共 220+ 条散文件断言（数字、默认值、行为描述），本轮只做了**抽样**（6 条）。**全量逐条核对未完成**——它需要一条一条定位常量/分支并判断措辞，不是脚本能替代的。剩下的按台账记为待办，不假装已覆盖。


**2026-10-19 · 收敛 C（第一半）：锚点语义核对，70 条机械修正，24 条留人工**

`check_doc_anchors.sh` 只验证 `file.go:NNN` 的行存在，**不验证那一行是不是文档所说的符号**。用脚本按"锚点前 120 字符内的标识符是否出现在该行 ±5 行"重核，共 **234 条**：

| 结果 | 数量 |
|---|---|
| 原本正确 | 140 |
| **机械修正**（符号在候选文件内有唯一定义行） | **70** |
| **仍需人工**（符号不在该文件/多义/是字符串） | **24** |

**两次错误尝试（记录在此，避免重犯）**：
1. 第一版脚本按 basename 选第一个同名文件 ⇒ 164 条假警报（`config.go` 有多个）；
2. 有一轮改成"全仓唯一声明"匹配，结果把 **40 条锚点搬到了无关文件**（如 `scheduler_execute.go:192` → `load_tracker.go:235`，只因 `confidence` 在那里有唯一声明）⇒ 已回滚，只保留"**候选文件内**唯一"的保守修正。

**剩余 24 条**属于三类：① 符号是结构体字段（`BackoffBase`/`BackoffMax` 指向 `retryBackoff` 那一行）；② 符号是字符串/事件名（`capability_unfit`、`KindFitness`）；③ 文件本身写错（`Reconcile`/`SubscribeGraphEvents` 不在 `coordinator.go`）。**改它需要先读懂那句话想指什么**，不是替换数字能解决的。


**2026-10-19 · 收敛 D：修掉一个 flaky 测试 + 覆盖率基线**

| 项 | 内容 |
|---|---|
| **flaky** | `TestWarnOutOfRangeValue_OncePerSource` 断言"首次告警"，而限流状态是**进程级**的（`outOfRangeWarnedSources` + 32 条上限）⇒ 同一进程第二次跑必然失败。补 `resetOutOfRangeWarnings()`（测试用钩子，不改变生产语义）并在测试首尾各调一次；`-count=3` 稳定通过。**根因不是"偶发"，是"只在第一次正确"** |
| **覆盖率基线** | `make check` 全量：**131 个有覆盖率的包**，平均 **81.7%**、中位 **84.2%**、最低 **14.7%**、最高 100%；**3 个包低于 30%**。收敛时以这张快照为对照，避免"感觉覆盖还行" |


**2026-10-19 · codescope 重建索引后的复测 + arena 死导出清理 + 一处"评分输入恒 0"的真缺陷**：

| 项 | 结论 |
|---|---|
| **索引重建** | `codescope index <path>` **不建索引**（它只是启动 MCP 服务）；真正可用的是 `index-parallel --workers N`。索引会**替换** DB 文件 ⇒ 运行中的服务读到空/旧数据，必须重启 |
| **复测（新索引 25505 节点 / 1729 文件）** | 陈旧类误报**消失**：`knowledge/retriever`、`postgres/services` 两条"死模块"不再出现；`internal/evoapi` 仍报（**真**，0 importer）。死函数仍大量误报（抽样 5/8），并新增一例 `proposalKeyFor`——它以**函数值**传给 `SetExperimentLog`，`DeadFunction` 看不见函数值。`trust_score` 仍为 0 |
| **`RecordFailover`/`FailoverCount` 已删** | 无任何生产者、也**不参与评分**（`CalculateScore` 不读），仅测试调用 ⇒ 按 TODO 一并移除（方法 + 快照字段 + 测试引用） |
| ⚠️ **`RecordRecovery`/`RecordConsistency` 保留并写清后果** | 它们的快照字段（`TotalRecoveries`、`Avg/Min/Max/LastRecoveryTime`、`DataConsistencyRate`）**被 `CalculateScore`/`calcConsistency` 读取**，但生产里**从不调用这两个方法** ⇒ **字段恒为 0**，评分的"恢复/一致性"分量**永久走启发式兜底**。这是**真缺陷**（评分输入从未被填充），不是清理项；删掉生产者会把证据一起删，故在方法注释里写明"先修评分，再删" |

**2026-10-18 · M10 收尾第一半：持久化实验日志（JSONL 追加）**

上一轮登记"M10 的记录零生产者"。真去接线时发现**更前置的问题**：`ExperimentLog` **在生产里根本没被构造过**（全仓只有一句注释提到它），唯一实现是内存版 ⇒ 即使接上生产者，写进的是**重启即空**的日志，等于把这个功能的目的（事后重放）抹掉。

本轮补上**持久化实现** `FileExperimentLog`（`internal/runtime/ares_evolution/experiment_log_file.go`）：

| 设计点 | 理由 |
|---|---|
| **JSONL + `O_APPEND`** | 一行一条记录 = 天然只追加；比数据库少一次迁移，出事时人能直接读 |
| **`Append` 里 `Sync()`** | 只在页缓存里的记录，正是崩溃时会丢的那条——而崩溃往往就是有人要读日志的原因 |
| **坏行 → 报错并指明行号** | 静默跳过会让日志回答"我们从没跑过那个实验"，而它其实跑过 |
| 无 `Update`/`Delete`、空 candidate id 拒写 | 与既有契约一致：可改的记录无法证明当时决定了什么 |

测试 4 条：**跨重启仍可读**（这是内存版给不了的性质）、追加顺序、拒绝无 ID、坏行可定位。

**剩余（下一半）**：把生产者接到"同时持有谱系 + fitness 配置"的 GA 循环（`genome_wiring_run.go` 的 `Run`，`BestStrategy()` 可取候选），并在 bootstrap 构造日志。**卡点已明确**：`ExperimentRecord.Accepted` 是布尔，而 GA 阶段只能给出"**已封存、决策未定**"——直接写 `false` 会把"未决"说成"拒绝"，需要先加三态 `Outcome`（pending/accepted/rejected，SchemaVersion → 2），再接线。

**2026-10-18 · 用 codescope 做 MCP↔源码对照，并更正 `internal/evoapi` 的两处假断言**：

**MCP 准确性实测**（逐条回源码）：行号系统性 **-1**（`ParetoDominance` 报 49 实为 50、`ParetoFront` 报 91 实为 92）；调用方只算同文件的一个函数（`ParetoDominance` 另有测试 6 处）；**缺席断言不可采信**——"死模块 `knowledge/retriever`"实有 2 个 import 者（旧索引）、"死模块 `postgres/services`"目录早已删除（新索引 0 实体）、抽样的 6 个"死函数"**全部有 1–11 处调用**（6/6 误报）、`Features`/`KeyFeatures` 等是 README 小标题被当成"能力声明"。它自报的 `trust_score: 0.2` 与实测吻合。**可用边界**：重建索引后 `find_symbol`/`search`/`trace` 适合做导航（省 token），但**缺席类结论必须回源码**（`go list -deps` 或 grep）。

**重建索引的副作用（必须知道）**：`index-parallel` 会**替换** `.codescope/codescope.db`；运行中的 MCP 服务在此之后返回 **0 实体**（`get_graph_stats → total_symbols: 0`），需重启服务才恢复。**触碰/改写 `mcp.json` 不会触发重载**（实测 pid 未变），`kill` 掉子进程后**客户端会在下一次调用时自动拉起**（实测新进程立即看到 25441 实体/7840 关系）。

**重建前 vs 重建后的对照（同一工具、同一参数）**：

| 类别 | 重建前 | 重建后 | 结论 |
|---|---|---|---|
| 死模块 `knowledge/retriever` | 报 | **消失** | **陈旧误报**（索引早于我接线） |
| 死模块 `postgres/services` | 报 | **消失** | 目录已删除，新索引 0 实体 |
| 死模块 `internal/evoapi` | 报 | **仍报** | ✅ 真（0 importer） |
| "死函数"（30+ 条；**抽样 8 条 → 5 误报 / 3 真死**，精度 37.5%） | 报 | **仍报** | **方法论误报**（漏"函数值"与"接口分发"），重建不能解决；**上一轮我写"6/6 误报"是错的**（当时按裸名 grep，未区分同名不同函数） |
| capability `Features`/`KeyFeatures`/`AKG…Frozen034Sch` | 10 条 | **10 条** | README 散文被当"能力声明"，属解析噪声 |
| `trust_score` | 0.2 | **0**（orphans 43） | 工具自己也承认不可信 |

⇒ **可用边界修正**：重建索引只消除"陈旧"这一类；**"死函数/能力缺失"是方法问题，重建不解决**，必须回源码。

**`internal/evoapi`（252 实体 / 11 文件）的裁决**：包内 `doc.go` 自陈是 legacy，且写明"生产路径不可达"——**属实**；但它另外两句**不成立**，已按实证更正：
1. "internal callers remain during the window" → **0 调用方**（全模块无人 import，`go list -deps ./cmd/ares` 不含它）；
2. "The production-observable surface … is exposed through NewGenomeWiringView" → `GenomeWiringView`/`NewGenomeWiringView` 全仓 **0 引用**，那是**意图**不是事实。
**处置**：**不删**（折进 `sdk`、v0.5.0 移除是既有计划，删它会偏离既有决策），但把 doc.go 改成实况，并写明"分析工具继续报它死代码是**正确的**，不是待修缺陷"。`sdk` 目前也**未** import 它。

**2026-10-18 · M6 接线后的深度 review：三个会静默出错的真问题（已修）**：

| # | 问题 | 后果 | 修法 |
|---|---|---|---|
| **1** | 适配器**没有应用 minScore**（tiered 路径本身没有任何阈值） | 开启开关会**静默降低质量门槛**：单发路径丢弃 <0.4 的对象，tiered 路径会放进 prompt | 适配器加 `minScore`（未设时按 `adapter.DefaultMinScore`=0.4 兜底，与单发路径同口径）；测试 `AppliesTheQualityBar` + **反证**（去掉门槛 → `objects below the threshold must not reach the prompt`） |
| **2** | **L2 合并不受 `MaxResults` 约束**（L1 命中被并进 L2 结果） | 调用方要 5 条可能收到整张图 | 适配器按 Score 排序后**截断到 topK**；并在 `tiers.go` 把"L2 路径不受限、需要硬上限的调用方自行截断"**写进契约**（不谎称有界） |
| **3** | 启动日志称"无 tenant/user 上下文的请求只会走 L2" | **假话**：`defaultDistillTenant` 非空 ⇒ 无上下文会落到**默认租户**并照常跑 L1 | 日志改为"L1 按**解析后的**命名空间收窄；空命名空间才跳过 L1；无上下文请求会落到默认租户" |

**未覆盖（如实）**：`L2Retriever` 是**具体类型**（`*Retriever`），适配器层无法用假件触发"合并超限"分支，因此截断逻辑**没有直接测试**；它的正确性依据是"排序后截断"这一简单性质 + `tiers.go` 的契约说明。**装配测试**（开关开/关时 appends 的差异）也仍缺——`wireRetrievers` 需要真实 `KnowledgeRuntime` 才能构造该分支。

**2026-10-18 · M10：可复现实验记录 + append-only 日志 + AKG 接缝（只收窄）**：

| 件 | 内容 |
|---|---|
| `ExperimentRecord` | 记录**决定结果的那部分**：genome 谱系、行为（模板+参数）、**收窄用的证据 ID**、**当时生效的 fitness 配置值**（不是引用）、维度、分数、各门 verdict（**带理由**）、是否接受、部署后表现（**未观测为 nil，不写 0**）。带 `SchemaVersion` |
| `ReplayKey()` | **确定性**指纹：map 键**排序后**再哈希（Go 随机遍历顺序，否则同一实验两次会得到不同身份）；**刻意排除时间与分数**——那正是重放要比较的东西，折进身份会让"同实验不同结果"无法表达 |
| `ExperimentLog` | **只追加**，**没有 Update/Delete**（可改的记录无法证明当时决定了什么）；读者拿到的是**深拷贝**（浅拷贝会共享 map/slice，"改一下返回值就能改写历史"） |
| 自强化环 | `KnowledgeGuidanceProvider` **只产出 hints**（收窄候选）；`RecordStrategyOutcome` **有意不写回图**——写回会让 GA 的判断变成事实、下轮又被它塑造。outcome 归 `ExperimentLog`，GA 读它是**历史**而非候选生成输入 |
| 测试 | 4 条记录/日志（可复现 / 身份不含结果 / 不同配置分离 / append-only + **深拷贝**）+ 2 条接缝（只收窄 + 租户隔离 + 失败关闭 / **回写不增图节点**）+ **3 条反证**（去证据排序 / 放开空 namespace / ——） |
| **如实限制** | ① `MemoryExperimentLog` **只在内存** ⇒ 重启即失，跨部署的"可复现"要接持久化存储；② **`ExperimentRecord` 目前零生产者**——决策点（`candidate_pipeline` 的 promote/reject）只掌握候选/证据/理由，而**谱系与当时生效的 fitness 配置在 GA 循环**；在这些数据凑齐之前产记录，会得到一个"重放键漏掉决定因素"的假可复现记录 ⇒ 已按 `code_rules §0.3` 在该处留 `TODO(tech-debt)` 并写明由**同时持有两者**的循环来接；③ `HintsForTask` 的 `Query` 错误分支、`canonicalJSON` 的不可序列化分支**未覆盖** |
| **§3.4 治理护栏** | ✅ 写进 README：**Agent 提议、kernel 强制**（GA 可调参/选组合/偏置候选；不可绕权限、超预算与 blast-radius、绕过 kernel 改运行态、自己 apply）；**先锁单一主生产目标**（agent 策略参数优先，六步闭环跑通再扩面），并说明"同时演进多个子系统会让每次回归都含义不明" |

**2026-10-17 · M9-② ：维度接进 GA 打分（service 路径），并如实标出内部路径未接**：

| 项 | 状态 |
|---|---|
| `SystemConfig.DimensionWindow` | ✅ 新增（函数形式，**在打分时按候选读窗口**；快照式会在每代沿用同一份证据） |
| `scoreAgents` 拆分 | ✅ `scoreAgents = scoreScalars + scoreDimensions`；**补充式**：无窗口 = 行为不变；**无证据的候选保留原标量分**（写 0 会被读成"测过全失败"并把它踢出选择） |
| 聚合口径 | ✅ `Score = AggregateDimensions(dims, DefaultDimensionWeights)`，与维度**同源**（分数与向量描述同一份证据） |
| `SourceStat` 导出 | ✅ 原为未导出 `sourceStat`，却被**公开字段** `WindowResult.PerSource` 使用 ⇒ 外部包既构造不了也读不了；已导出 |
| 测试 | ✅ 3 条（证据→维度+聚合 / 无证据不动 / 无窗口不启用）+ **反证**（`scoreDimensions` 变空操作 → `actual: map[string]float64(nil)`） |
| **收敛为单一执行路径（2026-10-17 续）** | ✅ 按 `code_rules_v2 §5.1`（同一语义只允许一条生产执行路径）**不再在 service 里另写一份**：维度填充下沉到 `genome.Population` —— `SetMultiObjectiveScorer` + `ScoreAgentsWithDimensions`（标量后接维度，顺序有依赖：维度 scorer 要读刚写好的 `agent.Score` 作无证据时的保留值）。**两条循环共用**：service 的三个打分分支与 in-process 的 `EvolveAfterScoring` **最终打分**都走它。测试：`TestEvolveAfterScoring_FillsDimensions`（内部循环真的填了）+ 零值不填一条 + service 侧 3 条；**反证**：把最终打分改回 `ScoreAgents` → `actual: map[string]float64(nil)` |
| **M9-③ latency/cost 证据采集（2026-10-17 落）** | ✅ `WindowResult.Metrics`：**独立的 metrics 通道**（不进加权均值、**不进 `Count`**——后者是 staging 门，注释明写"没人启用的通道不得替 staging 判定背书"）；observer 写两条独立证据（source `latency`/`cost`，值 = **惩罚的补值**，0=最快/最省 ⇒ 与 `Minimize` 方向一致）；`MapCandidateDimensions` **从 `Metrics` 读**这两维（并保住"缺样本省略、不写 0"）。测试 4 条：`Metrics` 不动 Mean/Count（**契约**）、映射从 Metrics 取（含**同名 PerSource 陷阱**）、completed 才写、未完成不写；**反证**：映射忽略 Metric 标志 → `expected 0.7 actual 0.1`。**未完成的任务不写度量**（写 0 = "瞬时且免费"，在 Minimize 上是最优值） |
| **仍未做** | `EvolveAfterScoring` 的**第一次**打分仍为纯标量（有理由：那一趟给 `EvolveOnIdle` 选父母打分，填维度描述的是即将被替换的那一代） |

**2026-10-17 · 0.3.5 起步：M8b 接线 + M9 维度映射（含一处归因 bug 与 Q2 拍板）**：

| 项 | 状态 |
|---|---|
| **M8a 归属** | **早已实现**（`generateDiffPatches` 要求 `strategyID` 非空并盖章 `patches[i].StrategyID`，且有生产调用方）⇒ 不用新做；`deployment_wiring.go` 里"无生产者设 StrategyID"的旧说法已更正 |
| **M8b ApplyGate** | ✅ **已接线**（`ares_bootstrap`，在**同时持有 coordinator 与 lifecycle** 的装配点安装；只拒"策略正被人工审批挂起"的 patch；**无归因 patch 放行**、**无 hold 时全放行** ⇒ 默认行为不变）。测试 2 条 + 反证（gate 变空操作 → 该拒的不拒） |
| **归因 bug（顺带查出）** | ✅ **已修**：`ProfileExecutor.Apply` 的 rollback patch **丢 `StrategyID`** ⇒ **回滚后策略永久不可比**（撤销动作抹掉了它自己的可判定性）。改为继承 + 测试 + 反证 |
| **M9 维度映射（第一步）** | ✅ `MapCandidateDimensions` 只取**候选可比**来源（`strategy`→`success_rate`、`dimension_eval`→`quality`）；`workflow`/`scheduler`/`recovery` **不进维度**（按设计忽略 strategyID ⇒ 每候选同值=常量维度）、`collaboration`/`tool_call` 默认权重 0 也不进；**缺样本省略，不写 0**（0 是"测过全失败"，缺席是"没测"）。测试 4 条 + 反证 |
| **反证第一次没失败（值得记）** | 加一个全局通道当维度，测试**没失败** ⇒ 发现我自己的**双源头**缺陷（`map` + `order` slice，往 map 加条目不生效）。合并成**单一有序列表**后反证成立。**"反证没失败"先怀疑实现/测试，不是宣布通过** |
| **Q2 拍板** | **两件都做**：① 收敛为候选可比子集（**已落**）；② **补 latency/cost 证据采集**（**未开工**——聚合器没有这两者的原始来源，只有 [0,1] 越大越好的 fitness 均值，属真工程；在此之前 `cost`/`latency` **留空，不拿均值冒充毫秒**） |

**2026-10-17 · 0.3.4 收口进度（按 `plan/0.3.x_plans/0.3.4-0.3.5_akg_ga.md` §8.2 的三条缺口）**：

§8.2 列出 0.3.4 仅剩三处"有代码有测试、零生产调用方"。核对验收测试后发现**三条验收测试本身缺失**（不只是没接线）：

| 里程碑 | 缺的验收测试 | 本轮 |
|---|---|---|
| **M2.5 关系门** | `TestRelationGate_FlagsConflict` | ✅ **关闭**：补上缺失的验收测试 + 它要求的能力 `FindRelationConflicts`/`RelationConflict{Candidate,Existing,Reason}`（判定**只用词表自带的 `contradicts`**，不发明"单值谓词"表；**只标记不丢弃**）+ **接线**：`DistillBridge.extractRelations` 现在先过 `FilterRelations`（按对象 `Type`）再写入，冲突告警。测试 3 条（门生效 / 门不是墙 / 冲突不丢弃）；**反证 2 条**（关掉检测 → `expected 1 conflict, got 0`；不把 subjectType 传给门 → `a 'fixes' relation from a user-typed subject must not reach the object`） |
| **M4 构建可观测/增量** | `TestAKG_IncrementalBuild_OnlyChangedByFingerprint` | ✅ **关闭 2026-10-17**：① **真 producer** `DistillConversationWithReport`（7 个计数器全部来自本次运行；`Rejected` 口径收窄为"低于晋升门槛、留为 candidate"——本链路从不因低分删除，这一点写进字段文档）；② **增量**：`persistAndPromote` 按 `ContentFingerprint` 跳过未变对象，新增 `Unchanged`；③ **旧验收改为只钉字段集**（原文自证"证明不了 producer"），真验收 = `TestDistillConversationWithReport_ProducesCounters` + `TestAKG_IncrementalBuild_OnlyChangedByFingerprint`（同内容不写 / 变内容才写）+ 包装器语义不变一条；④ **反证 2 条**（计数器不来自运行 → `ReadCount is what the distiller RETURNED`；禁用指纹跳过 → `unchanged content must not be written again`） |
| **M6 三级检索** | `TestRetrieval_TieredL1L2L3Fallback` | ❌ 未做（`TieredRetriever` 接线前须先解其 TODO 自陈的两个阻塞：整店入内存 per query、namespace 来源） |

**结论：0.3.4 尚未全部完成**，剩余项与顺序如上（M2.5 接线 → M4 → M6），每条都已有明确落点与验收名。

**2026-10-17 · 版本正向性检测：0.3.3 → 0.3.4（发现并修掉一处"产物说自己是旧版"）**：

**缺陷**：`VERSION` 文件停在 **0.3.3**，而 `README.md`（**6 处**）与多个包的注释（`knowledgeapi` "FROZEN as of 0.3.4"、`config_data.go` "pre-0.3.4"、`knowledge_akg.go` "pre-0.3.4 used agent_id…"）已自称 **0.3.4**。`make build` 用 `-X main.version=$(VERSION)` 注入 ⇒ **打出来的二进制 `ares version` 会报 0.3.3**，与它所属的发布不一致——而且**没有任何东西会失败**。

**修复与守卫**：

| 层 | 内容 |
|---|---|
| 值 | `VERSION` → `0.3.4`（与 README/代码的声称对齐） |
| 守卫 | 新增 `cmd/ares/version_test.go` 3 条：VERSION 可解析；**README 的版本声称不得高于 VERSION**；`main.version` 默认 `dev`（未注入时可辨识） |
| 守卫为何**单向** | 声称可以**更旧**（"pre-0.3.3 behaviour" 就是这么读的），但**不能更新**——那是产物兑现不了的承诺 |
| **反证** | 把 VERSION 退回 0.3.3 → `README claims [0.3.4 ×6] but VERSION is 0.3.3 — the built binary would report the older number` ✓ |
| **端到端正向** | `make build` + `./bin/ares version` → **`ARES 0.3.4 (darwin/arm64, go1.27.2)`** ✓ |

**残余扫描**：仍出现 `0.3.3` 的地方**全部是历史语义**（"removed in v0.3.3"、"pre-0.3.3"），没有孤立的"当前版本"声称；文档里也没有"current version/latest release"这类会与 0.3.4 冲突的断言。

**2026-10-17 · 导入快照的版本守卫（上次登记的残留，已收口）**：

`ImportStore` 现在**拒收比自己新的快照**（`SchemaVersion > CurrentSchemaVersion`，错误包住 `ErrSchemaVersionTooNew`），**接受更旧与遗留零版本**（由 per-object `MigrateObject` 升级）。三条要点：

1. **守卫放在 `StoreExporter` 快路之前**——否则"自己实现 `Import` 的 store"就成了唯一绕过守卫的路径：守卫只挡住了不需要它的那些 store，而漏掉了需要的那些。
2. **被拒不落盘**：半导入一个超新快照是最坏结果（对象进了一半、版本已被降级）。
3. **为什么拒收而不是"尽力导入"**：新版本快照可能带本版本不认识的字段，而回退路径会把它**重新盖成本版本的旧 schema 版本**——一次看起来成功的**降级**，只有在缺失的数据被用到时才会暴露。

测试 3 条（超新拒收且不落盘 / **当前与遗留版本可导入**（正向）/ 快路也受守卫）+ **反证**（去掉守卫 → 两条精确失败）。

**"功能都是正向的"审计（本会话全部开关）**：全部**默认保持原行为**，且每条都有**正向路径测试**，不是只有拒绝路径：

| 开关 | 默认 | 正向验证 |
|---|---|---|
| `Fabric.WithCascadeRetire` | **false** | `TestRetire_CascadeIsOptIn`：开启后整树退役且事件可归因 |
| `evolution.gates.regression_runtime_replay` | **false** | 运行时 scorer 4 条（执行器真被调用、判分/floor 不变） |
| `evolution.gates.eval_probe_interval` | **空=关** | `ReportsOnEachTick`：tick 上报、零锚定告警 |
| `evolution.guardrails.max_tools_enabled`（借用为告警线） | **0=不告警** | 只报告不丢工具（数量始终记录） |
| `postgres.DefaultVectorDimension` | **1024（值未变）** | 生产侧无第二处字面量 |

**自记（本轮两次"测试错、代码对"）**：① 我以为退役根后 `grandchild` 也是孤儿——**不是**（它父还活着），孤儿定义是"直接父已消失"；② `StoreExporter` **要求 `Export` + `Import` 两个方法**，我的假实现只有 `Import` ⇒ 根本没进快路，测试却在"通过"。→ 纪律已固化：**用可选能力接口写测试时，加 `var _ Interface = fake{}` 编译期断言**（已加在该测试文件里），否则"测试通过"可能只是没走到被测路径。

**2026-10-17 · 拍板 4 项 + 剩余项收口（我做的决定，理由都写在下面）**：

| 项 | 决定 | 落地 |
|---|---|---|
| **P2-7 级联①** | **做，但默认关**（`Parent` 是 provenance 的契约不能默认改） | `Fabric.WithCascadeRetire(bool)`：开启后 `Retire` **深度优先带走整棵子树**，同一把锁内完成（不会有子 agent 被并发 spawn 进半拆的树）；`children` 遍历**带 visited 防环**（它持锁跑，环=整个 fabric 挂死）；**每个被级联的 agent 各自发 `agent.retired` 事件**并带 `cascade_root=<根id>`，可归因。测试 2 条（默认不级联 / 开启后整树+归因；环索引下不挂死）+ **反证**（关掉级联块 → `child must be retired by the cascade` 等 3 条精确失败） |
| **P2-14② 上限形态** | **不发明数字**：复用仓里既有的 `evolution.guardrails.max_tools_enabled` 作为**告警线**（同一个关切"agent 该在多少个工具里选"） | `serve_wiring.go`：注册工具数 `> max_tools_enabled` 时 `Warn`，并写明"**这是报告不是上限：不会被静默丢弃**"。未配置时（默认 0）不告警，但**数量始终记录** ⇒ 面永远可见，只是不被评判 |
| **P2-10 阈值默认** | **保持 0.4 不变** | 理由：改默认=改**所有部署**的召回/精度，而两条路径**都已可配**（`memory.rag_min_score` / `knowledge.min_score`），且"0.4 对 1024 维余弦是弱下限、不是相关性保证"已写进两处常量注释。**决定=不改**，因为这不是缺陷而是需要数据支撑的调参 |
| **P0-1 定时真实评测** | **不做** | 理由：每个 case 一次模型调用，且要配**基线**才能判断漂移；而"语料陈旧/失去锚定"这半已由**零成本探针**覆盖。**这是本专项唯一我明确选择不做的事**，因为它把一个可观测性需求变成持续账单 |
| **P2-9② 表名烘焙维度** | **不做改名** | 表名是**迁移工程**（`knowledge_chunks_1024` 改名要动迁移+所有查询+存量数据），收益只是"名字更诚实"。维度一致性风险已用**单源常量**（`postgres.DefaultVectorDimension`，生产侧无第二处字面量）压住；表名与维度的耦合已写进该常量注释 |
| **P2-9③ 共实例** | **接受（部署属性）** | 向量与事件账本共用一个 PG 实例、无资源隔离是**部署拓扑**，代码层无法隔离。缓解=独立实例/连接池上限，属运维；本台账保留为**已知接受风险**而非"待修" |
| **§四 `StoreExporter`** | **关闭（台账过期）** | 复核：`ares knowledge export|import` **已存在**（`cmd/ares/knowledge_store.go`）、`ExportStore` 已实现且带 `RepresentationLister` 回退（快照连向量一起带，避免"往返后召回静默退化为纯词面"）⇒ **不是"无人实现"**。真正剩下的缺口是 `StoreSnapshot.SchemaVersion` **写入但导入时不校验**（实际守门是 per-object `MigrateObject`）——已改记为这一条 |
| **§四 `SearchSimilarTasks` 无 user 维度** | **不做** | 改它要动**接口签名 + 跨模块调用点**，且当前唯一调用方是 L2 的"相似任务"提示（不是权限边界）。**风险等级=提示质量**，不是越权读取；若将来该结果进入权限判定，必须先加维度 |

**2026-10-17 · code review（本会话全部代码改动）+ 自记错误**：

**静态**：`go vet` 本轮触及的包全绿；`make check` 含 `golangci-lint` **0 issues**；新文件**无静默吞错**（`_ = `/`, _ :=` 零命中）、无 TODO/FIXME。

**覆盖（逐函数，包级数字会掩盖新文件）**：`candidate_replay.go` **100%**；`corpus_monitor.go`：`InspectCorpus` 92.9%、`runCorpusProbe` **52.6% → 100%**；`runtime_arena_executor.go`：`NewRuntimeArenaExecutor` **71.4% → 100%**、`Execute` **0% → 42.9%**、其余 95–100%。

**本轮 review 查出并补掉的三处真缺口**（"接了但没验证"是本专项的常见失败模式）：

1. **`Execute` 覆盖率 0%**——测试只打了内部 `runtimeFor`，**而 scorer 真正调用的是 `Execute`**。已补：它把"无法运行的策略"与"Close 之后"两种失败**从 seam 里抛出来**（不能吞成空答案——空串会被判成"答得差"）。
2. **构造器缺件分支未测**——已补：无 provider / 无 base ctx / provider 返回 nil 三条都**按名字拒绝**。
3. **探针的 tick 循环体未测**——已补：首 tick 上报、**零锚定告警**、**语料被改坏后告警而不退出**（后者正是探针存在的理由：G3 只会在 promote 时才撞见）。

**如实标出的剩余缺口（未补）**：`Execute` 的 42.9% 里，**成功分支仍未覆盖**——它需要运行时真的产出一个答案，而 `CandidateReplayFactory` **不暴露 executor 注入**（默认走 router）；用 stub chat client 走默认 router 的产出不稳定，不宜写成单测。**结论**：成功分支目前**只有 live 集成路径能碰**，而那条 live 用例直接驱动 `CandidateRuntime`、**没经过 `RuntimeArenaExecutor`** ⇒ 这个差异是真的，记录在此，不假装已覆盖。

**自记（本会话我自己造的错，均已当场修，且各留下一条纪律）**：

1. **`CandidateRuntime.Close` 初版用 `sync.Once`** ⇒ "先 Close 再 Start"把 once 烧掉，之后 Close 变空操作、**drain 环永不停止**（每候选泄漏一个 goroutine）。*测试逼出来的*。→ 纪律：**有生命周期的 Close 用显式状态，不用 `sync.Once`**（Once 只适合"一次性的初始化"，不适合"可重复的收尾"）。
2. **删整行时用 `cut -c1-200` 的截断文本作匹配** ⇒ 文件里留下残句（`zh/02`）。→ 纪律：**删除整行必须用 `sed -n 'Np'` 的完整行**。
3. **多次"只看 `ok` 行"的假绿**：`go test … | grep -E '^ok'` 会漏掉同一次运行里的 `--- FAIL`（`make check` 全绿也可能掩盖单元失败）。→ 纪律：**门禁与单元分开看退出码**。
4. **反证变异未落地却当成"通过"**（`import` 变未使用导致编译失败、多行 `perl` 没匹配）。→ 纪律：**先数变异标记行数 + 确认编译通过，再跑测试**。
5. **替换代码块时 `old_str` 漏掉前面的 `r.mu.Lock()`** ⇒ 留下**重复加锁 = 自死锁**（`aresrecovery` 包 601s 超时，靠 goroutine 栈定位到 `recovery.go:373`）。→ 纪律：**替换一段代码必须包含定义其契约的邻近语句**（这里是"谁持锁"）；这类错**不会编译失败，只会静默挂死**。
6. **候选运行时缓存初版无界**（每候选一个、永不释放）⇒ 滚动部署下是**泄漏**不是缓存。→ 改为**有界保留 2**。→ 纪律：**缓存必须有界，且"有界"要能被测试（`BoundsWhatItKeeps`）**。

**2026-10-17 · 文章清理收口（`docs/articles/*` 的"自我更正框架"全部处理完）**：

判据：文章的**主体**是当前代码；"谁在什么时候写错了"归本台账，不归文章。处置方式三选一——
**与正文重复的横幅 → 直接删**｜**横幅托着失效正文 → 事实并入正文再删框架**｜**只讲"旧稿漏了/写错了"而无新信息 → 整条删**。

**最终复扫（扩充词典：更正/订正/勘误/传言/旧版/老文/偏离说明/correction/revised N/old version/earlier (draft|version)/older docs/the old article/the old draft/complete rewrite/In the old version/replaced wholesale）只剩 5 个文件命中，全部判定为正当**：

| 文件 | 命中 | 为何保留 |
|---|---|---|
| `en/23-quant-trading` | 5 | **整篇就是复盘**："The old article claimed the quant module had 9,768 lines…" / "§2.3 The old article itself (real, but its content is not)" / 教训"docs and reality drift apart"。引用即主题 |
| `en/24.3` + `zh/24.3` | 1 + 2 | benchmark **环境说明**（机器/编译器/`-benchtime`）+ "自行**订正**首轮的 GC 干扰"= 方法说明 |
| `zh/00-goagentx-intro` | 1 | **范围免责**："引用的 arena/orchestrator 标识为旧版本/演进过程文本" |
| `zh/11-autonomous-evolution` | 1 | `active(旧版)` 是**状态图里的状态名**，不是叙述 |

**本轮顺手改的两处**：`en/13` 开头的 "Older docs described… (historical narrative, unverified)" + **一段非真实代码**（`// ⚠️ Illustrative only`）→ **整块删除**，直接进入当下真实装配（`Bootstrap(ctx, cfg, deps)`）；`en/05` 的 "One important correction up front" → "clarification"（那是**对读者的澄清**，不是自我更正，与 `en/16` 措辞一致）。

**方法记录（我踩过两次）**：**不要引用旧的待办清单**——本轮按上一轮的清单去改 `en/22`/`en/06`/`en/12`，三个文件**早已清干净**（grep 空）。清单会过期，**每次动手前先重扫**。

**2026-10-17 · A 组 5 项：两项落地 + 一项用真 provider 实测定案（+ 一项顺带发现）**：

1. **P2-14② 工具总数可见（已落）**：`wiringServeToolchain` 在装配末尾打
   `"serve: agent tool surface registered" tools=<n> capped=false` + "若太宽怎么办"的下一步；
   **刻意不发明阈值**（上限该按 server／能力域／token 预算，是产品决定），因为"静默限流会不声不响地丢工具，
   比一个响亮的数字更糟"。缺陷的**内核是"无人知道有多大"** ⇒ 这一半已消除，上限形态仍开放。
2. **P2-10 阈值边界写明（已落）**：两处 `DefaultMinScore = 0.4` 都补上——**0.4 对 1024 维余弦是弱下限**
   （无关文本常在 0.5–0.6，所以它主要挡近似正交，**不是相关性保证**；0.4→0.5/0.6 是用召回换更锐的上下文块，
   属产品决定）；并写明两处**同号不同义**（经验置信度 vs store 的 FinalScore），**不合并**、但互相指认。
3. **P0-4A：决策输入用真 provider 实测完毕（这是该文档自己要求的先决步骤）**：
   新增 `-tags integration` 的测量用例（**只断言"测到了东西"**：模型至少调过一次工具，否则"没有工具回合"
   会被误读成"每个工具回合都空"）。实跑 **5 次**：**工具调用回合 5/5，其中 Content 非空 0/5，rune 数全为 `[0 0 0 0 0]`**。
   ⇒ **档 0（存 `resp.Content`）在最需要理由的回合上一无所得**；**档 1（存 thinking）类型上还不存在**
   （`GenerateResponse` **没有** `ReasoningContent` 字段，要先改类型）。**定案：走选项 ① `record_rationale` 元工具**
   （该文档原本的倾向，现在有数据支撑）。**实现尚未做**——它是要动 planner 生长路径的真功能，不是文案。
4. **顺带弄清的两件事实**：① `disposable-agent.md` 里 A1 的**方案 B 早已落地**（2026-10-13），
   所以"A/B 待拍板"指的是 `cognition-persistence.md` 那条线；② 测量本身证明"先测再选"是对的——
   若按档 0 直接实现，会在工具回合存下一串空字符串，而且**看不出是空的**。
5. **P2-7② 对账指标（已落）**：不做级联（`Parent` 是 provenance-only，改它=改契约），改为让**累积可见**：
   `agentfabric.AuditOrphans(sample)` = 孤儿数 + **它们仍持有的资源** + 有界样本；挂在 `aresrecovery.Recovery`
   上（该层已跑回收 sweep、已付全量遍历成本 ⇒ **不改任何签名**），sweep 末尾**仅非空时**打 Warn。
   测试 2 条（退役父留下的；**父记录整个丢失**也算孤儿）+ 反证（去掉"父已退役/缺失"判据 →
   `with the parent alive there is nothing to reconcile`）。**说明**：级联方向 ① 仍未做，那是契约变更，需拍板。
6. **P2-1：更正我自己的判断——这道绝对闸门早就存在**。`evolution.gates.eval_min_score` →
   `bootstrap_evolution.go:251` 的 `minScore` → 门 `MinScore`（`>0` 覆盖，否则门自身 **0.7**，`gate_eval.go:45`）。
   我上一轮把它列为"仍缺绝对水平闸门"是**错的**。**已补的**：启动日志打 `min_score=<有效值> source=<配置/门默认>`
   ——此前数值**从不露面**，运维既不知道在生效的绝对线是多少，也分不清读到的是自己的策略还是门的内置下限。
   ⇒ **A 组 5 项到此：5 项全部收口（P2-7②、P2-10、P2-14② 已落；P2-1 更正为"早已实现+补可见性"；
   P0-4A 连"注册/广告"也已落，见下条）**。
7. **P0-4A 收口，并更正我上一轮的判断（2026-10-17）**：我上轮说"真正的实现欠账只剩 `record_rationale` 元工具 /
   注册位置待拍板"—— **这句是过期的**：该工具早已端到端落地，且**注册位置的选择题并不存在**：
   ① `rationaleToolSchema()` 在 **planner 认知自己的广告面**里追加（`planner_cognition.go`，**不进通用注册表**）
   ⇒ 其他 agent 的工具面与启动工具总数都不变；② `growToolNodes` **跳过**该调用（它不长节点、不回放，
   参数已被 `rationaleFrom` 消费）；③ 只有它一个调用的回合用**自己的原因**结答（`answerCauseRationaleOnly`，
   不冒充 `l1_blocked`）。**9 条测试全过**（含"schema 与消费方的名字/键一致且能往返"这条防静默契约）。
   **教训**：查"某机制是否已落"要 grep **实现点**，不能凭上一轮的结论——我这条连续两轮的错误都是这么来的。

**2026-10-16 · code review（本会话全部改动）+ 自记错误（含已修）**：

**静态与覆盖**：`go vet` 本轮触及的包全绿；新文件**无静默吞错**、无 TODO/FIXME。覆盖率：`agentruntime` **84.5%**；`ares_bootstrap` 包级 65.2%，**新文件逐函数复核**——`candidate_replay.go` **100%**、`runtime_arena_executor.go` 多数 85–100%，**但 `Execute` 曾为 0.0%**（测试只打了内部 `runtimeFor`，没打 scorer 真正调用的那个入口）⇒ 已补入口测试（**无答案必须是错误、不得是空串**——空答案会被 scorer 当"答得差"计分）。

**自记（我自己造出来的错，均已当场修，且留测试或反证）**：

1. **`CandidateRuntime.Close` 初版用 `sync.Once`** ⇒ "先 Close（未 Start）再 Start"会把 once 烧掉，之后真正的 Close 变成**空操作、drain 环永不停止**（每个候选泄漏一个 goroutine）。**是测试逼出来的**，改成显式状态 + 拒绝 Close 后 Start。
2. **删整行时用了 `cut -c1-200` 的截断文本作匹配** ⇒ `zh/02` 留下残句"有一条分发路径。"。纪律：**删除整行必须用 `sed -n 'Np'` 的完整行**。
3. **多次"只看 `ok` 行"的假绿**：`go test … | grep -E '^ok'` 会漏掉同一次运行里的 `--- FAIL`（`make check` 全绿也因此可能掩盖单元失败）。纪律：门禁与单元**分开看退出码**。
4. **反证变异未落地却当成"反证通过"**：一次 `import` 变未使用导致**编译失败**、一次多行 `perl` **根本没匹配**，而我先看到了"测试通过"。纪律已固化为：**先数变异标记行数确认落地，再跑测试**。
5. **`RuntimeArenaExecutor` 初版对每个候选的运行时无界缓存**：滚动部署会一直比较新候选 ⇒ 不是缓存是**泄漏**（复查时发现；改为**有界保留 2**，更早的立即 `Close`）。

**审计发现的两处台账失准，均已更正**：P2-2 的"全量拿锁"（实测只在快照 ID 集时持读锁）；P2-6b 的"快照 1024 静默退化"（**是我上轮找错包说"未能定位"**；实际是 `fabric/agent` 的 `maxDeathSnapshots`，且有 1h 时间上限 ⇒ 属有意边界）。**教训**：审计结论里的"找不到"同样要标明**找过哪儿**，否则它和"不存在"看起来一样。

**2026-10-16 · P2 层"是否彻底完成"的逐条取证审计（结论：没有；并发现两处台账失准）**：

审计方法：不看标记，**回代码取证**（每条 1–2 处 grep / 读实现）。

| 项 | 取证 | 结论 |
|---|---|---|
| P2-5 两个预算无联合视图 | 审计时全仓无联合视图；**同日已落**（快照带任务预算 + 调用方一行同报两者，皆尽时 Warn） | ✅ 已落 |
| P2-6a 复活预算 lifetime 累积 | `MaxRestarts` 注释自陈 "the **total** restart attempts allowed"（无时间窗） | ✅ 仍开 |
| P2-7 孤儿 agent 累积 | `retireLocked` 只 `releaseLocked(a.resources)`，**不处理它 spawn 的子 agent** | ✅ 仍开 |
| P2-8 dead-letter 非持久 | `DeadLetterStore` = 进程内存（`entries []DeadLetter`，容量 1024，`internal/agentipc/deadletter.go:32`） | ✅ 仍开 |
| P2-9 维度写死 | `defaultVectorDimension=1024`（列类型 DDL）+ `VectorDimension: 1024`（provider 配置）**两处字面量**；表名 `knowledge_chunks_1024`/`experiences_1024` | **①已修 2026-10-17**（单源化：导出 `DefaultVectorDimension`，bootstrap 引用；静态验证生产侧无第二处维度字面量）；②表名仍烘焙维度、③共实例（部署属性）**仍开** |
| P2-10 RAG 默认关·阈值 0.4 | `EnableRAG bool`（Go 零值 = false）+ `MinScore` "Defaults to 0.4" | ✅ 仍开 |
| P2-11 工具名撞车 first-wins | `ErrToolAlreadyRegistered` + builtin 注释 "The pre-existing tool wins."；日志原为 `winner="builtin"` | **已关闭 2026-10-17**：策略有意（既存优先），缺陷是报告**臆断赢家**（两 MCP server 同名时会指名没参与的 builtin）⇒ 改为 `kept="pre-existing registration"` + `skipped_server` |
| P2-14 discovery 默认关 | `config_defaults.go:290` "opt-in via Enabled (**default false**)… the single security boundary" | **默认关判定为有意、非缺陷**；"工具规模无上限"**仍开**（演化白名单有 `MaxToolsEnabled`，启动注册总数无上限） |
| P2-3 stableRound 并发前提 | 实现自陈依赖"同一 stable round 重导出同一批 ID"（隐式单写者假设） | **已关闭 2026-10-17**：同一 session 的图由单写者推进（调度器按 session 串行 lease）⇒ 前提成立；留痕"若引入并发改同 session 图需重评" |
| **P2-2 Reconcile 全量拿锁** | `Reconcile` **只在拷贝 ID 集时持读锁**（`RLock` → `for id := range planIDs` → `RUnlock`），**重活在锁外** | ⚠️ **台账表述失准**：不是"全量拿锁"；O(nodes) 与 RUNNING desync 的可能仍在，但措辞要改 |
| **P2-6b 快照 1024 静默退化** | 我上轮在 `fabric/task` 与 `aresrecovery` 找不到上限；**2026-10-17 定位到**：`internal/fabric/agent/snapshot.go` 的 `maxDeathSnapshots = 1024`（死亡快照条数上限）+ `defaultDeathSnapshotRetention = 1h`（时间上限） | **已关闭**：双重上限 + 注释写明"按 `DiedAt` 淘汰最旧、恢复只取最新"⇒ 属**有意的边界**，非缺陷。**我上轮的"未能定位"也不准**（找错了包：应在 `fabric/agent`） |
| P2-1 G2 三道门盲区 | 本质是"门防回归、不防整体漂移"的设计缺口 | **被 P0-1 收窄**（判据四项已落：确定性 floor 堵住"两候选都答错、判官都给好评"）；**仍缺绝对水平闸门**（阈值待拍板） |

**处置（2026-10-17 更新）**：P2-2 措辞已改并**关闭**（漏事件路径本就有日志）；P2-6b **已定位并关闭**；P2-11 **已关闭**（报告不再臆断赢家）；P2-3 **已关闭**（单写者前提成立）；**P2-9 维度单源化已修**（另两半仍开）；P2-14 的 discovery 默认关判定为**有意**；P2-1 被 P0-1 收窄。

**仍未做（2026-10-17 全量登记，按"是否需要拍板"分组；本块属上面这份审计的结论，不另起章节号）**

**A 组 5 项最终状态（2026-10-17 全部收口；每行末尾写明"仍归你的决定"是什么）**

| 项 | 状态 |
|---|---|
| P2-14② | 启动注册工具总数的上限**形态**（按 server／按能力域／按 token 预算）——**已落 2026-10-17（可见性）**：`wiringServeToolchain` 启动时打 `tools=<n> capped=false` + 下一步选项；**不发明阈值**（上限形态仍是产品决定，但"无上限且无人知"已消除） |
| P2-7 级联 | ✅ **① + ② 都已落地（2026-10-17）**。**② 对账**：`agentfabric.AuditOrphans(sample)`（孤儿数 + 仍持有资源 + 有界样本），挂在 `aresrecovery.Recovery`，sweep 末尾仅非空时 Warn；测试 2 条 + 反证。**① 契约变更（你已同意）**：`Agent.Parent` 由 **provenance-only 改为生命周期关系**（仍**非权限**关系——invariant #1 不变），`NewFabric` **默认级联**：`Retire` 深度优先带走子树，每个被级联者各自发 `agent.retired` 且带 `cascade_root`；`WithCascadeRetire(false)` 保留旧的单点语义。**测试改写**：默认级联 1 条 + 显式关闭 1 条；3 条测"孤儿/边清理"的旧测试改为**显式 opt-out**（它们要造孤儿，且主题不是级联）。**review 发现并记录的一处不对称**：`ReclaimIdleAgents` 直接调 `retireLocked` ⇒ **仍是单点**，理由是"父空闲被回收不该连带杀掉正在干活的子"，遗留部分由 `AuditOrphans` 报——已写进代码注释，避免两条路径的语义差异变成静默 |
| P2-1 | 绝对水平闸门的**阈值与口径**——**更正我上一轮的判断：这道门早就存在且已接线**（`evolution.gates.eval_min_score` → `bootstrap_evolution.go` 的 `minScore` → 门的 `MinScore`，`>0` 时覆盖、否则用门自身 **0.7**，见 `gate_eval.go`）。**已落 2026-10-17（可见性）**：启动日志打 `min_score=<有效值> source=<你的配置/门默认>`，因为此前**数值从不露面**，运维无法判断"在生效的那条绝对线"是多少、也无法区分读到的是自己的策略还是门的内置下限。**阈值本身（0.7）仍是你的政策决定** |
| P2-10 阈值 | `DefaultMinScore = 0.4`（**两处**）——**已落 2026-10-17（边界写明）**：两处各自补上"0.4 对 1024 维余弦是**弱下限**、只挡近似正交、不是相关性保证"与"两处同号**不同义**（一处过滤经验置信度、一处过滤 store 的 FinalScore），并互相指认。**默认值本身仍是你的决定**（0.4→0.5/0.6 = 用召回换更锐的上下文块） |
| P0-4A | rationale 持久化：**决策输入实测（5/5 工具回合 Content 全空）⇒ 走选项 ①**；**已全部落地 2026-10-17**：① `rationaleFrom` 无 Content 时从 `record_rationale` 调用的**参数**取（首个、畸形即缺席、**同受 512 上限**）；② **该调用不长节点**（它是笔记不是活，否则每个陈述都在图里留一个空节点）+ 计数回传；③ **只给 planner 认知广告**这条 schema（**不进注册表** ⇒ 其他 agent 工具面与启动工具数都不变；且它**从不被执行**，参数已被消费、节点已跳过）；④ rationale-only 回合用**自己的原因**结答（`answerCauseRationaleOnly`，**不冒充** `l1_blocked`——那是模型行为，不是约束失败）。测试 **7 条**（含"schema 与消费方名字/键一致且能往返"这条**防静默**契约）+ 反证（去掉提取路 → `expected "comparing both reports…" actual ""`） |

**B. 仍需做的（2026-10-17 逐条回代码核实后仅剩这一条）**

| 项 | 状态 |
|---|---|
| **M6 `TieredRetriever`**（0.3.4 最后一条） | ✅ **关闭 2026-10-17（opt-in）**：① namespace 失败关闭（本就如此）；② 性能：`Limit:0` 改 `lexiconScanLimit=2000` 有界扫描，截断**用"多要一条"确定性判断**（`len>=cap` 在正好 N 条时会报假警）；③ 验收 `TestRetrieval_TieredL1L2L3Fallback`；④ **接线**：`knowledge.tiered_retrieval`（**默认关**）→ `wireRetrievers` 用 tiered **替换**（不叠加，避免同一语料注入两次）+ 适配器 `tieredContextRetriever`（图→snippet、**按请求解析 namespace**、Score 降序、空输入→空+nil）；这是 `retriever.New`/`compiler.NewDefaultCompiler` 的**首个生产调用方**。测试 3 条 + 反证（去排序 → 20 次取样在第 2 次失败）。**review 修的 3 处**：开而缺 runtime → Warn（不静默）；缺 store → 明说 **L1 DISABLED**；截断判据假阳性 |

**C. 已决定不做（有理由，不是遗漏）**

| 项 | 决定与理由 |
|---|---|
| P2-9② 表名烘焙维度 | **不做改名**：迁移工程（迁移+所有查询+存量数据），收益仅是"名字更诚实"；一致性风险已由 `postgres.DefaultVectorDimension` 单源压住 |
| P2-9③ 共实例 | **接受**：向量与事件账本共用 PG 是**部署拓扑**，代码层无法隔离；缓解=独立实例/连接池上限 |
| P0-1 定时真实评测 | **不做**：每 case 一次模型调用且需基线才能判漂移；"语料陈旧/失去锚定"已由**零成本探针**（`eval_probe_interval`）覆盖 |
| §四 `SearchSimilarTasks` 无 user 维度 | **不做**：要动接口签名+跨模块；当前只喂 L2 的"相似任务"提示（**不是权限边界**） |

**D. 已落（本会话，全部回代码核实；含此前台账写过"未做"但实际已落的）**：P0-2、P0-3、P1-1…P1-7、P2-2、P2-3、P2-4、**P2-5**、**P2-6①（`RestartWindow` 默认 1h）**、P2-6②、**P2-6③（滚动升级规则写进版本常量）**、**P2-7（可见性 + ② 对账）**、P2-9①（维度单源）、P2-10（边界写明）、P2-11、P2-12、P2-13、**P2-14②（工具总数可见）**、P2-1（更正为早已实现+补可见性）、**P0-4A（含 planner 专属广告/跳过/原因，9 测试）**、语料 **40→73**、`SetDependencies` DAG 校验、**P0-1**（判据四项 + per-candidate 回放四步 + **语料探针**）、**M2.5 关系门（接线）**、**M4（BuildReport producer + 增量）**、`StoreExporter`（**更正：CLI 与 `ExportStore` 早已存在**）。

**2026-10-16 · per-candidate 运行时的**构件**（步骤 1+2／共 4，**尚未接线**）**：

- **接缝已落**：`agentruntime.FixedStrategySource`——运行时的策略是经**单方法接口** `agents.StrategySource`
  读的（消费方只有 `planner_cognition`），所以"按候选跑"只需替换该来源：**不改运行时**。这也说明另一条路是
  **错的**而不是"不完整"——把真实执行器直接指向未改动的运行时，**所有候选都会用当前已晋升的策略打分**，
  比较会看起来稳定却在测同一个东西。
- **隔离运行时已落**：`agentruntime.NewCandidateRuntime`——每候选一套**独立 task/agent fabric** +
  `NewExecution{StrategySource: 候选}` + `kernel.New(...)` + 自己的 drain 环 + `Close()`。
  **executor 由调用方提供**（工厂签名 `func(fabric, agents) (map[string]CapabilityExecutor, error)`），
  因为 executor 绑定特定 fabric：接受为服务期 fabric 建好的 map，就会把一份运行时的任务派给另一份的执行体。
- **测试（6 条）**：缝的语义（含 nil=不干预）、四类缺件 fail-loud、**隔离**（两个候选的 executor 必须绑在
  各自 fabric 上、两 fabric 互不相同）、`Close` 停环（带 2s 上限断言，防挂死）、`Close` 幂等 + 未 Start 也安全、
  `Start` 幂等 + **Close 后拒绝 Start**。**反证**：让来源恒返回 nil → `the source must serve the candidate itself`；
  让 executor 绑到别的 fabric → `executors must be built over the runtime's own fabric`。
- **自记（我自己的测试抓出的真 API bug）**：`Close` 初版用 `sync.Once` ⇒ **先 Close（未 Start）会把 once 烧掉**，
  之后真正的 Close 变成空操作、循环永不停止。已改显式状态（`cancel/done/closed`）。**是测试逼出来的**。
- **真实 provider 已验证可用**（`-tags integration`，配置读仓库根 `ares.yaml`，**密钥从不打印、不落地**）：
  `*llm.Client` **结构上同时满足** `eval.LLMClient`(`Generate`) 与 `agentfabric.ChatClient`(`Chat`) ⇒ 评测链路
  与运行时都**不需要适配器**。实测 `provider=openai model=agnes-2.5-flash` → `PONG`。
  live 用例只断言**不变量**：case 没有答案必须**报错**（`ErrSessionStalled`）而不是空串——空串会被当成
  "答得差"计分，整轮评测就会在一个它报成低分的失败上排序。
- **工具边界（已评定并落为天花板）**：边界**从既有声明推出来**，不是我发明——每个 builtin 工具注册时都带
  `side_effects` / `mutates_state` 标签（`builtin.go`）。`EvaluationToolCeiling` = 只读子集**减去结果不可比的那些**
  （网络类 `WebSearch`/`HTTPRequest`/`EmbeddingTool` 因"两次运行结果会变、有成本与限流"被排除；`PDFTool` 读宿主文件）。
  **天花板语义**：候选自己的 `Params["tools"]` 只能**收窄**它，**不能放宽**（更宽要显式改导出的默认值，
  因为那是"用真实网络评分"这个测量决定，不是安全决定）。沙箱由**工厂自己装**，不靠调用方记得。
  **核实到的编码陷阱（重要）**：`ParamKeyTools` 的契约是"**空/缺省 = 不过滤（全部工具）**"⇒ 天真地把
  "交集为空"写成空串，会让候选**因为要了被禁的工具而拿到全部工具**。故对"空交集"**响亮拒绝**。
  **反证**：把拒绝改成 `if false` → `An error is expected but got nil`（测试先确认变异落地才跑）。
- **接线（步骤 3的一部分，已落）**：`ares_bootstrap.CandidateReplayFactory` + 配置开关
  `evolution.gates.regression_runtime_replay`（**默认 false**，理由是**成本**：LLM scorer 把一轮候选压成 2 次调用，
  而真实回放是**每候选每 case 都真跑**；而 arena 门本身是 auto-arm 的，把回放做成默认 = 让"默认"去乘订单）。
  工厂自带 fail-loud（缺 chat client / **缺 tool binder** 都按名字拒绝——binder 在 `cmd/ares`，是调用方必须交的那一半）、
  沙箱、默认 executor，并在装配时**打出边界（每个候选实际跑在哪些工具上）与成本**。
  **执行体适配器也补了**：`agentruntime.RouterExecutor`（router→`CapabilityExecutor`，**照抄服务期的
  `kctx.WithCallerID` 身份盖章**——不盖章，syscall provenance 会退回 LLM 给的可伪造身份）。服务期有等价物
  （`peerExecutorAdapter`）但在 `package main`，对别的装配不可见。
  **依赖顺序纠正**：executor 是 core 的 router 的适配器 ⇒ 工厂**先建 core 再建 executors**（`cfg.Executors`
  现接收 core；`nil` = 用默认）。
  **测试**：适配器 4 条（身份盖章、nil 结果、三类 fail-loud、默认集必须覆盖 plan 能力）+ 工厂 3 条。
  **反证**：盖错身份 → `expected "candidate-plan", actual "someone-else"`。
- **仍未接线（步骤 3 的最后一里 + 步骤 4）**：**消费方**——即"运行时回放的 arena scorer"+ 在读
  `regression_runtime_replay` 后**在门里选中它**（现状仍走 `LLMArenaScorer`），以及 `cmd/ares` 把
  `newToolBinder(internalReg)` 交给工厂的那一行。**在那之前，per-candidate 回放有全部构件但没有驱动它的开关**。
  真实 executor 在 `cmd/ares`（服务期 kernel 所在），所以还缺
  **① 那里的候选运行时工厂**（真工具/沙箱工具 + tool binder）、**② G3/arena 的 opt-in 开关 + 写明成本的启动日志**。
  在那之前，`CandidateRuntime` 是**有测试的构件**，不是在生产跑的路径。

**2026-10-16 · 语料扩容（P0-1 真缺口）+ `SetDependencies` DAG 校验**：

1. **语料扩容：40 → 73 条带锚**（`test/eval/golden.yaml` 追加 **33 条**，覆盖全部 7 轴）。
   逐条都带**可手算核对**的期望值（世界知识用 `contains`、算术用 `numeric`、短规范输出用 `exact`），
   对抗格新增 7 条（Sydney/Canberra、Saturn/Jupiter、柏林墙年份、字节位数、母语人数、最深海、
   9×9 这类"自信错答最便宜处"）。**守门阈值 20 → 65**（`minAnchoredCorpusCases`，略低于实际 73：
   删几条必须失败、加几条不用改它），并写明抬高理由。
   **反证**：阈值改 100 → `corpus has 73 …, want >= 100` 失败；追加一条"声明 `numeric` 却无
   `expected_output`"的用例 → 体检报 `declared check "numeric" is unresolvable … (corpus bug)`。
   **边界**：语料**仍然薄**（多为单事实/单算术），且新增用例的期望值由我人工核对——**锚的宽度仍等于语料宽度**。
2. **`SetDependencies` 补 DAG 校验（运行时改边路径原本一个校验都没有）**：
   `Create(..., WithResolvedDependencies())` 这条**严格路径**早已拒绝"未知依赖 / 空 id / 自环"，
   而**运行时改边**（`SetDependencies`，planprojection 的增量编译原语）**只查存在性与状态** ⇒ 能写出
   自环、悬挂依赖、闭环，三种都让任务**永不 READY 且无报错**（与 P0-2 同族）。
   **修法**：改动**之前**校验三类 —— 自环与闭环 → `apperrors.ErrDependencyCycle`、未知依赖 →
   既有 `ErrDependencyUnresolved`（与 create 路径同词表）；**被拒时依赖表不动**（§3.6）。
   环检测用**带 visited 的迭代遍历**：图里若已有历史环（旧 store 恢复）也必须终止，否则会**持锁挂死**
   整个 fabric——那比它要防的"停住"更糟。
   顺带把 `ErrDependencyUnresolved` 纳入 **T-F1 永久错表**：重试无法让依赖凭空出现，不列入就等于
   再犯一次"永久错白烧预算"。
   **反证**：去掉校验 → 四类非法改边全被写入（4/4 失败）；**改动后 `internal/fabric/...` 全树通过**
   （含 `planprojection` —— 它失败时会记 `SkippedOp`，所以拒绝是**被记录**的，不是新静默）。
   **既有测试冲突的处置**：`TestSetDependencies` 原夹具用了**不存在的 `"c"`** 并断言成功（它的意图是锁
   "fabric 自己持副本"）——已把夹具改成真实任务并注明原因，**没有**为了让老断言继续绿而放宽校验。
3. **本批未做（如实）**：per-candidate 真实执行器回放（架构方向）、P2 十条、P0-4 A 档（方案 A/B 待拍板）。

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
7. **⑥ `eval_suite` 默认指 golden（已落，含一处必须先解决的风险）**：拍板口径是"默认武装 G3、`eval_strict`
   保持 false"。但**实测发现该口径本身不成立**：`eval_strict` **只管"空值"那一路**，而 `eval_suite` 一旦非空、
   基础设施（registry/LLM client）缺失就是**硬失败**（`eval_gate_wiring.go`）⇒ 直接默认会**让没有 eval LLM key
   的部署起不来**。**落法**：`resolveEvalSuite` 解析出"是否来自默认值"，`buildEvalGate` **按来源分语义** ——
   **默认值**：缺文件/缺基础设施 ⇒ **降级为无 G3 + Warn**（没人武装过它，一个仓库相对路径不得挡住启动）；
   **显式配置**：仍**失败关闭**（typo 或不接 client 不得静默变弱）。默认路径**存在才启用**（否则退回"无门"）。
   两处反证：让存在性检查失效 → `absent default = ("test/eval/golden.yaml", true), want no suite`；
   让默认值走失败关闭 → `a defaulted suite must degrade, not fail bootstrap`。
8. **arena 门：保留"默认自动武装"**（拍板确认）。配套做的不是关掉，而是**把成本写在启动日志里**：
   `llm_scoring_rounds_per_check = 2 × runs`（取 `regGate.Config()` 的有效值，而非配置原值），
   并让 G3 与 arena **共用同一份解析后的 suite**（否则"过了 G3 却在 arena 回退"是在比两份不同的语料）。
9. **两个死类：已删除**（`internal/storage/postgres/services/`，约 8500 行含 ~6300 行测试）。
   证据链：先把仓内唯一消费方（示例 `11-knowledge-import`）迁到**生产路径** `repo.SearchByVector`
   （QueryPrefix/TopK/MinScore 语义在调用方保留，并处理 `ErrRecordNotFound` "无命中"契约）→ 全仓
   `postgres/services` 引用**归零** → 删目录 → **`go build ./...` + `go test ./internal/... ./cmd/...` 全绿**。
10. **CLI 路径接入配置权重**（`openAKGStore` 已加载配置，改用它而非库默认），避免同一语义两套排序。
11. **`genome` 的 `TestSUSSelection_UniformDistribution` 已修（并自记一处没查明的事）**：该用例对**随机流**做
    5000 次采样的分布断言 ⇒ CI 里等于掷骰子。已加可注入 seed（`WithSUSSeed`，照该包 `WithTournamentSeed`
    的惯用法）并固定种子，**连跑 6 次稳定通过**。**但：我用 4 条不同确定性流都没能复现那次失败**
    （⇒ 我原先给的"~3.5σ、每隔几百次踩一次"**只是假设、无证据支持**，那次 `make check` 失败的真实成因**仍未查清**；
    整包 `-race -count=2` 也全过）。**不夸大**：seed 修的是"不可复现"，不是"已知那个 bug"。
12. **两项判断定案**（不再挂着）：**recency 默认保持 0**（窗口已按 updated_at 偏置，分数里再来一次是重复计入；
    开启属需要实测的生产排序变更，开关已给）；**生产词面项保持 Jaccard**（长度归一化且已 CJK 感知；换覆盖率会
    偏好长文档；死类里那个 coverage 版本随包删除、不作证据）。

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
   **已补落（2026-10-16）**：`evolution.gates.eval_suite` **默认指向 `test/eval/golden.yaml`**——
   但按来源分语义：**默认值**缺文件/缺基础设施 ⇒ 降级为"无 G3"+Warn；**显式配置**仍失败关闭（typo 不得静默变弱）。
   **仍未做（明确）**：**per-candidate 的独立运行时回放**（要为每个 genome 实例化运行时，属架构方向）。
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
   **已补落（2026-10-16）**：**确定性 Layer 0 进了 arena 的 paired 比较**——`ArenaCase` 把
   `ExpectedOutput`/`Checks` 带进打分（`String()` 只渲染 Input，**提示词一字未改**），判 Fail 由
   `groundTruthFloor` 在 **LLM 之外**压到 0（单条 + 批量两条路径都接；批量才是生产走的那条）。
   **仍未做**：定时触发；per-candidate 的真实执行器回放（现只接确定性压分）。

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
3. **触发方式：离线/CI 已落，evolution 前那一半也已接**（`corpus_test.go` 语料体检：声明的检查必须可解析、
   种类必须已知、锚定数不低于 `minAnchoredCorpusCases`；**arena 的 paired 比较带确定性压分** ⇒
   "两个候选都答错、判官都给好评"不再读成平局）；**仍未做：定时触发**（无 evolution 的部署里喂非零样本 +
   盯语料陈旧与判官漂移）。
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

> **全量未做登记（2026-10-17）见「修订记录」内那一块**（按"是否需要拍板"分组：A 待拍板 5 项 / B 可继续做 6 项 + 本会话已落清单）。本表只留"前几轮残留"的历史条目。

| 项 | 状态 | 出处 |
|---|---|---|
| `SetDependencies` 无环/存在性校验 | **已修 2026-10-16**：三类拒绝（自环 / 未知依赖 / 闭环），改动**之前**校验、被拒时依赖表不动；环检测为**带 visited 的迭代遍历**（历史环也不得挂死，否则持锁卡住整个 fabric）；顺带把 `ErrDependencyUnresolved` 纳入 T-F1 永久错表。反证 4/4 精确复现 | T1/T2「修复」段 |
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

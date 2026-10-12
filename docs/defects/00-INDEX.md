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
>
> **注意**：`A`（disposable 复活）与 `A`（agent 工厂）共用前缀，看文件名区分；
> `S` 也撞：`S-mcp`=service-loading 的 S1-S6，`S-st`=retrieval-and-storage 的存储侧 S1-S3。
> 下标一律带文件前缀（如 `A-dispo`、`A-factory`、`S-mcp-S5`、`S-st-S2`）。

---

## P0 · 最致命（方向性 / 静默卡死，生产先咬人）

| # | 缺陷 | 一句话 | 共因 |
|---|---|---|---|
| P0-1 | **G1** GA 无"做对没"信号 | fitness 只奖励"成功+快省"，没 accuracy → **越跑越会糊弄** | 认知/正确性没持久化 |
| P0-2 | **T1+T2** LLM 直连 `Create` 不校验 | `create_task` 依赖不存在/成环 → **任务静默永不 READY**（operator 走 `CompilePlan` 是安全的，只有 LLM 旁路漏） | 引用/边界没校验 |
| P0-3 | **D1+D2** 提前交差是静默的 | maxPlanDepth 撞墙 / L1 全拦 → 强制出 answer，**不标记成因、不告警** | 静默早停 |
| P0-4 | **Q3+A-dispo1+A-F4** 认知有损 | checkpoint/复活只带"进度"不带"思路" → 换人/续跑**丢推理链** | 认知/正确性没持久化 |

## P1 · 该修（高频咬人 / 一处改受益一片）

| # | 缺陷 | 一句话 |
|---|---|---|
| P1-1 | **T-F1** `classifyFailure` 默认可重试 | 忘 `MarkPermanent` 的**永久错白烧满重试预算**（+ backoff） |
| P1-2 | **R2** 检索纯余弦无精排 | 假近进 top-K，量大噪声高（需宽召回 + cross-encoder） |
| P1-3 | **R1** ivfflat `lists=100` 写死 | 数据量大召回率掉（无 HNSW 选项） |
| P1-4 | **D(retrieval)** embedding 死信 | 向量生成失败的行落 `embedding_dead_letter` → **该经验对检索永久不可见且静默** |
| P1-5 | **I1** `ask_agent` 不回写 | 协作是"触发"不是"对话"，答案 C-1 未闭环，拿不到 |
| P1-6 | **A-factory3** agent quota 泄漏 | claim/release 漏释放 = **agent 池假死**（`ErrResourceQuotaExceeded`） |
| P1-7 | **S-mcp-S2** MCP 挂了工具静默消失 | server 连不上/断连 → 它的工具没进/留在 Registry，LLM **不知道自己少了什么**，会白调不存在的工具 |

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
| P2-11 | **S-mcp-S1** 工具名撞车 first-wins 静默 | 两个 server 同名工具只有先连的活，后到的静默 skip，无告警 |
| P2-12 | **S-mcp-S4** health 标了没人消费 | discovery 标 `Healthy=false` 但 MCP Manager 不摘工具 → 半死 server 工具仍可调 |
| P2-13 | **S-mcp-S5** transport 自动断连不摘工具 | 断连后工具留 Registry 变"僵尸"（execute 才报 connection refused） |
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
3. **T1+T2** — LLM 直连 `create_task`/`create_plan` **复用 `CompilePlan` 已有的**
   `resolveDependencies` + `detectPlanCycle`（或等价增量校验）。**消除 P0-2 静默卡死。**
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
- operator 建图**已有**依赖存在性 + 环检测（`CompilePlan`）
- PG/pgvector 分工（向量表 vs 关系表，不混一张表）
- G3 样本门槛/冷启动中性、G6 进化改 L1 不在跑任务无感
- **MCP/工具注册**：多 server 连接隔离（一个挂不连一个）、手动注册不被 discovery 误删（#51）、热重连先删旧再注新、Registry 拒绝重复名、discovery 默认关

# 缺陷与边界记录 · RAG / 记忆检索 + PG & pgvector 分工

> 本文档**只记"会咬你"的坑与已知边界**，不重复"RAG 怎么工作"（向量近邻 + TopK/MinScore）。
> 两条主线：**① 检索精度与规模**（向量那半边）、**② PG/pgvector 的分工与共享**（存储那半边）。
> 每条带源码锚点（v0.3.3 行号可能随 dev 分支漂移，定位以符号为准）。
> 性质标记：`缺陷`（建议修）/ `隐患`（规模/时间下暴露）/ `取舍`（设计权衡，知边界即可）/ `已做`（有保护，确认非缺陷）。
>
> **一句话总纲**：检索侧**能用但不精**（ivfflat 固定 lists、无精排、高维余弦阈值弱），
> 存储侧**分工清晰但共用一个 PG 实例**（连接池各自独立，但实例/IO 无隔离），
> 最隐蔽的一条是 **embedding 失败的 dead-letter 数据对检索"永久不可见"**。

---

## 一、先厘清 PG / pgvector 的分工（`已做`，先立正再谈坑）

**分工是"按数据类型分表"，不是"一张混合表"**（`migrate_storage.go`）：

| 半边 | 表 | 存什么 | 引擎 |
|---|---|---|---|
| **向量 + 文本** | `knowledge_chunks_1024`（`:15`）、`experiences_1024`（`:137`） | `embedding VECTOR(1024)` + 文本 + metadata + tsvector | **pgvector**（ivfflat）+ **tsvector**（全文） |
| **纯关系** | `tools`（`:239`）、`conversations`（`:280`）、`TaskResults`（`:316`）、`secrets`（`:360`）、`embedding_queue`（`:392`）、`embedding_dead_letter`（`:416`） | 结构化行 | 关系（无向量） |

**含义**：`knowledge_chunks` / `experiences` 是"**一行里同时挂向量 + 全文 + 元数据**"的表（pgvector + GIN tsvector），
其余走普通关系。**这是对的分工**（语义走向量、结构化走关系）——**先确认它没做错**，再往下看坑。

---

## 二、检索侧（RAG / 记忆）的坑

### R1 · ivfflat `lists=100` 写死，数据量大时召回率掉 〔缺陷 · 规模〕

`idx_knowledge_1024_embedding` 用 **`USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)`**
（`migrate_storage.go:74-75`）。

- **坑**：ivfflat 的召回率**强依赖 lists 与数据量的比例**。数据从几千涨到几十万时，
  `lists=100` 太粗 → 近似索引**漏召回**（把真相关的行落在没探测的 cluster 里）。
- 且是**写死的 100**——没有按数据量自适应调 lists / probes，也**没有 HNSW 选项**（HNSW 召回率更高，
  代价是内存 + 构建慢）。
- **建议**：数据一大，要么**动态调 ivfflat 的 lists/probes**（召回↑、查询↓），要么**上 HNSW**；
  当前"固定 lists=100"在规模上来后是召回率的天花板。

**修复（2026-10-15）**：**承认的真因不是"100 这个数不对"，而是"没有任何人再看它一次"**——
`CREATE INDEX IF NOT EXISTS` 对已存在的索引是惰性的，所以一个在空表上建出来的索引会永远保持
它当年的 lists，召回率随之静默下滑。

- **单一 DDL 源**（`vector_index.go` 的 `vectorIndexDDL`）：原先同一句索引 DDL 写在**三处**
  （迁移 + `CreateVectorTable` + `CreateCollection`），其中两处**根本没写 lists**。现在 method/lists
  只有一处定义。
- **`lists` 按数据量自适应**（`ivfflatListsForRows`）：取 **`rows/1000` 与 `sqrt(rows)` 的较大者**，
  夹在 `[1, 32768]`；**未测量（0 行）时保持历史默认 100**——从不测量猜一个小值，会把同一缺陷镜像成
  "大表上只有 1 个 cluster"。
- **启动时复核一次**（`AdaptVectorIndexScale`，由 `MigrateStorage` 收尾调用，故**生产可达**）：
  **按 (表, embedding 列) 从 `pg_index` 查索引**（不猜名字）+ 读它的 `reloptions` 与 partial 谓词，
  **只在超过 2 倍**时重建（DROP + CREATE，故意的阻塞操作：2 倍门限让重建罕见）。非 ivfflat 索引或
  读不到形状 → **跳过**，绝不盲重建。
- **同日复核修正（三处，都很关键）**：
  1. **索引名不匹配 = 整个能力是死代码**：初版按 `<table>_embedding_idx` 查，而迁移建的是
     `idx_<table>_embedding` ⇒ 每次启动都走"没有索引"的跳过分支，缺陷一点没修。**已改为按目录查询**，
     并补一条回归守卫（SQL 里不许出现名字约定）。**反证**：改回按名查 → 守卫用例失败。
  2. **lists 规则会让小表变慢**：`rows/1000` 假定 probes 被调大；默认 `probes=1` 时扫描比例 =
     `probes/lists`，500 行会得到 1 个列表、**扫全表**。改为取大者后，单次探针约扫 `sqrt(rows)` 行
     （500 行≈22 行、50 万行≈707 行）。**反证**：只用 `rows/1000` → `ivfflatListsForRows(500)=1, want 22`。
  3. **启动期不该做全表 count**：复核每次启动都跑，`count(*)` 在千万行表上是固定成本。**改为优先读
     `pg_class.reltuples`**（免费），仅在估计不可用（未 ANALYZE/新表）时兜底 count——2 倍门限本身就能
     容忍估计误差。另**保留索引自身的名字与 partial 谓词**（否则会把 `WHERE embedding IS NOT NULL`
     的部分索引换成全量索引：更大，且 planner 失去谓词证明）。
- **检索期探针可配**（`EmbeddingConfig.SearchProbes`）：这是**不用重建**就能提召回的那一半。
  **⚠️ 同日复核修正（重要，"接了不生效"）**：初版只是在 `VectorSearcher.Search` 里裸发了一条
  `SET LOCAL ivfflat.probes = N` —— 而 **`SET LOCAL` 只在事务块内生效**（注释自己也这么写了），
  那条路径没有事务 ⇒ **Postgres 静默忽略**；更关键的是**真正跑 RAG 热检索的是
  `KnowledgeRepository.SearchByVector`，它当时连这条逻辑都没有**。净效果：**配了探针的部署两条路径都没生效**，
  而 INDEX 却写成"已修"。修法（已落）：新增 `postgres.WithVectorSearchProbes` **按句柄形态建事务**
  （`*sql.Tx` 直接用；`*Pool`/`*sql.DB` 各自开事务；都不匹配则**响亮 Warn**"探测已配但无法生效"而不是静默），
  并把它接到**两条路径**上（`VectorSearcher.Search` 与 `KnowledgeRepository.SearchByVector` 抽取的
  `searchByVectorQuery`）；探针值由装配点注入（`repositories.WithSearchProbes`）。
  **测试**：`vector_probes_test.go` 用 sqlmock 断言 **BEGIN → SET LOCAL → 查询 → COMMIT** 的真实序列、
  失败必回滚、未配置不开事务、已是事务的句柄**不嵌套开事务**、不可用句柄仍执行且告警。
  **反证**：去掉建事务分支 → 事务相关用例失败（并打印上面那条 Warn）。
- **HNSW 选项有了**（`EmbeddingConfig.IndexMethod`）：DDL 走 `USING hnsw (m=16, ef_construction=64)`，
  且**不带 `lists`**（HNSW 没这个参数，发出去就是执行期报错）。
- **跨 method 切换（已落，一次性 + 响亮）**：目录查询扩到带 `pg_am.amname`（索引的访问方法），
  `AdaptVectorIndexScale` 增加 `VectorIndexReview{ExpectedMethod}`：**配置与目录不一致 → 一次性重建**
  （保留索引自身名字与 partial 谓词）+ **Warn（明说这是 BLOCKING 的数据面动作、带上 rows）** + 完成 Info。
  **"一次性"是设计的一部分**：重建后目录与配置一致，下次启动即 no-op ⇒ 不会"每次 boot 赌一把"。
  **空 `ExpectedMethod` = 不评判**（迁移命令没有配置可比，绝不为一个没人要的默认值重建别人的索引）；
  真正的评判落在**唯一同时拥有 pool 与配置**的地方（bootstrap 的向量存储装配）。
  **反证**：让判定恒为 false → 两条不一致用例失败。
- **`hnsw.ef_search`（已落，与 probes 同根）**：probes 与 ef_search 合并为一个 `VectorSearchTuning`
  （`EmbeddingConfig.SearchProbes` / `SearchEFSearch`），走**同一条 `WithVectorSearchTuning` 事务路径**
  （H1 已把事务边界建好，故这是一条分支而非新机制）。**两个旋钮必须成对出现**：各 index method
  **只读自己的那个、忽略另一个**，所以换 method 的部署要同步设对应值，否则"看起来配了"。
  测试覆盖：单旋钮/双旋钮渲染顺序稳定、空 tuning 不开事务、ef_search 边界、配置解析 nil 安全。
- **仍未做**：`AdaptVectorIndexScale` 的 method 重建目前只在 bootstrap 装配路径被调用（迁移命令刻意不评判）；
  若将来有 admin 端点，应复用同一函数而不是另写一套。

### R2 · 纯余弦近邻，无精排（rerank） 〔缺陷〕

热路径检索 SQL（`vector.go:89-92`）：

```sql
SELECT id, 1 - (embedding <=> $1) AS distance, metadata
FROM ... WHERE tenant_id = $3 AND embedding IS NOT NULL
ORDER BY embedding <=> $1::vector
LIMIT $2
```

- **只有向量近邻 + `ORDER BY ... LIMIT`**，**没有 cross-encoder / LLM 二次精排**。
- **补充（v0.3.3 实测）**：更宽的 `RetrievalService`（`retrieval_search.go` 里的 `rerankResults` / `mergeAndRerank`）**曾**有 query/source 权重 + **时间衰减**（`EnableTimeDecay`，默认 true）；但 **`NewRetrievalService` 在生产 wiring 中从未被构造**，所以"**生产热路径无精排、无新鲜度加权**"这一结论当时仍成立。
  **该包已于 2026-10-16 整包删除**（零引用；见本文件 R2 段"两个死类：已删除"）——正因为没人构造它，**在那里面改检索行为等于改不到任何东西**（R2 第一次就是这么修空的）。代码留在 git 历史里。
- **坑**：量大时"**假近**"（向量近但内容不相关）直接进 top-K → 注入 prompt 的是**噪声**。
- **建议**：宽召回（top-50）→ 精排（cross-encoder）→ 取 top-5。这条是"检索噪声"最直接的解法。

**修复（2026-10-15）**：先纠正一条**比初稿更准的事实**：生产路径**早就在做宽召回**
（`SimpleRetrievalService.Search` 取 `TopK*5`），**缺的是精排**——所以那 5 倍候选取回来之后，
直接按召回顺序截前 K，那还是余弦顺序，等于白花一次查询。修的就是这一跳。

- **确定性精排**（`services/simple_rerank.go`）：`相似度 + 词面覆盖 + 新鲜度` 三项加权，
  缺失项**剔除并重新归一**（未知时间戳**不猜**成"旧"，否则没有 metadata 的行会被无声埋掉）。
  **如实标注：这不是 cross-encoder**——仓库没有 rerank 模型/服务，硬凑一个才是假实现；
  它修的是"假近直接进 top-K"这一类噪声，跨 encoder 语义匹配仍待有模型时再接。
- **`Score` 语义不变**（仍是余弦），排序依据另立 `RerankScore` 字段并在字段注释里写明：
  按 `Score` 重排会把精排结论退回余弦序，这是调用方需要知道的边界。
- **新鲜度项要真拿得到时间戳才算接通了**：`SearchByVector` 原本**没把 `created_at` 放进
  metadata**（列选了、扫了，就是没带出去），于是补上；否则 recency 项会永远"未知→剔除"，
  变成"接了但永不生效"（本缺陷已经这样过一次：`ExactMatch`/`ToolUsage`/`KeywordPresence`
  三个 evaluator 就是**生产零调用**的死代码）。
- **词面覆盖用覆盖率而非 Jaccard**：长文档把问的词都提到了就该拿满分，不能因为自身词汇量
  大而被压；短/标点 token 丢弃，避免"人人看起来都有一点相关"。
- **同日复核修正（中文场景失效）**：初版按"非字母数字切分 + 丢弃 <3 rune"切词，而**中文没有空格** ⇒ 整句
  变成**一个超长 token** ⇒ 词面项退化成近似整串相等，**恰好在最需要它的场景（判官提示词与语料都是中文）
  贡献≈0**。**已加无空格脚本的 bigram 切分**（Han/假名/谚文按相邻字符成对，单字符查询保留；拉丁仍走原规则），
  使部分重合可度量（实测"人工智能的最新趋势" vs "人工智能领域的最新趋势" = 7/8）。
  **反证**：让 CJK 走普通切分 → 中文用例失败（0 ≠ 0.875）。
- **同日复核修正（回归面：`created_at` 是本批才随 metadata 带出的）**：新鲜度项依赖
  `metadata["created_at"]`，而该键是本批才加的 ⇒ **本批之前写入的存量候选没有它**。文档明确要求锁死
  "缺时间戳只丢新鲜度项、**不得整条消失**"——已补
  `TestRerankCandidates_UnknownAgeDropsOnlyTheRecencyTerm`（无时间戳的候选仍被返回、分数等于"关掉新鲜度项"
  的分数、且不高于同文本的新候选）。**反证**：把"未知时间"当"很旧" → 该用例与新分数断言同时失败。
- **测试**：`simple_rerank_test.go`（核心一条：**余弦更近但没提到所问内容的候选必须被压下去**，
  同时断言纯余弦序恰好相反）、词面/新鲜度/剔除未知/全零权重回退/不改调用方切片/部分配置兜底。
- **⚠️ 复核更正（同日，重要）：上面这条"修在生产路径上"的说法是错的。** 实测：`SimpleRetrievalService`
  的**非测试引用只有 `examples/_internal/11-knowledge-import`**，`RetrievalService` 只有测试文件 ⇒
  **两个 service 都是零生产引用**，上面那套精排**修在了没人用的类上**。真正的生产 RAG 是
  `store_adapter → knowledge.HybridSearch → ScoreHybrid`（`knowledge/hybrid.go`）。
  **这是与 H1（probes 的 `SET LOCAL` 无事务）同类的"修在无人路径"**。
- **生产路径的真实形状（核实后）**：**宽召回与加权重排本来就都有** —— `hybridRecallLimit`（宽召回）+
  `ScoreHybrid`（0.7 向量 + 0.3 词面）+ 按 `FinalScore` 排序。**真缺的只有 recency 项**（SQL 是
  `ORDER BY updated_at DESC` ⇒ 窗口已偏置，但分数里没有）。词面项生产侧**本来就有 CJK bigram**
  （`hybrid.go` 的 `tokenize`/`cjkBigrams`），所以那半也不缺。
- **已落（生产路径闭环）**：
  1. **recency 项**（`ScoreHybrid` 第三项 + `ScoredObject.RecencyScore` 可诊断）；
  2. **权重是相对的、按可用项归一**（缺向量时不把候选压向 0 —— 保持既有"未嵌入仍可检索"行为）；
  3. **年龄口径复用既有 `EffectiveObservedAt()`**（不另造 fallback 链），并**记下**它与窗口用的
     `updated_at` 并非同一字段（两处口径差异写明而非抹平）；
  4. **默认权重 = 0.7/0.3/0**（recency 关）——**理由是 SQL 窗口已按 updated_at 偏置，分数里再来一次是
     重复计入**，是否有效要按语料实测；擅自改生产排序＝未测量的回归。**"默认关"意味着 P1-2 的生产闭环
     目前是"机制已接、默认未开"**，开启方式见下。
  5. **接线（闭合"可配但无人配"）**：`postgresstore.WithDefaultWeights`（**一个装配点覆盖
     adapter/provider/context_retriever 三条检索路径**）+ 配置旋钮 `knowledge.recency_weight`
     + 装配处日志（生效值可观测）+ 两份示例配置同步。
  6. **dedup 显式用"纯相关"权重**：它的阈值本来就是 `VectorScore`（安全），但候选窗口按 `FinalScore` 排，
     显式覆盖避免继承部署的 recency 决定（一个类的两个用途不该共享一个排序偏好）。
  7. **`FreshnessScore` 抽成共享规则**（`quality.go`）：写入期质量分与查询期 recency 用同一条分档，
     未知时间戳=中性 0.5（**不是最旧**）、未来时间戳=1.0。
  - **实机反证**：默认权重里塞 recency → 兼容性用例报 `FinalScore = 0.9, want legacy 0.85` 且平局被打破；
    忽略存储默认权重 → `store default was not applied`。
- **同日落"标注"这一步（按拍板口径"删**或**标注"，选了标注，并说明为何现在不删）**：
  两个死类都加了 `Deprecated:` 独立段落 + `TODO(tech-debt)`。
  - **证据**：`RetrievalService`（580 行 + `retrieval_rewrite.go`）**除自身测试外零引用**；
    `SimpleRetrievalService`（434 行 + `simple_rerank.go` 284 行）**唯一消费方是仓内示例**
    `examples/_internal/11-knowledge-import/store.go`。
  - **为何不直接删**：合计 ~1500 行 + 一大批测试，且要同时迁移或砍掉那个示例、重写其测试面（§5.5 要求在
    同一改动里迁移全部调用方）。这类删除**值得单独一次改动**，不该混在缺陷修复里——否则 revert 会缠在一起。
  - **标注本身有实际作用**：注释里**点名"别在这里修检索缺陷"**并写清原因（R2 第一次就是这么修空的），
    这是防止再犯的那道拦阻。
  - **TODO(tech-debt) 的后续计划**：确认导出符号无外部消费者后，一次改动删掉这两个类型、它们的兄弟文件与测试。
- **两项"判断项"定案（不再挂着）**：
  ① **recency 默认保持 0**。不是"没做"，是**决定不做默认开启**：SQL 召回窗口已 `ORDER BY updated_at DESC`
     偏置，分数里再来一次是**同一信号计入两次**；是否净收益要按语料实测，而**擅自改生产排序就是未测量的回归**。
     开关已给（`knowledge.recency_weight`），要开是一行配置。
  ③ **生产词面项保持 Jaccard（不换覆盖率）**。Jaccard 是**长度归一化**的相似度、且已 CJK 感知；覆盖率会系统性
     偏好"长而只提到一次关键词"的文档。**我上轮在死类里写的 coverage 版本随该包一并删除，不构成证据**，
     换算法属行为变更、需实测——**记录为"已知取舍"，不是待办**。
- **两个死类：已删除（含示例迁移）**。证据链：① 先把示例 `examples/_internal/11-knowledge-import`
  从 `SimpleRetrievalService` 迁到**生产路径**（`repo.SearchByVector` + 配置里的 `QueryPrefix`/`TopK`/`MinScore`，
  其中 MinScore 由调用方按 `metadata["similarity"]` 过滤，并处理 `ErrRecordNotFound` 这一"无命中"契约）；
  ② 迁移后 `grep -rn "postgres/services"` **全仓归零**；③ 删除整个 `internal/storage/postgres/services/`
  （两个死类 + 辅助 + 其测试，约 8500 行，其中测试约 6300 行；§8.2 删干净，历史留在 git）；
  ④ **删除后 `go build ./...` 与 `go test ./internal/... ./cmd/...` 全绿**——这就是"零悬挂引用"的验证。
- **CLI 路径已接入配置权重**：`openAKGStore` 本就加载了配置，现在把同一份
  `DefaultHybridWeights().WithRecency(cfg.Knowledge.RecencyWeight)` 交给存储，避免"serve 里生效、CLI 里不生效"
  这种同一语义两套排序。
- **仍未做（真正的剩余）**：无（本节范围内）。更外层仍是：per-candidate 运行时回放需要在 arena 里接真实执行器
  （现只接确定性压分）；cross-encoder 明确不修（单二进制无 ML serving）。

### R3 · hybrid 融合**已实现**（0.7 向量 / 0.3 lexical）〔已做 · 与初稿结论相反〕

- **有 lexical 能力**：`migrate_storage.go:59` 建了 **tsvector trigger**，`:78` 建了
  `idx_knowledge_1024_tsv`（GIN 全文索引）；`repositories/knowledge_repository.go:590-601`
  `SearchByKeyword` 真在用 `ts_rank(tsv, plainto_tsquery('simple', $1))`。
- **确实做了融合、且权重明确**：`internal/knowledge/hybrid.go:156`
  `final = 0.7*vec + 0.3*lex`（函数 `ScoreHybrid`，`:111-160` 注释逐字写明
  "FinalScore = 0.7*VectorScore + 0.3*LexicalScore when a vector is available"）。
  与初稿猜的"0.3 lexical vs 0.7 vector"**完全吻合**。
- **并且已接入生产 RAG**：AKG store 的 `HybridSearch`（`internal/knowledge/store/postgres/store.go:495-625`）
  经 `NewKnowledgeRetrieverWithStore`（`internal/knowledge/adapter/context_retriever.go:189-239`）
  接到 `sdk/memory_wiring.go:437-448`。
- **结论**：**"有全文索引 ≠ 做了混合检索"的疑虑可以关闭**——混合检索**已实现**，本条应从
  "隐患·未坐实"改判为"**已做**"。
- **能力面留痕（2026-10-16，删除死代码时一并记）**：被删的 `retrieval_search.go` 里的 `bm25Search*`
  （`tsvector` 全文检索 + 与向量融合）是 `knowledge_chunks_1024` 侧的**宽词面召回**。删掉之后，
  **词面检索只剩 Go 侧的 Jaccard**（`knowledge/hybrid.go`：只算**召回窗口内**的 token 重叠，**不走 DB 全文索引**）。
  **这不属于"丢了在跑的能力"**：那条路径的生产入口 `RetrievalService` 从未被构造（死代码 ⇒ 等价于从未生效）。
  但"确实没了"的部分要留痕，免得将来有人要"更宽的 lexical 召回"时误判为新回归：
  - **schema 层仍在**：tsvector trigger 与 `idx_knowledge_1024_tsv`（GIN）照旧由迁移创建；
  - **仓库层仍在**：`KnowledgeRepository.SearchByKeyword`（`ts_rank(tsv, plainto_tsquery('simple',$1))`）
    仍在，只是**目前没有调用方**（`provide_distillation.go` 用的是**经验仓**的同名方法，另一张表，仍在生产）；
  - ⇒ **将来要 DB 全文召回，接线即可，不必重做**。
- **限定（2026-10-16 更正）**：`knowledge_chunks_1024` 的 tsvector 融合所在的 `RetrievalService`
  （`retrieval_search.go`）**从未被生产 wiring 构造**，所以"生产热路径无精排"（R2）当时即成立；
  真正在生产跑的是 AKG 那条 hybrid 路径。
  **该包已于同日整包删除**（零引用，证据见 R2 段"两个死类：已删除"）——本段原先写的
  "唯一保留的限定"因此**作废**：它把**已经删掉的类**当成"还在的限定"引用，读者会以为那条 tsvector 融合还在跑。
  能力面的留痕见下面 R3 末尾的"DB 侧宽词面召回随死代码删除"。

### R4 · RAG 默认关（opt-in）→ "记忆复用"这条线默认不生效 〔取舍 · 边界〕

`manager.go:435/437`（`DefaultMemoryConfig`，函数起于 `:416`）：**`EnableRAG` 默认关**（`:435`），`RAGTopK=5` / `RAGMinScore=0.4` 只是 seed（`:436/437`；字段定义 `:172/176/181`）。（初稿锚点 `:425/429`、`:171` 已漂移。）

- **坑**：不开 RAG，agent **没有"按语义检索旧经验"的能力** → "越跑越聪明"的**记忆复用**这条线
  **默认是不走的**（agent 白纸起步，只有 spawn 时的单条 `ExperiencePrior`，不是全量 RAG）。
- **和 G1 同族**：GA 靠"经验/成绩"，经验检索默认关 → **进化先验也缺数据**。
  "记忆"和"进化"两条线都卡在"默认不启用"上。
- **建议**：至少把"RAG 关着"这件事**可观测化**（面板显示"记忆检索未启用"），别让它静默地不工作。

### R5 · embedding 异步队列 → 未回填的行检索不到 〔隐患〕

写入是"**先存行、异步回填向量**"（`embedding_queue.go`，幂等 `ErrDuplicateTask` `:17`）。

- **坑**：回填完成前，该行 `embedding IS NULL` → 检索 `WHERE embedding IS NOT NULL`
  （`vector.go:89`）**跳过它**；ivfflat 也 **"never indexes NULL rows"**（`migrate_storage.go:67`）。
- 即"**刚写的经验要等向量回填完才可检索**"——回填慢/积压时，最新经验**召回不到**。
- 这条本身是"写读解耦"的代价（不阻塞事件循环），但要知道**"写入 ≠ 立即可检索"**。

### R6 · 高维余弦阈值 0.4 语义偏弱 〔取舍 · 边界〕

`RAGMinScore = 0.4`（`manager.go:171/429`）。

- **含义**：1024 维高维空间里，余弦相似度普遍聚在 **0.5~0.9**（"维度灾难"，前面 §24 讲过），
  **0.4 作为"最小相关"偏低**——放进去的可能有"其实不相关"的；作为"截断噪声"又太松。
- 这是 RAG 通用难题（**高维余弦区分度低**），不是本项目的 bug，但**要明白 0.4 不是"很相关"，
  是"勉强不算无关"**。调高它 = 召回更少更准（但可能漏），调低 = 更多更噪。

---

## 三、存储侧（PG / pgvector 共享）的坑

### S1 · 1024 维写死在表名 + `VECTOR(1024)`，换模型要"建表 + 全量重嵌" 〔隐患〕

`knowledge_chunks_1024` / `experiences_1024`——**维度锁死在表名 + `VECTOR(1024)`**（`migrate_storage.go:15-19`）。

- **坑**：换 embedding 模型（输出 768 / 1536 维）→ **不能"同表换维度"**，必须
  **建新表 + 全量重嵌 + 数据迁移**。且 0.3.3 删掉了 `storage.pgvector.dimension`（那个默认 1536
  的误导性键，见 §Removed）——现在**表维度纯写死 1024**（Go 侧默认见 `vector.go:17 defaultVectorDimension`）。
- **一处需澄清（v0.3.3 实测）**：`internal/ares_config/config_server.go:71-72` **仍有**
  `EmbeddingConfig.Dimension`（yaml 键 `dimension`）——但那是 **embedding 客户端**的维度旋钮，
  **不是 pgvector 表维度**。所以初稿"维度**没有任何**配置项"偏绝对，准确说法是"**表维度**纯写死 1024"。
- **含义**：维度是**硬架构约束**，模型升级成本被放大。这是前面 §24"embedding 版本化"那块的坐实。

### S2 · 向量表与事件账本共一个 PG 实例，无资源隔离 〔隐患 · 规模〕

前面"多租户"那条坐实过：**EventStore（PG backend）+ knowledge_chunks + experiences +
embedding_queue 都在同一个 PG 实例**（`migrate_storage.go` 一张 schema）。

- **坑**：向量检索（ivfflat + cosine，重查询）和事件账本（append-heavy + 高频事务）**争 IO**。
  没有"向量库独立实例"的隔离。
- **一处需澄清（v0.3.3 实测）**：**连接池其实是各自独立的**——事件账本（`serve_wiring.go:335`）、
  evidence/knowledge（`sdk/knowledge.go:345`）、memory（`sdk/memory_wiring.go:373`）各自
  `postgres.NewPool`，只是打同一个 PG 实例。所以"争 IO"成立，"**无独立连接池**"不成立。
- **规模上来时**：检索风暴（大量 RAG 查询）可能**拖慢事件账本的事务**（反之也成立）。
- **建议**：规模一大，**向量侧独立实例**（或干脆外置向量库），别和账本抢同一个 PG。

### S3 · 未外置向量库（Milvus/Pinecone） 〔取舍 · 边界〕

用 pgvector 内嵌（`migrate_storage.go` 注释也说明）。

- **好处**：一份运维、**向量行与关系行同库同事务**（一致性最好）。
- **代价**：**没有专门向量引擎的规模能力**（超大 embedding 量、多租户向量隔离、GPU 加速都靠 pgvector 扛）。
  到"**百万级向量 + 多租户 + 亚秒检索**"就该外置。当前量级内嵌是**对的取舍**，但要知道天花板。

---

## 四、跨半边的一类根因：embedding 死信 = 数据"永久不可见"

### D1 · `embedding_dead_letter`：向量生成失败的经验对检索永久不可见 〔缺陷〕

`embedding_dead_letter`（`migrate_storage.go:416`）：embedding 反复生成失败的行落这里。

- **坑（R5 + 死信合起来）**：一条经验的 embedding **一直生成失败**（provider 挂 / 超限 / 维数错）
  → 进 dead-letter → **它的向量永远 NULL** → 检索 `WHERE embedding IS NOT NULL` **永远跳过它**
  → **这条经验对 agent 永久不可用，且静默**（不报错，只是"没被召回"）。
- **含义**："召回不到某条经验"的可能原因不止"语义不近"，还可能是**它躺在 dead-letter 里**。
  排障时要查 `embedding_dead_letter`。
- **建议**：dead-letter 加**重放 + 告警**（"N 条经验的向量从未生成成功，已不可检索"），
  别让"embedding 失败"变成"数据永久蒸发"。

**修复（2026-10-15）**：重放与告警都做了，且**每条内容只自动重放一次，永不循环**。

- **可行动性的拆分**（`DeadLetterBacklog`）：`Pending`（还没拿到过自动重放机会）与
  `GivenUp`（重放过一次又失败 = 文档过长/维度不匹配，**要人处理**）。只报一个总数会让告警失去意义。
- **单发重放**（`EmbeddingQueue.ReplayDeadLetters`）：一条语句里"取候选 → 插回队列 → 盖章"，
  **原子**（崩溃不会造成既重插又没盖章）。防循环靠 `auto_replayed` 标记，且
  `MarkFailed` 的死信 INSERT **把这个标记一起搬过来**（`deadLetterInsertSQL`）——否则重放再失败会生成
  一条新死信、标记归零，于是每 tick 重放一次，永久循环。另有 `NOT EXISTS` 守卫跳过已在队列里的内容
  （否则 Reconcile + 重放会让同一行同时在飞）。
- **告警 + 顺序**（`ares_bootstrap/embedding_worker.go`）：tick 里 **先重放、再清理、最后报**——
  重放必须在清理之前，否则"超过保留期的 pending"会被保留期**静默决定**内容不可恢复；报告放最后，
  报出来的数字就是**本 tick 之后仍然不可见**的那部分。`Warn`（不是 Info），因为这些行的 embedding 是
  NULL、被所有检索查询无声跳过。
- **补了一个"零生产调用"的既有事实**：`RequeueDeadLetter`（单条人工重排）此前**没有任何调用方**，
  连运维都够不着；现在批量路径与它并存，且批量路径是**自动接线**的。
- **同日复核修正（告警噪音）**：非空 backlog 初版**每 tick 一条 Warn**（默认 10 分钟 ⇒ 一条永远修不好的
  内容约 144 条/天），这会把真告警淹掉。改为**按状态变化记录**（`deadLetterReporter` 记住上次报过的
  `{pending, given_up, count_err}`）：首次照响、不变不刷、增长/缩小/计数开始失败都再响一次。
  **反证**：去掉"每状态只报一次" → 该契约用例失败（同状态被重复报出）。
- **测试**：`embedding_queue_deadletter_test.go`（单发标记/防重复入队/边界校验/可行动性拆分，
  含"标记必须随死信搬过去，否则无限重放"的 SQL 契约）+ `embedding_worker_deadletter_test.go`
  （批量上限、失败不吞掉后续报告、空 backlog 必须安静、非空必须 Warn 且带上拆分数字）。
  **实机反证**：把盖章改成 `FALSE` → 单发契约测试失败；恢复即绿。

---

## 五、性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| **R2 纯向量无精排** | 缺陷 | 高（宽召回 + cross-encoder） |
| **R1 ivfflat lists=100 固定** | 缺陷 | 中（数据量大了调 lists/probes 或上 HNSW） |
| **D1 embedding 死信=数据永久不可见** | 缺陷 | 高（dead-letter 重放 + 告警） |
| R3 hybrid 融合 | ✅ 已做（0.7 向量 / 0.3 lexical，已接生产 RAG） | 无需修（原判"未坐实"已过时） |
| R5 未回填不可检索 | 隐患 | 知"写入≠立即可检索" |
| S1 维度写死 1024 | 隐患 | 知边界（换模型=重嵌）；表维度写死，`EmbeddingConfig.Dimension` 是客户端旋钮 |
| S2 向量与账本共实例 | 隐患 | 中（连接池已各自独立；规模上来要独立**实例**） |
| R4 RAG 默认关 / R6 阈值 0.4 / S3 未外置 | 取舍 | 知边界（含"RAG 关着"可观测化） |
| PG/pgvector 分工（向量表 vs 关系表） | ✅ 已做 | 无需动 |

---

## 面试串法（把"检索 + 存储"讲对）

> "检索和存储我分两半看。**存储分工是对的**（语义走 pgvector 的 `knowledge_chunks_1024`/
> `experiences_1024`，结构化走关系表，不混一张表）；**但精度和规模都没做重投入**——
> ① ivfflat `lists=100` 写死，量大召回率掉（没 HNSW 选项）；② 纯余弦近邻**没有精排**，
> 假近进 top-K（注：**hybrid 融合其实有做**——AKG store 是 0.7 向量 + 0.3 lexical，已接生产 RAG）；③ 高维余弦阈值 0.4 偏弱。**最隐蔽的一条**：embedding 是异步回填，
> 生成失败的行落 `embedding_dead_letter` → 向量永远 NULL → **这条经验对检索永久不可见、且静默**。
> 另外 RAG **默认是关的**，记忆复用这条线不显式开就不走。
> 规模上，向量表和事件账本**共用一个 PG 无隔离**——检索风暴会拖慢账本。
> 这些和 GA 那块（G1 缺正确率信号）是一族：**系统'越跑越好'的前提（记忆检索准 + 有正确性信号）
> 现在都还没到位。**"

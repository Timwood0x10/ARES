# 缺陷与边界记录 · RAG / 记忆检索 + PG & pgvector 分工

> 本文档**只记"会咬你"的坑与已知边界**，不重复"RAG 怎么工作"（向量近邻 + TopK/MinScore）。
> 两条主线：**① 检索精度与规模**（向量那半边）、**② PG/pgvector 的分工与共享**（存储那半边）。
> 每条带源码锚点（v0.3.3 行号可能随 dev 分支漂移，定位以符号为准）。
> 性质标记：`缺陷`（建议修）/ `隐患`（规模/时间下暴露）/ `取舍`（设计权衡，知边界即可）/ `已做`（有保护，确认非缺陷）。
>
> **一句话总纲**：检索侧**能用但不精**（ivfflat 固定 lists、无精排、高维余弦阈值弱），
> 存储侧**分工清晰但共享无隔离**（向量表 + 事件账本共一个 PG 实例），
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

### R2 · 纯余弦近邻，无精排（rerank） 〔缺陷〕

热路径检索 SQL（`vector.go:89-92`）：

```sql
SELECT id, 1 - (embedding <=> $1) AS distance, metadata
FROM ... WHERE tenant_id = $3 AND embedding IS NOT NULL
ORDER BY embedding <=> $1::vector
LIMIT $2
```

- **只有向量近邻 + `ORDER BY ... LIMIT`**，**没有 cross-encoder / LLM 二次精排**，
  也**没有质量/新鲜度加权**（前面 §24 检索降噪那节的"贵降噪"这里同样缺）。
- **坑**：量大时"**假近**"（向量近但内容不相关）直接进 top-K → 注入 prompt 的是**噪声**。
- **建议**：宽召回（top-50）→ 精排（cross-encoder）→ 取 top-5。这条是"检索噪声"最直接的解法。

### R3 · 全文索引建了，但"是否真走 hybrid 融合"需确认 〔隐患 · 未坐实〕

- **有 lexical 能力**：`migrate_storage.go:59` 建了 **tsvector trigger**，`:78` 建了
  `idx_knowledge_1024_tsv`（GIN 全文索引）。
- **但检索侧（`vector.go`）热路径是向量 `<=>`**——**是否把 tsvector 的 lexical 结果和向量结果
  做 hybrid 融合、融合权重多少，我没在检索路径里坐实**。
- **实事求是标注**：**"有全文索引" ≠ "做了混合检索"**。索引在，但热路径是否真的 lex + vector
  融合、权重如何，**需要再查检索代码确认**（避免把"建了索引"误读成"用了 hybrid"）。
- 若**没**融合：lexical 只当"能全文搜"的备胎，向量那半边仍按 R1/R2 走；
  若**有**融合：要看**融合权重**（0.3 lexical vs 0.7 vector 之类），高维向量权重过高会压制 lexical 的补强。

### R4 · RAG 默认关（opt-in）→ "记忆复用"这条线默认不生效 〔取舍 · 边界〕

`manager.go:425/429`：**`EnableRAG` 默认关**，`RAGTopK=5` / `RAGMinScore=0.4` 只是 seed（`:429`）。

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
  的误导性键，见 §Removed）——现在**维度没有任何配置项，纯写死 1024**。
- **含义**：维度是**硬架构约束**，模型升级成本被放大。这是前面 §24"embedding 版本化"那块的坐实。

### S2 · 向量表与事件账本共一个 PG 实例，无资源隔离 〔隐患 · 规模〕

前面"多租户"那条坐实过：**EventStore（PG backend）+ knowledge_chunks + experiences +
embedding_queue 都在同一个 PG 实例**（`migrate_storage.go` 一张 schema）。

- **坑**：向量检索（ivfflat + cosine，重查询）和事件账本（append-heavy + 高频事务）**争 IO / 连接池**。
  没有"向量库独立实例 / 独立连接池"的隔离。
- **规模上来时**：检索风暴（大量 RAG 查询）可能**拖慢事件账本的事务**（反之也成立）。
- **建议**：规模一大，**向量侧独立连接池 / 独立实例**（或干脆外置向量库），别和账本抢同一个 PG。

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

---

## 五、性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| **R2 纯向量无精排** | 缺陷 | 高（宽召回 + cross-encoder） |
| **R1 ivfflat lists=100 固定** | 缺陷 | 中（数据量大了调 lists/probes 或上 HNSW） |
| **D1 embedding 死信=数据永久不可见** | 缺陷 | 高（dead-letter 重放 + 告警） |
| R3 hybrid 融合未坐实 | 隐患 | 先查清"是否真 hybrid"再定 |
| R5 未回填不可检索 | 隐患 | 知"写入≠立即可检索" |
| S1 维度写死 1024 | 隐患 | 知边界（换模型=重嵌） |
| S2 向量与账本共实例无隔离 | 隐患 | 中（规模上来独立池/实例） |
| R4 RAG 默认关 / R6 阈值 0.4 / S3 未外置 | 取舍 | 知边界（含"RAG 关着"可观测化） |
| PG/pgvector 分工（向量表 vs 关系表） | ✅ 已做 | 无需动 |

---

## 面试串法（把"检索 + 存储"讲对）

> "检索和存储我分两半看。**存储分工是对的**（语义走 pgvector 的 `knowledge_chunks_1024`/
> `experiences_1024`，结构化走关系表，不混一张表）；**但精度和规模都没做重投入**——
> ① ivfflat `lists=100` 写死，量大召回率掉（没 HNSW 选项）；② 纯余弦近邻**没有精排**，
> 假近进 top-K；③ 高维余弦阈值 0.4 偏弱。**最隐蔽的一条**：embedding 是异步回填，
> 生成失败的行落 `embedding_dead_letter` → 向量永远 NULL → **这条经验对检索永久不可见、且静默**。
> 另外 RAG **默认是关的**，记忆复用这条线不显式开就不走。
> 规模上，向量表和事件账本**共用一个 PG 无隔离**——检索风暴会拖慢账本。
> 这些和 GA 那块（G1 缺正确率信号）是一族：**系统'越跑越好'的前提（记忆检索准 + 有正确性信号）
> 现在都还没到位。**"

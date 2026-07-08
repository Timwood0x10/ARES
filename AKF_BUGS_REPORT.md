# AKF（ARES Knowledge Fabric）代码审查 + AKG.md 完成度评估

> 审查范围：`internal/knowledge/**`（AKF 全部实现）
> 设计文档：`AKG.md`、`AKF_PLAN.md`
> 编译状态：`go build ./internal/knowledge/...` ✅ 通过（问题均为**逻辑/设计落地缺陷**，非编译错误）
> 审查日期：2026-07-08

---

## 一、AKG.md 功能完成度评估

AKG.md 定义了 12 个核心模块，AKF_PLAN.md 又采纳了 10 条修订 + 4 条 final critique（v3）。按组件逐项核对实际代码：

| # | AKG.md 组件 | 状态 | 说明 |
|---|------------|------|------|
| 1 | 核心数据模型 `KnowledgeObject` / `Evidence` / `Relation` | ✅ 完成 | `object.go`、`relation.go` 齐全；但**所有 Provider 都不填充 `Evidence`**（证据链名存实亡） |
| 2 | Graph Builder / `WorkingGraph` | ✅ 完成 | `runtime.go` + `relation.go` 实现 Plan→Load→Link→Reduce |
| 3 | Memory Distillation 集成 | 🟡 部分 | `adapter/memory.go` 单向 `Memory→KnowledgeObject`；**无 Distill 回写**，且未与 AKF 同层编排 |
| 4 | Knowledge Store（可选持久层） | 🟡 部分 | 仅 `store/memory` 实现；设计要求的 Postgres/SQLite/Mongo/Redis/Qdrant/Milvus/Neo4j **均未实现** |
| 5 | Graph Provider | ✅ 完成 | `postgres`/`mysql`/`memory`/`code`/`evolution` 五个 Provider（超出计划） |
| 6 | Builder Plugin | ✅ 完成 | `linker/` 下 Decision/Architecture/Timeline/Similarity 四个 Linker |
| 7 | Context Compiler（多格式） | 🟡 部分 | 仅 Prompt/Markdown/JSON；**XML、ToolSchema 缺失**；且 `markdown == prompt`（注释"暂同"） |
| 8 | Retriever（Intent→Graph→Expand→Prune→Compile） | ❌ 缺失 | 全代码库**无 Retriever**，只有 Build+Compile；Embedding 检索未接入 |
| 9 | 插件架构 `Register()` | 🟡 部分 | Provider/Linker 可注册；但无统一的"全部组件 Register 后即插即用"的门面 |
| 10 | 对外 API（HTTP / MCP / Agent） | 🟡 部分 | MCP 仅暴露 2/4 工具；**无 HTTP 路由**；Agent 调用链路未串联 |
| 11 | Dynamic Cognitive Graph（运行时图） | 🟡 部分 | `LazyGraph` 已实现但**从未被启用**（`Config.LazyLoading` 形同虚设） |
| 12 | 三条设计原则 | ✅ 原则 | Source of Truth / Ephemeral / Knowledge-First 作为注释存在，非代码 |

### v2 / v3 修订点落地情况

| 修订点 | 状态 | 评价 |
|--------|------|------|
| 1. Store 去中心（可选） | ✅ | 正确 |
| 2. Provider `Stream()` 模式 | ✅ | 正确（含 ctx 取消处理） |
| 3. `Intent{Goal,Scope,Constraints,Budget}` | ✅ | 结构体齐全 |
| 4. Builder 拆 Planner/Loader/Linker/Reducer | ✅ | 结构到位 |
| 5. `Relation{Name,Properties}` 可扩展 | ✅ | 到位 |
| 6. `KnowledgeObject` 三层 Raw→Normalized→Summary | ✅ | 到位 |
| 7. Memory Distillation 走 Pipeline | 🟡 | 类型有，但**管道从未实际运行**（见 B8） |
| 8. Knowledge Resolver（别名/合并/冲突） | ❌ **损坏** | 见 B1 |
| 9. 多格式 Compiler | 🟡 | 见上表第 7 项 |
| 10. Knowledge Planner 解耦 | 🟡 | 解耦了但**是空壳**（见 B14），且 QueryPlanner 产物被丢弃（见 B2） |
| 11. Embedding 独立 `Representation` | ⚠️ 死代码 | 结构体在、存/取接口在，但**无人调用**（见 B12） |
| 12. Resolver 三阶段 Normalize→Resolve→Validate | ❌ | 见 B1 |
| 13. Planner 不知道 Provider + QueryPlanner 翻译 | ❌ | 见 B2（QueryPlan 算完即弃） |
| 14. Builder 改名 KnowledgeRuntime | ✅ | 已改名 |

### 完成度总评

```
整体约 50–55% 完成。
骨架（类型系统、Runtime 编排、Provider 接入、Linker、Compiler 基础格式）完整可用；
但"智能层"——Resolver、QueryPlanner、Distill 闭环、语义 Search、多格式 Compiler、
Lazy Graph 启用——多为桩 / 死代码 / 损坏，是 AKG.md 真正价值所在的部分。
```

---

## 二、Bug 清单（挑刺）

按严重度分级。每条附 `文件:行号` 证据。

### 🔴 Critical（功能性 / 安全）

**B1 — KnowledgeResolver 完全失效（AKG 第 8 点"缺 Resolver"等于没修）**
- `pipeline.go:111-131`：`Process` 调用 matcher 时 `candidates` 传 **`nil`**，且 matcher 循环体内有 `break`（只跑第一个 matcher）。
- `normalizer.go` `DefaultEntityMatcher.Match`：`if obj == nil || len(candidates) == 0 { return IsNew }` → 永远返回"新实体"。
- 后果：`"Redis"` / `"redis"` / `"Redis Cache"` 全部成为**独立节点**，别名合并、冲突检测从未发生。设计最强调的 Resolver 形同虚设。

**B2 — QueryPlanner 产物被运行时直接丢弃（v3 第 13 点未落地）**
- `planner/default.go:101-115`：`SourceDiscovery.Discover` 认真为每个需求生成 `QueryPlan`。
- `runtime.go:139-146`：`loadAndProcess` 完全忽略 `src.Query`，自己重建 `intent` 然后 `prov.Stream(ctx, intent)`。
- 后果：整个 `QueryPlanner` / `QueryPlan` 子系统是**死代码**；Provider 永远拿不到翻译后的 SQL/Cypher/Vector 查询。

**B3 — MySQL Provider SQL 注入（CWE-89）**
- `provider/mysql/provider.go:131-148`：`buildQuery` 直接字符串拼接 `p.mapping.IDColumn/SummaryColumn/...` 与 `p.config.Table`，**未校验、未加引号、未参数化**。
- 对照 `postgres/provider.go:28-45, 197-199` 有 `validateIdentifier` + `quoteIdentifier`。MySQL 版**完全缺失防护** → 通过配置即可注入。

**B4 — MySQL Provider 无 LIMIT + ID 无命名空间（OOM / ID 碰撞）**
- `provider/mysql/provider.go:131-148`：`buildQuery` 无 `LIMIT`，违背 Stream 设计，大表直接全量加载（OOM 风险）。
- `scanRow`（`provider/mysql/provider.go:174-181`）：`ID` 直接取 `id`，**无 namespace 前缀**；而 Postgres 版用 `namespace:id`。跨表同 id 会碰撞。

### 🟠 High

**B5 — ArchitectureLinker 在缺 tag 时生成全连接 depends_on 图（图爆炸 + 假关系）**
- `linker/architecture.go:47-55`：`if len(code.Tags)==0 || len(arch.Tags)==0` → 给**每一对** code×arch 加 `depends_on` 边。
- 后果：凡未打 tag 的 code/architecture 对象两两相连，图规模 O(n²) 且语义错误。

**B6 — nil KnowledgeObject 导致 panic**
- `pipeline.go:92-143`：`Process` 对 normalizer/matcher/summarizer 返回 `nil` 不加保护。
- `runtime.go:163`：`objects[obj.ID] = obj`，若 `obj` 为 nil → **nil 指针解引用 panic**。
- 触发条件：任意 Provider 发送 nil 对象，或任意 pipeline 阶段返回 nil。

**B7 — `KnowledgeStore.Get` 契约违反**
- `store.go:22` 接口注释：**"Returns nil, nil if not found"**。
- `store/memory/store.go:47-55` 实现：返回 `nil, ErrObjectNotFound`。
- 后果：按接口契约编写的调用方会误判"找到"。属契约不一致。

**B8 — 整个 Pipeline 阶段从未实际运行（AKG 第 7 点管道从未接入）**
- `sdk/sdk.go:334`：传 `nil, // pipeline: use defaults`——但 `NewKnowledgeRuntime` **不提供默认 pipeline**。
- `examples/knowledge-fabric/main.go:122`：`NewKnowledgePipeline(nil, nil, nil, nil)`。
- 后果：`Normalizer`/`EntityMatcher`/`Validator`/`Summarizer` 实现存在但**永不运行**，运行时 `if r.pipeline != nil` 直接跳过。

### 🟡 Medium

**B9 — Postgres `buildQuery` 在 `TimeColumn` 为空时生成非法 SQL**
- `provider/postgres/provider.go:180-189`：`orderCol := quoteIdentifier(p.mapping.TimeColumn)`；`TimeColumn==""` → `ORDER BY ""` → SQL 语法错误。
- 注意：provider 只校验 `id_column`/`summary_column`，**未要求** `TimeColumn`，故该路径可达。

**B10 — MemoryProvider 永远被选中**
- `provider/memory/provider.go:41-43`：`IntentMatch` 恒返回 `0.8` > 阈值 `0.1`。
- 后果：无论 goal 是什么，memory provider 都参与，**浪费 token / 注入无关记忆**。

**B11 — Compiler JSON 手工拼接 + 格式残缺**
- `compiler/compiler.go:160-199`：用 `fmt.Fprintf` + `%q` 手工拼 JSON（脆弱，非 `encoding/json`）；`formatMarkdown` 直接返回 `formatPrompt` 结果；**XML / ToolSchema 完全没有**。

**B12 — 语义 Search 实为关键词 + Embedding 死代码**
- `store/memory/store.go:123-154`：`Search` 忽略 `model` 参数，纯关键词匹配，与接口"semantic search by embedding"不符。
- `SaveRepresentation` / `GetRepresentation` 全代码库**无调用方**（grep 仅定义+测试）→ 整个 `Representation` 子系统是死代码。

**B13 — `Config.LazyLoading` 永不生效**
- `runtime.go:45-58`：`Execute` 只读取 `MaxConcurrentProviders`，**从不检查 `cfg.LazyLoading`**；`NewLazyGraph` 也无任何调用方。懒加载模式永远不启用。

**B14 — defaultPlanner 无视 goal（"decoupled" 是空壳）**
- `planner/default.go:36-60`：`generateRequirements` 对 goal 只做字符串插值，**永远返回 decision/history/architecture 三个固定需求**（注释声称"keyword matching"但未实现）。

**B15 — `ObjectType` 被滥用非枚举值 `"struct"`/`"interface"`**
- `provider/code/provider.go:22-26`、`linker/architecture.go:23-26`：用伪类型 `"struct"`/`"interface"`，与 AKG 固定枚举冲突；`ArchitectureLinker` 依赖这些伪类型匹配，脆弱。

### 🟢 Low

**B16 — DefaultReducer O(n²) bubble sort + 极端裁剪**
- `runtime/components.go:62-118`：冒泡排序；`maxNodes = ForGraph/50`，`ForGraph==0` 时保底 1，可能把大图砍到只剩 1 个节点。

**B17 — `Store.Query` 忽略 `Offset`**
- `store/memory/store.go:57-108`：`Query` 结构体有 `Offset` 字段但查询未应用。

**B18 — `CompiledContext.Intent` 永远为空**
- `compiler/compiler.go:97`：编译不填 `Intent` 字段（设计要求在输出中包含 Intent）。

**B19 — TimelineLinker 语义粗糙**
- `linker/timeline.go:44-85`：相邻两周内必 `generated_by`，超过必 `supersedes`；易产生误导性边（如一个 issue 比某 decision 晚两周就"supersedes"它）。

**B20 — adapter `Confidence` 越界**
- `adapter/memory.go:31`：`Confidence = Importance/100.0`，`Importance>100` → confidence > 1，超出 [0,1]。

**B21 — MCP 工具残缺 + `handleQueryKnowledge` 死代码**
- `mcp/mcp.go:55-68`：`Tools()` 只注册 `build_graph` / `compile_context`；设计要求的 `query_knowledge` / `distill_memory` 未暴露。
- `mcp/mcp.go:182-219`：`handleQueryKnowledge` 已实现却**未注册**；且即便注册，其实现是"重新跑 `Execute` 建图"而非"查 Store"（语义错误）。`distill_memory` 完全缺失。

---

## 三、优先级修复建议

1. **先修安全/崩溃**：B3（MySQL 注入）、B6（nil panic）、B4（MySQL 无 LIMIT/OOM）、B1（Resolver 失效——核心卖点）。
2. **再修"智能层"死代码**：B2（QueryPlan 丢弃）、B8（Pipeline 不接线）、B12（Embedding 死代码）、B21（MCP 残缺）。
3. **质量收尾**：B5（全连接图）、B9（Postgres 空 TimeColumn）、B7（Store 契约）、B11（Compiler 多格式）、B13/B14（LazyLoading/Planner 空壳）。

---

## 四、一句话结论

> AKF 的**骨架已经立起来了**（类型系统、Runtime 编排、5 个 Provider、4 个 Linker、基础 Compiler 都能编译跑通 demo），
> 但 AKG.md 真正值钱的"认知"部分——**实体解析、查询规划、蒸馏闭环、语义检索、多格式编译、懒加载**——
> 目前要么是空壳、要么被当作死代码丢弃、要么直接损坏。
> 说人话：**"建图"能跑，"懂知识"还差得远，整体约完成一半。**

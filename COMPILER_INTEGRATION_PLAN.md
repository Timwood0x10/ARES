# Conversation Compiler 集成开发计划（方向 B）

> 目标：让「AKG + 记忆蒸馏 + 对话压缩」真正组合成一条协作管线，而不是三段各自
> 「接口对齐但空转」的孤立模块。方向 B —— 新压缩管线作为旧管线的**补充**，产出
> 的知识注入 prompt，并与旧管线共享同一个 AKG 存储池。
>
> 关联文档：`CONVERSATION_COMPILER.md`（原设计）、`COMPILER_REVIEW.md`（2026-07-23 审查）。
> 本计划只描述改造路线，不重复声明接口签名——签名以源码为准。

---

## 0. 现状基线（事实，已核对源码）

| 组件 | 位置 | 现状 |
|------|------|------|
| 压缩提取 | `internal/ares_memory/compiler/akg_extractor.go` | zero-LLM，规则/正则；**仅英文有效**，中文场景近乎零产出 |
| 图谱编译 | `internal/ares_memory/compiler/graph_compiler.go` | 实体/事实 → KM 图谱，功能正常 |
| 记忆蒸馏（新） | `internal/ares_memory/compiler/km_distiller.go` | 复用 `distillation.MemoryClassifier`+`ImportanceScorer`，rule-based，产出 `NodeMemory`（拼凑摘要） |
| 记忆蒸馏（旧） | `internal/ares_memory/distillation/` + `internal/knowledge/adapter/distill.go` | LLM-based 语义蒸馏，`DistillBridge` **已接线且有真实调用方** |
| AKG 写入 | `internal/ares_memory/compiler/akg_builder.go` | 产出 `knowledge.KnowledgeObject`；接线时传了 `memorystore.New()`（**独立 store**），**跳过了 `KnowledgePipeline` 精加工** |
| 接线 | `internal/ares_bootstrap/bootstrap_steps_knowledge.go` | `wireKnowledgeCompiler` 挂 `comp.KnowledgeCompiler`；**全仓无 live agent 消费者** |
| Prompt 路径（旧） | `production_manager.go:571` `BuildPromptMessages` | 直接从 session store 拼 prompt，不经 compiler |
| 并发安全渲染 | `context_lifecycle.go:174` `RenderPrompt` | 持锁，并发安全（`CurrentModel` 有并发 map panic 风险，**注入点不可用它**） |

**三处断裂**（详见 COMPILER_REVIEW.md）：
1. 新旧两套蒸馏管线独立，仅"复用分类器"表象，不互通。
2. `AKGBuilder` 用独立 store + 跳过 `KnowledgePipeline` → 没用上 AKG 的加工能力，也没和其他消费者共享池。
3. `RenderPrompt`/`ShouldCompile` 无任何 live agent 调用方。

---

## 铁律：顺序不可颠倒

**先修提取质量 → 再走 AKG 精加工 → 再接 prompt 注入 → 最后共享池。**
先接线后修质量 = 把噪声补充进信号，是已被点名的失败模式（"能跑通但方向不对"）。
每个 Phase 有独立验收门，未过门不进入下一 Phase。

---

## Phase 1：修复提取质量（gating，最高优先级）

> 不接任何线。目标：让 compiler 对**中文对话**能提取出有效（非碎片）知识。
> 全部改动限定在 `internal/ares_memory/compiler/akg_extractor.go`（+ 新增 test）。

### 1.1 三元组提取支持多词 object
- `extractTriple`（`akg_extractor.go:470`）当前只取 object 第一个词
  （`"ARES uses Patch for runtime updates"` → `object="Patch"`）。
- 改为取到句末或下一个从句边界的完整短语，保留修饰语。

### 1.2 分句规则加中文 + 缩写/小数点豁免
- `splitSentences`（`akg_extractor.go:522`）当前遇 `.` `!` `?` 即切，
  会误切 `3.14`、`v0.2.7`、`e.g.`，且不认中文句号。
- 增加：中文标点 `。！？；` 作为分句符；数字间的 `.` 不切；
  常见缩写（`e.g.` `i.e.` `vs.` `v0.` 等）豁免。

### 1.3 实体提取支持中文
- `extractEntities`（`akg_extractor.go:191`）靠"大写英文词且非常见词"，
  对中文完全失效。
- 增加中文实体启发式：连续 CJK 字符段 + 技术术语词典（可复用/扩展
  `normalizer.go` 的 alias 表）；英文路径保留。

### 1.4 决策/约束/权衡/开放问题模式加中文关键词
- `extractDecisions/Constraints/Tradeoffs/OpenQuestions`
  （`akg_extractor.go:265` 起）全是英文模式串。
- 补中文：决策「我们选择/决定/采用/放弃/否决了」；
  约束「必须/不能/需要/要求/禁止」；权衡「但是/然而/代价是/牺牲了」；
  开放问题「待定/待确认/TODO/需要调研/尚未决定」。

### Phase 1 验收门
- 新增中文对话样本测试：一段真实中文技术讨论 → 断言提取出
  ≥ N 个实体、≥ M 条事实、≥ 1 条决策，且 object 不被截断。
- `go test ./internal/ares_memory/compiler/... -race` 全绿，覆盖率不低于当前 87.2%。
- **人工抽检**：提取结果是有效知识而非噪声碎片（这是方向正确性验收，
  不是"能跑通"验收）。

---

## Phase 2：压缩产出走 AKG 精加工 + 共享池

> 目标：解决断裂 2。压缩提取的粗料先经 `KnowledgePipeline`
> （Normalizer→Resolver→Summarizer）精加工，再写入**共享** AKG store。

### 2.1 AKGBuilder 接入 KnowledgePipeline
- 现状：`akg_builder.go` 的 `Build()` 直接 `store.Save()`，绕过 pipeline。
- 参照旧路径 `internal/knowledge/adapter/distill.go:99` 的做法：
  `objects → pipeline.Process() → store.Save()`。
- 给 `AKGBuilder` 增加可选 `*knowledge.KnowledgePipeline` 依赖（option 模式，
  nil 时退化为当前直存行为，保持向后兼容）。

### 2.2 共享 AKG store 而非独立 store
- 现状：`bootstrap_steps_knowledge.go:59` `NewAKGBuilder(memorystore.New())`
  是**新建的独立 in-memory store**，其他消费者（evolution/retriever/service）看不到。
- 改为：从 Components 取**已有的共享 AKG store/runtime**注入
  （需确认 bootstrap 中共享 KnowledgeStore 的持有者，接线时复用同一实例）。
- 这样压缩产出的知识才能被 AKG 的其他消费者共享——落实你说的
  "AKG 不止服务于压缩"。

### Phase 2 验收门
- 集成测试：压缩管线跑一遍 → 断言产物经过 pipeline（summary 被精加工，
  非原始拼凑串）→ 断言写入的是共享 store（另一个消费者能读到）。
- `go build ./...` + 相关包 `-race` 全绿。

---

## Phase 3：Prompt 注入点（opt-in）

> 目标：解决断裂 3。蒸馏后的 Memory 节点注入 agent prompt，默认关闭。

### 3.1 定义注入接口
- 在 memory manager 侧定义一个可选的 "context augmenter" slot：
  当 `knowledge_compiler.enabled=true` 且 lifecycle 存在时，
  调用 `ContextLifecycle.RenderPrompt(ctx, format)`（**并发安全**，
  不要用 `CurrentModel`）取渲染好的 context block。

### 3.2 接入 BuildPromptMessages
- 在 `ProductionMemoryManager.BuildPromptMessages`（`production_manager.go:571`）
  返回前，把 compiler 渲染的 context block 作为一条 system/context message 追加。
- 注入是**加法、opt-in**：flag 关闭时零行为变化；开启时只追加不改写既有消息。
- 注意接口面：`BuildPromptMessages` 被 6 处实现/mock 覆盖，
  **不改接口签名**——通过给 `ProductionMemoryManager` 注入可选依赖实现，
  避免波及全仓 mock。

### 3.3 喂给 compiler 的对话来源
- lifecycle 需要 `Compile(messages)` 才有内容可渲染。
- 在 `AddMessage`/`AddStructuredMessage` 路径上，当 flag 开启时把消息
  异步喂给 `ContextLifecycle.Compile`（用 `ShouldCompile` 做节流，
  注意 `chars/4` 的 token 估算对中文低估——见 3.4）。

### 3.4 修 token 估算（中文）
- `ShouldCompile` 的 `chars/4` 对中文低估 4–8 倍，导致触发严重滞后。
- 中文按字符数近似 token（CJK 字符 ~1 token/字），或分别统计 CJK/ASCII。

### Phase 3 验收门
- flag 关闭：`BuildPromptMessages` 输出与改造前**逐字节一致**（回归测试）。
- flag 开启：prompt 末尾出现压缩 context block，且内容来自真实对话蒸馏。
- `-race` 全绿（注入路径与 Compile 并发安全）。

---

## Phase 4：两管线协作去重

> 目标：新旧管线产出都进同一 AKG 池，消费者统一取，避免重复知识。

### 4.1 明确分工
- 旧管线：LLM 语义蒸馏 → `DistillBridge` → AKG store（语义丰富，成本高，低频）。
- 新管线：规则提取 → AKG pipeline 精加工 → AKG store + prompt 注入（快，零成本，高频）。

### 4.2 去重策略
- 两者写同一 store，靠 AKG pipeline 的 Resolver/EntityMatcher 做实体归并；
- Memory 层面复用 `km_distiller.go` 已有的 token-Jaccard 相似度去重
  （`memorySimilarityThreshold=0.75`）跨来源合并。

### Phase 4 验收门
- 同一对话分别经新旧管线 → AKG store 中不产生重复实体/记忆节点。
- 端到端测试：对话 → 压缩+蒸馏 → AKG → prompt 注入，全链路打通。

---

## 代码层遗留问题（穿插各 Phase 修，来自 COMPILER_REVIEW.md）

- **P1** `Prune`（`types.go:230`）O(n²) 冒泡 → 换 `sort.Slice`。（随 Phase 1/2）
- **P1** `memoryID`（`km_distiller.go:453`）FNV-32a 碰撞风险 → 升 FNV-64a 或 SHA。（随 Phase 2）
- **P2** `CurrentModel` 裸指针并发风险 → Phase 3 统一改用 `RenderPrompt`，
  并在 `CurrentModel` 文档/或废弃标注上加强。
- **P2** 增量编译 mutate 调用者 model 指针 → Phase 3 明确契约或拷贝返回。

---

## 里程碑与依赖

```
Phase 1 (提取质量)  ──►  Phase 2 (AKG 精加工+共享池)  ──►  Phase 3 (prompt 注入)  ──►  Phase 4 (协作去重)
   gating              依赖 1 的有效产物            依赖 2 的共享池           依赖 1/2/3 全部
```

- Phase 1 是**硬门**：不过不进入后续任何接线工作。
- Phase 2、3 可部分并行（2 管存储、3 管消费），但 3 的验收依赖 2 的共享池就绪。
- 每个 Phase 独立可交付、可回退（flag 保护），任何一步 `-race` 不绿即停。

## 不做什么（范围边界）

- 不给 compiler 引入任何 LLM 调用——保持 zero-LLM 设计。
- 不改 `BuildPromptMessages` 接口签名——避免波及 6 处实现/mock。
- 不动旧 `DistillBridge` 的既有行为——新管线是补充不是替换。
- 不重写 `AKG.md` / `AKG_plan.md`（用户自有规格文档，只读）。

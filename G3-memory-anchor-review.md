# G3 独立评审：滑窗外历史的"记忆锚点"LLM 摘要（需显式授权的新功能）

> 状态：**待授权（proposal）**，不在 0.3.3 收口轨内。
> 定位：这是附录 B 四条里**唯一本质为"新增能力"**的一条——它给热路径之外引入一次 LLM 调用和一条新失败路径。附录 B 的 G1/G2/G4 都是"闭环/修 bug/接线既有实现"，G3 不是。按仓库铁律"方向优先于接线"（`plan/rules/code_rules_v2.md` §0.4）与用户"优化和闭环、不加新功能"的原则，**它必须单独拍板，不能顺手并入**。
> 本文给出：现状事实、G3 要动什么、代价与风险、设计选项（重点是如何不堵热路径）、红线、验收、推荐。

---

## 1. 现状（逐文件核实）

| 事实 | 证据 |
|---|---|
| `BuildContext` 同步截断：只留最近 `MaxHistory` 条，**出窗的旧历史被直接丢弃** | `internal/runtime/memory/manager_impl.go:565`（`messages = messages[len-maxHistory:]`） |
| `BuildContext` 在**每个 planner 量子前被同步调用** | `fabric/agent/planner_cognition.go` 的 `assembleContext` 读路径；serve 热路径 |
| 已存在一个 LLM 摘要器，但**生产零调用**（仅自测引用），已按 C10 标注为 G3 前置 | `internal/knowledge/pipeline/llm_summarizer.go:60-67`（`NOTE(tech-debt … C10)`） |
| 摘要器只需注入一个生成函数 | `type LLMGenerateFunc func(ctx, prompt) (string, error)`（`llm_summarizer.go:13`） |
| memory manager 的 `BuildContext` 热路径**当前不持有 LLM client** | `manager_impl.go` 热路径无 LLM 调用（只有 RAG 向量检索） |
| 已有**异步蒸馏管线**（off-hot-path 的 LLM 类工作） | `internal/runtime/memory/pipeline.go:52` `Distiller` 接口 + `manager_distill_wire.go` |
| RAG 已能召回"相关但不最近"的旧知识，但它是**语义相关、非时序** | §24；附录 B.4 已载明 RAG 是"related"不是"chronological" |

**一句话现状**：出窗历史现在是"丢掉"，RAG 从**相关性**侧部分补偿，但"很久以前第 3 步的一个关键决定"既不在窗内、也未必被 RAG 命中——这是 §26.4 已承认的已知短板。G3 想补的正是这个**时序性缺口**。

---

## 2. G3 要动什么

1. 把 `BuildContext` 丢弃的出窗前缀（`messages[:len-maxHistory]`）**摘要成一条固定位置的 "memory anchor" 消息**，拼在窗口历史之前。
2. 摘要用 LLM（可复用 `LLMSummarizer`），因此 memory manager 需**新注入一个 `LLMGenerateFunc` 依赖**。
3. 锚点仍受 G2 的 token 预算约束（锚点也是消息，参与裁剪）。
4. 开关：`memory.memory_anchor`（默认 **false**），闭环到 ares.yaml（沿用 G1/G2 的接法）。

---

## 3. 代价与风险（这就是它不是"闭环"的原因）

| 维度 | 说明 | 严重度 |
|---|---|---|
| **热路径延迟** | `BuildContext` 每个量子都跑；若在其中同步调 LLM 摘要，等于给**每一步**加一次 LLM 往返 → serve 明显变慢。**这是红线**。 | 高 |
| **Token 成本** | 每次（或每累积 N 条出窗）多一次 LLM 调用，真实烧 token，必须计入任务成本通道（否则 GA 的成本惩罚失真） | 中 |
| **新失败路径** | LLM 超时/限流/返回空——若处理不当会把一个"可降级的上下文优化"变成"阻断整个量子"的硬失败 | 高 |
| **非确定性** | LLM 摘要每次不同 → 锚点区域**破坏 prompt 前缀缓存**（与 G4 刚修的方向相反）；且摘要本身有损、可能漏掉关键细节 | 中 |
| **依赖注入扩面** | memory manager 热路径从"无 LLM"变"需 LLM"，装配复杂度上升；SDK/serve 两条装配都要接 | 中 |

---

## 4. 设计选项（核心是：绝不在热路径同步调 LLM）

- **(A) 异步预算 + 缓存（推荐，若要做）**：复用既有蒸馏管线（`pipeline.go` 的 `Distiller`，本就 off-hot-path）在会话推进时**异步**把出窗前缀摘要成锚点，按 session 缓存；`BuildContext` **只读缓存的锚点**，热路径零 LLM 调用。缓存缺失/过期/摘要失败 → **按现状降级（照常丢窗）**，不阻塞、不新增热路径失败点。
- **(B) 惰性触发 + 下轮生效**：`BuildContext` 发现锚点过期时**异步**发起摘要、本轮仍用现状行为，下一轮量子才用上新锚点。实现比 (A) 简单，但锚点总"慢一拍"。
- **(C) 同步摘要（否决）**：直接在 `BuildContext` 里同步调 LLM。**违反红线**，不考虑。

无论 (A)/(B)：默认关、失败降级到现状、锚点 token 计入成本通道、锚点仍过 G2 预算。

---

## 5. 红线（若授权，必须全部满足）

1. **热路径不同步阻塞**：锚点生成一律异步/预算外；`BuildContext` 只读已算好的锚点。
2. **失败只降级、不升级**：摘要失败 = 回到"丢窗"现状，不得让量子失败、不得丢已有窗内历史。
3. **默认关 + 闭环到 ares.yaml**：`memory.memory_anchor: false` 默认；配置→bootstrap→manager 全链接通（别重蹈 G1 第一版"代码里有、运维开不了"）。
4. **成本可见**：锚点的 LLM token 计入任务成本通道（与 planner 量子同一通道）。
5. **估算/有损显式标注**：锚点是有损摘要，文档与注释都要讲清"不是无损历史，可恢复性仍以 L2 图 + checkpoint 为唯一真相"（沿用附录 B.4 红线 3）。

---

## 6. 验收（DoD）

- [ ] 默认关：`memory_anchor` 缺省 false，关闭时 `BuildContext` 与现状逐字节一致（回归断言）。
- [ ] 热路径无同步 LLM：注入一个"会阻塞/报错的"假生成器，断言 `BuildContext` **不阻塞、不报错**（走降级）。
- [ ] 失败降级：摘要返回 error/超时 → 上下文回退到纯丢窗，窗内历史不丢（单测）。
- [ ] 成本计入：锚点 LLM 用量出现在任务成本通道（单测/集成）。
- [ ] 闭环到 ares.yaml：`memory.memory_anchor: true` 解析→生效（Load 测试 + bootstrap 映射）。
- [ ] 文档：§26 增"记忆锚点（可选、异步、有损）"一节，`CHANGELOG` 记录。

---

## 7. 推荐

**默认建议：先不做，除非有具体需求方。**

理由：
1. G3 是**加能力**，与本轮"不加新功能"的原则冲突；附录 B 自己也把它列为高风险、默认关。
2. 它要解决的"超长会话时序性缺口"目前**没有确认的真实需求方**——现状是 RAG 部分补偿 + §26.4 已诚实标注为已知短板。为一个无需求方的短板引入"热路径旁的 LLM 调用 + 新失败路径 + 非确定性 + 依赖扩面"，是典型的投机式复杂度。
3. 真要做，**只接受设计 (A)/(B) 的异步形态**，且五条红线全满足；绝不接受 (C)。

**触发条件（满足其一再启动）**：
- 出现明确的长程任务场景，实测 RAG 召回不了"不最近但关键"的时序信息，且该缺口造成了可观测的质量下降；
- 有人愿意为锚点的 token 成本与运维复杂度买单。

在那之前，`LLMSummarizer` 维持 C10 的"capability reserve + 仅测试引用"标注即可，不接线。

---

## 8. 与已完成项的关系

- G1（turn-aware 清理）、G2（token 预算）、G4（l1Priors 排序）**已完成且闭环到 ares.yaml**（见 `plan/0.3.3_task.md` §B.8/§B.9）。附录 B 的"闭环且不加功能"部分到此为止。
- G3 是附录 B 的唯一剩项，且性质不同（加功能）——本文即其独立评审。**结论：等需求与授权，不随 0.3.3 收口。**

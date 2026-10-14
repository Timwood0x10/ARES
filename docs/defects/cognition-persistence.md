# 缺陷与边界记录 · 认知持久化专项（P0-4 / A / P0-1）

> 本文档是 P0-4（Q3/A-dispo1/A-F4 认知有损）"真存思路"那半边（方案 A + P0-1）的
> **存什么 / 如何存** 设计记录。姊妹篇：`disposable-agent.md` A1「核实 / 修复方案」、
> `dynamic-dag-and-quantum.md` Q3、`genetic-algorithm.md` G1。
> 性质标记：`决策`=已议定方向 / `取舍`=知道边界即可 / `待验证`=动手前要核一事。
>
> **一句话总纲**：丢的不是"全量输出"，是**planner 每量子那段"为什么这么排"的自然语言推理**
> （工具输出 / 调用顺序早已在 L2 图 + envelope 里，不重复）。存法走**既有 `res.Metadata` 跨包键
> 通道**进 `StepCheckpoint`（`any`，无版本门），**暂不升 schema v6**；读侧 `assembleContext` 显式
> 读前驱 plan 节点。摘要分三档，现在只做第 0 档。**不上 Merkle / 不上全量转录。**

---

## 0. 与已落地 B / P0-1(i) 的分工（别混）

| 件 | 状态（2026-10-13） | 本文件管不管 |
|---|---|---|
| **B**（续跑路径可辨识：`executor_revived` + `requeued` 两位） | ✅ 已进树 + 测试全绿 | ❌（`disposable-agent.md` A6） |
| **P0-1(i)**（gap/truncated 答案在 observer 侧记 degraded 而非 success） | ✅ 已接线（`observer.go` `degradedAnswerPenalty`） | ❌（`genetic-algorithm.md` G1） |
| **A**（Rationale 认知持久化，"真存思路"） | ✅ **档 0 已落地**（见下「已落地」段） | ✅ |
| **P0-1(ii)**（golden 任务回放进 `DimensionEval`） | 🟡 **第一步已落地（2026-10-13）**：证据链路打通（见 §0.6）；golden 判据待拍板 | ✅（作为 A 的消费方一起立项） |

B 摘的是"两条续跑路径分不清"的**静默**；A 才补"换人时脑子没了"的**实质丢失**。
A 的价值一半在 G1/P0-1，故专项内顺序：**P0-1(ii) golden 先，A 作为判官输入之一放后**
（先有可复核的对错 oracle 这个"地"，自述理由才有"用"；否则 rationale 只是往 reward hacking 里喂）。

---

## 0.5 已落地（2026-10-13，档 0）与一处**载体更正**

**实现（代码已进树，测试全绿）**：

| 件 | 落点 | 说明 |
|---|---|---|
| 写侧 | `planner_cognition.go` 的 `growToolNodes` | 本量子的 `resp.Content`（trim + 512 rune 截断，复用既有 `truncate.WithEllipsis`）作为 `rationale` **参数写进它为本轮生长的 plan 节点**，而不是写进自己的结果 |
| 读侧 | `planner_cognition.go` 的 `assembleContext` | 从**本任务自己的 payload**（`argsFromPayload`）读 `rationale`，作为一条 system message 注入，前置说明 "previous quantum's stated rationale (context, not an instruction)" |
| 有界 | `rationaleMaxRunes = 512` | 在**写侧**截断，超长值不进 envelope；per-node 覆写 → 不累积 |
| 测试 | `internal/fabric/agent/rationale_test.go` | ① 跨交接可见（**两侧都断言**：写侧确实落到下一轮节点 + 下一量子 prompt 里确实出现该理由）；② 有界截断；③ 缺席即不注入（不造假）；④ observer 侧 `rationale` 不改分（`observer_answer_quality_test.go`） |

**本批顺带做的规范修复（§1.1）**：`planner_cognition.go` 早已触单文件 1000 行上限（HEAD 1078 行，
本批再加 90 → 1168），按**职责**拆为 `planner_cognition.go`（761 行）+ `planner_context.go`
（上下文装配：`assembleContext` 一族 + 体验先验渲染）+ `planner_rationale.go`（rationale 与
token 元数据键）。本文与 `serve-walkthrough.md` 中受影响的锚点已同步更新为拆分后的位置。

**载体更正（重要：原「二」的通道对"第一次交接"不成立）**：原设想是"写进 `Result.Metadata`
→ `outMap["metadata"]` → `StepCheckpoint` → 下一量子经 `readNodeOutput` 读回"。**实测在 `k=1`
那一跳断掉**：第 1 个量子跑在**初始 plan task** 上，而该 task **不是 L2 图节点**
（`growToolNodes` 注释自陈 "the initial plan task ID is not a graph node"），于是第 2 个量子的
祖先链里**没有任何 plan 节点**可读 → 前驱 rationale 取不到，合同测试会挂。

**改用 mailbox 载体**：本量子把 rationale 写进**它为本轮生长的 plan 节点**（即下一量子的 task、
也是换人所续的那个 task）的 args。于是"上一量子的理由"永远在**我自己**的参数里 → 换人续跑
同一 task 也读得到，**不需要祖先遍历、不需要回读 fabric**。envelope 的 `Payload` 本就承载
node args，因此**依然不升 schema**（"暂不升 v6"的结论不变）。

**仍未做**：`record_rationale` 元工具（按设计先测真实 provider 的 content 空率）—— 目前没引入
任何新工具，理由**只**取自模型本就写出的 content；provider 不给时如实缺席。
另：yield（非终态）分支的写入点**对本 planner 不适用**（`plannerCognition` 恒返回 `Done: true`），
留待非 planner cognition 接入时再补。

---

## 0.6 已落地（2026-10-13，P0-1(ii) 第一步：`dimension_eval` 证据链路打通）

**做了什么**（详见 `genetic-algorithm.md` G1「修复」段）：把"评测层 → fitness"的 accuracy 通道接通 ——
`Evidence.Value`（payload `{"value": 0..1}`，与门分**同一条归一规则**）、`DimensionJudgeSource` 导出、
aggregator 改按 **kind** 过滤（原先按 kind 字符串当 source 查，永远 0 命中）、payload 解析改为
`*float64`（**无标量不再被当成 0.0 计入**）、生产 `setupEvaluators` 开
`WithDimensionAveraging()` + `WithEvidenceStore(evStore)`（**此前根本没接桥**）。

**实机反证**：查询退回修复前形态 → 端到端测试失败（真生产者→真消费者）。

**仍未做（需你拍板后才动，别当成已完成）**：
1. **oracle 形态**：参考答案精确/包含匹配 / rubric+LLM judge / 分层？（四维提示词**已含**
   `{{.ExpectedOutput}}` —— 参照物本来就喂给判官了）；
2. **golden 语料**：现仅 `test/eval/basic.yaml`、`test/eval/tools.yaml` 两份小样例，规模远不够；
3. **触发方式**：离线 / 定时 / evolution 前；
4. **判据已部分落地（2026-10-13，真跑，非注入）**：`sdk/adversarial_e2e_test.go` 在 **SDK 的 L2 core** 上
   真跑一次会话（零 LLM：`rt.llmSvc = mockLLMSvc`），链路为
   `真会话 → SessionEvalExecutor 读回真实 provenance → Layer 0 floor → bridge.Emit → RuntimeFitnessAggregator.Window`：
   - **gap 用例**：脚本每轮给空 content → 答案节点无 body → 运行时报 `gap` → floor 压到 **0**，
     `PerSource["dimension_eval"].Mean ≤ 0 + 0.05` ✅；
   - **对照用例**：同一 wiring 给真答案 → 报 `supplied` → `.Mean > 0.8` ✅（防"全低假绿"）。
   - **truncated 用例：已真跑产出（2026-10-13，`t.Skip` 已删除）**。`sdk.WithMaxPlanDepth(1)` 下会话
     真的迭代到增长上限（planner 日志 `max plan depth reached, depth=1 max=1`），答案节点带 `truncated`，
     floor 压到 **0.5**。**当初 skip 的真因是一个缺陷，不是 seam 缺失**：节点 arg 在 engine 中**字符串化**
     存储（`argsMetadata` 对每个值 `json.Marshal`），而 `answerCognition` 的读取处断言 `.(bool)` ——
     `truncated` 被每个撞墙会话写入、被所有读取方读丢（`answer_cause` 是字符串所以没暴露）。后果是
     P0-1(i) 的"`truncated=true` → score=0.5"在生产侧**从未生效**。已修（`answerArgTruncated` 读 wire 形状，
     同时接受内存中的 bool），并在 `answer_truncation_test.go` 用**真实 wire 形状**（走 `argsMetadata`）
     锁死 —— 旧测试传真 bool，恰恰掩盖了它。另：e2e 的脚本必须让"答案是合成的"那一跳拿到 content
     （实测调用序：planner 第 1 轮要工具 → 答案节点合成），否则结局落 gap（penalty 0）而非中间档。
   - 架构锁未破：复用 `rt.ensureL2()` 的 core，**未新增 `NewExecution` 调用点**。
5. `rationale`（A 的产出）作为判官输入之一，仍排在 oracle 之后。
5. **信号的量级目前很薄（必须写出来，别把"通道已通"读成"已防住糊弄"）**：判据是判官拿 `ExpectedOutput`
   比对，而 golden 语料只有 `test/eval/basic.yaml` + `test/eval/tools.yaml` 两份小样例。所以现在 GA 拿到的
   是一层**很薄的正确率**；**真正"防 reward hacking"要等语料扩到能代表任务面之后才兑现**。这一批交付的准确
   表述是"**正确率信号从 0 变成非 0，且链路可复核**"，不是"GA 已经会做对了"。

---

## 一、存什么（What）

### 真正丢的那段 = planner 的 `resp.Content`（L0）

`planner_cognition.go` `ExecuteStep`（`:335`）用 `len(resp.ToolCalls) == 0` 判定终答。
**有工具调用的量子**（`growToolNodes` 分支，`:351`）里，`resp.Content` 被**丢弃**——
只有 `resp.ToolCalls`（结构化"调什么、传什么"）被长进 L2 图。

"思维链"要分三层，能拿到的完全不同（**别当一层存**）：

| 层 | 是什么 | 现状 | 能否持久化 |
|---|---|---|---|
| **L0** 模型自然语言输出（`resp.Content`） | "我先查 A 因为用户问的是 X" | 产生了但**有工具回合常为空 + 被丢弃** | ✅ 存这段（本文方案） |
| **L1** extended thinking / reasoning block | 模型内部推理（Anthropic thinking 等） | `internal/llm/client.go` **未捕获**；provider 相关 | ⚠️ provider 支持才存（档 1） |
| **L2** 隐式 CoT（"为什么选 A 不选 C"） | 模型没写出来的内部推理 | **不存在于任何载体** | ❌ 不可观测，只能靠 L0/L1 间接表达 |

**关键**：L2 永远拿不到。能落地的只有 L0（+ 部分 L1）。"完整记录 LLM 整个输出"是**误解**——
工具输出、工具调用参数**早已在 L2 图 + fabric envelope 里**（`assembleContext` 沿前驱链重建，
`readNodeOutput` 注释 "the envelope is the only place output lives"，`planner_context.go:67`）。
**增量只有每量子几百字节 L0 叙事**。

---

## 二、如何存（How）

### 载体决策：走 `res.Metadata` 跨包键，暂不升 v6 〔决策〕

证据链（逐环已核实）：

```
planner 写 Result.Metadata["rationale"]
  → scheduler_quantum.go 终态分支：outMap["metadata"] = res.Metadata
  → 该 map 作为 StepCheckpoint 进 envelope（StepCheckpoint 是 any，无版本门）
  → 下一量子 readNodeOutput：fabric.Task(nodeID) → DecodeCheckpoint → StepCheckpoint
```

- `extractOutputContent` / `PlanLoop.collectOutput` 只读特定键 → **加键不破坏既有消费者**；
- `res.Metadata` 已是**既定跨包键契约**（token 通道 `tokenUsageFromResult`、P0-3 的
  `answer_cause`/`answer_body_source` 同走此路）——**复用同一条路，不另起炉灶**（规范 §5.1）；
- **有界是形状自带的，不用加淘汰**：L2 每轮是独立 task/节点（`SessionNodeID(sid, round, "plan", 0)`），
  每个节点 envelope 只写一次；yield 路径 `StepCheckpoint` 每量子**覆写** → 只留最新一份，
  **不随 session 线性膨胀**（规避 `disposable-agent.md` A3 那类"1024 上限静默退化"坑的方式是
  把形状选对，不是加淘汰策略）；
- **暂不升 typed v6**：现在只有内部消费者。若将来出现**外部契约**消费者，再把字段提升为
  typed `CheckpointEnvelope.Rationale`（forward-only、rollback 需 drain，沿用 v1→v5 先例）——
  那时升版是自然演进，不是预付成本。

### 写侧契约：三选一，推荐专用工具〔决策〕

**机制坑（动手前必须定）**：工具调用回合 `resp.Content` **通常为空**（planner 正是靠
`len(resp.ToolCalls)==0` 判终答）→ 纯靠 content 存不住"理由"。三选一：

| 选项 | 说明 | 评估 |
|---|---|---|
| **① `record_rationale` 专用工具**（**倾向**） | planner 主动调它写理由，随 `growToolNodes` 落进当前节点 | 可校验、可截断、可测；契约显式 |
| ② 首个工具调用 args 带 `rationale` 字段 | 顺手带 | 模型易漏，且污染工具 args 语义 |
| ③ content 约定"要调工具必先写理由" | 最便宜 | 各 provider 对 tool-call 回合 content 处理**不一致**，脆 |

### 读侧：`assembleContext` 显式读前驱 plan 节点 〔待验证〕

`assembleContext`（`planner_context.go:16`）现在**只走 `tool/` 节点**
（`!strings.HasPrefix(step.AgentType, "tool/")` 直接 skip，`:60`）。要读到"上一量子的
plan 节点理由"，需**显式把前驱 plan 节点的 `Result.Metadata["rationale"]` 读进来**，
作为一条 system message（"上一量子的决定与理由"）注入。这样**同 agent 续跑与换人都能读到**。

**动手前待验证的一件事**：yield（非终态）分支走 `out.Checkpoint`，与终态的 `outMap` **不同源**
→ planner 在同一会话**多次 yield** 时 rationale 是否也随 `out.Checkpoint` 携带，需确认或补齐
（否则会退化成"只有终量子带 rationale"）。

### 有界 〔决策〕

生产者侧硬上限（**512~1024 char**）+ 截断（`s[:n] + "…"`）。per-node 覆写 → 无累积膨胀。

### 摘要的三档（成本递增，别一步到位）〔取舍〕

"做个摘要"是对的，但最便宜那档**模型自己已经写出来了**，先存它，别上来就额外 LLM call：

| 档 | 做什么 | 何时 | 额外 LLM 成本 |
|---|---|---|---|
| **0（现在做）** | 存 `resp.Content` / `record_rationale`（512 截断） | 本专项 A | **0**（文本已产生，只是之前没存） |
| **1（看 provider）** | 存 `resp.ReasoningContent`（thinking） | provider 确定后 | **0**（response 里已带，只是之前没存；需加捕获 + 截断尾部） |
| **2（等 G1）** | 每量子一次 micro-summarization（"1 句话总结决定"） | **P0-1(ii) golden 专项时** | **+N 次 call / session**（token 税翻倍，**不默认开**） |

### 可靠性上限（必须写进设计）〔取舍〕

rationale 是**模型自述**，是 **reward hacking 的喂食点**（"理由写得漂亮" ≠ "做对了"）。
它只能当**判官输入之一**，须与**真实工具结果 / golden oracle** 对照判定，
**不得单独作为高权重 correctness 通道**（否则 G1 那条"越跑越糊弄"的漂会被自述理由放大）。

---

## 三、"会不会内存炸" 与 "存 hash / Merkle tree" 〔取舍：不上〕

### 1. 不会炸，且真正的瓶颈不是字节

checkpoint 存多大**根本不是约束**（每量子几百字 KB 级，放 PG/envelope 便宜）。
**真正约束是"往下一量子 prompt 里注入多少"**——注入面每 1 字节都花 LLM token（Q2 税）+
挤 context window + 稀释注意力。所以"完整存、按需取"成立，但**取完仍要截到 512 内才注入**——
**注入面有界与存储方案解耦**，存储侧选什么都改变不了这一点。

### 2. Merkle tree 解的是另一个问题，本场景收益为零 〔取舍〕

| 诉求 | 该用 | 该排除 |
|---|---|---|
| 省空间 | gzip/zstd 压文本（叙事压缩比 ~3x），或**存摘要不存原文**（100 字摘要 < 500 字原文） | Merkle 叶 |
| 防篡改审计 | **一个整体 hash 就够**（envelope 加 `ContentHash` 字段，沿用规范 §6.2 的 `SpecHash` 先例） | Merkle **树** |
| 跨节点校验 / P2P 共识 | （没有此需求） | —— |

**两条硬理由：**

- **hash 不可逆**：Merkle 只给"这段内容确实产生过且没被篡改"的密码学证明，**叶存的是
  SHA-256，还原不出文本**。"省空间 + 能逆向复现原文"两个诉求它只满足前一半，后一半得**另配
  原文 blob 存储**（hash 当指针去取原文）→ 存成"原文 + 一份哈希树"，比直接存原文**还多
  一份账本**，省空间诉求自相矛盾。
- **消费方是 LLM，不是审计员**：Merkle 的经典场景（审计取证 / IPFS 式去重 / 跨节点共识）
  本项目**一条都不踩**——单进程 IPC（`collaboration-and-factories.md` I3 明确"单租户单进程
  v1"），没有第二个节点要你证明什么。为单进程内存系统造密码学树 = 规范 §0.2"禁空壳机制"
  级别的复杂度（分块策略、树存储位置、chunk TTL、哈希链断裂处理），**为不存在的威胁模型买单**。

**结论**：完整性真要就一个 `ContentHash` 字段（整体 hash + 既有 schema version，§6.2 既有
模式）。**Merkle 那条路 pass。**

---

## 四、合同测试（动手时写，锁语义不锁实现）

| 用例 | 断言 |
|---|---|
| `TestRequeueWithRationale_InjectedIntoSuccessor` | 换人后下一量子的 `assembleContext` message 序列**含前驱 rationale**（锁"换人读得到思路"） |
| `TestRationaleBounded` | 1000 字 rationale 被截断到 512 + `…`（锁有界） |
| `TestRationalePerNodeNoAccumulation` | 多次 yield 后 StepCheckpoint 只留最新一份，不随 session 膨胀 |
| `TestOldEnvelopeNoRationale_EmptyCompat` | 无该键的旧 envelope 读出来为空，不报错（锁前向兼容，暂不升 v6 的前提） |
| `TestYieldCarriesRationale` | （对应二、"待验证"那条）yield 非终态分支 rationale 随 `out.Checkpoint` 携带；若当前不携带，这条测试会先失败，先补写侧再翻绿 |
| `TestRationaleNotSoleCorrectnessSignal` | observer 不因 rationale 存在就把 outcome 判 success（锁"可靠性上限"，防 reward hacking） |

**测试纪律（规范 §7）**：禁 `time.Sleep` 同步、表驱动、用例名说明场景、与文件同包同目录、
无调试输出。

---

## 五、性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| 存 `resp.Content`/`record_rationale`（L0，512 截断，走 `res.Metadata` 通道，暂不升 v6） | 决策 · 现在做 | 高（"真存思路"的实质，摘"换人有损"） |
| 读侧 `assembleContext` 显式读前驱 plan 节点 rationale | 待验证（yield 多次场景） | 与上同步 |
| 写侧契约选 `record_rationale` 专用工具 | 决策 | 动手前定 |
| 摘要档 1（thinking） | 取舍 | provider 确定后 |
| 摘要档 2（micro-summarization） | 取舍 | **等 P0-1(ii) golden** |
| rationale 作判官输入之一，**不得单独做高权重 correctness** | 取舍（可靠性上限） | 写进设计 |
| 整体 `ContentHash` 做完整性（若需要） | 取舍 | 可选，沿用 §6.2 先例 |
| **Merkle tree / 全量转录** | 取舍 · 不做 | pass（见三、） |

**专项内顺序**：P0-1(i) 已接线 ✅ → **P0-1(ii) golden** → **A（Rationale，本文）** 作为判官
输入 + 续跑思路，放最后。A 排后理由：它的主价值在 G1，而 G1 主信号来自**可复核的 golden**，
不来自自述理由——先有 golden 这个"地"，rationale 才有"用"。

---

## 面试串法（把"认知持久化"讲对）

> "P0-4 丢的不是全量输出，是 planner 每量子那段'为什么这么排'的自然语言（L0，`resp.Content`）——
> 工具输出和调用顺序**早就在 L2 图 + envelope 里**，`assembleContext` 靠图结构重建，不丢。
> 存法走**既有 `res.Metadata` 跨包键通道**进 `StepCheckpoint`（`any` 无版本门），**暂不升 schema
> v6**（现在只有内部消费者，将来外部契约出现再升 typed 字段，是自然演进不是预付成本）。
> per-node 覆写**天然有界**，不用滑窗。摘要分三档：现在存 L0（免费，模型已写出）、看
> provider 存 thinking（免费）、micro-summarization（+1 call/量子，等 G1 才做）。
> **可靠性上限**要写死：自述理由是 reward-hacking 喂食点，只能当判官输入之一，须对照
> golden + 真实工具结果，不得单独当 correctness。
> '会不会内存炸'问反了——存储不是瓶颈，**prompt 注入面（token）才是**，有界截断解决，
> 与存储方案解耦。有人问要不要 Merkle tree 省空间：不上——它解的是防篡改/审计/跨节点，
> 我们单进程、消费方是 LLM 不是审计员，收益为零；要完整性一个整体 `ContentHash`（沿用
> `SpecHash` 先例）就到顶了，hash 还不可逆、'逆向复现原文'不成立。"

# ares 系列开篇：无聊自己搓一个 Agent 框架（0.3.x）

> 我一直觉得，最好的学习方式就是自己造一个轮子。
> 不是因为轮子不够用——是因为造完之后，你再也不会被轮子卡住了。

---

## 闲话 + 背景

嗯……这是一个系列文章的开篇。属于思绪飞扬、放飞自我的那种。和管理员申请过了，说可以推荐自己的项目——那我就厚脸皮一回 :rofl:

先聊点闲话。

这两年 AI 发展有多快不用我多说，Agent 早就融入了大家的日常。但问题来了：**大家是怎么学 Agent 的呢？**

赶上 AI 大跃进的时代，不学肯定掉队。学吧——工作本来就够烦了，还要被逼着学新东西。我当时的想法是：打不过就加入。后来一合计：**不如自己操刀设计一个算了。**

## 简单回顾

咱也不是那种老实巴交写代码的人，hhh，所以在职场混得很差

最早接触 Agent 要追溯到去年和朋友联合创业做的 **Music AI**。当时我自己设计架构，手搓了一个 music tool，可以对音频按音轨进行分层处理。我要做的不是普通的 AIGC——而是修复和完善。类似于现在 AI 可以把老电影变成 4K 画质——我们想做的是完善音频，找到音频中不和谐的地方，经过 AI 审查分析之后给出完善建议。

当时大模型都设计好了，投入了训练，用的是 MLX + PyTorch。可惜资本市场萎靡，项目太监了……

带着这份不甘心，我继续投入 AI 的学习。期间做了两个可交互工具：

- [**Model_explorer**](https://github.com/just-for-dream-0x10/Model_explorer) —— ML 底层数学的可交互可视化工具
- [**Transformer_explorer**](https://github.com/just-for-dream-0x10/Transformer_explorer) —— Transformer 底层原理的可交互版

都是基于我自己的笔记做的。后来做了个独立项目上了 crates.io，反响不错，洋哥们儿用得也挺开心。

然后就到了今天的主角：**ares**。

## 技术选型：为什么是 Go？

我本身是后端开发，熟悉 Rust 和 Go。

**先排除了 Python。** 不是说 Python 不好——但用起来总觉得不舒服，看起来也不舒服。尤其是并发场景，又慢又乱。

然后在 Rust 和 Go 之间纠结：

- **Rust**：用得得心应手，但编译速度太劝退了。每次改点东西都要等，开发体验被打断。
- **Go**：简洁、极致性能、天生的并发支持。

还有一个私心：有个 HR 说我不懂 Go、没有 Go 项目。作为一个有操守的技术人——**我必须选择 Go，消灭一切质疑的声音。**

## 从 Python 痛苦到 Go 重生

最开始我也和很多人一样，从 Python 入门。先搭了个本地知识库，用 vector database + Ollama。

第一次跑通的时候还挺兴奋的：文档切分、embedding、入库、查询……Ollama 居然真的能回答我的本地笔记内容！

但兴奋没持续多久。

当我想加 **tool calling、多步推理、跨会话记忆、多 Agent 协作** 的时候——一切都崩了。Python 在并发场景下又慢又乱，内存管理一塌糊涂，工作流逻辑变成一堆 callback 和状态机的 spaghetti code。每次想改流程（加 failover、加 human-in-the-loop）都要重写一大半代码，调试长时任务像是在抓鬼。

我心想：**一定有更好的方式。**

Go 的简洁、极致性能和天生的并发支持让我眼前一亮。于是我决定：从头用 Go 重写整个 Agent 系统。

**Shedding Old Baggage, I Designed My Own Agent Framework** —— 这就是 ares 的诞生故事。

从最基础的 LLM 调用和简单 RAG 开始，但这次感觉完全不一样：

- **Goroutines** —— 并发 Agent 天生又快又轻量
- **强类型 + 干净的接口** —— 设计出更清晰的抽象
- **Channel 和 Context** —— 工作流编排和取消变得可靠可靠

## 核心特性

随着项目发展，我逐步加入了最想要的特性。从 0.2.x 到 0.3.x，架构经历了一次大幅升级——从"Agent 编排框架"（Leader + Sub）演进为**"面向 Agent 的动态计算运行时"**——Agents are not orchestrated. They are scheduled.

| 特性 | 说明 |
|------|------|
| **ARES Kernel（0.3.x）** | 三支柱架构：Task Fabric（持久任务意图 + DAG 依赖 + 租约 + 检查点）、Agent Fabric（一次性 Agent 生命周期：spawn/suspend/resume/retire/kill/recover）、Scheduler（capability-aware 调度 + work stealing + cooperative preemption）。**Agent 死亡 ≠ Task 死亡** |
| **Execution Quantum** | Agent 不一口气跑完任务——每个 quantum 结束 `yield()` 交回执行权，Scheduler 决定 continue/suspend/preempt/handoff。LLM Agent 无法在任意 instruction 上被打断，只在 quantum 边界切换 |
| **Dynamic DAG Workflows** | 执行流可以在运行时动态构建和修改，再也不用硬编码；MutableDAG 支持运行时增删替换节点，配合 GraphPatchExecutor 和 RecoveryReplaceNode 实现节点级故障自愈 |
| **Memory Distillation → Experience Distillation** | 0.3.x 将记忆蒸馏归位到进化管道：Trace（发生了什么）→ Experience（说明了什么）→ Memory（正式知识）。候选知识与正式知识分库存放，一条经验须 ≥2 条非失败轨迹支持才能转正 |
| **Agent IPC（0.3.x）** | Peer-mesh 消息总线替代旧 AHP：Send / Request / Reply / Delegate / Handoff / Subscribe 六原语。Agent 是同级认知进程（A ≡ B ≡ C），父子只有 spawn provenance，不构成权限层级 |
| **可插拔向量存储** | 支持 PostgreSQL pgvector、Qdrant 等，核心操作 <1µs，零分配热路径 |
| **MCP 协议集成** | 原生支持 Model Context Protocol，动态发现和调用外部工具 |
| **事件系统与飞行记录器** | 每一个 Agent 动作都变成不可变记录，支持状态恢复和审计追踪。0.3.x 事件类型升级为完整 Task 生命周期（Created/Ready/Acquired/Started/Yielded/Checkpointed/Preempted/Released/Completed/Failed/Expired/Stolen）|
| **SkillCatalog & Capability Fabric** | 框架原生技能发现、索引和加载能力 — MCP 服务器、Git 仓库、本地可执行文件和 HTTP 清单作为一等公民来源；支持经验学习相关性先验。五件套 catalog 工具（skill_search/load/activate/list/experience）实现 Level-0/1/2 渐进披露 |
| **会话租赁 → 通用 Lease（0.3.x）** | SessionLease 抽象为 TaskLease / ResourceLease / CapabilityLease。所有带所有权的操作携带 fencing token（epoch），防止"A 过期 → B acquire → A 迟到 Release"误杀 B |
| **操作日志 (Action Logs)** | 每个 Agent 操作记录为不可变审计轨迹 — 与事件存储配合支持回放和恢复 |
| **混沌工程 (Chaos Engineering)** | 14 种混沌动作随意注入（kill_leader、network_partition、tool_timeout 等），随机暗杀生产级 Agent，验证系统反脆弱性；支持生存模式（30 分钟高压随机故障）和场景编排（YAML 定义多步混沌实验）；三维加权评分（可用性 40%、恢复 30%、一致性 30%）配合 Welch's t-test 回归检测 |
| **候选发布闭环（0.3.0）** | 进化从"策略进化"升级为**分层发布门禁**：Candidate → 三层验证（静态 + 证据 + LLM 回归）→ Release 发布门禁（门3 再确认）→ SetStable → Promoted。**候选生成容易，上线难——发布这道门禁决定进化系统的安全性**。BatchScorer 把 LLM 请求合并以应对低 rpm 限流 |
| **遗传算法 (Autonomous Evolution)** | 0.3.x 中 GA 降级为可选高级功能，主力进化模式变为 Failure → Diagnosis → Patch → Verify。DreamCycle 和 Genome GA 仍保留但不作为主力 |

## 一个比较颠儿的功能：Agent 暗杀测试

我还做了一个比较颠儿的功能——**随意暗杀一个正在工作的 Agent，看看它是否能真的秽土转生**。

```
2026/06/14 19:46:29 INFO arena: killed agent id=agent-1
2026/06/14 19:46:29 INFO orchestrator: agent killed, resurrecting id=agent-1 name="Architecture Review"
2026/06/14 19:46:29 INFO orchestrator: agent started id=agent-6 name="Architecture Review"
2026/06/14 19:46:29 INFO orchestrator: resuming agent from step id=agent-6 resume_from=agent-1 start_step=4 total_steps=3
```

5 个 Agent 同时在跑，随机杀掉其中几个——Kernel Scheduler 自动复活并恢复进度，MCP 数据、对话上下文、执行步骤全部无缝衔接。0.3.x 之后这个能力不再只是 Beta——Task Fabric 的检查点恢复 + Agent Fabric 的生命周期管理 + lease 过期自动 requeue 组成了完整的 Runtime Recovery 链路。

## 最后

如果你在低谷期，希望这个故事能激励你。**要善待自己，持续输出，拥抱变化。**

---

## 0.3.x 更新说明

这篇文章最初写于 0.2.x 时代。项目已经演进到了 0.3.x，架构进行了大幅升级：

- **Leader/Sub 模式被 ARES Kernel 替代**——不再有中央编排者，Agent 是同级认知进程，由 Scheduler 调度
- **AHP 进化为 Agent IPC**——从 Leader 分发变成 peer-mesh 六原语（Send/Request/Reply/Delegate/Handoff/Subscribe）
- **记忆蒸馏归位为 Experience Distillation**——成为进化管道的一部分，不再是孤立模块
- **进化系统引入候选发布闭环**——三层验证 + Release 门禁，安全第一
- **引入 Execution Quantum 和 Cooperative Preemption**——Agent 在 quantum 边界 yield，不做 OS 硬抢占

核心哲学没变：**Agent 是一次性的，Task 是持久的。Agent 死亡 ≠ Task 死亡。**

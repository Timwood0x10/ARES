# GA / Memory / Tool Results 全面融合方案

## 目标

让 GA 真正融入项目主流程，在系统空闲时自动进化，尽量不依赖 LLM，或者只在少数高价值节点使用极少 token。

同时保留数据库层的插件化能力，PostgreSQL 只是参考实现，不是唯一后端。

## 现状

当前不只是三个系统分离，而是整条学习链路还没有闭环：

- GA 主要基于 heuristic 或 LLM 评分
- Memory 主要记录 success / failure 和 bandit 权重
- Tool Call Results 主要停留在执行层，没有被结构化回流到 GA
- Observability 记录了不少事实，但没有进入进化决策
- Agent runtime 产生了大量过程数据，但没有被统一汇总
- Workflow / scheduler 有触发能力，但没有形成稳定的学习周期
- Storage 层有多种实现，但没有被抽象成统一的进化后端

结果是：

- GA 看不到真实执行表现
- Memory 看不到策略层的全量上下文
- Tool Results 不能驱动选择和进化
- 运行时事件不能沉淀为经验
- 监控指标不能影响选择
- DB 后端切换会牵连核心逻辑

## 设计原则

1. 真实证据优先，GA 只相信执行结果和经验统计。
2. 低 token 优先，默认不让 LLM 参与高频闭环。
3. 自动运行优先，空闲时间自动 evolve。
4. 插件化优先，DB 和存储实现可替换。
5. 可解释优先，第二天给用户的是“我学到了什么”，不是黑箱分数。

## 推荐架构

```text
GA 生成候选策略
    ↓
注册到真实执行路径 / Agent 池 / Workflow
    ↓
Agent 在空闲窗口或真实任务中执行
    ↓
收集 Tool Call Results + 任务质量 + 耗时 + 错误率 + runtime metrics
    ↓
Raw Experience
    ↓
Normalizer
    ↓
Experience Store
    ↓
Evidence Aggregator
    ↓
MemoryAwareScorer / Fitness
    ↓
Selection 决定保留 / 淘汰 / 推广
    ↓
输出简洁进化报告
```

## 闲暇时间自动进化

这里不限定夜间，而是任何空闲窗口：

- 系统负载低
- 用户任务队列为空
- 距离上次 evolve 已超过冷却时间
- 没有高优先级阻塞任务

触发后执行：

1. 生成少量候选策略
2. 选择影子执行或低风险真实执行
3. 收集 Tool Call Results
4. 更新经验库
5. 用真实证据重新评分
6. 保留最优策略
7. 记录可读报告

## 全量数据输入面

GA 进化不应该只吃 Tool Results，它需要多个组件共同提供信号。

### 1. Agent runtime

来自 agent 执行过程的信号：

- 任务完成时长
- 重试次数
- 中断次数
- 工具调用深度
- tool chain 形态
- 失败阶段位置
- 回滚或恢复次数

这些数据适合进入 Experience 的过程特征层。

### 2. Tool system

工具层能提供最直接的事实：

- 每次工具调用的输入输出摘要
- 错误码
- 返回大小
- 是否重复调用
- 是否出现空结果
- 是否命中 fallback

这类数据应该进入 ToolCallResult 聚合器，再写入经验层。

### 3. Observability

监控和日志层是 GA 的高密度事实来源：

- `RecordToolCall`
- `RecordLLMCall`
- tracing span
- metrics counters
- latency histogram
- error rate
- throughput

建议把 observability 当成“低成本旁路信号”，用于补足经验库中的缺口，而不是替代真实执行结果。

### 4. Workflow / Scheduler

调度层决定 GA 什么时候跑：

- 空闲窗口触发
- 冷却期结束
- 达到样本阈值
- 达到异常阈值
- 收到特定事件后触发局部 evolve

Scheduler 不只是触发器，它也是节流器，防止 GA 在高压状态下乱跑。

### 5. Storage / Repository

存储层保存的是长期记忆：

- strategy 历史版本
- experience 历史记录
- execution events
- evaluation snapshots
- promotion / demotion 记录
- report 历史

存储层必须抽象，否则 GA 会和 PostgreSQL 绑定死。

### 6. Retrieval / Memory Distillation

retrieval 和 distillation 负责把长历史压成可用信号：

- 把相似经验找出来
- 把重复模式压成摘要
- 把过期经验清理掉
- 把高价值经验提升权重

这层是 GA 学习速度的关键，不是附件。

### 7. Leader / Aggregator

leader / result aggregator 可以提供任务级聚合结论：

- 成功任务的共性
- 失败任务的共性
- 对不同任务类型的表现差异
- 候选策略在真实流量下的稳定性

这类聚合信息非常适合进入 Experience 和 scorer。

## 组件协同方式

可以把整个系统看成四条流：

### 事实流

Tool Results, runtime metrics, traces, logs, task outcomes

### 经验流

Experience Store, distillation, retrieval, memory scoring

### 决策流

GA, scorer, selection, promotion, demotion

### 控制流

Scheduler, workflow engine, plugin bus, locks, cooldown

四条流要分开，但彼此有明确输入输出。

## Experience 到 Fitness 的分层

建议把原始信号拆成四层，不要让原始 Experience 直接进入 GA。

### 1. Raw Experience

原始输入来源：

- `strategy_id`
- `task_type`
- `success`
- `latency_ms`
- `retry_count`
- `error_rate`
- `tool_chain`
- `result_quality`
- `token_cost`
- `wall_time`
- `timestamp`

这一层可以来自 Tool Results、runtime、observability、task outcome、trace。

### 2. Normalized Experience

Normalizer 做的是：

- 字段统一
- 类型统一
- 单位统一
- 噪声过滤
- 缺失值填充
- 去重

这一层仍然是单条事实，只是格式稳定了。

### 3. Evidence

Evidence Aggregator 做的是聚合，不做原始清洗：

- 按 strategy / task_type 聚合
- 按时间窗口聚合
- 按 tool_chain 模式聚合
- 按成功率 / 延迟 / 错误率 / 成本聚合
- 形成稳定的统计证据

### 4. Fitness

`MemoryAwareScorer` 只消费 Evidence，不直接消费 Raw Experience。

它输出的是：

- 基础 fitness
- 经验修正项
- 最终 score
- 置信度

这样 GA 看到的是稳定、可解释、低噪声的证据，而不是原始日志流。

## MemoryAwareScorer 的定位

`MemoryAwareScorer` 应该保持为只读评分层：

- 基础分来自现有 scorer
- 经验修正来自 Evidence
- 不直接消费原始工具日志
- 不负责执行，只负责判断

建议修正维度：

- 成功率
- 平均耗时
- 工具链稳定性
- 错误密度
- 成本收益比
- 对任务类型的适配度

## LLM 的角色

LLM 不应成为主循环的一部分，只适合少量节点：

- 生成简短进化摘要
- 解释异常回退原因
- 生成给用户看的自然语言报告
- 在规则无法归因时做辅助判断

默认策略：

- 评分不用 LLM
- 归因不用 LLM
- 报告尽量模板化
- LLM 只补最后一层解释

## DB 插件化

数据库层需要预留接口，不绑定 PostgreSQL。

建议抽象接口：

- `StrategyStore`
- `ExperienceStore`
- `ExecutionEventStore`
- `ReportStore`
- `LockProvider`
- `MetricsSink`

后端可以是：

- PostgreSQL
- SQLite
- Redis
- 文件存储
- 用户自定义数据库

PostgreSQL 只作为参考适配器，不作为强制选择。

建议再往上一层抽象出 `EvolutionBackend`：

- `AppendExperience`
- `QueryExperience`
- `LoadStrategy`
- `SaveStrategy`
- `AcquireRunLock`
- `StoreReport`

这样不同数据库、文件系统、甚至外部服务都能接进来。

## 次日输出

系统第二天给用户的不是原始数据，而是压缩后的结论：

- 本轮学到了什么
- 哪些策略更强了
- 哪些策略被淘汰或降权了
- 哪些变化建议应用
- 哪些变化先观察

## 风险控制

- 不允许单次成功就大幅提权
- 不允许低置信度经验直接覆盖主策略
- 不允许 DB 细节泄漏到 GA 核心逻辑
- 不允许 LLM 参与高频内部循环
- 不允许未验证策略自动升级为默认策略

## 推荐落地顺序

1. 打通 `Execution -> Raw Experience`
2. 加 `Normalizer`
3. 把经验写入 `Experience Store`
4. 加 `Evidence Aggregator`
5. 让 `MemoryAwareScorer` 使用 Evidence
6. 接入 observability 和 runtime metrics 作为辅助证据
4. 把 GA 接入真实执行路径
7. 抽出 DB 插件接口
8. 加入空闲调度
9. 生成进化报告
10. 引入 distillation / retrieval，做长期记忆压缩
11. 接入 leader / aggregator，形成任务级统计

## 结论

这个方向的核心不是“让 GA 变聪明”，而是让它能在真实系统里持续、低成本、可审计地学习。

GA 负责尝试，Memory 负责沉淀，Tool Results / runtime / observability 负责提供事实，Scheduler 负责节流，Storage 负责长期保存，LLM 只在必要时做轻量总结。

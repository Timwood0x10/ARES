# 缺陷与边界记录 · 可丢弃 agent（disposable agent + 复活）

> 本文档**只记"会咬你"的坑与已知边界**，不重复"agent 可丢弃 / 任务持久 / lease+epoch"怎么工作。
> 前提共识：`agent = 可丢弃的执行体，任务 = 持久意图`；agent 挂了 → 换人从 checkpoint 续跑，或同 ID 原地复活（death snapshot）。
> 每条带源码锚点（v0.3.3 行号可能随 dev 分支漂移，定位以符号为准）。
> 性质标记：`缺陷`（建议修）/ `隐患`（规模/时间/并发下暴露）/ `取舍`（设计权衡，知边界即可）/ `已做`（有保护，确认非缺陷）。
> **路径说明**：本文所有 `recovery.go` 锚点指向 **`internal/aresrecovery/recovery.go`**（不是 `internal/fabric/agent/recovery.go`，该文件不存在）。
>
> **一句话总纲**："可丢弃"的核心（任务比 agent 活得久、lease/epoch 保唯一写者、换人/原地复活两条路）**是扎实的**；
> 真正的坑集中在**"带过去的脑子有多少"和"复活预算/快照的生命周期"**——
> 决定了一个死掉的 agent 到底能"复活成多像它自己"。

---

## A1 · "脑子"是可选且不透明的 → 可丢弃的隐形成本 〔缺陷 · 最核心〕

原地复活（`RestartAgent`，`recovery.go:287`）带过去的是 `CognitiveState`：

```
// internal/fabric/agent/agent.go:110-128
CognitiveState = {
    SchemaVersion, Context any, Observation, WorkingMemory, Decision, ToolState, Checkpoint
}
```

> 注：初稿把结构体简写成 `{ SchemaVersion, Context any, ToolState }`，实际还有
> `Observation` / `WorkingMemory` / `Decision` / `Checkpoint` 四个字段（`agent.go:110-128`）。

- **`Context` 是 opaque `any`**（`agent.go:116`；注释 "legacy opaque state carried in Context" 在 `context.go:85`，初稿写的 `:41` 是漂移锚点）——**存多少完全取决于 agent 有没有往 `Context` 塞东西**。
- peer 模式里，认知大量是**每量子从 L2 图 + 经验先验现组**的（planner 不存 CoT，见量子缺陷 Q3）。所以 `Context` 常常**很薄 / 甚至是零值**。
- **后果**：agent 可丢弃的前提是"任务进度持久在 fabric"，但**"复活成多像它自己"取决于 `Context`/`ToolState` 里真塞了多少**。塞得少 = replacement 几乎是"白盘复活"（拿到任务做到哪了，但拿不到它的推理思路）。
- **建议**：要么明确"复活到底要带什么"（把关键推理/工具中间态结构化进 `CognitiveState`，别留 opaque any），要么**在文档/运维层面承认"可丢弃 = 换人是有损的，原地复活才有脑"**，两条路径的"脑含量"必须可辨识。
- **与 Q3 的关系**：Q3 从"任务 checkpoint"侧讲"丢推理链"，A1 从"agent 复活"侧讲同一个根因（认知没被结构化持久化）。**修一处两边都受益。**

---

## A2 · 复活预算是"lifetime 累积"（非连续）→ 长期存活但偶发挂的 agent 会被永久拒活 〔隐患〕

`RestartAgent` 的 budget 语义（`recovery.go:42-43` 字段注 + `:289-290`）：

> "Lifetime-cumulative budget: **successful revivals do NOT reset the counter — only total deaths consume it**."

- **含义**：不是"连续挂 5 次"才耗尽，而是"**一生总共挂 5 次**"就永久 `recovery_exhausted`（`recovery.go:88`）。
- **坑**：一个**长期健康、只是偶尔**（跨好几天）崩 1~2 次的 agent，累计到 5 次后**就被永久拒活**（StopReason=`recovery_exhausted`），哪怕每次崩溃都无因果、且上次复活后跑得很稳。
- **没有"时间衰减 / 全健康后回补"**：计数器只增不归（成功复活也不回退，`recovery.go:289`），只能靠**换一个新 identity（respawn）才重置**。
- **建议**：加**时间窗**（如"近 N 小时的死亡数"而非 lifetime）或**长期健康后回补计数**；否则长寿命 agent 会在"偶发故障累计"下被误判为"坏 agent"永久踢出。

---

## A3 · 死亡快照上限 1024 + byCap 取"最近死亡" → 高 churn 下原地复活静默退化成"新 agent" 〔隐患〕

`snapshotStore`（`snapshot.go`）：

- `byID` 有界：超过 `maxDeathSnapshots = 1024`（`snapshot.go:57`）时**淘汰 `DiedAt` 最旧的**（`:69-76`，注释 "Evict the oldest DiedAt entries"）。注意：是**按死亡时间**淘汰，不是插入序 FIFO。
- `findByCapability` **偏好最近死亡的**那个（`snapshot.go:109-133`，判定 `:125` `snap.DiedAt.After(best.DiedAt)`；注释在 `:54` "prefers the most recent"）。

- **后果**：高故障率 / chaos 风暴 / 大量并发 agent 死亡时，**早期死亡 agent 的快照被挤掉** → 它们后续想原地复活时**找不到 snapshot** → 只能退回"requeue 换新 agent"（= 有损，见 A1）。
- **坑**：这是**静默的**——快照没了不会报错，只是"从原地复活"悄悄降级成"换人"。排障时容易误以为"复活机制失效"，其实是**快照被 1024 上限挤掉了**。
- **建议**：把"因快照被 prune 而退化为换人"打一条 log/指标；或按 agent 重要度/能力稀缺度做**非 FIFO** 淘汰（能力越稀缺的 agent 快照越不该被挤掉）。

---

## A4 · 认知快照 schema 版本 mismatch → 滚动升级挡住复活 〔隐患〕

`DecodeCognitiveState`（`context.go`）：

- 版本 **大于** 当前 `CognitiveStateSchemaVersion` → 直接 `ErrCognitiveStateSchemaVersion`（"future version the current code cannot interpret"，`context.go:46`）。
- 未来版本的状态**拒读**，要么迁移要么拒 agent recovery。

- **坑**：**滚动升级**时，新节点写的 death snapshot（vN+1）会被旧节点（vN）拒读 → 该 agent **无法被旧节点复活**，只能等新节点或换人。跨版本部署窗口内，"原地复活"对**新写的快照不可用**。
- **建议**：快照存储带上"写入版本"，滚动升级时要么做**向后兼容读**（旧节点读新快照的公共子集），要么**在升级时清空/迁移待复活快照**，别让它变成"升级后一段时间不能原地复活"的隐雷。

---

## A5 · 双状态码只对齐了"能不能调度" 〔隐患 · 已知〕

`AgentState`（fabric：IDLE/PAUSED/RETIRED，注意 `StatePaused` 的**线值是 `"SUSPENDED"`**，`agent.go:33`）与 `models.AgentStatus`（subAgent：starting/ready/**busy**/stopping/offline，`internal/core/models/types.go:62-70`）是两套。`busy` 桥（`agent.go:81-87`）只对齐了"**能否被调度**"这一个点（`IsIdle = StateIdle && !busy`，实现在 `fabric.go:187`）。

- **没对齐的**：`stopping`（正在优雅停）/ `offline`（已下线）/ `starting`（还没就绪）这些 subAgent 态**没有跨到 fabric 层**——fabric 层只知道 IDLE/PAUSED/RETIRED。
- **坑**：一个 subAgent 正在 `stopping`（要下线了）但 fabric 层还显示 IDLE → 调度器可能仍把任务派给它 → 派到"即将死的 agent"上，靠 lease 过期兜底才回收。**这是"多一个不协调的窗口"**。
- **建议**（可选）：把 `stopping/offline` 也桥到 fabric（stopping → 不再进候选）。但至少**文档要写清"调度一致性只在 IDLE+!busy 保证，stopping 是灰区"**。

---

## A6 · "复活"与"换人"是两条耐久度不同的路，且选哪条常取决于时序 〔取舍 · 需可观测〕

| 路径 | 触发 | 带过去的 | 预算 |
|---|---|---|---|
| **原地复活**（`RestartAgent`） | agent panic/chaos kill + 有 death snapshot | `CognitiveState`（脑子，可能薄）+ 同 ID | 消耗 lifetime budget |
| **换人**（`RequeueExpiredLeases` + **新 replacement executor**；`RecoverTaskCheckpoint` 仅 TEST/CHAOS） | lease 过期 / 无 snapshot | 仅 `Task.Checkpoint`（任务做到哪了） | 不消耗 agent 复活预算 |

- **坑**：同一个"agent 没了"，**最终是原地复活还是换人，取决于"有没有 snapshot、snapshot 有没有被 A3 挤掉、recovery loop 有没有 wire 到"**。两条路"脑含量"不同（A1）、预算不同（A2），但**对上层常常是静默的**——你不知道任务是被"复活的原 agent"做完的，还是被"全新 agent"做完的。
- **建议**：给"最终由哪种路径续跑"打标签/指标（`StopReason` 已有 `crashed` vs 无，但"实际续跑走 snapshot 还是 fresh"没单独记），让"续跑耐久度"可观测。

---

## 已做（确认非缺陷，列出以防误判）

| 保护 | 现状 | 锚点 |
|---|---|---|
| 唯一写者（lease + epoch fencing） | 旧 holder 的写全被 `ErrEpochMismatch` 拒，换人不会和僵尸 agent 打架 | `ErrEpochMismatch` 定义在 `internal/fabric/task/fabric.go:22`，拒写在 `ownerLocked`（`fabric.go:367-377`；`fabric_lifecycle.go` 只是调用方） |
| 换人保 checkpoint（resume not restart） | requeue 保留 `Checkpoint`，不回 READY 从头跑 | `recovery.go` RecoverTaskCheckpoint |
| 复活预算防风暴 | 原子预留（reserve 再 sleep）+ 指数 backoff（1s/2s/4s…30s cap） | `recovery.go` restarts + Backoff |
| 成功复活不回退计数（防循环） | "broken agent 不能无限重启循环" | `recovery.go:289` |
| 原地复活 provenance 连续 | 同 ID，审计不断档 | `RestartAgent` |

---

## 性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| **A1 "脑子"opaque/可选 → 可丢弃有损** | 缺陷 | **高**（与 Q3 同根，结构化 CognitiveState） |
| A2 复活预算 lifetime 累积 | 隐患 | 中（加时间窗/健康回补） |
| A3 快照 1024 上限静默退化 | 隐患 | 中（prune 时打日志/指标） |
| A4 快照 schema 版本挡复活 | 隐患 | 中（滚动升级做兼容读） |
| A5 双状态码只对齐调度点 | 隐患 | 低（stopping 灰区，至少文档化） |
| A6 复活 vs 换人静默 | 取舍 | 低（加可观测标签） |
| 唯一写者/防风暴/provenance | ✅ 已做 | 无需动 |

**可丢弃 agent 这块"创新可行性强"，核心（唯一写者 + 任务持久 + 两条复活路）是对的。**
真正要补的是 A1（**认知的结构化**，决定"可丢弃"到底是"可换脑"还是"可白盘"）——它和量子化的 Q3 是同一根因，**值得作为一类"认知持久化"专项一起修**。

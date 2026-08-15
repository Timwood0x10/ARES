# ares 架构深度解析（二十七）：上下文管理 — token 预算的三道防线

> 说明：本文基于实际代码（`internal/ares_memory/context/cleaner.go`、`internal/ares_memory/manager_impl.go`、`internal/knowledge/skills`），是 docs 系列中上下文管理层的专门篇。

## 一、为什么上下文管理是 Agent 的命门

LLM 的 context window 是硬约束：Agent 每轮对话都会累积历史，工具调用结果（尤其 `tool_result`）动辄几百上千 token，几十轮下来轻松击穿窗口。

ARES 用**三道防线**控制 token 预算：

| 防线 | 机制 | 位置 | 解决什么 |
|------|------|------|----------|
| ① turn 分组 | `TurnID` 关联会话消息 | `internal/ares_memory/context` | 让"一轮对话"成为可整体处理的单元 |
| ② 差分压缩 | `ContextCleaner` 角色感知压缩 | `internal/ares_memory/context/cleaner.go` | 工具噪声、重复内容的 token 削减 |
| ③ 渐进披露 | skills Level-0 常驻 + body 按需 | `internal/knowledge/skills` + memoryManager | 100 个技能 ≠ 100 份完整指令 |

## 二、ContextCleaner：角色感知的差分压缩

`internal/ares_memory/context/cleaner.go` 的 `ContextCleaner` 是第二道防线的核心。它的设计洞察是：**不同类型的消息对 token 的价值密度不同**，压缩策略必须按角色差异化：

```go
// ContextCleaner intelligently cleans conversation context before LLM calls.
// It applies differential compression based on message role:
//   - tool_call / tool_result → aggressively compressed to first sentence
//   - assistant with ToolCalls → treated as tool-like content
//   - pure assistant reasoning → code blocks compressed, content truncated
//   - user / system → straightforward truncation
type ContextCleaner struct {
    mu          sync.Mutex
    stats       CleanerStats          // 工具调用数 + 节省字节统计
    codePattern *regexp.Regexp        // ```...``` 代码块识别
}
```

**四类角色四种策略**：

| 消息角色 | 压缩策略 | 理由 |
|----------|----------|------|
| `tool_call` / `tool_result` | **激进压到首句** | 工具往返是最大噪声源：结果里 90% 是分页/重复，首句概括足矣 |
| `assistant` + ToolCalls | 按工具类内容处理 | 带工具调用的推理通常不需要完整保留 |
| 纯 `assistant` 推理 | **代码块压缩 + 内容截断** | 推理步骤可截断，代码块用正则识别后压缩 |
| `user` / `system` | 直接截断 | 用户输入是语义锚点，策略保守 |

关键实现细节：
- **保留全部字段**：`Clean` 返回新切片时保留 `Time`、`TurnID` 等元数据——压缩的是内容，不是结构
- **原切片不可变**：`Returns a new slice with compressed content; original slice is not modified`——零副作用，可安全重试
- **统计可观测**：`CleanerStats`（工具调用数 + 节省字节数）内部跟踪，压缩效果可度量

## 三、turn 分组：让"一轮对话"成为整体

上下文压缩的粒度不是单条消息，而是 **turn（一轮对话）**。`internal/ares_memory/context` 层用 `TurnID` 把一次交互（用户输入 + Agent 的思考 + 工具往返 + 最终回复）关联成组：

- **结构化消息**：`AddStructuredMessage` 带 `TurnID`/`ToolCallID`/`ToolCalls` 元数据写入会话，保留完整 turn 结构（供 turn-aware cleaning 使用）
- **清理以 turn 为单位**：压缩/截断按 turn 边界处理，不会把一轮对话的中间态切得支离破碎
- **蒸馏复用**：`manager_impl.go` 的 `buildCleanedDistillationMessages` 在蒸馏前也走清理——同一套 turn 分组语义贯穿"清理"与"蒸馏"两个消费方

## 四、渐进披露：100 个技能 ≠ 100 份完整指令

第三道防线在知识层：`internal/knowledge/skills.Registry` 只让 **name + 一句话描述**常驻上下文（Level-0），完整 SKILL.md 指令体（Level-1）按需加载。

memoryManager 的挂载（`manager_impl.go`）：

```go
// SetSkillsRegistry attaches a skills registry for progressive disclosure.
// When set, BuildContext prepends a resident "Available skills" block listing
// each skill's name and description only; full skill details are fetched on
// demand by ID via the registry.
func (m *memoryManager) SetSkillsRegistry(reg *skills.Registry) {
    m.skillsRegistry = reg
}
```

- **常驻块**：`BuildContext` 顶部注入 "Available skills"（每个技能 ~100 token 的 name+描述）
- **按需取**：`LoadDetail` 返回完整 body，只在 Agent 明确需要时才取
- **Capability Fabric 升级**（0.3.0）：`internal/ares_skills` 的 `SeedRegistry` 把 skill 目录索引灌进同一 registry，且 `skill_load` 工具负责按需取 body——三阶段渐进披露（metadata → SKILL.md → resources）在 0.3.0 完整闭环

## 五、生产接线

`ContextCleaner` 在两条生产路径注入：

```go
// manager_impl.go
ctxCleaner: memctx.NewContextCleaner()   // 经典 memoryManager

// production_manager.go
ctxCleaner: memctx.NewContextCleaner()   // production 变体
```

`BuildContext` 组装时：常驻 skills 块（若已 seed）→ 会话历史经 `ContextCleaner.Clean` 压缩 → 拼接成最终 prompt。压缩在 **LLM 调用前**执行（`before LLM calls`），保证每次请求都吃最小的上下文。

## 六、总结

| 防线 | 机制 | 度量 |
|------|------|------|
| turn 分组 | `TurnID` 结构化消息 | 一轮对话一个整体 |
| 差分压缩 | role-aware `ContextCleaner` | `CleanerStats` 节省字节 |
| 渐进披露 | skills Level-0 常驻 | 100 skills ≈ 100 × ~100 token |

**设计主线：上下文不是"塞进窗口"而是"预算管理"。** 三条防线各管一段——分组管结构、压缩管噪声、披露管知识——合起来让 Agent 在有限窗口里装下"历史 + 工具往返 + 技能知识"三类内容。这也是 Capability Fabric"不把所有 Skill 内容塞给 LLM"原则在上下文层的呼应。

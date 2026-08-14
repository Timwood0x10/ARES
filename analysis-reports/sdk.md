# 模块分析报告：`sdk`（SDK 层）

> 分析范围：`/Users/scc/go/src/goagent/sdk/`（23+ 个 Go 文件）

---

## BUG（高置信度）

### 1. `sdk.go` `New()` 错误路径资源泄漏
- **位置**：`sdk.go` 699-748、752-755、768-771、785-788 行
- **说明**：一旦 `llmSvc := llm.NewService(...)` 成功（699 行）且 `wireMCPClients` 返回 clients（745 行），后续任何错误返回（`wireMCPClients` err 746、`wireKnowledge` err 753、`registerAKFTools` err 769、`buildSDKEvidenceStore` err 786、`wireMemory` err 733）都 `return nil` **不调用 `llmSvc.Close()`** 也不关闭已连接的 MCP clients。另外 defer `bootstrapCancel()` 在错误路径会触发（好），但 `bootstrapComp.WaitBackground()` 从不调用，Bootstrap 后台 goroutine 被取消但未排空。构造失败时这些资源泄漏。
- **状态**：✅ 已核实修复（2026-08-14）——`New()` 现用 `defer` 清理错误路径：`bootstrapCancel()` + `bootstrapComp.WaitBackground()`（排空后台 goroutine）+ `llmSvc.Close()` + 关闭全部 `mcpClients`（由 `bootstrapCancelTaken` 标志区分成功/失败路径）；报告条目过时。

### 2. `sdk.go` `wireMCPClients` MCP 客户端泄漏
- **位置**：`sdk.go` 650-677 行
- **说明**：若 `client.ListTools` 失败（662 行），已连接的 `client` 未关闭就返回错误；若循环中后续 MCP 连接失败，先前追加到 `mcpClients` 的客户端也不关闭。部分失败时连接/池泄漏。
- **状态**：✅ 已核实修复（2026-08-14）——`wireMCPClients` 现用 `closeAll` 闭包：连接失败或 `ListTools` 失败时关闭全部已连接客户端（含当前失败的 client）；报告条目过时。

### 3. `team.go` `Team.leader`/`runtime` 未校验 nil
- **位置**：`team.go` 75-83、127、388-410 行
- **说明**：`NewTeam` 存储 leader/members 无 nil 检查。`Team.Run` 调 `t.leader.Run(...)`（经 `leaderDiscover`，172 行），`t.synthesize` 解引用 `t.leader.instruction`（405 行），`t.runtime` 也无检查即解引用。nil leader 会 panic。公开 API 应 guard nil leader 或文档化前置条件。
- **状态**：✅ 已核实修复（2026-08-14）——`Team.Run` 开头已 guard：`if t.runtime == nil`（"has no runtime"）与 `if t.leader == nil`（"has no leader agent"）均返回错误而非 panic；报告条目过时。

### 4. `cleaning.go` 哑 `time` import
- **位置**：`cleaning.go` 102 行
- **说明**：`var _ = time.Time{}` 仅为保留未使用的 `time` import。死代码/代码异味，可直接删 import。
- **状态**：✅ 已修复（2026-08-14）——删除 `"time"` import 与 `var _ = time.Time{}` 哑引用，build/vet/test 通过。

### 5. `team.go` 字节级截断会切断 UTF-8 字符
- **位置**：`team.go` 459-464 行
- **说明**：`truncate` 用 `len(s)`（字节），对非 ASCII 输入可能切断多字节 UTF-8 rune 中段，产生非法字符串。

### 6. `evolve.go` `applyToolSelector` "manual" 静默回退到全量工具
- **位置**：`evolve.go` 206-216 行
- **说明**：若策略选 `manual` 但 agent 无 `search`/`read` 工具，返回整个 `toolList` 不变（216 行）。这静默使 manual 选择器对缺这些工具的 agent 失效，而非返回空/受限集合。

### 7. `sdk.go` `memStrategyStore.GetHistory` 的 `n==0` 语义
- **位置**：`sdk.go` 245-255 行
- **说明**：`n==0` 时返回 `s.history[:0]`（空切片）而非错误或完整历史。调用方传 0 表示"不限"会得到空列表。契约歧义，非明确 bug。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `sdk.go` 699-788 | New() 错误路径资源泄漏（llmSvc、MCP clients、bootstrap 不排空） |
| **高** | `sdk.go` 650-677 | wireMCPClients 部分失败时客户端泄漏 |
| **高** | `team.go` 75-127 | leader/runtime 无 nil guard |
| 中 | `evolve.go` 206 | manual 选择器静默回退全量 |
| 低 | `cleaning.go` 102 | 哑 time import |
| 低 | `team.go` 459 | 字节截断切坏 UTF-8 |

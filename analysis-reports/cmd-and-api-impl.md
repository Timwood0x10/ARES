# 模块分析报告：`cmd/ares`、`internal/api_impl`、`api/client`、`api/handler`

> 分析范围：`cmd/ares/`（27 个文件）、`internal/api_impl/`（13 个文件）、`api/client/`（13 个文件）、`api/handler/`（16 个文件）

---

## cmd/ares

### 1. `start.go` `svc` 变量数据竞争（BUG）
- **位置**：`start.go` 53-62 行
- **说明**：信号处理 goroutine（53 行）读 `svc`（56 行 `if svc != nil`），而 `svc` 在主 goroutine 62 行赋值（`svc, err = apiimpl.StartService(...)`）。若 SIGINT/SIGTERM 在 goroutine 启动与赋值之间到达，或并发调度，则是未同步的读写数据竞争。若信号早到，`svc` 被读为 nil，`Stop` 被静默跳过（只跑 `cancel()`）。`serve.go` 用 `atomic.Pointer` 解决了同样问题，此文件没有。

### 2. `actions.go` nil-guard 不一致
- **位置**：`actions.go` 202-216 vs 222-229 行
- **说明**：`handleCallTool` guard `if h.tools != nil`，但 `handleListTools`（224 行 `names := h.tools.List()`）不 guard，且经 `GET /api/tools`（100 行）可达。若调用方构造 `actionHandler` 无 registry 会 panic。当前只在 serve.go 以非 nil registry 构造，是潜在防御不一致。

### 3. `actions.go` 所有 Execute 错误都报 "tool not found"
- **位置**：`actions.go` 203-210 行
- **说明**：`h.tools.Execute` 失败时一律响应 `"tool not found: "+req.Name`，不论真实原因（执行/运行失败）。只有真正 "not found" 才该这样标。

### 4. `arena.go` 吞 JSON 反序列化错误返回成功
- **位置**：`arena.go` 79-83 行
- **说明**：`/arena/scenario/run` 响应体无法反序列化为 `arena.ScenarioReport` 时，打印原始 body 后 `return nil`（无错误）。命令以退出码 0 成功退出，即便报告无法解析。

### 5. `flight.go` `buildGraph` 父链接用 `evts[0].ID` 而非计算节点 ID
- **位置**：`flight.go` 307-313、315-324 行
- **说明**：非根事件 `parent_id` 为空时，`parentID = evts[0].ID`。但若 `evts[0].ID == ""`，根节点实际节点 ID 是合成的 `"evt-<version>"`（290-292 行），赋值的 parent `""` 不匹配任何节点——产生悬空/空父引用。

### 6. `dev.go` `parseRunArgs` 不处理 `--config=<值>` / `-c=<值>`
- **位置**：`dev.go` 322-343 行
- **说明**：只剥离空格分隔形式（`-c`、`--config` + 独立值）。用户传 `--config=my.yaml` 或 `-c=my.yaml` 时该 token 泄漏进 prompt 参数。

---

## internal/api_impl

### 7. `adapters.go` `resurrectionTotal` 字段死且计数错误
- **位置**：`adapters.go` 159、254 行
- **说明**：`resurrectionTotal` 在 `Execute` 内对**每个成功动作**自增（`if success { a.resurrectionTotal++ }`），与动作类型无关——"复活"计数语义错误（只应在 kill/recover 动作时增加）。且它从不被读取（`Stats()` 264、`History()` 274、`ResilienceScore()` 292 都不含它）。字段实际是死的且会计误导。

---

## api/client

### 8. `client.go` `Runtime()` 丢弃第二参数
- **位置**：`client.go` 144-150 行
- **说明**：`func (c *Client) Runtime(config *runtimeSvc.Config, _ interface{})` 忽略第二参数，每次调用都构造新的 `runtimeSvc.NewService`，无视任何客户端状态。`_` 参数是死的，函数忽略已有客户端配置（除 nil 默认）。

---

## api/handler

### 9. `runtime.go` `StartAgentRequest` 类型未用
- **位置**：`runtime.go` 28-31 行
- **说明**：`StartAgentRequest` 声明 `AgentID` 字段，但 `HandleStart`（34-46）从不解码 body 或读取它。类型是死代码。

---

## 已确认无问题的部分

- `api/agent/agent.go`：纯类型别名再导出 + 编译期接口检查，无问题。
- `api/handler/` 多数文件：nil-service guard 齐全、context 处理正确、SSE flush/顺序正确。
- `parsePagination`（memory.go）正确（memory repo 在 `Offset <= 0` 时从 `Page` 派生 offset）。
- `cmd/ares/*` 其余文件、`api/client/*` 其余、`internal/api_impl/agent/*` 等无额外确认问题。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `cmd/ares/start.go` 53-62 | `svc` 数据竞争（应学 serve.go 用 atomic.Pointer） |
| 中 | `api_impl/adapters.go` 159/254 | resurrectionTotal 死字段 + 计数错误 |
| 中 | `cmd/ares/actions.go` 222 | ListTools 无 nil guard |
| 中 | `cmd/ares/actions.go` 203 | 所有 Execute 错误标 "tool not found" |
| 中 | `cmd/ares/arena.go` 79 | 吞解析错误返回成功 |
| 中 | `cmd/ares/flight.go` 307 | buildGraph 悬空父引用 |
| 低 | `api/handler/runtime.go` 28 | StartAgentRequest 死类型 |
| 低 | `api/client/client.go` 144 | Runtime() 丢弃参数 |

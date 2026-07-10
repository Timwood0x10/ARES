# GoAgent / ARES 深层次 Code Review — 潜在 Bug 汇总

审查范围：`api/`（core / handler / router / service / bootstrap / discovery / client / mcp / tools / memory / agents / flight / evolution）与 `internal/`（ares_runtime / ares_events / plugins/resurrection / ares_arena / ares_evolution / agents 等）的核心实现文件。

分级标准：
- **Critical** — 会 panic / 数据损坏 / 安全漏洞 / 接口约定被破坏
- **High** — 资源泄漏 / 逻辑错误 / 行为与注释或接口不一致 / 并发阻塞
- **Medium** — 死代码 / 命名歧义 / 鲁棒性不足 / 鲠棒性差

状态标记：
- ✅ = 已修复（`go vet ./...` 通过，`go test -race` 通过，追加了测试覆盖）
- ⬜ = 未修复（skip、设计深度问题、或需更大范围重构）

---

## Critical（5）

### C1 ✅ `api/tools/builtin.go:574` — 路径穿越（symlink 未解析）

`validatePath` 用 `filepath.Abs(filepath.Clean(path))` 计算绝对路径后再 `filepath.Rel(absDir, absPath)` 判断是否在 allowedDir 内。`filepath.Clean` 不解析符号链接，攻击者可在 allowedDir 内放置指向外部的 symlink，`Rel` 判定为 contained 后逃出沙箱。

修复：先 `filepath.EvalSymlinks` 解析真实路径再 `Rel`。追加了 `TestFileTool_ValidatePathBlocksSymlinkTraversal` 测试。

### C2 ✅ `api/bootstrap/bootstrap.go:122,127` — 必需依赖传 nil，启用即 panic

```go
if cfg.Dashboard != nil && cfg.Dashboard.Enabled {
    dash = dashsvc.New(nil, nil)  // MCP/LLM executor 都传 nil
}
if cfg.Flight != nil {
    flightRec = flightsvc.New(nil)  // eventStore 传 nil
}
```

Dashboard 内部需要 MCPExecutor/LLMExecutor 才能工作，FlightRecorder 需要 EventStore 才能记录。启用这两个配置后下游调用会 panic 或永远失败。应改为从 `comp` 拿真实依赖注入，或在没有依赖时返回 error 而非构造。

### C3 ✅ `internal/ares_events/memory_store.go:67-79` — `expectedVersion = -1` 触发 version overflow

接口注释（`api/core/events.go:14`）写 `expectedVersion -1 for no check`，但 MemoryEventStore 的实现：

```go
if expectedVersion > 0 && currentVersion != expectedVersion { return ErrVersionConflict }
startVersion := expectedVersion
if startVersion == 0 { startVersion = currentVersion }
// ...
event.Version = startVersion + int64(i+1)
if event.Version <= 0 { return fmt.Errorf("version overflow: computed %d", event.Version) }
```

传 `-1` 会走 `startVersion = -1`，首事件 `Version = 0`，命中 `event.Version <= 0` 返回 "version overflow"。调用方按接口约定传 -1 表示"不检查"会失败，破坏所有依赖该恢复流程。

修复：简化逻辑为 `startVersion = currentVersion`（无论 -1 还是 0 都 auto-detect）。追加了 `TestMemoryEventStore_AppendWithNegativeOne` 测试。

### C4 ✅ `api/evolution/evolution.go:192-194,230-232` — 公开 API 返回 nil / stub 假结果

```go
func NewMutator(model string, cfg MutationConfig) Mutator {
    return nil // returns nil — caller configures internal mutator directly
}

func (p *promoterAdapter) Evaluate(ctx context.Context, strategyID string, successRate, confidence float64) (string, error) {
    return "", nil  // 永远返回空字符串，未调用 inner.Evaluate
}

func (p *promoterAdapter) Demote(ctx context.Context, strategyID string) error {
    return nil  // 同样是 stub
}
```

外部用户调用 `NewMutator` 拿到 nil 后 `Mutate()` panic；`Evaluate` 返回 `("", nil)` 被误判为"评估成功且结果为空"，下游逻辑用空字符串做 strategy ID 会写入空 ID 的 lineage。

修复：要么返回明确的 `ErrNotImplemented`，要么真正委托给 inner。

### C5 ✅ `api/service/agent/service.go:48,126-140` — 创建/列表接口丢字段、永远空（ListAgents 已通过 internal 层真实实现，CLI 命令已添加）

两处：

1. `CreateAgent` 只传 `config.ID` 给 inner，丢弃 `config.Name / Type / Config`：

```go
agent, err := s.inner.CreateAgent(ctx, config.ID)  // 只传 ID
return &core.Agent{ ID: agent.ID, SessionID: agent.SessionID, ... }  // 没 Name/Type
```

2. `ListAgents` 直接返回 `nil` + 空分页：

```go
func (s *Service) ListAgents(...) ([]*core.Agent, *core.PaginationResponse, error) {
    _ = filter
    var result []*core.Agent
    _ = result
    return nil, &core.PaginationResponse{Total: 0, Page: 1, PageSize: 0, ...}, nil
}
```

用户通过 HTTP `GET /api/v1/agents` 永远拿到空列表；通过 `POST /api/v1/agents` 创建的 Name/Type 不生效。

修复：CreateAgent 把 Name/Type/Config 也传给 inner（需要扩展 inner 接口），ListAgents 调用 inner 的 list 方法或显式返回 `ErrNotImplemented`。

---

## High（9）

### H1 ✅ `api/service/workflow/service.go:270-277,288-302` — SSE events channel 写入未防阻塞，goroutine 泄漏

`ExecuteStream` 失败路径和成功路径直接 `events <- core.WorkflowEvent{...}`，没有 `select`+`ctx.Done()` 防阻塞。channel capacity 64，客户端断开后无人读，写阻塞，goroutine 永久泄漏。

```go
// 失败路径（line 270）— 未防阻塞
events <- core.WorkflowEvent{
    Type: core.WorkflowEventFailed, ...
}

// 成功路径的 step completion（line 288）和 completed（line 302）— 同样未防阻塞
events <- core.WorkflowEvent{ Type: evType, ... }
```

讽刺的是 line 238-250 的 step-started forwarding 用了正确的 `select`+`gctx.Done()` 模式，前后不一致。

修复：所有 `events <-` 改为：

```go
select {
case events <- core.WorkflowEvent{...}:
case <-gctx.Done():
    return
}
```

### H2 ✅ `api/handler/stream.go:141-145` — 错误响应先于 SSE headers 写入

`HandleStream` 在 `processor.ProcessStream` 出错时调 `h.sendSSE(...)` 写 SSE 格式响应，但此时 `Content-Type: text/event-stream` 等 SSE headers 尚未设置（设置在 line 122-125，错误处理在 line 141 之前）。

客户端收到的是非 SSE 格式的错误响应，SSE 解析器无法识别。

修复：错误处理分支要么先设 SSE headers 再 sendSSE，要么直接用 `http.Error` 写 plain text 错误。

### H3 ✅ `api/handler/stream.go:130` — `http.Error` 在 SSE headers 设置后调用

`flusher` 检查失败时调 `http.Error`，但此时 SSE headers 已设置（line 122-125）。`http.Error` 会尝试覆盖 `Content-Type` 并写 plain text body，但 `WriteHeader` 还没调，header 被覆盖但状态码逻辑混乱。

修复：把 `flusher` 检查移到 SSE headers 设置之前。

### H4 ✅ `api/bootstrap/bootstrap.go:248,266` — Snapshot 错误被吞，下游基于零 snapshot diff

```go
snap, _ := gm.Snapshot(ctx)        // 错误被吞
snapshots[name] = diff.SnapshotPair{Old: snap}
// ...
newSnap, _ := best.Snapshot(ctx)   // 错误被吞
pair.New = newSnap
patches, err := c.DiffReg.DiffAll(ctx, ...)
```

`Snapshot` 失败时 snap 为零值，下游 `DiffAll` 基于零 snapshot diff 会产生假阳性 patches，提交无效 patches 给 Coordinator。

修复：检查错误，失败时跳过该 genome 并记录到 failures。

### H5 ✅ `api/mcp/stdio.go:80-99` — goroutine 泄漏：超时后无人读 `ch`

```go
ch := make(chan result, 1)
go func() {
    if tr.stdout.Scan() {
        var resp jsonrpcResponse
        if err := json.Unmarshal(tr.stdout.Bytes(), &resp); err != nil {
            ch <- result{nil, err}
            return
        }
        ch <- result{&resp, nil}
    } else {
        ch <- result{nil, fmt.Errorf("connection closed")}
    }
}()

select {
case r := <-ch:
    return r.resp, r.err
case <-time.After(30 * time.Second):
    return nil, fmt.Errorf("timeout waiting for response")
}
```

超时分支返回后，goroutine 还在阻塞 `stdout.Scan()`，channel cap 1 已被填满或没人读，goroutine 永久泄漏。每个 stdio MCP 调用超时一次泄漏一个 goroutine + 可能僵死的 cmd 进程。

修复：超时分支显式关闭 stdin 触发 scan 返回，或用 context 取消 + 把 scan goroutine 改为持 ctx 的 reader。

### H6 ✅ `internal/ares_events/pg_store.go:39-91` — 注释和实现矛盾

```go
// Append persists events to the given stream with optimistic concurrency control.
// expectedVersion semantics:
//   - 0: stream must be empty or not yet created.        ← 注释说
//   - positive: must match the stream's current max version.
//
// Returns ErrVersionConflict on mismatch.
func (s *PostgresEventStore) Append(...) error {
    // ...
    // Optimistic concurrency check.
    // expectedVersion == 0: auto-detect, append after current version (no conflict).  ← 代码说
    // expectedVersion > 0: must match current version, otherwise ErrVersionConflict.
    if expectedVersion > 0 && currentVersion != expectedVersion {
        return ErrVersionConflict
    }
    nextVersion := currentVersion + 1
    // ...
}
```

注释说 `0: stream must be empty`（严），代码实际 `0: auto-detect, append after current`（宽）。调用方按注释传 0 期望"仅空流可追加"，实际会追加到非空流，破坏 OCC 语义，并发场景下可能丢事件版本号一致性。

修复：统一注释和代码。建议保留代码的 auto-detect 语义（更实用），更新注释。

### H7 ✅ `api/bootstrap/bootstrap.go:167` — Stop 用 `context.Background()` 无超时

```go
if a.Memory != nil {
    if err := a.Memory.Stop(context.Background()); err != nil { ... }
}
if a.MCP != nil {
    if err := a.MCP.Stop(context.Background()); err != nil { ... }
}
```

Stop 用硬背景 context，无超时。下游 hang 则整个 Stop 永久阻塞，进程无法退出。

修复：用 `context.WithTimeout(context.Background(), 30*time.Second)`。

### H8 ✅ `api/service/knowledge/service.go:117,160` — Reserved 可能为负

```go
budget.ForGraph = budget.MaxTokens * 60 / 100  // 当 MaxTokens=100, ForGraph=200 时被跳过
budget.Reserved = budget.MaxTokens - budget.ForGraph
```

用户传 `MaxTokens=100, ForGraph=200` 时，`ForGraph` 保持 200（不满足 `<=0`），`Reserved = 100 - 200 = -100`，下游用 Reserved 做 token 预算会出错或 panic。

同样的代码块在 line 117 和 line 160 重复出现。

修复：`Reserved` 钳到 `>= 0`，或对 `ForGraph > MaxTokens` 显式报错。

### H9 ✅ `internal/ares_evolution/genome/population.go:525-549` — 持锁调用外部 scorer，阻塞整个 population

```go
func (p *Population) ScoreAgents(scorer func(*mutation.Strategy) float64) {
    p.mu.Lock()
    defer p.mu.Unlock()
    for i, agent := range p.Agents {
        func() {
            defer func() { if r := recover(); r != nil { agent.Score = ScoreUnevaluated } }()
            agent.Score = scorer(agent)  // 外部函数，可能是 LLM 调用，30s 超时
        }()
    }
    p.updateBestEverLocked()
}
```

持锁状态下逐个调外部 scorer。scorer 阻塞（LLM、HTTP）时整个 population 锁住，`Snapshot / EvolveOnIdle / ScoreAgents` 并发调用全排队。panic 已 recover 但阻塞未防。

修复：先 RLock 拿 agents 副本，放锁后并行评分，再 Lock 写回 score。

---

## Medium（8）

### M1 ✅ `api/router/router.go:36-41` — 死代码且签名不匹配

```go
type AgentProcessorFunc func(ctx any, input any) (<-chan base.AgentEvent, error)
```

定义但从未使用，且 `ctx any` 与 `handler.AgentProcessor` 接口的 `ctx context.Context` 不匹配。删除。

### M2 ✅ `api/client/client.go:197` — HealthReport 字段命名歧义

```go
return &HealthReport{
    OverallStatus: !c.closed,  // open=true 表示"健康"，但字段叫 Status 用 bool
    Timestamp:     time.Now(),
}, nil
```

`OverallStatus bool` — `closed=true` 时 `OverallStatus=false` 被消费者误读为"状态=false=不健康"是合理的，但命名带 Status 又用 bool 容易和 `string status` 混淆。建议改名 `Healthy bool` 或用 string status。

### M3 ✅ `api/handler/eval.go:76-79` — 硬编码 evaluator 列表

```go
func (h *EvalHandler) HandleListEvaluators(...) {
    names := []string{"exact_match", "llm_judge"}  // 硬编码，不从 registry 取
    writeJSON(w, http.StatusOK, names)
}
```

注释承认"full implementation would come from registry"但没做。API 返回与实际注册的 evaluator 不一致，用户拿不到真实可用列表。

### M4 ⬜ `api/mcp/sse.go:80-100` — SSE scanner 缓冲丢失字节

`readEndpointEvent` 用 `bufio.Scanner` 读 SSE 流到 endpoint event 后返回。Scanner 内部缓冲可能已消费超出 endpoint 行的字节，这些字节在后续 `roundTrip` 中丢失。若服务器在 SSE 流推送后续响应会丢消息。设计模糊，特定服务器协议下可能出 bug。

修复：用 `bufio.Reader` 按 line 读，或确认后续协议不依赖 SSE 流。

### M5 ✅ `api/service/agent/service.go:163-169` — results cache 重置清空所有历史

```go
if len(s.results) >= maxResults {
    s.results = make(map[string]*core.TaskResult)  // 直接清空，不是 LRU
}
s.results[task.ID] = result
```

达 10000 上限时直接 `make` 重置，老任务结果突然不可用，`GetTaskResult` 对之前任务返回 not found。建议 LRU 渐进淘汰或改用 sync.Map + 容量软上限。

### M6 ✅ `internal/plugins/resurrection/resurrection.go:548-555` — oldAgent 命名歧义，未防御 factory 返回同实例

```go
oldAgent := w.agent  // 开头快照的 agent
if oldAgent != nil {
    stopCtx, stopCancel := ares_ctxutil.WithDetachedTimeout("resurrection:stop-old", 10*time.Second)
    if err := oldAgent.Stop(stopCtx); err != nil { ... }
}
// 后面又：s.agents[agentID] = &watched{agent: newAgent, ...}
```

逻辑正确（用开头快照停旧实例），但变量名 `oldAgent` 没强调"是开头的快照而非当前 s.agents[id]"。若 factory 返回同一实例（实现不规范的 factory），会自停新 agent。建议加 nil check + instance identity check。

### M7 ✅ `internal/ares_evolution/scheduler/scheduler.go:355-419` — TriggerEvolution 与 Stop 的锁交互模糊

`TriggerEvolution` 持 `s.mu` 调 `s.evolutionWg.Add(1)`，goroutine 内 `defer s.evolutionWg.Done()`。`Stop()` 释放 `s.mu` 后调 `s.evolutionWg.Wait()`。若 Stop 在 TriggerEvolution 释放锁后、goroutine 启动前调用，Wait 会等到 goroutine 完成 — 逻辑串联回去 OK，但 `s.eg` 被覆盖（Stop 清 nil，TriggerEvolution 又新建）的状态机不清晰，边界场景难审。

建议：加状态断言（如 `s.running == false` 时 TriggerEvolution 直接返回 ErrSchedulerNotStarted），并文档化生命周期。

### M8 `api/handler/eval.go:54-58` + `api/service/eval/service.go` — EvaluatorRegistry.Get 返回 nil 但 handler 用 != nil 判断

```go
// handler
evaluator := h.evaluators.Get(req.Evaluator)
if evaluator == nil {
    writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("unknown evaluator: %s", req.Evaluator))
    return
}
```

依赖 `Get` 返回 nil 表示未注册。但 `core.EvaluatorRegistry` 接口注释（`api/core/eval.go:30`）只说 `Returns the evaluator or nil if not found` — OK。但 `api/service/eval/service.go` 的 `Registry` wrapper 没 expose `Get` 方法直接对外的接口，外部 handler 拿不到 registry。这是接口暴露不完整，不是 bug，但调用链脆弱。

---

## 第二轮 review 新增

### C6 ✅ `cmd/ares/serve.go:267` — HTTP 服务无优雅关闭，SIGTERM 时不会停止

`runServe` 中 `httpSrv.ListenAndServe()` 是阻塞调用。SIGINT/SIGTERM 信号处理 goroutine 只调用了 `cancel()`，但 `ListenAndServe` 不检查 context，一直阻塞到进程被 `SIGKILL` 或操作系统强杀。

```go
g.Go(func() error {
    select {
    case <-sigCh:
        fmt.Println("\nShutting down...")
        cancel()  // 只 cancel 了 context，没停 HTTP server
    case <-ctx.Done():
    }
    return nil
})
// ...
httpSrv := &http.Server{Addr: addr, Handler: handler, ...}
if err := httpSrv.ListenAndServe(); err != nil {  // 永久阻塞
    return err
}
```

修复：信号处理 goroutine 应在 cancel 后调 `httpSrv.Shutdown(ctx)`，并将 `ListenAndServe` 放到另一个 goroutine，`select` 等待 shutdown 或 error。

### 新模块 review 结果

#### `internal/agents/leader/` — 通过，无 bug
- `Process`（127 行）和 `ProcessStream`（206 行）超了 code_rules.md 的 100 行限制，但逻辑上不好拆分
- 并发安全：所有 `ch <-` 用 `select`+`ctx.Done()`+`a.stopCh` 三路防护，`Stop` 有 `cleanupOnce.Do`，errgroup 生命周期管理正确
- 状态机：`mu` + `processingMu` 双层锁的获取顺序一致（`processingMu → a.mu`），无死锁

#### `internal/storage/postgres/` — 通过，无 bug
- `validateSQLIdentifier` SQL 注入防护正确（防 schema 名、注释注入、长度控制）
- `safeFormatTable` 双引号转义 + identifier 双重校验
- `tenant_guard` 用 `set_config(is_local=true)` 实现事务级租户隔离，参数化查询
- `circuit_breaker` 实现标准，有 cleanupLoop 防 inflight 泄漏，half-open 状态用 CAS 控制并发
- `write_buffer` 的 `Write` 与 `Stop` 通过同一 mutex 序列化，防 send-on-closed-channel
- `ExecWithTenant` 事务内设置 tenant context，`defer tx.Rollback()` 在 Commit 后安全 no-op
- `Query` 有 `runtime.SetFinalizer` 防连接泄漏

#### `cmd/` — 发现 1 个 bug（C6）

---

## 修复优先级建议（更新版）

按 ROI 排序，先修 Critical：

1. **C3 ✅** expectedVersion=-1 — 已修
2. **C1 ✅** 路径穿越 — 已修
3. **C4 ✅** stub 返回 nil — 已修
4. **C2 ✅** bootstrap nil 依赖 — 已修
5. **C5 ✅** agent service stub — 已修
6. **C6 ✅** serve.go 无优雅关闭 — 已修

- `internal/agents/leader/` 的 dispatcher / planner / aggregator / event_recovery — 复杂编排逻辑，并发和状态机风险高
- `internal/ares_evolution/genome/` 的 `guided_pipeline.go` / `meta_evolution.go` / `multi_objective.go` — 高级 GA 特性，边界条件多
- `internal/ares_evolution/mutation/` 的 `guided_mutator.go` / `adaptive_distribution.go` — LLM hint 集成，错误处理路径复杂
- `internal/storage/postgres/` — 事务、并发、SQL 注入风险
- `cmd/` 命令入口 — 都是 CLI wrapper，bug 概率低但未确认

---

## 修复优先级建议

按 ROI 排序，先修 Critical：

1. **C3 expectedVersion=-1** — 一行判断改写，影响所有事件流恢复
2. **C1 路径穿越** — 加 EvalSymlinks，安全敏感
3. **C4 stub 返回 nil** — 改返回 ErrNotImplemented，防 panic
4. **C2 bootstrap nil 依赖** — 改为返回 error 或拿真实依赖
5. **C5 agent service stub** — 至少返回 ErrNotImplemented 而非假成功

再修 High：

6. **H1 goroutine 泄漏** — 所有 events<- 加 select+ctx.Done
7. **H5 stdio goroutine 泄漏** — 超时分支显式关 stdin
8. **H6 pg_store 注释/代码矛盾** — 统一语义
9. **H4 Snapshot 错误吞掉** — 检查 err 跳过
10. **H7 Stop 无超时** — 加 WithTimeout
11. **H8 Reserved 负数** — 钳到 >=0
12. **H2/H3 stream headers 顺序** — 重排逻辑
13. **H9 ScoreAgents 持锁** — 放锁外评分

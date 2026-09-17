# `ares serve` 启动 → 一个任务跑完：逐步代码走读

> 本文从 `main()` 开始，沿真实调用链走到一个任务 `COMPLETED`。
> 每一步都给出 `file:line` 锚点，可直接跳源码核对。
> 未加渲染的引号内容均为源码原文摘录。

基线：`dev` @ `64d55fe4`，`VERSION=0.3.1`。

---

## 0. 读前须知：三个名字先分清

走读过程中会反复出现这三个，先钉死：

| 名字 | 文件 | 是什么 |
|---|---|---|
| `kernelScheduler` | `cmd/ares/kernel.go:367` | **类型别名**：`type kernelScheduler = kernel.Scheduler`。不是独立类型，就是 `internal/kernel` 的 `Scheduler` |
| `Scheduler` | `internal/kernel/scheduler.go:163` | 内核调度器。`New(fabric, executors, tracker)` 构造 |
| `Fabric`（两个） | `internal/fabric/task/`、`internal/fabric/agent/` | **Task Fabric** 管任务状态机；**Agent Fabric** 管 agent 能力/身份。同名不同物 |

任务状态机定义在 `internal/fabric/task/state.go:6-19`：

```
READY / LEASED / RUNNING / SUSPENDED / COMPLETED / FAILED
```

转移许可在 `state.go:25-33` 的注释与 `canTransition`：

```
READY → LEASED (acquire), FAILED (dependency-failure cascade)
LEASED → RUNNING (start), READY (release)
RUNNING → COMPLETED, FAILED, SUSPENDED (yield), READY (preempt/release)
SUSPENDED → LEASED (re-acquire with preserved checkpoint), READY (release)
```

---

## 1. 进程入口

`cmd/ares/main.go:58` 定义根命令：

```go
var rootCmd = &cobra.Command{
	Use: "ares",
	Short: "ARES — Agent Runtime & Evolution System",
	...
}
```

`main()` 在 `main.go:68`：

```go
func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
```

`serve` 子命令在 `cmd/ares/serve.go:36` 定义，`:59` 的 `init()` 挂到根命令：

```go
var serveCmd = &cobra.Command{
	Use: "serve",
	Short: "Start full agent monitoring with LLM + MCP + dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServe()
	},
}
```

支持的 flag（`serve.go:61-66`）：`--config/-c`、`--host`、`--port/-p`、`--llm-url`、`--llm-api-key`、`--llm-model`。

`runServe()` 在 `serve.go:70`，是整个装配的骨架。

---

## 2. 配置装载

### 2.1 `loadServeConfig()` — `serve.go:224`

两条路径，互斥：

**路径 A：最小模式**（给了 `--llm-url`）

```go
if serveLLMURL != "" {
 cfg := ares_config.NewMinimalConfig(serveLLMURL, serveLLMKey, serveLLMModel)
 if serveHost != "" { cfg.Server.Host = serveHost }
 if servePort > 0 { cfg.Server.Port = servePort }
 return cfg, nil
}
```

不读任何文件。`NewMinimalConfig`（`internal/ares_config/config_defaults.go:86`）内部会 `cfg.setDefaults()` 填齐默认值。

**路径 B：配置文件**

```go
allowConfigDirFor(configPath)
cfg, err := ares_config.Load(configPath)
if err != nil { return nil, fmt.Errorf("load config: %w", err) }
if err := ares_config.LoadFromEnv(cfg); err != nil { ... }
// CLI flags win over env (SERVER_HOST/SERVER_PORT) and YAML
if serveHost != "" { cfg.Server.Host = serveHost }
if servePort > 0 { cfg.Server.Port = servePort }
```

优先级：**CLI flag > 环境变量 > YAML**。

`allowConfigDirFor`（`serve.go:214`）把路径遍历守卫 `SetAllowedConfigDir` 绑到配置文件所在目录——注释说明这是修 review C-3：原先该守卫"有文档无调用方"。

未显式给 `--config` 时自动探测 `ares.yaml` / `./ares.yaml`（`serve.go:241-258`），并把解析结果写回 `serveConfigPath`，好让后面的热加载 watcher 也能启动。

### 2.2 `validateServeConfig()` — `serve.go:288`

```go
if err := cfg.Validate(); err != nil {
 return fmt.Errorf("serve: invalid configuration: %w", err)
}
authConfigured := cfg.Security.AuthEnabled && cfg.Security.JWTSecret != ""
if isWildcardHost(cfg.Server.Host) && !authConfigured && cfg.Introspect.Token == "" {
 return fmt.Errorf("serve: server.host %q binds all interfaces while ...")
}
```

第二段是 fail-closed：`0.0.0.0`/`::` 绑定 + 无鉴权 + 无 introspect token → **拒绝启动**。三个逃生口：开 `security.auth_enabled`（并设 jwt_secret）、设 `introspect.token`、改绑 loopback。

`isWildcardHost` 在 `serve.go:411`，只认 `"0.0.0.0"` 和 `"::"`。

---

## 3. 信号与关闭协调

`runServe` 先建信号通道和 `errgroup`（`serve.go:90-107`）：

```go
shutdownMgr := ares_shutdown.NewManager(30 * time.Second)
shutdownMgr.RegisterPhase(ares_shutdown.PhasePreShutdown, 5*time.Second)
shutdownMgr.RegisterPhase(ares_shutdown.PhaseGraceful, 20*time.Second)
shutdownMgr.RegisterPhase(ares_shutdown.PhaseForce, 5*time.Second)
shutdownMgr.RegisterPhase(ares_shutdown.PhaseDone, 1*time.Second)

g, ctx := errgroup.WithContext(ctx)
var compPtr atomic.Pointer[ares_bootstrap.Components]
wiringServeSignalWatch(g, sigCh, ctx, cancel, shutdownMgr, &compPtr)
```

`compPtr` 用 `atomic.Pointer` 是因为信号 goroutine 和主 goroutine 会并发访问 `comp`。

### `wiringServeSignalWatch` — `cmd/ares/serve_wiring.go:36`

goroutine 里：

1. 等第一个信号
2. 立刻再起一个 goroutine 等**第二个信号**，收到就 `os.Exit(1)`——注释写明这是为了"挂死的关闭阶段不能把操作员困在无法终止的进程里"
3. 用 30s 预算跑 `shutdownMgr.StartShutdown`
4. 再给 SystemRuntime 单独 15s 预算跑 `shutdownSystemRuntime`——注释解释为什么不共享那 30s：阶段回调可能耗光共享预算，导致 MCP/Runtime/FlightRecorder 的 Stop 被跳过、泄漏 goroutine 和连接
5. `cancel()` 停后台 goroutine
6. 记录 pre-shutdown 快照，`comp.WaitBackground()` 等 Bootstrap 起的后台 goroutine 退出

---

## 4. 事件存储 + Bootstrap

### 4.1 `wiringServeEventStore` — `serve_wiring.go:120`

```go
serveStore, closeStore, err := newServeEventStore(cfg)
comp, err := ares_bootstrap.Bootstrap(ctx, cfg, &ares_bootstrap.BootstrapDeps{
 EventStore: serveStore,
})
```

**关键**：store 由 serve 侧建好后**通过 deps 注入** Bootstrap，而不是让 Bootstrap 自己建一个内存 store 再替换。这样 Runtime/Memory 从一开始就绑在真实的 serve store 上。

失败路径上 `closeStore()` 释放 PG 连接池——注释说这镜像了 Bootstrap 对 evidence pool 的清理姿态。

### 4.2 `newServeEventStore` — `serve_wiring.go:311`

```go
if cfg.Storage.Enabled && cfg.Storage.Host != "" {
 pool, err := postgres.NewPool(pgCfg)
 store, err := ares_events.NewPostgresEventStore(pool)
 return store, store.Close, nil
}
compactable, _, err := archive.NewCompactableStoreWithArchive(cfg.Memory.Archive)
return compactable, compactable.Close, nil
```

两条路：配了 PG 就用 `PostgresEventStore`（任务事件跨重启持久化），否则用带归档的内存 `CompactableStore`。

函数头部注释写明 PG 构造失败是 **FATAL** 而不是静默回退内存：

> "silently falling back to the in-memory store would make the persistence feature lie about durability"

### 4.3 `Bootstrap` — `internal/ares_bootstrap/bootstrap.go:233`

```go
bctx, bcancel := context.WithCancel(ctx)
comp := &Components{}
b := &bootstrapBuilder{...}
if err := b.assembleCore(); err != nil { return nil, err }
if err := b.assembleExperience(); err != nil { return nil, err }
if err := b.assembleEvolutionDAG(); err != nil { return nil, err }
if err := b.assembleNewEvolution(); err != nil { return nil, err }
if err := b.assembleLegacyEvolution(); err != nil { return nil, err }
b.wireEvolutionWiring()
if err := b.wirePlatform(); err != nil { return nil, err }
return comp, nil
```

`bctx` 是 `ctx` 的子 context，专门服务**失败路径**：`runCleanups` 取消它，让已经启动的后台 worker 立刻停，而不是等调用方恰好 cancel。

### 4.4 `assembleCore` — `bootstrap_builder.go:68`

五步，顺序固定：

| # | 动作 | 代码 |
|---|---|---|
| 1 | EventStore：来自 deps，否则 `NewMemoryEventStore()` | `:74-79` |
| 1b | PG 模式下按 `events_retention_days` 挂保留期 cleaner（0 = 永不删） | `:88-94` |
| 2 | `ProvideRuntime(comp.EventStore)` → `comp.Runtime` | `:96-102` |
| 3 | `wireMemory(cfg, comp.EventStore)` → `comp.Memory` | `:104-111` |
| 4 | `ProvideMCP(ctx, cfg.MCP)` → `comp.MCP` | `:113-124` |
| 4b | `wireSkills` → `comp.SkillsRegistry` / `comp.SkillCatalog` | `:134-152` |

`wireMemory`（`bootstrap.go:295`）的禁用契约是 `(nil, nil)`：

```go
if !cfg.Memory.IsEnabled() {
 log.Info("bootstrap: memory disabled ..., skipping construction")
 return nil, nil
}
```

注释明确："every caller probes `comp.Memory != nil` ... none treats nil as an error"。

Skills 那步还会起一个 `startSkillOutcomeWriter`，把 `task.completed/failed` 事件写成 Experience 先验——注释说这是 M4.4 闭环的写侧，读侧在 `Fabric.Schedule` 里。

`Components` 结构体在 `bootstrap.go:38`，字段就是系统器官清单：`MCP` / `LLM` / `Evolution` / `NewEvolution` / `Runtime` / `Memory` / `EventStore` / `Distillation` / `SkillsRegistry` / `SkillCatalog` / `Discovery` / `KnowledgeRuntime` / `VectorStore` / `KnowledgeStore` / `AKGBridge` 等。

`assembleCore` 之后还有六步，每步失败都 `runCleanups()` 回滚。调用顺序（`bootstrap.go:257-271`）：

| # | 步骤 | 建什么 |
|---|---|---|
| 2 | `assembleCore` | EventStore / Runtime / Memory / MCP / Skills（第4.4节） |
| 3 | `assembleExperience` | LLM + 经验蒸馏 + AKG 闭环 + 观测面（第4.5节） |
| 4 | `assembleEvolutionDAG` | 进化 DAG + live memory store + KnowledgeRuntime + EvidenceStore（第4.7节） |
| 5 | `assembleNewEvolution` | 运行时进化系统 + FlightRecorder（第4.8节） |
| 6 | `assembleLegacyEvolution` | 旧进化系统 + 调度器关停 watcher（第4.9节） |
| 7 | `wireEvolutionWiring` | 检索器 + 部署管线 + 最小 DAG 注册（第4.10节） |
| 8 | `wirePlatform` | **GA 进化** + Discovery + SystemRuntime + 过期清理（第4.11节） |

这六步里藏着本文前半段一直没提的三条闭环：**经验蒸馏闭环**（第4.6节）、**AKG 知识闭环**（第4.6节）、**GA 进化闭环**（第4.11节）。它们都在 `Bootstrap` 里完成装配，但要等第12节 调度器拿到分数之后才真正开始转。

---

### 4.5 `assembleExperience` — `bootstrap_builder.go:162`

四件事，顺序固定：LLM → 经验蒸馏 → AKG → 观测面。

**LLM**（`:167-177`）：`deps.LLMClient` 非空则直接用，否则 `ProvideLLM(cfg.LLM)` 构造。

**经验蒸馏**（`:183`）：

```go
guidanceProvider, embClient := wireDistillation(bctx, cfg, comp, deps, &b.cleanups)
comp.ExpRepo = deps.ExpRepo
```

`embClient` 会被后面的 `wireRetrievers`（第4.10节）复用来建 MemoryRetriever——**蒸馏写入与 RAG 读取共享同一个 embedding 客户端**（`:181-182` 注释）。`guidanceProvider` 交给 GA 做经验引导变异（第4.11节）。

**AKG 闭环**（`:189-198`）：`wireAKGLoop` 建 `KnowledgeStore`（默认内存，PG 可选）和写侧 `DistillBridge`，门控在 `cfg.Knowledge.RetrievalEnabled`：

```go
knowStore, akgBridge := wireAKGLoop(cfg, deps, embClient, &b.cleanups)
comp.KnowledgeStore = knowStore
comp.AKGBridge = akgBridge
```

**事件订阅**（`:200`）：`subscribeDistillationEvents(bctx, comp)`——蒸馏与 AKG 的触发点，见第4.6节。

**观测面**（`:202-220`）：`EvolutionTracer` / `FeedbackStore` / `GlobalTracer` **只在这里创建一次**。dashboard 读、运行时写钩子（GA 代记录、任务/agent 生命周期追踪）写的是**同一批实例**——所以控制面端点返回的是活数据而非空列表（`:204-207` 注释）。旧的 `:8090` 独立 dashboard server 已移除（`:214-215` 注释），观测数据直供 `introspect.ControlServer`。

---

### 4.6 蒸馏与 AKG 的事件闭环

这是第4.5节第 `:200` 行那句 `subscribeDistillationEvents` 的内部，也是本文此前完全缺失的一环。

**入口** `subscribeDistillationEvents` — `bootstrap_steps.go:102`：

```go
if comp.Distillation == nil || comp.EventStore == nil {
 return // :108 无蒸馏服务则整体 no-op
}
ch, err := comp.EventStore.Subscribe(ctx, ares_events.EventFilter{
 Types: []ares_events.EventType{
 ares_events.EventTaskCompleted, // :113
 ares_events.EventTaskFailed, // :114
 },
})
```

即：**任务完成/失败事件 → 蒸馏成经验 + 写入 AKG 知识事实**。

订阅循环里再起一个 `akgEg, akgCtx := errgroup.WithContext(ctx)`（`:124`），把每次 AKG 蒸馏丢进独立 goroutine——**注释明确说这是为了让慢桥接调用（LLM/embedding）不阻塞经验蒸馏**（`:122-123`）。

循环退出时 `akgEg.Wait()` join 所有在飞的 AKG 蒸馏（`:129-135`），每次蒸馏由 `akgBridgeTimeout`（30s）封顶。

**触发** `triggerAKGBridge` — `knowledge_akg.go:225`。内容守卫（`:239`）：

```go
if tenantID == "" || len(taskText) < 10 || len(resultText) < 20 {
 return
}
```

无租户或文本太短直接跳过——**蒸馏器凑不出有意义的记忆**。通过后把 task/result 拼成 user→assistant 对话（`:243-246`），在 30s 超时的 goroutine 里调 `bridge.DistillConversation`（`:248-255`）。全程 best-effort：失败只 `log.Warn`，不影响主路径。

**`wireAKGLoop`** — `knowledge_akg.go:172`。`cfg.Knowledge.RetrievalEnabled` 为假直接返回 `(nil, nil)`（`:178-180`）。建完 store 后有一个**读写分档**（`:194-205`）：`embClient` 或 `deps.ExpRepo` 缺任一，就只留读环、跳过写环，日志说 `read loop active`——store 仍可被检索，只是没人往里写。

**`wireDistillation`** — `bootstrap_steps.go:26`。门控是三态的 `cfg.Memory.DistillationEnabled()`（`nil` 默认 true，只有显式 YAML `false` 才关，`:29-34`）。真正的接线条件是 **PG + Embedding 同时就绪**（`:35`）：

| 动作 | 位置 |
|---|---|
| `provideDistillation` → service / expRepo / guidanceProvider / embClient | `:36` |
| `deps.ExpRepo` 为空则回填 | `:45-47` |
| 经验表的 decay purge 挂到维护 worker | `:53-57` |
| 其它保留期表（sessions/conversations/secrets/knowledge_chunks）共用同一个 pool 挂 cleaner | `:63` |
| embedding 队列 worker + reconciler（异步回填向量） | `:79-80` |
| `comp.VectorStore = postgres.NewVectorSearcher(...)` 供知识向量检索 | `:85` |

`:64-73` 的注释值得留意：**LLM 抽取调用（30s）仍在订阅循环里跑，只有 embed 被推迟到 worker**——蒸馏路径先落一行无向量的经验，再塞一个回填任务。

---

### 4.7 `assembleEvolutionDAG` — `bootstrap_builder.go:228`

建四样东西：进化 DAG、live memory store、KnowledgeRuntime、EvidenceStore。

**装配顺序是被刻意安排的**，`:233-238` 的注释解释了原因：`ProvideNewEvolution`（下一步）会创建共享的 `newEvol.EvidenceStore`，而 flight recorder 必须**在它之后**构造，否则 recorder 拿到 nil store，workflow/scheduler/recovery 三路 fitness 证据会被静默丢弃——注释直说这正是历史上发生过的 bug。

**进化 DAG**（`:247` → `bootstrap.go:326`）：

```go
if !enabled { return nil, nil } // bootstrap.go:327-329
```

启用时建一条三节点链 `input → process → output`（`bootstrap.go:330-334`），agentType 分别是 `parser` / `processor` / `formatter`。这是**占位拓扑**—— 第4.10节 会说明它为何是合成的。

**live memory store**（`:257` → `bootstrap.go:346`）：把 `comp.Memory` 类型断言成 `MemoryConfigStore`。内存被禁用时回退到最小 manager，**保证进化系统总有 store 可写**，而不是空指针。

**KnowledgeRuntime**（`:265-273`）：

```go
var embForRuntime apiembed.EmbeddingService
if embClient != nil {
 embForRuntime = embClient
}
knowRt := BuildKnowledgeRuntime(comp.VectorStore, embForRuntime, knowStore)
comp.KnowledgeRuntime = knowRt
```

`:265-271` 注释点名了一个 Go 陷阱：**nil 指针塞进非 nil 接口**——`embClient` 为 nil 时若直接赋值，nil 检查会通过但方法调用（如 `GetModel`）直接 panic。所以显式分支，只在非 nil 时转换。

这个 runtime 是**共享的**：进化系统和 agent 的 AKF 工具用同一个（`:259-264` 注释），所以知识 genome 补丁生效在 agent 真正用的那份 runtime 上。

**EvidenceStore**（`:275-311`）：配了 PG 就建持久化 store，**fail-loud**——`postgres.NewPool` 或 `NewPostgresStore` 失败直接 `runCleanups()` + 返回错误（`:289-297`），因为"已配置的 PG 连不上"必须阻断启动而非静默降级。同时把 `pgStore` 注册为 `evidence_records` 表的过期清理器（`:303-304`）。

未配 PG 时 `evidenceStore` 保持零值，`assembleNewEvolution` 会兜底。

---

### 4.8 `assembleNewEvolution` — `bootstrap_builder.go:319`

**运行时进化系统**（Genome + Diff + Coordinator）+ 共享 FlightRecorder。

```go
newEvol, evStore, evErr := wireNewEvolution(cfg.Evolution.Enabled, dag, knowRt, liveMemoryStore, evidenceStore)
comp.NewEvolution = newEvol
comp.EvidenceStore = evStore
```

`wireNewEvolution` — `bootstrap.go:366`，禁用路径是**文档化的 `(nil, nil)` 契约**（`:360-365` 注释 + `//nolint:nilnil`）：

```go
if !enabled {
 return nil, evidence.NewMemoryStore(), nil // bootstrap.go:373-375
}
```

关键在**第二返回值永远非 nil**：禁用时也返回一个独立内存 store。所以 `comp.EvidenceStore` 恒有值，下游不会把"禁用"误判成"失败"——flight recorder 的 fitness 证据流在任何生产路径上都不断。

**FlightRecorder**（`:336-353`）：

```go
if comp.EventStore != nil {
 comp.FlightRecorder = flight.NewFlightRecorder(flight.FlightRecorderConfig{
 EventStore: comp.EventStore,
 EvidenceStore: evStore,
 })
```

`:336-342` 的注释点明了两件事：

1. 它**独立于旧进化系统的 deps**（`ExpRepo`）而创建——所以即使 `ProvideEvolution` 被跳过，fitness 证据写环照样工作。
2. 它与 GA genome **读同一个 evidence store**。

`Start` 失败只是 `Warn`，fitness 证据禁用但服务继续（`:348-351`）。

---

### 4.9 `assembleLegacyEvolution` — `bootstrap_builder.go:359`

旧进化系统，三重门控（`bootstrap.go:401`）：

```go
if !cfg.Evolution.Enabled || deps.EventStore == nil || deps.ExpRepo == nil {
 return nil, nil
}
```

任一不满足即跳过，`(nil, nil)` 是文档化契约——**"没有旧调度器"是受支持的配置，不是失败**（`bootstrap.go:388-392` 注释）。

它在 FlightRecorder **之后**构造，所以直接复用 `comp.FlightRecorder`，不会造出第二个 recorder（`:364-369` 注释）。

`:376-391` 记了一段 goleak 抓出来的泄漏：`ProvideEvolution` 会调 `scheduler.Register()`，后者用**自己的 `context.Background()`** 订阅 EventStore 并在事件 channel 上停一个 goroutine。**此前没有任何地方调 `Shutdown()`**，那个 goroutine 加上喂它的订阅 goroutine 会活过每一个 Runtime 和 Bootstrap，直到进程结束。注释里那句"counting goroutines by hand never would have"就是这次修复的由来。

现在补了一个 watcher：

```go
comp.bgGroup.Go(func() error {
 <-bctx.Done() // bctx：Bootstrap 失败时 runCleanups 会取消它
 sched.Shutdown()
 return nil
})
```

用 `bctx` 而非调用方 `ctx`：Bootstrap 失败时 `runCleanups()` 取消它，订阅立刻关掉——调用方的 `ctx` 可能比失败的 Bootstrap 活得久。

---

### 4.10 `wireEvolutionWiring` — `bootstrap_builder.go:399`

三件事：注入检索器、接部署管线、注册最小 DAG。这一步**没有 error 返回**，全部 best-effort。

**检索器**（`:419`）→ `retriever_wiring.go:58`。把 `MemoryRetriever`（蒸馏经验）和 `KnowledgeRetriever`（AKG 条目）注入 MemoryManager，**每次 `BuildContext` / `BuildPromptMessages` 在 `cfg.EnableRAG` 为真时用检索到的上下文增强 prompt**（`:409-414` 注释）。

两个细节：

- MemoryRetriever 建成后挂一个 evidence emitter，`Source "memory"`（`retriever_wiring.go:105-107`）——**检索命中/未命中写进共享 evidence store，GA 的 MemoryGenome 就能用真实使用数据给记忆质量打分**。
- KnowledgeRetriever 带 store 时用 `WithNamespace(defaultDistillTenant)` 限定命名空间（`retriever_wiring.go:134-137`）——注释说**空命名空间等于跨命名空间扫描**。

顺序上它必须在 `ProvideNewEvolution` 之后跑，就是为了这个 evidence 反哺（`:416-418` 注释）。

**部署管线**（`:429-450`），三重门控 `cfg.Evolution.Enabled && cfg.Evolution.Deployment.Enabled && comp.NewEvolution != nil`：

```go
staging := &deploymentStagingRuntime{
 reg: comp.NewEvolution.PatchReg,
 agg: evolution.NewRuntimeFitnessAggregator(comp.EvidenceStore, ...),
 coldStartScore: 0.5,
 asm: comp.NewEvolution.ActiveStrategyManager,
}
```

- `coldStartScore: 0.5`：无证据的补丁拿保守 0.5，而不是一刀切 0.0 拒绝（`:432-435` 注释）。
- `asm`：baseline 分数在 Evaluate 时**实时**从 ASM 解析当前激活策略，避免构造时的陈旧 ID（`:438-440` 注释）。

禁用时 Coordinator 回退到直接应用补丁（`:427-428` 注释）。

**注册最小 DAG**（`:455-457`）：`comp.Runtime.RegisterAgentDAG(runtime.AgentDAGEvolutionKey, dag)`。但紧接着 `:459-470` 有一段很诚实的日志：

```go
log.InfoContext(ctx, "bootstrap: evolution verdicts available but no live agent topology to act on",
 "live_dag_registered", false,
 "synthetic_dag_key", runtime.AgentDAGEvolutionKey)
```

即：**standalone Bootstrap 没有 agent 群体，没有真实拓扑可操作**。serve 入口（`buildLiveAgentDAG` + `UpdateLiveDAG`）才是唯一的 live-DAG 供给者，之后会取代这个占位 DAG。日志把这件事说破，而不是让合成图静默吞掉 promotion。

---

### 4.11 `wirePlatform` — `bootstrap_builder.go:474`

四件事：**GA 进化**、Discovery、SystemRuntime、过期清理 worker。

**GA 进化**（`:483-488`），门控 `cfg.Evolution.Enabled && comp.NewEvolution != nil`：

```go
if err := wireGAEvolution(bctx, cfg, comp, comp.NewEvolution, guidanceProvider, &b.cleanups); err != nil {
 b.runCleanups()
 return err
}
```

`guidanceProvider` 来自第4.5节 的蒸馏——**经验蒸馏的输出在这里回流进 GA 的变异**，这是两条闭环的交汇点。

#### 4.11.1 `wireGAEvolution` — `bootstrap_evolution.go:510`

GA 进化面的完整装配，`:505-509` 的函数注释列了九个部件：strategy store、SystemConfig、scorer/shadow-gate 姿态、lifecycle gates、runtime observer、channel feedback、scheduler、后台 ticker、LLM suggestion 循环。

**① StrategyStore**（`:511-512`）：

```go
memStore := buildStrategyStore(ctx, cfg, cleanups)
newEvol.StrategyStore = memStore
```

**② 知识闭环回填**（`:514-519`）：`attachEvolutionKnowledgeProvider` 把激活/历史策略**作为决策类知识对象流进 KnowledgeRuntime**，让服务端进化查询能检索到策略决策。注释解释了为何在这里才注册：**StrategyStore 到这一步才存在**（KnowledgeRuntime 更早就建好了），所以是 late registration。

**③ 根基因组**（`:521-524`）：

```go
base := &mutation.Strategy{
 ID: "bootstrap-root",
 Params: map[string]any{paramTemperature: 0.7, paramMaxTokens: 4096},
}
```

**④ SystemConfig** — `assembleGAConfig`（`bootstrap_evolution.go:63`）。从 `evolution.DefaultSystemConfig()` 起手，逐块覆盖：

| 配置块 | 位置 | 要点 |
|---|---|---|
| `EnableScheduler = true` | `:71` | ticker 强制走 `scheduler.Tick`，从而总是过 `shouldEvolve` + guardrails |
| Rollback 策略 | `:81-88` | 三态 `rbCfg.IsEnabled()`；阈值 0.15 / 窗口 5 / 最小样本 3 |
| Shadow 评估 | `:91-96` | `MinSamples 20` / `MinWinRate 0.55` |
| replay 窗口 | `:107-125` | 非法 duration 只 Warn 不 fatal；超宽 horizon 也 Warn |
| Lifecycle | `:129-132` | 含 `RollbackArmed` 同一个三态判断 |
| Prometheus 指标 | `:139-143` | `NewPrometheusMetrics` 幂等，复用 `/metrics` 那个 collector |
| YAML GA 调参 | `:150` | `applyGATuning`，见下 |
| Guardrails | `:157` | `buildEvolutionGuardrails`，见下 |
| 经验引导变异 | `:161-162` | `EnableExperienceGuidedMutation = guidanceProvider != nil` |

`applyGATuning`（`:594`）的覆盖判据值得单说（`:578-588` 注释）：**字段只有在"不等于 config 层默认值"时才覆盖引擎默认**。因为 cfg 走过 `setDefaults`，每个字段都非零，用朴素的 `> 0` 判断会**永远为真**，从而静默替换掉 GA 引擎自己调好的默认值（例如 `EliteCount` 引擎默认 3，config 层默认 2）。已知 tradeoff：运维显式设成等于 config 层默认的值，与"未设置"无法区分，保留引擎默认——这条被 `TestApplyGATuningExplicitDefaultValues` 锁住。

Guardrails 那行（`:151-156` 注释）是段自白：**此前 `gaCfg.Guardrails` 在本包从未被赋值过**，于是 `WithAdapterGuardrails` 被跳过，`runPreGuardrails` 和旧调度器的 `checkGuardrails` 都在 nil 上短路——**G1 是一道存在于代码里、运行时什么都不做的闸**，连它自称的"唯一真正防线" adapter 层也是哑的。

**⑤ Scorer 与 G2 姿态** — `wireScorerAndShadowGate`（`:169`）。要点是**G2 闸的姿态必须在 `NewWiredEvolutionSystem` 之前定下来**（`:203-207` 注释），否则事后反注册。

不变量（`:205-207`）：**跳过部署前验证，只允许在部署后验证已布防时**；两者皆无则 G2 保持 fail-closed。

`hasScorer := gaCfg.Scorer != nil || gaCfg.DeterministicScorerEnabled`（`:217`）。`:199-201` 这段解开了一个死锁：LLM scoring 关闭时置 `DeterministicScorerEnabled = true`，让 G2 闸保持注册，**仅凭执行归因证据就产出 shadow 对比**，一次 LLM 调用都不需要——注释称之为打破"zero-token ⇒ no G2"。

闸被跳过时不止打日志，还 `RecordEvolutionGateSkipped("shadow", gateReason)`（`:231-233`）——**"缺席"必须可度量，而不是只报一次**。

**⑥ 生命周期闸** — `wireLifecycleGates`（`:239`）。注册 G3 eval 闸与 arena 回归闸，原则是 `:237` 那句：**armed-but-broken 的闸让 Bootstrap 失败（fail-closed），故意缺席的闸是诚实跳过**。

- G3 只在配了 `evolution.gates.eval_suite` 时才建（`:242-245`）；没配就**不注册任何闸**——注释明说这是 honest absence，不是"永久放行却假装在验证"。Warn 里还提示 `eval_strict` 会让缺席变成 fatal（`:278`）。
- Arena 回归闸默认 auto-armed（`:281-286`），是 G3 绝对分数闸的**相对补充**：候选 vs 激活策略在同一套保留用例上 A/B，只拒绝**统计显著的下降**（`:282-284`）。显式 `regression_enabled=false` 是文档化退出路径。

**⑦ 观察与反馈** — `startLifecycleWatch`（`:319`）给 lifecycle 注入 evidence store 并 `Start(ctx)`，`startEvolutionObserver`、`startChannelFeedback`（`:553`）补齐另外两条感知通道的 OBSERVE 阶段。

**⑧ 代际循环** — `runEvolutionTicker`（`:410`）。周期默认 5 分钟，`evolution.min_interval` 可覆盖（`:414-419`）。每次 tick 优先走 `legacySched.Tick`，其次 `wired.Scheduler.Tick`，都没有才退到 `popAdapter.Run`（`:432-443`）——注释说这是为了**让分数可见，使 `shouldEvolve` + guardrails + MinInterval 总是被应用**。跑完把这一代轨迹写进共享 tracer（`:448-451`），`/evolution/trajectory` 才有活数据。

**⑨ LLM 建议管线** — `runLLMSuggestions`（`:461`）。15 分钟一轮：`buildEvolutionSuggestionPrompt`（基于当前进化状态与近期证据）→ `Generate` → `Parse` → 逐条 `Coordinator.Submit(proposal)` → `Coordinator.Evaluate(ctx)`。解析失败只 `Debug`——**LLM 回复不匹配任何已知模式是预期内的**（`:485-488`）。

交叉算子在 `internal/evoapi/genome/genome.go`：`uniform` / `single_point` / `two_point` / `scattered`（`:17-22`），提示模板继承模式 `PromptInherit` / `PromptHalfSplit` / `PromptUniform`（`:27-31`）。`:78-80` 的注释记了一个历史 bug：`CrossoverType` 曾被静默丢弃，每次调用都跑内层引擎的 uniform 默认。

**Discovery**（`:490-503`）：禁用返回 `ErrDiscoveryDisabled`，走 `errors.Is` 分支置 `comp.Discovery = nil`——**不是错误**。

**SystemRuntime** — `wireSystemRuntime`（`system_runtime_wiring.go:122`）：把装配好的组件图注册进系统级控制面。这是**观测性的**，构造与启动仍归 Bootstrap（`:505-508` 注释）。

两个设计点：

- `eventstore` 是依赖叶子（`:125-131`）。逆拓扑关停保证每个依赖它的组件（runtime / memory / flight recorder / 内核支柱）**先于**它停止，所以关掉 store 不会切断活跃写者。这一步释放 PG 池并 join 在飞的 compaction worker。
- Knowledge 组件的诚实姿态（`:150-162`）：**AKG 检索开启但写侧依赖缺失（`AKGBridge == nil`）时，组件注册为 `ModeDegraded` 并带一个 readiness 错误**，而不是假装 Ready。其余情况才是 `ModeRequired`。

orchestrator 的后台组件失败会记到共享 EventStore（`:168-171`），flight recorder 的时间线订阅了整条流，所以看得见。

**过期清理 worker** — `wirePlatform` 的最后一行（`bootstrap_builder.go:522`）：

```go
startExpiryCleanupWorker(bctx, comp)
```

`maintenance_worker.go:189`。**放在最后启动是有意的**（`:517-521` 注释）：这样它才看得到上面每一步注册进来的 cleaner——sessions、conversations、knowledge、secrets、evidence store。没有任何 cleaner 时是 no-op（`:190-192`）。

周期 `expiryCleanupInterval = 1 小时`（`:182`），**首次运行推迟整整一个 interval**（`:180-181` 注释），所以启动中的系统不会为清理付延迟。

设计原则是 best-effort（`:184-188` 注释）：**单个 cleaner 失败或 panic 既不会取消循环、也不会阻塞其它 cleaner**。`runExpiredCleanup`（`:214`）对每个 cleaner 单独 `recover()`：

```go
defer func() {
 if r := recover(); r != nil {
 logMaintenance.ErrorContext(ctx, "bootstrap: expiry cleanup panicked", ...)
 }
}()
```

注释一句话说明理由：**maintenance 不能把进程带崩**。

前面几处出现的 `comp.ExpiryCleaners = append(...)` 都是往这个列表里塞（第4.6节 经验表、第4.7节`evidence_records`、第4.5节`wireExpiryCleaners` 铺开的其余保留期表），最终都在这里被同一个 ticker 驱动。

---

## 5. 配置热加载 / LLM / 工具链

回到 `runServe`，`serve.go:112-137`：

### 5.1 `wiringServeCfgStoreWatch` — `serve_wiring.go:148`

```go
cfgStore := ares_config.NewConfigStore(cfg)
if serveConfigPath != "" {
 g.Go(func() error { return cfgStore.Watch(ctx, cfgPath) })
}
```

只有显式给了配置文件才起 fsnotify watcher。最小模式不起 watcher，但 store 仍然能提供当前生效配置的快照。

末尾打印 SystemRuntime 组件快照（名称/模式/生命周期状态）。

### 5.2 ~~`createLLMAdapterWithFallback`~~ — 已移除（0.3.1 / independent-review F-07）

旧版在此构造 `internal/llm/output` 的 `LLMAdapter` 并穿针引线传入 `createAndServeAgents`/`createPeerAgents`，但函数体从未消费它——它宣称的"运行时 fallback 链"从未真正执行。0.3.1 删除了这条死装配，`cmd/ares/llm_adapter.go`（含 `ErrNoLLMAdapter`）随之移除。`internal/llm/output` 包本体保留（仍有 `evolution` 侧的 Parse 消费者与测试），登记为 0.4 删除候选。

运行期的 provider 降级只剩一条链：**`FailoverClient`**（见 5.3）。

### 5.3 `createChatClient` — `agent_kernel.go:406`

`serve.go:128` 调用，是**唯一**的 LLM 客户端装配点（原生 tool calling 与 agent 共用），内部是带 fallback 链的 `FailoverClient`。

```go
configs = append(configs, &llm.Config{Provider: cfg.LLM.Provider, ...}) // :408-415 主配置
for _, fb := range cfg.LLM.Fallbacks { ... } // :416-429 fallback 链
timeout := time.Duration(cfg.LLM.Timeout) * time.Second
if timeout <= 0 { timeout = 60 * time.Second } // :432-435
return llm.NewFailoverClient(configs, timeout, rate, burst) // :438
```

fallback provider 为空时默认 `"openai"`（`:417-419`）。全部失败时返回 `all N clients failed; last error: ...`，被拒绝的请求（ctx 已取消）不再触发 failover（见 `internal/llm/failover.go` 的 caller-abort 早退，FINAL-REVIEW R4）。注意：底层每个 client 的重试/熔断被显式关闭——failover 层拥有 provider 级切换语义，避免内部重试拖慢切换（independent-review F-08 的现设计意图）。

### 5.4 `wiringServeToolchain` — `serve_wiring.go:182`

装配工具面：公开 registry、MCP 桥、AKF 工具、原生命令发现、能力搜索、技能目录工具，以及带 planner bridge 的 `ToolBinder`。返回 `(toolBinder, registry, internalReg, err)`。

其中 **AKF 工具注册**单独提出来（`serve_wiring.go:268`）：

```go
if comp.KnowledgeRuntime != nil {
 akfSvc := akf_mcp.NewAKFService(comp.KnowledgeRuntime, &compiler.DefaultCompiler{})
 for _, akfTool := range akfSvc.Tools() {
 adapted := &akfToolAdapter{name: t.Name, desc: t.Description, fn: t.Execute}
 internalReg.Register(adapted)
 }
}
```

关键在 `comp.KnowledgeRuntime` 是第4.7节 建的**那一个共享实例**——`serve_wiring.go:197-202` 的注释点明：进化系统的 `KnowledgePatchExecutor` 和 agent 的 AKF 工具必须共用同一个 runtime，否则知识 genome 补丁改不到工具真正读的那份。

---

## 6. Peer Agent 装配

`serve.go:141`：

```go
subAgents, peerKernel, err := createAndServeAgents(ctx, cfg, internalReg, chatClient, toolBinder, comp, mgr)
```

### 6.1 `createAndServeAgents` — `cmd/ares/serve_peer.go:35`

先做进化相关的前置（`injectToolClassDAG` 把 L1 ToolClass 能力图注入进化系统），然后：

```go
subAgents, peerKernel, err := createPeerAgents(ctx, cfg, comp, chatClient, toolBinder, comp.EventStore, strategySrc, comp.ExpRepo)
if err != nil { return nil, nil, fmt.Errorf("create peer agents: %w", err) }
for _, sa := range subAgents {
 mgr.RegisterAgent(sa, factory)
}
wireLiveDAGAndCompile(ctx, cfg, comp, peerKernel, mgr)
wireEvolutionLoops(ctx, cfg, comp, peerKernel)
chaosStatus := wireIntrospectPanel(ctx, comp, peerKernel)
wireChaos(ctx, comp, cfg, peerKernel, ...)
if err := peerKernel.adopt(ctx, comp.SystemRuntime); err != nil { return nil, nil, err }
return subAgents, peerKernel, nil
```

### 6.2 `createPeerAgents` — `cmd/ares/agent_kernel.go:71`

```go
a := &peerAssembly{
 ...
 kernel: &kernelHandle{}, // ← 空壳，字段在下面 13 步里逐个填
 peers: normalizedPeers(cfg),
}
a.subAgents = createPeerSubAgents(a.peers, store)
if len(a.subAgents) == 0 {
 return nil, nil, errors.New("peer mode: no peer agents configured (agents.peers or agents.sub)")
}

if err := a.assembleFabric(); err != nil { return nil, nil, err }
a.wireDispatchAndScheduler()
a.wireEvolutionFeedback()
a.startCollabGC()
a.assembleAgentFabric()
if err := a.assembleExecution(); err != nil { return nil, nil, err }
a.wireSessionCleanup()
a.computeGovernance()
if err := a.spawnPeers(); err != nil { return nil, nil, err }
a.wireSyscalls()
a.injectPriorities()
a.startLoops()

return a.subAgents, a.kernel, nil
```

13 步顺序固定。挑几个关键的：

#### `wireDispatchAndScheduler` — `peer_assembly.go:111`

```go
kernel.executors = make(map[string]CapabilityExecutor, len(subAgents))
// 每个 peer 的完整能力集提供给 scorer
subCaps := append(subCaps, subAgentCapability{ID: p.ID, Type: typ, Caps: p.Capabilities...})

kernelDispatcher, kernelFlag := wireKernelDispatcher(subCaps)
kernel.dual = kernelDispatcher
kernel.flag = kernelFlag

tracker := newLoadTracker()
kernel.tracker = tracker

enableKernelExecution(kernel.dual, kernel.fabric)

sched := NewKernelScheduler(kernel.fabric, kernel.executors, tracker)
if store != nil { sched.WithEventStore(store) }
if cfg.Kernel.MaxConcurrent > 0 { sched.WithMaxConcurrent(cfg.Kernel.MaxConcurrent) }
if d := parseKernelPollInterval(cfg.Kernel.PollInterval); d > 0 { sched.PollInterval = d }
if ttl := parseKernelLoopConfig(cfg).LeaseTTL; ttl > 0 { sched.WithTTL(ttl) }

kernel.scheduler = sched
kernel.flipped = true
```

`NewKernelScheduler`（`cmd/ares/kernel.go:375`）就是 `kernel.New(...)` 的薄封装。

#### `assembleExecution` — `peer_assembly.go:288`

建**共享 L2 执行核**：

```go
exec, err := agentruntime.NewExecution(agentruntime.ExecutionConfig{
 Fabric: kernel.fabric,
 Agents: agents,
 ChatClient: chatClient,
 ToolBinder: toolBinder,
 StrategySource: strategySrc,
 L1DAG: l1DAG,
 MaxPlanDepth: resolveMaxPlanDepth(cfg.Kernel.DAGExecution),
 ReaperGrace: resolveReaperGrace(cfg.Kernel.DAGExecution),
 SessionIdleTTL: resolveSessionIdleTTL(cfg.Kernel.DAGExecution),
 CompileStore: store,
 PromptEnricher: resolveServePromptEnricher(cfg, comp.Memory, slog.Default()),
 Logger: slog.Default(),
})
...
kernel.sessionReg = exec.Sessions.Reg
kernel.compileCoord = exec.Compile
kernel.submitter = exec.Submitter
kernel.submitter.Seed(restoredSeq) // 跨重启 ID 防碰撞
```

注释强调：`NewExecution` 只允许在三处构建（`sdk/l2.go`、`cmd/ares/agent_kernel.go`、`cmd/ares/peer_assembly.go`），由 `sdk/arch_test.go:135` 锁定。

还会起一个 l2-reaper 后台循环（`peer_assembly.go:340`）——每个长出的节点都是 fabric 任务，fabric 自己不回收，没有这个循环内存任务表会单调增长。

#### `spawnPeers` — `peer_assembly.go:457`

```go
for _, sa := range subAgents {
 agents.Spawn(ctx, agentfabric.SpawnSpec{
 Identity: sa.ID(),
 Capabilities: peerCapabilities(toolBinder.ListTools()),
 // The execution body is always the L2 router
 CognitionFactory: func([]string) agentfabric.Cognition {
 return peerRouter
 },
 ExperiencePrior: loadExperiencePrior(ctx, expRepo, sa.ID()),
 Governance: agentGovernance,
 })
}
...
sched.WithGovernance(agents)
sched.WithAgentFabric(agents)
```

**所有 peer 的执行体都是同一个 `peerRouter`**（L2 router），不是各自独立的 ReAct 循环。

#### `startLoops` — `peer_assembly.go:565`

```go
schedCtx, schedCancel := context.WithCancel(ctx)
schedDone := make(chan struct{})
runBackground(ctx, comp, sysCompScheduler, func(context.Context) error {
 defer close(schedDone)
 sched.Run(schedCtx)
 return nil
})
kernel.schedulerStop = schedCancel
kernel.schedulerDone = schedDone
```

调度器主循环在这里启动。随后还有 recovery loop，其 kick 通道绑定到 scheduler 的 stale-winner 提示（`peer_assembly.go:600` 附近）。

### 6.3 `injectToolClassDAG` / `buildToolClassDAG` — `serve_peer.go:457` / `:568`

在 `createPeerAgents` **之前**跑（`serve_peer.go:53-58`），把 L1 ToolClass 能力图注入进化系统。

`:55-57` 注释点明了一条关键边界：**L1 图不编译进 task fabric——它是能力目录，不是执行计划（L1 ≠ L2）**。

```go
l1DAG, err := buildToolClassDAG(toolBinder.GetToolSchemas())
comp.NewEvolution.SetToolClassDAG(l1DAG)
```

一个节点 = 一个 ToolClass，键是 `toolName + "#" + argShape`（`:554-559` 注释）。argShape 是参数名的**排序集合**——所以按**类型签名**归一而非按值：`read_file(path=foo.txt)` 和 `read_file(path=bar.txt)` 折叠成同一个节点。

节点 Metadata 携带三个进化约束（`:548-552`，因为 `engine.Step.Metadata` 只支持 string，budget/prior 存字符串形式）：

```go
l1MetaEnabled = "enabled"
l1MetaBudget = "budget"
l1MetaPrior = "prior"
```

`:561-565` 注释解释了这条链的完整闭环：**genome 补丁改 L1 节点的 enabled/budget/prior → planner 在长出 L2 tool 节点前读它们 → L2 执行统计作为 fitness 流回**。

失败分档（`:462-470`）：`errNoToolSchemas` 是 `Info`（约束默认宽松），其余 `Warn` 但同样降级——**空图不注入**。

### 6.4 `wireLiveDAGAndCompile` — `serve_peer.go:92`

把配置的 agent 群体当作**活的工作流拓扑**，经共享编译协调器投影进任务织物。

`:99-104` 的注释解释了为什么必须有这一步——**它修的是一个真实历史 bug**：

> 没有它，workflow/recovery 补丁会永远改在合成的 `input→process→output` bootstrap DAG 上，"live promotion" 什么都观察不到。

这正是第4.10节 里那段"no live agent topology to act on"日志的**另一半**：serve 入口在这把它补上。

```go
liveDAG, dagErr := buildLiveAgentDAG(cfg)
mgr.RegisterAgentDAG(runtime.AgentDAGLiveKey, liveDAG)
comp.NewEvolution.UpdateLiveDAG(liveDAG)
```

`buildLiveAgentDAG`（`:497`）：一个 peer 一个节点，`AgentType` = 首个 capability（`:519-527`）；旧 `agents.sub` 条目声明的 `Dependencies` 会被搬过来（`:503-510`），所以老配置保持原有拓扑。没配 peer 时返回哨兵 `errNoLiveAgentDAG`（`:475`），调用方**匹配哨兵后保留 bootstrap 占位图**而不是注入空图（`:151-152`）。

**编译协调器的复用是个坑**（`:122-131`）：

```go
if peerKernel.compileCoord == nil {
 peerKernel.compileCoord = planprojection.NewCompileCoordinator(peerKernel.fabric, comp.EventStore)
}
```

注释说得很直白：**必须复用共享 L2 执行核心已建好的那个协调器**（`agentruntime.NewExecution`），在这里造第二个会把 per-session 图订阅分裂到 Sessions 持有的那个之外。

随后 `CompileDAG` 初编译 + `SubscribeGraphEvents` 订阅——**结构补丁（Insert/Remove/AddEdge）触发无需重启的重编译**（`:116-121` 注释）。最后把协调器挂成 lifecycle 的 `CompileInfoProvider`（`:139-149`），`/api/evolution/lifecycle` 才带得出归因三元组 `(generation, gates, compile_id)`——协调器因有 `CompileID`/`DAGVersion`/`CompileCount` 方法而直接满足接口。

### 6.5 `wireEvolutionLoops` — `serve_peer.go:162`

三条后台循环，原则都是注释里那句 **"Evolution decides; Kernel enforces"**。三条**共同的失败模式历史**也一样：**部署下去的策略没人消费**。

| 循环 | GA 发布的参数 | 施加到哪 | 位置 |
|---|---|---|---|
| quota | `quota.budget` | Agent Fabric 的资源准入预算 | `:176-185` |
| spawn gate | `spawn.{enabled,max_concurrent,preferred_capabilities}` | recovery 替换 spawn 的时序闸与能力偏好 | `:187-202` |
| population | `population.{spawn,retire}` | Agent Fabric 的 spawn/retire 原语，按 cadence 增减活体 | `:204-221` |

三条各自的注释都解释了"没有它会怎样"：

- **quota**：部署的预算被白白消耗——fabric 永远用启动配置里的预算（`:171-173`）
- **spawn gate**：部署的 spawn 策略没人消费，recovery 永远走普通 fabric spawn（`:192-194`）
- **population**：spawn gate 只塑形 RECOVERY 替换，**真正的顶层增减路径是缺失的**（`:209-211`）

spawn gate 有个例外要留意（`:190-191`）：**population cap 对 recovery 豁免**——自愈 spawn 不能被配额困死。

三条都是 best-effort：nil store → nil policy source → Apply 是 no-op，向后兼容（`:173-175`）。

### 6.6 `wireIntrospectPanel` — `serve_peer.go:228`

运行时内省面板。**pull-only**：collector 每 2s 把 latest-wins store 刷新一次（`:270-281`），handler 在 `GET /introspect` 供嵌入式 UI、`/api/v1/introspect/*` 供 JSON。

```go
collector := introspect.NewCollector(introspect.Sources{
 Kernel: peerKernel.scheduler.Snapshot,
 Fabric: peerKernel.fabric.LeaseSnapshot,
 Agents: peerKernel.agents.AgentsView,
 Chaos: chaosStatus.Snapshot,
 Tasks: peerKernel.fabric.TaskSnapshot,
 Decisions: peerKernel.scheduler.DecisionsSnapshot,
 Collab: collabReporter.Snapshot,
})
```

`:254-256` 有段诚实注释：**今天没有任何 producer 记录协作边，所以 Collab reporter 产出空图**。要先接上 producer（如 spawn/collaboration IPC 路径）才能开这个面板标签。

`:239-242` 是安全警告，值得原样记住：

> **introspect handler 是未认证的，其 eventstream 端点会暴露原始事件载荷（任务输入、checkpoint）。只应绑定到 localhost/内网，或置于认证反向代理之后——绝不要直接绑公网地址。**

`WithSystemRuntime`（`:265`）把 SystemRuntime 组件图也带进快照，所以一个"假 Ready"的内核在读面上看得见。

`chaosStatus` 在这里创建并返回，正是为了让 collector 和第6.7节 的 `wireChaos` **看到同一个 frame**（`:236-237` 注释）。

### 6.7 `wireChaos` — `serve_chaos_domain.go:165`

混沌注入子系统。`cfg.Kernel.Chaos.Enabled` 为假直接返回（`:173-176`）。

模式分档，**每一道降级都是拒绝武装而非勉强跑**：

| 条件 | 结果 |
|---|---|
| `mode=shadow` | 跑 `shadowSandboxLoop`（沙箱，不碰真实 agent） |
| `mode=live` 但 `allow_live=false` | 退回 shadow（`:196-200`） |
| live 但 `eligible_capabilities` 为空 | **拒绝武装**——空名单不得默认成"全是目标"（`:201-210`） |
| live 但 `stop_token` 为空 | **拒绝武装**——不带紧急停机凭证不许开（`:211-216`） |
| live 但 kernel handle 不全 | 退回 shadow（`:224-227`） |
| 未知 mode | 退回 shadow（`:229-232`） |

`:201-205` 的注释写得很直接：**live chaos 是危险的——它杀的是真生产 agent**。所以只有在显式确认**且**非空目标白名单都配好时才构造 Chaos harness。武装成功时打的是 `log.Warn`（`:223`），不是 Info。

`effectiveChaosMode`（`:239`）镜像 `wireChaos` 里的分支，**让面板报告真正生效的模式**，而不是配置里写的那个字符串——否则运维看到 `live`、实际跑 shadow，会误判。

**GA 静默窗口**：`wireChaos` 的 `gaActive` 参数是 `comp.NewEvolution.GAGenerationActive()`（`serve_peer.go:77-82`），live 循环据此**在一代 GA 运行中推迟注入**——避免把"进化自己造成的抖动"误判成故障。

---

## 7. 注册表 / 控制面 / Runtime 启动 / HTTP

回到 `runServe`：

```go
reg, err := setupPeerRegistry(ctx, g, subAgents, comp, peerKernel) // :151
if peerKernel != nil { peerKernel.peerRegistry = reg }

intelEngine, controlServer, err := setupServeControlPlane(...) // :164
if err := mgr.Start(ctx); err != nil { ... } // :170

// Sub-agents are execution units only (ares-runtime: agents are not
// orchestrated, they are scheduled). The Kernel owns dispatch: the
// kernelScheduler drives each task through RunQuantum →
// sub.Agent.ExecuteStep; agents never subscribe to the event stream and
// self-dispatch (self-dispatch was removed).

startServeHTTPAndHooks(ctx, g, cfg, cfgStore, controlServer, ...) // :186
return normalizeShutdownErr(g.Wait()) // :194
```

`normalizeShutdownErr`（`serve.go:200`）把 `context.Canceled` 视为正常退出返回 `nil`——Ctrl-C 不算失败。

### 7.1 `setupPeerRegistry` — `serve_peer.go:315`

P2P 消息注册表。有进化系统时桥接到 evolution-aware IPC，否则走普通直连通道：

```go
switch {
case comp.NewEvolution != nil:
 bridge, err := wireEvolutionIPC(subAgents, comp.NewEvolution.StrategyStore, comp.Observability.GlobalTracer, kernel)
 reg = bridge.reg
...
}
```

`wireEvolutionIPC` 在 `evolution.go:369`。

**死信可观测性**（`:330-357`）是这里最值得记的一段。bus 会记录每条投递失败/不可投递的请求，**但 store 原本没有 reader——失败在 error 返回边界上就消失了**。现在补了个 30s 的后台循环把计数和近期原因样本报出来：

```go
cur := bridge.DeadLetterCount()
if cur > 0 && cur != last {
 dl := bridge.ipc.Bus().DeadLetters().Snapshot()
 reasons := make(map[string]int, 4)
 for _, e := range dl { reasons[e.Reason]++ }
 slog.WarnContext(ctx, "peer mode: IPC dead letters retained ...", "count", cur, "reasons", reasons)
}
```

`:334-336` 注释说明了它**刻意 observe-only**：自动重投会把非瞬时失败也重试一遍，所以重投留给运维决定。

**`buildPeerRegistry`**（`:299`）有个诚实的 TODO（`:293-298`），必须原样记住：

> **没有任何生产 agent 再暴露 `SendMessage` 面——它随 sub.Agent 消息队列一起被移除了。所以这个注册表总是空的，非进化的 ask_agent 发送会以 "not registered" 大声失败**（等价于此前恒失败的 nil-queue 投递）。保留它是为了 discovery 契约；删掉就得重构非进化 ask_agent 分支。

也就是说第7节 开头那句 `peerKernel.peerRegistry = reg` 挂上去的，当前是个**契约占位**，不是活的通信路径。

### 7.2 `setupServeControlPlane` — `serve_wiring.go:356`

运行时内省控制面：智能引擎 + 只读控制服务器。`:353-355` 注释记录了迁移史——旧的 `MonitorPlugin` / tabs / PluginBus 桥都已删除，**内省面板（`internal/introspect`）是唯一的观测面**。

**智能引擎**（`:367-371`）从共享事件流打分健康度/检异常。喂数据的 goroutine（`:377-398`）独立于内省面板的 sink——**这个订阅只服务 health/anomalies/insights**。best-effort：订阅坏了只 Warn，引擎保持空（deny-by-default 健康度）。channel 关闭时直接 return 而非空转（`:389-393` 注释）。

**只读控制服务器**（`:400-444`）暴露 `/api/agents`、`/api/agents/:id`、`/api/health`、`/api/anomalies`、`/api/insights`。agent 数据来自 peer kernel 的 agent fabric；**kernel 不存在时端点报 503**——注释说"部分路径也必须能编译并服务"（`:400-403`）。

挂载的 option 一览（都是从已删除的 `:8090` dashboard 迁来的）：

| option | 内容 | 位置 |
|---|---|---|
| `WithIntel` | 智能引擎 | `:425` |
| `WithRuntimeConfig` | 脱敏配置快照 + 配置变更历史 | `:408-423` |
| `obs.IntrospectOptions()` | 进化轨迹 / 人工反馈 / 跨 fabric span | `:428-432` |
| `WithFlight` | flight recorder 的 timeline/summary/graph/decisions/diagnostics/genealogy | `:433-438` |
| `WithLifecycleSnapshot` | `/api/evolution/lifecycle` 的进化生命周期快照 | `:439-442` |

`evolutionLifecycleForServe`（`serve_wiring.go:343`）就是从这里取 lifecycle：控制面快照端点和 actionHandler 的审批端点**共用同一个实例**，避免两份状态各说各话。

### 7.3 `startServeHTTPAndHooks` — `serve_wiring.go:452`

先打印控制台横幅和安全姿态：

```go
fmt.Println("=== ARES Console — Live Runtime ===")
fmt.Printf("Console: http://%s/introspect\n", ...)
log.Info("serve: control-plane exposure state",
 "bind", addr, "wildcard_bind", isWildcardHost(cfg.Server.Host),
 "auth", authConfigured, "api_key", serveAPIKey != "",
 "introspect_token", cfg.Introspect.Token != "")
log.Info("serve: control-plane endpoint registry",
 "routes", len(actionRoutes), "none", ..., "read", ..., "write", ..., "local", ...)
```

`serveAPIKey` 来自环境变量 `ARES_API_KEY`（`serve_wiring.go:484`），为空时所有破坏性请求 deny-by-default。

然后构造 handler 和 HTTP server（`serve_wiring.go:540-576`）：

```go
handler := &actionHandler{
 inner: controlServer,
 cost: comp.LLM.CostDashboard, costMux: buildCostMux(...),
 mgr: mgr, tools: registry,
 apiKey: serveAPIKey, auth: authMW, readAuth: readAuthMW, audit: auditLogger,
 introspectToken: cfg.Introspect.Token,
 kernel: peerKernel,
 chaosStopToken: cfg.Kernel.Chaos.StopToken,
 intro: peerKernel.intro,
 lifecycle: evolutionLifecycleForServe(comp),
}

httpSrv := &http.Server{
 Addr: addr, Handler: handler,
 ReadTimeout: 15 * time.Second,
 WriteTimeout: 15 * time.Second,
 IdleTimeout: 60 * time.Second,
}
g.Go(func() error { return httpSrv.ListenAndServe() })
shutdownMgr.AddCallback(ares_shutdown.PhasePreShutdown, func(ctx context.Context) error {
 return httpSrv.Shutdown(ctx)
})
```

HTTP 就此监听。

`buildCostMux`（`agent.go:129`）挂在 `actionHandler` 上，与 `controlServer` 并列——cost 面是独立的一小片 mux，不走 introspect。

**关停链注册的顺序即执行的逆序基础**。`httpSrv.Shutdown` 注册在 `PhasePreShutdown`（`:1057-1059`），所以 HTTP 是**第一个**被优雅关闭的。加上第3节 的阶段预算，完整链路是：

```
第一个信号
 ├─ StartShutdown(30s)
 │ ├─ PhasePreShutdown (5s) → httpSrv.Shutdown ← HTTP 先停，不再收新请求
 │ ├─ PhaseGraceful (20s) → MCP / runtime 等
 │ ├─ PhaseForce (5s)
 │ └─ PhaseDone (1s)
 ├─ shutdownSystemRuntime(15s 独立预算) ← 逆拓扑：先停依赖者，最后关 EventStore
 │ └─ orch.Shutdown → 各组件 Stop（EventStore 的 Close 是叶子）
 └─ cancel() → comp.WaitBackground() ← 等蒸馏订阅 / GA ticker / LLM 建议循环退出
```

`comp.WaitBackground()`（`serve_wiring.go:93-96`）等的正是第4.6节 的蒸馏订阅、第4.11节 的 GA ticker 与 LLM 建议循环——**没有它，这些 goroutine 会活过优雅关闭**。

EventStore 的关闭顺序由第4.11节`wireSystemRuntime` 的依赖图决定（`:125-131` 注释）：它是依赖叶子，逆拓扑关停保证每个依赖它的组件先停，所以关掉它不会切断活跃写者。

---

## 8. HTTP 分发

### 8.1 `actionHandler.ServeHTTP` — `cmd/ares/agent.go:543`

```go
if r.Method == http.MethodPost && r.Body != nil {
 r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
}
for _, spec := range actionRoutes {
 if spec.Available != nil && !spec.Available(h) { continue }
 if !spec.match(r.Method, r.URL.Path) { continue }
 princ, ok := h.authorize(spec.Auth, w, r)
 if !ok { return }
 spec.Handler(h, w, r, princ)
 return
}
h.inner.ServeHTTP(w, r)
```

线性扫描路由表，按**声明顺序**匹配，第一个命中即处理。

### 8.2 路由表 — `agent.go:407`

`actionRoutes` 共 18 条，鉴权级别定义在 `agent.go:335-347`：

| 级别 | 含义 | 例子 |
|---|---|---|
| `authNone` | 公开 | `/introspect`（面板 HTML shell，无数据）、`/metrics` |
| `authRead` | 读权限 | `/api/v1/introspect/...`、`GET /api/tools`、`GET /api/mcp/tools` |
| `authWrite` | 写权限（破坏性） | `POST /api/tasks`、`POST /api/graphs`、`POST /api/tools/call`、`/api/chaos/...` |
| `authLocal` | 仅本地 | **当前无路由使用**（`agent.go:345` 注释：declared so the level exists） |

任务提交入口：

```go
{Method: "POST", Path: "/api/tasks", Auth: authWrite,
 Desc: "peer task submission (submitPeerTask)",
 Handler: (*actionHandler).routeSubmitTask},
```

最后两条是兜底：`* /api/...` 走读侧控制服务器，`* /...` 直接透传。

---

## 9. POST /api/tasks 提交

### 9.1 `routeSubmitTask` → `handleSubmitTask` — `agent_routes_tasks.go:23,56`

```go
func (h *actionHandler) handleSubmitTask(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
 w.Header().Set("Content-Type", "application/json")
 if h.kernel == nil {
 w.WriteHeader(http.StatusServiceUnavailable)
 writeJSON(w, map[string]any{"error": "peer runtime not active", "status": "error"})
 return
 }
 var req submitTaskRequest
 if err := json.NewDecoder(r.Body).Decode(&req); err != nil { ... 400 }
 if req.Capability == "" { ... 400 "capability is required" }
 if req.TenantID != "" {
 if req.Payload == nil { req.Payload = map[string]any{} }
 req.Payload["tenant_id"] = req.TenantID
 }
 taskID, err := submitPeerTask(r.Context(), h.kernel, req.Capability, req.Payload)
 ...
 w.WriteHeader(http.StatusAccepted)
 writeJSON(w, map[string]any{"task_id": taskID, "status": "submitted", ...})
}
```

注意返回 **202 Accepted**：提交是异步的，响应只确认受理，不等执行完成。

请求体结构（`agent_routes_tasks.go:38-49`）：

```go
type submitTaskRequest struct {
 Capability string `json:"capability"`
 Payload map[string]any `json:"payload"`
 TenantID string `json:"tenant_id,omitempty"`
}
```

`TenantID` 的注释明确当前是**调用方声明**，真正的多租户部署必须服务端从已认证主体派生。

### 9.2 `submitPeerTask` — `agent_kernel.go:186`

```go
if kernel == nil || kernel.submitter == nil || kernel.fabric == nil {
 return "", errors.New("peer mode: kernel fabric not wired")
}
taskID, _, err := kernel.submitter.Submit(ctx, capability, payload)
```

三层 nil 守卫后直接交给 submitter。

---

## 10. `Submitter.Submit` — `internal/agentruntime/submit.go:151`

这是提交链的核心，做七件事：

```go
// 1. 复制 payload（调用方仍持有原 map，不能被下面的盖戳改掉）
cp := make(map[string]any, len(payload)+1)
for k, v := range payload { cp[k] = v }
payload = cp

// 2. 解析 session_id / prompt / tenant_id
sessionID, _ = payload["session_id"].(string)
prompt, _ := payload["input"].(string)
tenantID, _ := payload["tenant_id"].(string)
if sessionID == "" {
 sessionID = fmt.Sprintf("sess-auto-%d", s.seq.Add(1))
 payload["session_id"] = sessionID
}

// 3. 能力归一到单一 L2 执行路径
if capability != PlanCapability {
 slog.InfoContext(ctx, "agentruntime: capability normalized to single L2 execution path",
 "from", capability, "to", PlanCapability, "session_id", sessionID)
 capability = PlanCapability
}

// 4. prompt 丰富（记忆折叠）
if s.enricher != nil && strings.TrimSpace(prompt) != "" {
 if enriched := s.enricher(ctx, sessionID, prompt); enriched != "" {
 prompt = enriched
 payload["input"] = prompt
 }
}

// 5. 会话准入
if err := s.sessions.Admit(ctx, sessionID, prompt); err != nil {
 return "", sessionID, err
}

// 6. 构造任务
taskID = fmt.Sprintf("peer-plan-%d", s.seq.Add(1))
env := taskfabric.NewCheckpointEnvelope(payload)
env.SessionID = sessionID
env.TenantID = tenantID
task := &taskfabric.Task{
 ID: taskID,
 Capability: capability,
 // Origin stays "" — this is a root task (user-submitted work)
 RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
 Checkpoint: env,
}

// 7. 入织物
if err := s.sessions.Fabric.Create(task); err != nil {
 return "", sessionID, fmt.Errorf("agentruntime: create task: %w", err)
}
```

第 3 步值得注意：**所有提交的能力都被归一成 `PlanCapability`**。`PlanCapability` 定义在 `agentruntime/submit.go:20`，是 `agentfabric.PlanCapability` 的别名，值为 `"ares/plan"`（`internal/fabric/agent/l2graph.go:78,90`）。原能力名只留在日志里。

`Origin` 留空表示这是用户提交的根任务；agent 自己创建的任务才从 `create_task` syscall 的工具上下文拿 Origin。

默认重试策略 `MaxRetries: 2`。

---

## 11. `Sessions.Admit` — `internal/agentruntime/session.go:104`

```go
if strings.Contains(sessionID, "/") {
 return fmt.Errorf("agentruntime: session id %q must not contain a slash", sessionID)
}
unlock := s.lockAdmission(sessionID)
defer unlock()
if _, err := s.Reg.GetSession(sessionID); err == nil {
 return nil // 已存在，幂等返回
}
if s.Compile == nil || s.Fabric == nil {
 return fmt.Errorf("agentruntime: cannot admit session %q without compile coordinator and fabric", sessionID)
}

liveCtx := context.WithoutCancel(ctx)
g, err := s.Reg.InitSession(sessionID, prompt, nil,
 func(subCtx context.Context, dag *engine.MutableDAG) (stop func()) {
 return compile.SubscribeGraphEvents(subCtx, dag)
 })
```

斜杠检查的理由在注释里：`SessionIDFromNode` 在第一个斜杠反解析，`"a/b"` 会让 reaper 把**活跃**会话的历史当死任务收割。

`liveCtx := context.WithoutCancel(ctx)`：屏蔽提交请求的取消，因为下面的 root 编译必须活过这次 HTTP 请求。

### 11.1 `InitSession` — `internal/fabric/agent/session_registry.go:103`

```go
if strings.Contains(sessionID, "/") { ... 同样的斜杠契约，单点强制 }
rootID := SessionRootID(sessionID) // "sess/" + sessionID + "/root"
g, err := NewL2Graph(rootID, prompt, params)

entry := &sessionEntry{graph: g}
entry.lastAccessNano.Store(time.Now().UnixNano())
if compileCoord != nil {
 // The subscription runs on a context the REGISTRY owns, never on a
 // caller-scoped one
 subCtx, cancel := context.WithCancel(context.Background())
 ...
}
```

ID 规则（`session_registry.go:263-273`）：

```go
func SessionRootID(sessionID string) string { return sessionIDPrefix + sessionID + "/root" }
func SessionNodeID(sessionID string, depth int, tool string, seq int) string {
 return fmt.Sprintf("sess/%s/d%d/%s#%d", sessionID, depth, tool, seq)
}
```

### 11.2 root 编译

回到 `Admit`（`session.go:165`）：

```go
rootStep := g.DAG().StepIndex()[g.Root()]
if _, err := s.Fabric.CompileNode(liveCtx, planprojection.ProjectStep(rootStep)); err != nil {
 if !errors.Is(err, taskfabric.ErrTaskExists) {
 s.ReleaseQuietly(sessionID)
 return ...
 }
 // 已存在的 TERMINAL root 属于同 ID 的上一个会话
 if stale, terr := s.Fabric.Task(g.Root()); terr == nil &&
 (stale.State == taskfabric.StateCompleted || stale.State == taskfabric.StateFailed) {
 n := Harvest(s.Fabric, sessionID)
 ...
 s.Fabric.CompileNode(liveCtx, planprojection.ProjectStep(rootStep))
 }
}
```

`ProjectStep`（`internal/fabric/planprojection/projection.go:47`）把 DAG 的 `engine.Step` 翻成 fabric 的 `PlanStep`：

```go
payload := map[string]any{"input": s.Input}
for k, v := range s.Metadata { payload[k] = v }
return taskfabric.PlanStep{
 ID: s.ID, Capability: s.AgentType, DependsOn: deps,
 MaxRetries: maxRetries, Priority: parsePriority(s.Metadata), ...
}
```

`CompileNode`（`fabric/task/workflow_plan.go:207`）是 `CompilePlan([]PlanStep{step})` 的单元素薄封装。

### 11.3 `Fabric.Create` — `internal/fabric/task/fabric_lifecycle.go:15`

```go
func (f *Fabric) Create(t *Task) error {
 strategyID := f.strategyStampID() // 锁外采样：stamp fn 是外部代码
 pending := make([]*pendingAppend, 0, 1)
 f.mu.Lock()
 defer f.flushAppends(&pending)
 defer f.mu.Unlock()
 return f.createLocked(t, strategyID, &pending)
}
```

`createLocked`（`:30`）：

```go
if t.ID == "" { return ErrTaskIDRequired }
if _, exists := f.tasks[t.ID]; exists { return ErrTaskExists }

cp := *t // 复制，织物持有独立实例
if len(t.Dependencies) > 0 {
 cp.Dependencies = append([]string(nil), t.Dependencies...) // 切片也要复制
}
cp.State = StateReady
cp.Owner = ""
cp.Lease = nil
cp.CreatedAt = f.now()
cp.UpdatedAt = cp.CreatedAt
stampStrategyAttribution(&cp, strategyID)
f.tasks[t.ID] = &cp
*pending = append(*pending, f.recordLocked(&cp, EventTaskCreated))
return nil
```

`recordLocked`（`fabric_events.go:46`）构造事件并入内存日志，同时返回一个 `pendingAppend`：

```go
ev := TaskEvent{Type: typ, TaskID: t.ID, AgentID: t.Owner, Origin: t.Origin,
 State: t.State, Checkpoint: t.Checkpoint, At: f.now()}
t.UpdatedAt = ev.At
f.events = append(f.events, ev)
// 内存日志有上限，超过 2×max 时压缩到 max
if f.store == nil { return nil }
// 必须持久化的事件带完整重建载荷
payload := map[string]any{
 restoreKeyTaskID: t.ID,
 restoreKeyAgentID: t.Owner,
 restoreKeyOrigin: t.Origin,
 restoreKeyState: string(t.State),
 // The fencing epoch rides on EVERY persisted event, not just the
 // must-persist ones
 restoreKeyEpoch: f.epoch,
}
```

注释解释为什么 epoch 要骑在**每一条**持久化事件上：`Acquire` 会 bump `f.epoch` 并记录 observability-only 的 `task.acquired`，若 epoch 只带在 must-persist 事件上，最后一次 checkpoint 之后发出的每个 token 都会丢失，重建后的织物会**重新发放**它们，崩溃前的僵尸持有者就能通过 `ownerLocked` 的 epoch 检查。

`flushAppends`（`fabric_events.go:200`）保证并发的织物调用按**记录顺序**落库：

```go
for p.seq > f.flushedSeq+1 {
 if !time.Now().Before(deadline) { orderTimedOut = true; break }
 timer := time.AfterFunc(time.Until(deadline), f.flushCond.Broadcast)
 f.flushCond.Wait()
 timer.Stop()
}
```

超时路径（`:222`）的处理是：推进 `flushedSeq` 让屏障不被永久毒化，然后 must-persist 事件**乱序落库**而不是丢弃——注释写明"Durability outranks causal order"。

至此，第一个任务（session root）以 `READY` 状态进入织物，`task.created` 事件已记录。

---

## 12. 调度器主循环

### 12.1 `Scheduler.Run` — `internal/kernel/scheduler.go:253`

```go
s.running.Store(true)
defer s.running.Store(false)

pollInterval := s.PollInterval
if pollInterval <= 0 { pollInterval = s.preemptInterval() }
ticker := time.NewTicker(pollInterval)
defer ticker.Stop()

// 协作式抢占的独立扫描器
preemptTicker := time.NewTicker(s.preemptInterval())
go func() {
 for {
 select {
 case <-ctx.Done(): return
 case <-preemptTicker.C:
 func() {
 defer func() { if r := recover(); r != nil { ... } }()
 s.PreemptLowerPriority(s.fabric.ResumableTasks())
 }()
 }
 }
}()

// 事件订阅（有 store 时）
var events <-chan *ares_events.Event
if s.eventStore != nil {
 ch, err := s.eventStore.Subscribe(ctx, ares_events.EventFilter{
 Types: []ares_events.EventType{
 ares_events.EventTaskCreated,
 ares_events.EventTaskReady,
 ares_events.EventTaskCompleted,
 ares_events.EventTaskFailed,
 ares_events.EventTaskYielded,
 },
 })
 ...
}

for {
 select {
 case <-ctx.Done(): return
 case <-ticker.C: s.safeDrain(ctx)
 case _, ok := <-events:
 if !ok { events = nil; continue } // 关闭后退化为纯轮询
 s.safeDrain(ctx)
 }
}
```

两条触发路径：**定时轮询** + **事件驱动**（`created/ready/completed/failed/yielded` 到达即刻 drain，不必等下一个 tick）。

抢占扫描器独立成 goroutine 的原因写在 `:274` 的注释里：`drain()` 会 `wg.Wait()` 阻塞到所有已派发量子结束，只在 drain 入口检查抢占永远观察不到 RUNNING 任务——那个分支在生产循环里不可达。

### 12.2 `safeDrain` → `drain` — `scheduler_dispatch.go:25,43`

```go
func (s *Scheduler) safeDrain(ctx context.Context) {
 defer func() { if r := recover(); r != nil { log.Error("kernel scheduler: panic in drain, continuing", "panic", r) } }()
 s.drain(ctx)
}
```

`drain` 本体：

```go
s.reconcileFabricDeaths() // 清理 fabric 里已死但静态注册还在的僵尸 executor

tasks := s.fabric.ResumableTasks()
if len(tasks) == 0 { return }

s.PreemptLowerPriority(tasks) // 抢占：READY 任务优先级高于 RUNNING 的，协作式让出

sem := make(chan struct{}, s.drainLimit())
var wg sync.WaitGroup
drainLoop:
for _, taskID := range tasks {
 select {
 case <-ctx.Done(): break drainLoop
 default:
 }
 select {
 case sem <- struct{}{}:
 case <-ctx.Done(): break drainLoop
 }
 wg.Add(1)
 go func(id string) {
 defer wg.Done()
 defer func() { <-sem }()
 defer func() { if recover() != nil { log.Error(...) } }()
 if err := s.execute(ctx, id); err != nil { s.logFailure(id, err) }
 }(taskID)
}
wg.Wait()
```

`ResumableTasks`（`fabric/task/dag.go:56`）：

```go
for id, t := range f.tasks {
 switch t.State {
 case StateReady:
 if depsCompletedLocked(f.tasks, t.Dependencies) { out = append(out, id) }
 case StateSuspended:
 if t.Lease != nil && !t.Lease.IsExpired(f.now()) { out = append(out, id) }
 }
}
```

两类可跑：依赖已全部 COMPLETED 的 READY，以及租约未过期的 SUSPENDED（上一个量子 yield 出来的）。

`drainLimit`（`:120`）的回退链：显式 `WithMaxConcurrent` > max(静态 executor 数, fabric 活跃候选数)，下限 1 上限 32。注释解释为什么不能只取静态注册数——peer 模式下静态注册**按设计为空**，只取它会让并行度塌到 1。

`wg.Wait()` 是关闭诚实性的关键（`:84` 注释）：`Run()` 一返回就清 `s.running`，如果这里直接 return，关闭会在 goroutine 仍持有租约、仍在改织物状态时完成。

---

## 13. `execute` → `executeWithCandidates`

### 13.1 `execute` — `scheduler_execute.go:52`

```go
execs := s.allExecutors()
if boundID, bound := s.boundFor(taskID); bound {
 // 恢复绑定：绑定到本任务的替换体是唯一候选
 if agent, ok := execs[boundID]; ok && agent != nil {
 if tk, tkErr := s.fabric.Task(taskID); tkErr == nil &&
 taskfabric.CapabilityOverlap(tk.Capability, []string{string(agent.Type())}) > 0 {
 cands = append(cands, taskfabric.Candidate{...})
 }
 }
 if len(cands) == 0 { return s.executeUnbound(ctx, taskID) }
 return s.executeWithCandidates(ctx, taskID, cands)
}
return s.executeUnbound(ctx, taskID)
```

正常路径（无恢复绑定）走 `executeUnbound` → `executeWithCandidates(ctx, taskID, s.buildCandidates(taskID))`。

### 13.2 `buildCandidates` — `executor_registry.go:129`

```go
for agentID, agent := range execs {
 if agent == nil { continue }
 if s.isBoundToAnyTask(agentID) { continue }
 // peer 模式下 fabric 活跃population 是唯一候选源
 if s.agents != nil && !s.hybridStatic { continue }
 cands = append(cands, taskfabric.Candidate{
 AgentID: agentID,
 Capabilities: []string{string(agent.Type())},
 Load: s.tracker.Load(agentID),
 Confidence: s.tracker.Confidence(agentID),
 Priority: s.tracker.Priority(agentID),
 })
}
if s.agents != nil && s.hybridStatic {
 return s.hybridPreferStatic(taskID, cands, s.appendFabricCandidates(nil, execs))
}
return s.appendFabricCandidates(cands, execs)
```

peer 模式（`s.agents != nil && !hybridStatic`）下静态注册被跳过，候选全部来自 fabric 的活 agent。

### 13.3 `executeWithCandidates` — `scheduler_execute.go:149`

这是最长的一段，按顺序：

```go
tk, err := s.fabric.Task(taskID)
if len(cands) == 0 {
 return apperrors.Kernel("schedule", "no_capable_candidate", taskID, "", taskfabric.ErrNoCapableCandidate)
}

// 预算过滤：在 Schedule 发租约之前就把预算/期限耗尽的候选剔掉
cands = s.filterBudgetAffordable(cands)
if len(cands) == 0 { return ... ErrNoCapableCandidate }

// 按 (agentID, 任务能力) 重新解析置信度
prior := s.fabric.PriorConfidence(tk.Capability)
for i := range cands {
 conf, measured := s.tracker.ConfidenceForMeasured(cands[i].AgentID, tk.Capability)
 switch {
 case measured: cands[i].Confidence = conf
 case prior > 0: cands[i].Confidence = 0 // 让 Schedule 用先验填充
 default: cands[i].Confidence = conf // 中性先验
 }
}

winner, epoch, err := s.fabric.Schedule(taskID, cands, s.ttl)

// 决策记录（成功失败都记，面板要能解释"为什么没调度"）
if s.decisions != nil { ... s.decisions.Record(d) }
if err != nil { return err }

// 解析 executor：先 fabric，后静态注册
var executor CapabilityExecutor
if s.agents != nil { executor = s.fabricExecutor(winner) }
if executor == nil {
 executor, ok = s.LookupExecutor(winner)
 if !ok || executor == nil { return s.handleStaleWinner(taskID, winner, epoch) }
}

// 抓取当前 checkpoint 作为 meta，跨 yield→resume 保住 UserProfile
meta, decodeErr := taskfabric.DecodeCheckpoint(tk.Checkpoint)

// 量子前预算门
if !s.budgetOK(winner) {
 s.fabric.Release(taskID, winner, epoch)
 return nil
}
// 准入门：原子抢占忙槽
if !s.tracker.TryBegin(winner, maxConcurrentPerAgent) {
 s.fabric.Release(taskID, winner, epoch)
 return nil
}
```

准入门那段的注释说明了为什么必须**原子**：候选快照在 Schedule 之前建，两个并发 drain goroutine 可能都看到同一个 agent 空闲，给它两个任务——一个 agent 进程上跑两个量子，其认知状态不可重入。

然后是 panic 守卫、量子边界钩子、租约心跳：

```go
slotReleased := false
var stopHeartbeat func()
defer func() {
 if r := recover(); r != nil {
 if stopHeartbeat != nil { stopHeartbeat() }
 if !slotReleased { s.tracker.EndNeutral(winner); slotReleased = true }
 }
}()

s.beforeQuantum(ctx, taskID, winner)
stopHeartbeat = s.startLeaseHeartbeat(ctx, taskID, winner, epoch)

quantumStart := time.Now()
var usage quantumUsage
err = s.fabric.RunQuantum(taskID, winner, epoch, s.buildQuantumStep(ctx, executor, tk, meta, &usage))
quantumLatency := time.Since(quantumStart)
retries := quantumRetries(err)

stopHeartbeat()
s.afterQuantum(ctx, taskID, winner, err)
s.endQuantumOutcome(winner, tk.Capability, taskID, err, quantumLatency, retries)
slotReleased = true

if s.governance != nil { s.consumeBudget(winner, usage.tokens) }
if err == nil {
 // 数任务不数量子
 if tkEnd, tkErr := s.fabric.Task(taskID); tkErr == nil && tkEnd.State == taskfabric.StateCompleted {
 s.Scheduled.Add(1)
 }
}
s.unbindRecoveryExecutorAfterTerminal(taskID)
return err
```

`startLeaseHeartbeat`（`:365`）以 `ttl/3`（下限 5s）续约，防止长量子被租约过期重排导致同一任务被并发执行两次。

---

## 14. `Schedule` → `Pick` → `Acquire`

### 14.1 `Fabric.Schedule` — `fabric_schedule.go:24`

```go
t, err := f.Task(taskID)

// 经验先验填充：候选没声明置信度时用 ConfidenceSource 补
f.mu.Lock(); src := f.confidence; f.mu.Unlock()
if src != nil {
 if conf := src.Confidence(t.Capability); conf > 0 {
 for i := range candidates {
 if candidates[i].Confidence <= 0 { candidates[i].Confidence = conf }
 }
 }
}

best := Pick(t.Capability, candidates)
if best == nil { return "", 0, ErrNoCapableCandidate }

epoch, err := f.Acquire(taskID, best.AgentID, ttl)
return best.AgentID, epoch, nil
```

### 14.2 `Score` / `ScoreBreakdown` — `fabric/task/scheduler.go:44,71`

```go
func ScoreBreakdown(taskCapability string, c Candidate) ScoreParts {
 overlap := CapabilityOverlap(taskCapability, c.Capabilities)
 load := clamp01(c.Load)
 conf := clamp01(c.Confidence)
 boost := 1.0
 if c.Priority > 0 { boost = 1.0 + c.Priority }
 parts := ScoreParts{Overlap: overlap, Load: load, Confidence: conf, PriorityBoost: boost}
 if overlap > 0 {
 parts.Score = overlap * (1 - load) * conf * boost
 }
 return parts
}
```

**评分公式**：`能力重叠 × (1 - 负载) × 置信度 × 优先级加成`，能力不重叠直接 0。

`ScoreParts` 的存在理由写在 `:48`：观测消费者（调度器的决策记录器）要渲染**调度器实际排序用的**那个分解，不允许重算任何因子，否则解释和决策会漂移。

### 14.3 `CapabilityOverlap` — `scheduler.go:147`

```go
trimmed := strings.TrimSpace(required)
if trimmed == "" { return 1.0 } // 无约束 → 任何候选都行

// 精确整链匹配短路
for _, h := range have {
 if strings.TrimSpace(h) == trimmed { return 1.0 }
}
// 否则按 "/" 分段做前缀比例
```

注释解释为什么要精确匹配短路：`"tool/write"` 这种带命名空间的能力，只按前缀规则会被记 0.5 分，和所有 `tool/*` 兄弟打平，胜负交给 map 迭代顺序——会把 `"tool/write"` 任务误派给 `"tool/research"` executor。

### 14.4 `Pick` — `scheduler.go:98`

```go
for i := range candidates {
 c := &candidates[i]
 s := Score(taskCapability, *c)
 if s > 0 && (best == nil || s > bestScore) { best = c; bestScore = s }

 overlap := CapabilityOverlap(taskCapability, c.Capabilities)
 if overlap <= 0 { continue }
 fb := overlap * (1 - clamp01(c.Load)) // 无置信度的兜底层
 if c.Priority > 0 { fb *= 1 + c.Priority }
 if fb <= 0 { continue }
 if lastResort == nil || fb > resortScore { lastResort = c; resortScore = fb }
}
if best != nil { return best }
return lastResort
```

两层：正常评分选出的 `best`，和一个**不看置信度**的 `lastResort` 兜底。满负载（或零容量）的 agent 在两层里都不可达。

### 14.5 `Acquire` — `fabric_lifecycle.go:78`

```go
t, ok := f.tasks[id]
if !ok { return 0, ErrTaskNotFound }
if agentID == "" { return 0, ErrAgentIDRequired }
if t.State != StateReady && t.State != StateSuspended { return 0, ErrTaskNotReady }

f.epoch++ // ← fencing token 递增
lease := Lease{
 Owner: agentID,
 ExpiresAt: f.now().Add(ttl), // 用织物时钟，不用墙钟
 Epoch: f.epoch,
}
if err := t.transition(StateLeased); err != nil { return 0, err }
t.Owner = agentID
t.Lease = &lease
pending = append(pending, f.recordLocked(t, EventTaskAcquired))
return lease.Epoch, nil
```

返回的 `epoch` 就是 fencing token，后续所有携带所有权的操作都要出示它。

租约用 `f.now()` 而非墙钟的理由在注释里：过期判定用的是 `f.now()`（`CheckExpiredLeases`），混用两套时钟会让租约一出生就过期。

---

## 15. `RunQuantum` — `internal/fabric/task/quantum.go:72`

```go
func (f *Fabric) RunQuantum(taskID, agentID string, epoch uint64, step QuantumStep) error {
 if err := f.Start(taskID, agentID, epoch); err != nil { return err }

 // 量子计数在 step 跑之前累加
 f.mu.Lock()
 if t, ok := f.tasks[taskID]; ok { t.Quantum++; t.UpdatedAt = f.now() }
 f.mu.Unlock()

 checkpoint, done, stepErr := runStepRecovered(step)
 if stepErr != nil {
 if isCancellation(stepErr) {
 // 取消不是失败：Release 回 READY，不烧重试预算，保留 checkpoint
 if releaseErr := f.Release(taskID, agentID, epoch); releaseErr != nil {
 return errors.Join(stepErr, releaseErr)
 }
 return stepErr
 }
 if failErr := f.Fail(taskID, agentID, epoch); failErr != nil {
 return errors.Join(stepErr, failErr)
 }
 return stepErr
 }
 if done {
 if checkpoint != nil {
 return f.CompleteWithCheckpoint(taskID, agentID, epoch, checkpoint)
 }
 return f.Complete(taskID, agentID, epoch)
 }
 return f.Yield(taskID, agentID, epoch, checkpoint)
}
```

四种出口：

| step 结果 | 织物动作 | 状态 |
|---|---|---|
| `err` 且是 `context.Canceled` | `Release` | READY（无主，checkpoint 保留，重试预算不动） |
| `err` 其他 | `Fail` | 有重试额度 → READY；否则 FAILED + 级联 |
| `done` 且有 checkpoint | `CompleteWithCheckpoint` | COMPLETED |
| `done` 无 checkpoint | `Complete` | COMPLETED |
| `!done` | `Yield` | SUSPENDED（checkpoint 保留） |

`isCancellation`（`:121`）只认 `context.Canceled`：

```go
func isCancellation(err error) bool { return errors.Is(err, context.Canceled) }
```

注释说明 `DeadlineExceeded` **故意不算**取消——跑爆自己预算的 step 是真失败，重试策略必须看见。

`runStepRecovered`（`:129`）是 panic 边界：step 里 panic 被就地转成 error，让织物状态机走正常的 Fail/requeue 路径，而不是把任务留在 RUNNING 且租约还活着直到 TTL 过期。

`Start`（`fabric_lifecycle.go:113`）：

```go
t, err := f.ownerLocked(id, agentID, epoch) // epoch 校验在这
if err := t.transition(StateRunning); err != nil { return err }
pending = append(pending, f.recordLocked(t, EventTaskStarted))
```

`ownerLocked`（`fabric.go:304`）是 fencing 的落地点：校验当前持有者和 epoch 都匹配，否则拒绝。

`Fail`（`fabric_lifecycle.go:213`）的重试分支：

```go
t.RetryPolicy.Attempts++
if t.CanRetry() {
 t.transition(StateReady)
 // 记失败事件时失败 agent 还挂着，终态事件不能丢掉行动者
 pending = append(pending, f.recordLocked(t, EventTaskFailed))
 t.Owner = ""
 t.Lease = nil
 pending = append(pending, f.recordLocked(t, EventTaskReady))
 return nil
}
t.transition(StateFailed)
pending = append(pending, f.recordLocked(t, EventTaskFailed))
f.cascadeFailureLocked(t.ID, &pending) // 终态失败向下级联
```

`CompleteWithCheckpoint`（`:190`）的顺序有讲究：

```go
// The state transition is validated BEFORE the checkpoint is written
if err := t.transition(StateCompleted); err != nil { return err }
t.Checkpoint = checkpoint
pending = append(pending, f.recordLocked(t, EventTaskCompleted))
```

注释解释：先写 checkpoint 再校验转移，会让一个从未完成的任务带着"已完成那次的结果"。

---

## 16. `buildQuantumStep` — `scheduler_quantum.go:38`

`RunQuantum` 执行的 step 闭包在这里构造：

```go
return func() (any, bool, error) {
 type stepResult struct { out *sub.StepOutcome; err error }
 done := make(chan stepResult, 1)
 go func() {
 defer func() {
 if r := recover(); r != nil {
 done <- stepResult{err: apperrors.Kernel("run_quantum", "executor_panic", ...)}
 }
 }()
 // 任务的租户骑在量子的 context 上
 mt := s.ToModelTask(tk)
 out, stepErr := executor.ExecuteStep(tenantctx.With(ctx, mt.TenantID), mt)
 done <- stepResult{out: out, err: stepErr}
 }()

 var out *sub.StepOutcome
 var stepErr error
 select {
 case res := <-done: out, stepErr = res.out, res.err
 case <-ctx.Done(): return nil, false, fmt.Errorf("quantum aborted by scheduler shutdown: %w", ctx.Err())
 }

 if stepErr != nil { return nil, false, stepErr }
 if out == nil { return nil, false, ... ErrNilStepOutcome }
 if out.Result != nil && out.Result.Error != "" {
 return nil, false, apperrors.Kernel("run_quantum", "step_error", ...)
 }
 if usage != nil && out.Result != nil {
 usage.tokens = tokenUsageFromResult(out.Result, "input") + tokenUsageFromResult(out.Result, "output")
 }

 if !out.Done {
 // Yield：保住 meta，累加 token
 return taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
 UserProfile: meta.UserProfile, Payload: meta.Payload,
 UsedExperienceID: meta.UsedExperienceID, StrategyID: meta.StrategyID,
 SessionID: meta.SessionID, StepCheckpoint: out.Checkpoint,
 InputTokens: meta.InputTokens + tokenUsageFromResult(out.Result, "input"),
 OutputTokens: meta.OutputTokens + tokenUsageFromResult(out.Result, "output"),
 }), false, nil
 }
 // Done：把 worker 的真实输出装进 checkpoint
 outMap := map[string]any{"result": "ok"}
 if res := out.Result; res != nil {
 if items := res.Items; len(items) > 0 { outMap["items"] = items }
 if res.Reason != "" { outMap["reason"] = res.Reason }
 if len(res.Metadata) > 0 { outMap["metadata"] = res.Metadata }
 }
 return taskfabric.EncodeCheckpoint(... StepCheckpoint: outMap ...), true, nil
}
```

step 跑在**独立 goroutine** 里，理由在 `:46` 注释：卡住的 executor 不能永久阻塞 drain 关闭；ctx 取消时量子立刻返回 error，被遗弃的 step goroutine 等 executor 自己返回后丢弃结果（fencing 会拒绝任何迟到的完成）。

`tenantctx.With(ctx, mt.TenantID)`：租户骑在量子 context 上，下游每个认知体、工具调用、知识查询都从 `tenantctx.From(ctx)` 解析租户，而不是读进程全局。

### 16.1 `ToModelTask` — `scheduler_quantum.go:272`

把织物任务映射回 executor 期望的 `models.Task`：

```go
t := models.NewTask(tk.ID, models.AgentType(tk.Capability), nil)
dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint)
t.UserProfile = reifyUserProfile(dc.UserProfile)
// Payload 必须是副本，不能是信封 map 的别名
t.Payload = copyPayloadMap(dc.Payload)
t.UsedExperienceID = dc.UsedExperienceID
t.StrategyID = dc.StrategyID
```

注释标明别名问题是 audit HIGH #1：executor 会往 `t.Payload` 写，别名会把量子作用域的 key 永久污染进持久化的 checkpoint 信封。

---

## 17. Executor → Cognition

### 17.1 `fabricAgentExecutor.ExecuteStep` — `kernel/fabric_executor.go:60`

```go
a, err := e.agents.Get(e.id)
out, err := a.ExecuteStep(ctx, task)
return &sub.StepOutcome{Done: out.Done, Checkpoint: out.Checkpoint, Result: out.Result}, nil
```

类型转换注释（`:28-31`）说明这是把 live fabric agent 适配到调度器的 `CapabilityExecutor` 契约——"a spawned fabric agent is a REAL executor — not a phantom"。

### 17.2 `Agent.ExecuteStep` — `fabric/agent/executor.go:105`

```go
a.mu.RLock(); c := a.cognition; a.mu.RUnlock()
if c == nil { return nil, ErrAgentNotExecutable }
if task != nil { task.Payload = withExecutingAgent(task.Payload, a.Identity) }
return c.ExecuteStep(ctx, task)
```

`withExecutingAgent` 盖的是 `executing_agent_id`（`executor.go:125`）。注释说明：一个共享 Cognition 服务所有 agent，任务是携带执行者身份的唯一载体，也是读执行者认知状态（spawn 时的经验先验）的 join key。

### 17.3 `routerCognition.ExecuteStep` — `fabric/agent/l2graph.go:375`

按**任务能力**分发到具体认知体：

```go
name := string(task.AgentType)
switch {
case strings.HasPrefix(name, "tool/"):
 tool := strings.TrimPrefix(name, "tool/")
 return (&toolCognition{tool: tool, binder: r.binder, logger: r.logger}).ExecuteStep(ctx, task)
case name == answerAgentType: // "ares/answer"
 return (&answerCognition{...}).ExecuteStep(ctx, task)
case name == planAgentType: // "ares/plan"
 if r.planner != nil { return r.planner.ExecuteStep(ctx, task) }
 return nil, fmt.Errorf("agentfabric: plan node %q has no planner cognition", name)
case name == rootAgentType: // "ares/root"
 return (&rootCognition{}).ExecuteStep(ctx, task)
default:
 return nil, fmt.Errorf("agentfabric: unsupported L2 capability %q", name)
}
```

四个认知体，能力常量在 `l2graph.go:78-84`：`"ares/plan"` / `"ares/answer"` / `"ares/root"`，工具节点是 `"tool/" + 工具名`。

---

## 18. 四个认知体的行为

### 18.1 `rootCognition` — `l2graph.go:421`

零工作量的准入量子：

```go
func (c *rootCognition) ExecuteStep(_ context.Context, task *models.Task) (*StepOutcome, error) {
 prompt, _ := task.Payload["input"].(string)
 result := models.NewTaskResult(task.TaskID, task.AgentType)
 result.SetSuccess([]*models.RecommendItem{{ItemID: task.TaskID, Content: prompt}}, "session admitted")
 return &StepOutcome{Done: true, Result: result}, nil
}
```

它把 session prompt 作为 root 的输出发出去——这样 prompt 活在 root 任务的信封里，planner 可以通过和读任何前驱输出**相同的 ID-join** 读到它，而不是只活在图 Metadata 里。

类型注释解释为什么 root 也要编译成 fabric 任务：否则 tool 节点的 `ResolveDeps` 解析不到它（`CompileNode` 拒绝悬空依赖）。

### 18.2 `toolCognition` — `l2graph.go:444`

单次工具调用，单个量子完成：

```go
ctx = kctx.WithCallerID(ctx, executingAgentID(task.Payload))
res, err := c.binder.CallTool(ctx, c.tool, argsFromPayload(task.Payload))
result := models.NewTaskResult(task.TaskID, task.AgentType)
result.SetSuccess([]*models.RecommendItem{{ItemID: task.TaskID, Content: stringify(res)}}, "tool "+c.tool+" completed")
return &StepOutcome{Done: true, Result: result}, nil
```

`WithCallerID` 的注释强调：spawn 谱系和 `Task.Origin` 从 context 取，**绝不从 LLM 传的参数取**。

`argsFromPayload` 只提取 `arg.` 前缀命名空间下的 key，信封管道（`"input"`、scheduler 恢复 key）永远到不了 `CallTool`，所以严格 schema（`additionalProperties:false`）的工具能接受这个调用。

### 18.3 `plannerCognition.ExecuteStep` — `planner_cognition.go:235`

这是生长图的地方，也是唯一打 LLM 的地方：

```go
sessionID := task.SessionID
g, err := c.sessions.GetSession(sessionID)

depth := g.PlanDepth()
if depth >= c.maxDepth {
 c.forcedAnswers.Add(1)
 return c.growAnswerNode(ctx, g, task, "max plan depth reached", nil)
}

prompt, err := c.assembleContext(ctx, task, g) // 从前驱路径组装上下文

if priors := c.l1Priors(); len(priors) > 0 {
 prompt = append(prompt, &llmcore.LLMMessage{
 Role: roleSystem,
 Content: "evolution priors (hints only, tool choice stays with you):\n- " + strings.Join(priors, "\n- "),
 })
}

llmParams := map[string]any{}
if st := c.activeStrategy(ctx); st != nil {
 if strings.TrimSpace(st.Prompt) != "" {
 prompt = append(prompt, &llmcore.LLMMessage{Role: roleSystem,
 Content: "evolution strategy (deployed " + st.ID + "):\n" + st.Prompt})
 }
 for k, v := range st.Params { llmParams[k] = v }
}

var llmTools []llmcore.Tool
if c.binder != nil {
 schemas := c.binder.GetToolSchemas()
 llmTools = make([]llmcore.Tool, 0, len(schemas))
 for _, s := range schemas { llmTools = append(llmTools, toCoreTool(s)) }
}

resp, err := c.chat.Chat(ctx, prompt, llmTools, llmParams)

if len(resp.ToolCalls) == 0 {
 return c.growAnswerNode(ctx, g, task, resp.Content, resp) // 终局
}
grown, err := c.growToolNodes(ctx, g, task, resp.ToolCalls, sessionID)
if grown == 0 {
 return c.growAnswerNode(ctx, g, task, resp.Content, resp) // 全被 L1 约束跳过
}
result.SetSuccess(nil, "planner grew "+strconv.Itoa(grown)+" tool nodes")
result.Metadata = tokenUsageMetadata(resp)
return &StepOutcome{Done: true, Result: result}, nil
```

**planner 不执行工具，只生长图。** LLM 的 tool calls 变成图上的节点，由后续量子执行。

`activeStrategy` 的注释说明错误被吞掉：策略 store 缺失不破坏生长，降级为无引导。

### 18.4 `growToolNodes` — `planner_cognition.go:626`

```go
round := stableRound(g, task.TaskID)
prev := task.TaskID
if !g.HasNode(prev) { prev = g.Root() }

for seq, tc := range toolCalls {
 toolName := tc.Function.Name
 if !c.isToolEnabled(toolName) { continue } // L1 约束
 if !c.toolBudgetRemaining(g, toolName) { continue }

 nodeID := SessionNodeID(sessionID, round, toolName, seq)
 if g.HasNode(nodeID) { prev = nodeID; grown++; continue } // 幂等

 args := map[string]any{}
 if tc.Function.Arguments != "" { json.Unmarshal([]byte(tc.Function.Arguments), &args) }

 metadata := args
 if metadata == nil { metadata = map[string]any{} }
 metadata[planMetadataKey] = sessionID
 // UNCONDITIONAL, exactly like session_id: an LLM-supplied "tenant_id"
 // tool argument must never survive into the grown node's envelope
 metadata[tenantMetadataKey] = task.TenantID

 g.AddToolNode(ctx, nodeID, toolName, metadata, prev)
 prev = nodeID // 同轮内工具串行链式
 grown++
}

newPlanID := SessionNodeID(sessionID, round, "plan", 0)
if planExists := g.HasNode(newPlanID); !planExists && grown > 0 {
 g.AddToolNode(ctx, newPlanID, "plan", planArgs, prev)
}
```

`tenant_id` 的**无条件覆盖**是防伪造：LLM 传的 `tenant_id` 工具参数绝不能活进长出的节点信封——条件盖戳下，无租户任务会让模型自己选择其工作执行和知识召回所用的租户。

`stableRound`（`:616`）从 `taskID` 派生轮次而不是用 `PlanDepth`——后者会随后续轮次增长，重执行的量子会拿到不同的 answer ID，产生重复分支。

### 18.5 `growAnswerNode` — `planner_cognition.go:819`

```go
answerID := SessionNodeID(task.SessionID, stableRound(g, task.TaskID), "answer", 0)

if !g.HasNode(answerID) {
 pred := task.TaskID
 if !g.HasNode(pred) { pred = g.Root() }
 args := map[string]any{"content": content, planMetadataKey: task.SessionID}
 if task.TenantID != "" { args[tenantMetadataKey] = task.TenantID }
 g.AddToolNode(ctx, answerID, "answer", args, pred)
}
result.SetSuccess(nil, "planner grew answer node")
return &StepOutcome{Done: true, Result: result}, nil
```

幂等靠 `stableRound`：重执行的量子找到自己已有的 answer 节点，直接完成——以前重加会撞重复 ID 烧光整个重试预算。

---

## 19. 图节点 → fabric 任务

`AddToolNode` 只改 L2 图，**不直接创建 fabric 任务**。中间隔着事件投影。

### 19.1 `L2Graph.AddToolNode` — `l2graph.go:280`

```go
var agentType string
switch tool {
case "answer": agentType = answerAgentType // "ares/answer"
case "plan": agentType = planAgentType // "ares/plan"
default: agentType = "tool/" + tool
}
step := &engine.Step{ID: id, AgentType: agentType, Metadata: argsMetadata(args)}
if strings.TrimSpace(dependsOn) != "" { step.DependsOn = []string{dependsOn} }
if err := g.dag.AddNode(ctx, step); err != nil { ... }
```

### 19.2 `MutableDAG.AddNode` — `workflow/engine/mutable_dag.go:77`

校验（nil / 空 ID / 重复 ID）、依赖存在性检查（缺失则回滚已加的边和节点）、然后：

```go
m.steps[id] = step
m.version++

m.hub.Publish(GraphEvent{
 Change: GraphChange{
 Type: ChangeAddNode,
 NodeID: id,
 // Publish an isolated CLONE, never the live *Step
 Step: cloneStepForSnapshot(step),
 Timestamp: time.Now(),
 },
 ...
})
```

克隆的理由在注释里：订阅者在**锁外**读事件，而 `AddEdge`/`SetNodeMetadata` 在 DAG 锁内原地改 step——给活指针就是 data race。

### 19.3 `GraphEventHub.Publish` — `graph_events.go:116`

```go
h.seq++
event.Seq = h.seq
for id, ch := range h.subscribers {
 select {
 case ch <- event:
 default:
 h.dropped[id]++ // 非阻塞：缓冲满就丢，但计数
 }
}
```

注释写明："A miss is never silent — the counter plus the Seq gap on the next delivered event tell the subscriber to reconcile."

### 19.4 `SubscribeGraphEvents` — `planprojection/coordinator.go:695`

订阅 goroutine 的主循环（`:734-788`）：

```go
case evt, ok := <-ch:
 if !ok { return }
 if haveSeq && evt.Seq != lastSeq+1 {
 log.Warn("planprojection: graph event gap detected; reconciling", ...)
 c.reconcileNow(ctx, dag, "sequence gap")
 }
 haveSeq = true
 lastSeq = evt.Seq

 compileCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
 res, err := c.ApplyChange(compileCtx, dag, evt)
 cancel()
 if err != nil {
 log.Error("planprojection: incremental compile failed", ...)
 // 失败的增量编译本身是收敛信号
 c.reconcileNow(ctx, dag, "incremental compile failed")
 armTailCheck(tailCheck)
 continue
 }
 for _, s := range res.Skipped { log.Warn(...) }
 lastDropped, lastDropVersion = c.checkDrops(ctx, dag, subID, lastDropped, lastDropVersion, false)
 armTailCheck(tailCheck)

case <-tailCheck.C:
 // Tick path: 没事件送达时，版本变了说明有事件被丢
 lastDropped, lastDropVersion = c.checkDrops(ctx, dag, subID, lastDropped, lastDropVersion, true)
 armTailCheck(tailCheck)
```

三条补偿路径：**Seq 跳号** → 全量 reconcile；**增量编译失败** → 全量 reconcile；**drop 计数或版本变化** → reconcile。`tailCheck` 是 standing timer，每次 fire 后重新武装——这是 F-21 的修复（突发尾部落在一次性检查窗口之外就永远补偿不到）。

`checkDrops` 的信号是**双值**的（计数器 + DAG 版本），理由在 `:848` 附近的长注释：只看计数器会在 reconcile 看到**部分图**时消费掉信号，突发结束后计数器不再动，被丢的尾部永远不物化，会话永久卡死。

### 19.5 `ApplyChange` → `applyAddNode` → `compileOrAdopt` — `coordinator.go:314,516,449`

```go
case engine.ChangeAddNode:
 if err := c.applyAddNode(ctx, dag, evt.Change, &res); err != nil { ... }

// applyAddNode
step := ch.Step
if step == nil { step = stepFor(dag, ch.NodeID) }
createdID, err := c.compileOrAdopt(ctx, dag, step, res)

// compileOrAdopt
id, err := c.fabric.CompileNode(ctx, ProjectStep(step))
if err != nil {
 if !errors.Is(err, taskfabric.ErrTaskExists) { return "", err }
 c.addTracked(step.ID)
 c.reconcileRefresh(dag, step.ID, step, res)
 return "", nil
}
c.addTracked(id)
return id, nil
```

`ErrTaskExists` 被当作**采纳**而非错误：任务已在，刷新它即可。

### 19.6 `CompilePlan` — `workflow_plan.go:82`

```go
byID := make(map[string]PlanStep, len(steps))
for _, s := range steps {
 if s.ID == "" { return nil, ... "step id required" }
 if _, dup := byID[s.ID]; dup { return nil, ... "duplicate step id" }
 byID[s.ID] = s
}
strategyID := f.strategyStampID()

f.mu.Lock()
// 依赖闭包：先批内，再已在织物里的
if err := resolveDependencies(steps, byID, f.tasks); err != nil { f.mu.Unlock(); return nil, ... }
if err := detectPlanCycle(steps, byID); err != nil { f.mu.Unlock(); return nil, err }

// 锁内全有或全无创建
```

跨批依赖解析是运行时图生长的关键——运行时长出的节点依赖早先批次编译的任务，而那些通常已经 COMPLETED，所以新任务立刻 READY。

`resolveDependencies` 的解析顺序（`:220-232` 注释）：批内定义 > 已在织物里的任务 > 报错。批内定义优先，防止一批重定义的节点静默绑到早先编译留下的同 ID 任务上。

新任务经 `createLocked` 以 `READY` 入织物（同第11.3节）。

**至此，LLM 的每个 tool call 都变成了织物里一个 READY 任务，依赖链正确。**

---

## 20. answer 收尾

当 planner 最终长出 answer 节点、answer 任务被调度执行时：

### 20.1 `answerCognition.ExecuteStep` — `l2graph.go:539`

```go
body, ok := argsFromPayload(task.Payload)[answerContentKey].(string)
if !ok || strings.TrimSpace(body) == "" {
 body = c.synthesizeAnswer(ctx, task)
}
if strings.TrimSpace(body) == "" {
 body = unansweredBody
 c.logAnswerGap(task)
}
result := models.NewTaskResult(task.TaskID, task.AgentType)
result.SetSuccess([]*models.RecommendItem{{ItemID: task.TaskID, Content: body}}, "answer node terminated session")

if c.sessions != nil && strings.TrimSpace(task.SessionID) != "" {
 if err := c.sessions.ReleaseSession(task.SessionID); err != nil {
 c.logger.Warn("agentfabric: answer released an unknown session", ...)
 }
}
return &StepOutcome{Done: true, Result: result}, nil
```

**会话在这里终结**：丢掉图句柄、停掉增量编译订阅，防止新节点长进已完成的会话（reaper 收割任务）。

### 20.2 `synthesizeAnswer` — `l2graph.go:576`

answer 路径唯一一次 LLM 调用：

```go
if c.synthesis == nil || c.synthesis.chat == nil || c.synthesis.assemble == nil { return "" }
msgs, ok := c.synthesis.assemble(ctx, task)
if !ok { return "" }
msgs = append(msgs, &llmcore.LLMMessage{Role: "user", Content: answerSynthesisInstruction})
// No tool schemas and no param overrides
resp, err := c.synthesis.chat.Chat(ctx, msgs, nil, nil)
if err != nil { ... return "" }
if resp == nil || strings.TrimSpace(resp.Content) == "" { ... return "" }
return resp.Content
```

失败契约（`:566` 注释）：**任何**失败都返回 `""` 让调用方发 gap body。返回 error 会让量子失败、在对每个会话都在失败的 LLM 上烧光重试预算；gap body 是诚实的降级输出。

不带工具 schema 的理由：会话正在终结，工具调用永远执行不了，策略引导也没有可引导的东西了。

---

## 21. 终态与观测

`RunQuantum` 返回后回到 `executeWithCandidates`（第13.3节 末尾）：

### 21.1 `endQuantumOutcome` — `scheduler_quantum.go:228`

```go
if errors.Is(err, taskfabric.ErrNotOwner) || errors.Is(err, taskfabric.ErrEpochMismatch) {
 s.tracker.EndNeutral(winner) // 抢占 fencing，良性
 return
}
if errors.Is(err, taskfabric.ErrIllegalState) || errors.Is(err, taskfabric.ErrTaskNotFound) ||
 errors.Is(err, context.Canceled) {
 s.tracker.EndNeutral(winner) // 非 executor 的失败条件
 return
}
s.tracker.End(winner, err == nil)
if s.attribution != nil {
 s.attribution.RecordWithMetrics(winner, capability, err == nil, latency, retries, 0)
}
```

三类不计入置信度：fencing 拒绝、并发终态/移除、调度器关闭中止。注释说明为什么 `context.Canceled` 必须豁免——把优雅关闭的中止算成失败，会在每次重启时污染所有在飞 agent 的置信度。

正常路径则 `tracker.End(winner, err == nil)` 记入负载与历史成功率，并把结果推给进化反馈环。

### 21.2 后续

- `consumeBudget(winner, usage.tokens)` 记账本次量子消耗的 LLM token 和 1 轮工具
- 终态 COMPLETED 时 `s.Scheduled.Add(1)`（数任务不数量子）
- `unbindRecoveryExecutorAfterTerminal(taskID)` 解绑恢复用的替换 executor

任务以 `COMPLETED` 躺在织物里，`task.completed` 事件已持久化（PG 模式下跨重启可恢复）。

---

## 22. 一个任务的完整时序

以最简情形（LLM 一次就给最终答案）为例：

```
HTTP POST /api/tasks {capability:"code", payload:{input:"..."}}
 │
 ├─ agent.go:543 ServeHTTP → 路由匹配 → authorize(authWrite)
 ├─ routes_tasks.go:56 handleSubmitTask → 校验 kernel/capability
 ├─ agent_kernel.go:186 submitPeerTask
 ├─ submit.go:151 Submit
 │ ├─ 归一 capability → "ares/plan"
 │ ├─ session.go:104 Admit
 │ │ ├─ session_registry.go:103 InitSession → NewL2Graph
 │ │ │ └─ coordinator.go:695 SubscribeGraphEvents（订阅挂上）
 │ │ └─ workflow_plan.go:207 CompileNode(root)
 │ │ └─ fabric_lifecycle.go:15 Create → READY + task.created
 │ └─ fabric_lifecycle.go:15 Create(提交任务) → READY
 │
 ├─ HTTP 202 Accepted（异步，不等执行）
 │
 ├─ scheduler.go:253 Run 循环被事件唤醒
 ├─ dispatch.go:43 drain → ResumableTasks 返回 root
 ├─ execute.go:52 execute → buildCandidates（fabric 活 agent）
 ├─ execute.go:149 executeWithCandidates
 │ ├─ schedule.go:24 Schedule → Pick（评分）→ Acquire（epoch=1）
 │ ├─ lifecycle.go:78 Acquire → LEASED
 │ ├─ quantum.go:72 RunQuantum
 │ │ ├─ lifecycle.go:113 Start → RUNNING
 │ │ ├─ quantum.go:38 buildQuantumStep 闭包
 │ │ │ ├─ ToModelTask → models.Task
 │ │ │ ├─ tenantctx.With(ctx, tenantID)
 │ │ │ └─ fabric_executor.go:60 ExecuteStep
 │ │ │ └─ executor.go:105 Agent.ExecuteStep
 │ │ │ └─ l2graph.go:375 routerCognition
 │ │ │ └─ l2graph.go:421 rootCognition → Done(prompt)
 │ │ ├─ quantum.go:72 Complete → COMPLETED
 │ │ └─ 事件 task.completed 落库
 │ └─ endQuantumOutcome → tracker.End
 │
 ├─ 下一轮 drain：提交任务（"ares/plan"）就绪
 │ └─ 同样链路 → planner_cognition.go:235
 │ ├─ chat.Chat 打 LLM
 │ ├─ 无 tool calls → growAnswerNode
 │ │ └─ l2graph.go:280 AddToolNode("answer")
 │ │ └─ mutable_dag.go:77 AddNode → hub.Publish
 │ │ └─ coordinator.go:734 收到事件
 │ │ └─ ApplyChange → compileOrAdopt → CompileNode
 │ │ └─ answer 任务 READY
 │ └─ planner 量子 Done
 │
 ├─ 再一轮 drain：answer 任务被调度
 │ └─ l2graph.go:539 answerCognition
 │ ├─ synthesizeAnswer（若 content 不在 payload 里）
 │ ├─ SetSuccess(body)
 │ └─ ReleaseSession → 会话终结
 │
 └─ 全部任务 COMPLETED，会话释放
```

多轮工具调用时，中间多出若干次 `growToolNodes → AddToolNode → 事件 → CompileNode → tool 任务 READY → toolCognition.ExecuteStep → binder.CallTool` 的循环，直到 LLM 不再给 tool calls。

---

## 23. 附：容易混淆的点

| 易混 | 实际 |
|---|---|
| `kernelScheduler` vs `Scheduler` | 同一个类型，前者是 `cmd/ares/kernel.go:367` 的别名 |
| Task Fabric vs Agent Fabric | `internal/fabric/task/`（任务状态机）vs `internal/fabric/agent/`（agent 能力/身份） |
| `internal/runtime.Manager` vs `kernel.Orchestrator` | 前者管 agent 生命周期 + 插件总线；后者管系统组件图的控制面。`kernel/component.go:5-8` 有专门注释区分 |
| `ares run` vs `ares serve` | 前者走 SDK 进程内路径（`runRun` 在 `main.go:335`，`sdk.NewRuntime` 在 `main.go:365`），**全程无 HTTP**；后者走 Bootstrap + HTTP 控制台。执行核都是 `agentruntime` |
| **「动态图」** vs 第19节 的图投影 | `DynamicExecutor` / `WorkflowReloader` / `WorkflowService`（`docs/zh/features/dynamic-graph.md` 所述）是 Leader/Sub 时期的引擎，v0.3.x 已随该架构删除，**在 `cmd/` 与 `ares_bootstrap/` 中零引用，未接入 serve**。第19节 穿的是任务织物的图事件投影，两者不是一回事。第4.7节 建的进化 `MutableDAG` 又是第三样——那是给进化系统打补丁用的占位拓扑 |
| GA genome vs 策略 | 一个 genome 承载一组策略参数（temperature / max_tokens 等），fitness 从共享 evidence store 读。策略的历史版本存 `evolution_strategies`（append-only，每版本一行），激活态由 ASM 管理 |
| 量子 vs 任务 | 一个任务可以跑多个量子（yield→resume）。`t.Quantum` 是任务的执行深度，跨租约持有者累加 |

---

## 24. 附：关键文件索引

| 阶段 | 文件 |
|---|---|
| CLI 入口 | `cmd/ares/main.go`、`cmd/ares/serve.go` |
| 装配 wiring | `cmd/ares/serve_wiring.go`、`cmd/ares/serve_peer.go`、`cmd/ares/peer_assembly.go`、`cmd/ares/agent_kernel.go` |
| Peer 装配后置接线 | `cmd/ares/serve_peer.go`（`injectToolClassDAG` / `buildToolClassDAG` / `wireLiveDAGAndCompile` / `wireEvolutionLoops` / `wireIntrospectPanel` / `setupPeerRegistry`）、`cmd/ares/evolution.go`（`wireEvolutionIPC`） |
| 混沌注入 | `cmd/ares/serve_chaos_domain.go`（`wireChaos` / `effectiveChaosMode` / `shadowSandboxLoop` / `liveChaosLoop`） |
| AKF 工具 | `cmd/ares/serve_wiring.go:268`（`wiringServeAKFTools`） |
| 维护 worker | `internal/ares_bootstrap/maintenance_worker.go`（`startExpiryCleanupWorker` / `runExpiredCleanup`） |
| Bootstrap | `internal/ares_bootstrap/bootstrap.go`、`bootstrap_builder.go` |
| 经验蒸馏 | `internal/ares_bootstrap/bootstrap_steps.go`（`wireDistillation` / `subscribeDistillationEvents`）、`provide_distillation.go` |
| AKG 知识闭环 | `internal/ares_bootstrap/knowledge_akg.go`（`wireAKGLoop` / `triggerAKGBridge`）、`retriever_wiring.go`（`wireRetrievers`） |
| 进化 DAG / NewEvolution | `internal/ares_bootstrap/provide_new_evolution.go`、`provide_evolution.go`、`system_runtime_wiring.go` |
| **GA 进化** | `internal/ares_bootstrap/bootstrap_evolution.go`（`wireGAEvolution` / `assembleGAConfig` / `wireScorerAndShadowGate` / `wireLifecycleGates` / `runEvolutionTicker` / `runLLMSuggestions`）、`bootstrap_fitness.go`（`buildEvolutionSuggestionPrompt`） |
| GA 交叉算子 | `internal/evoapi/genome/genome.go` |
| HTTP | `cmd/ares/agent.go`（路由表 + ServeHTTP）、`cmd/ares/agent_routes_tasks.go` |
| 提交 | `internal/agentruntime/submit.go`、`session.go` |
| 织物 | `internal/fabric/task/fabric_lifecycle.go`、`fabric_schedule.go`、`quantum.go`、`fabric_events.go`、`state.go`、`dag.go`、`scheduler.go`、`workflow_plan.go` |
| 图 | `internal/fabric/agent/l2graph.go`、`planner_cognition.go`、`session_registry.go`、`executor.go` |
| 投影 | `internal/fabric/planprojection/coordinator.go`、`projection.go` |
| 图引擎 | `internal/fabric/task/workflow/engine/mutable_dag.go`、`graph_events.go` |
| 内核 | `internal/kernel/scheduler.go`、`scheduler_dispatch.go`、`scheduler_execute.go`、`scheduler_quantum.go`、`executor_registry.go`、`fabric_executor.go` |

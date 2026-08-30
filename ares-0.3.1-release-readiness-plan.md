# ARES 0.3.1 定版就绪计划（Release Readiness Plan）

> 生成日期：2026-08-29
> 当前分支：`dev`　当前 `VERSION`：`0.3.1`
> 依据：2026-08-29 全仓深度评审（内核闭环、迁移完整性、鲁棒性与安全性）
> 目标：把当前状态从「可用的 0.3.x 预发布」推进到「可定版 GA」

---

## ⚙️ 本迭代执行状态（2026-08-30 更新）

> 以下为一次迭代内的实际落地情况，供对照下文各任务的验收清单。**已勾选** = 通过
> 自动化的确证项（单测 / `make check` / `-race -count=5`）；**未勾选** = 依赖真实
> 环境（跨主机访问、`docker-compose`、真实 LLM、12h soak、`TEST_POSTGRES_DSN`），
> 需在具备条件的机器上执行。

| Task | 状态 | 结果 |
|------|------|------|
| T1 HTTP 绑定默认 `127.0.0.1` | ✅ 通过 | `Host` 空 → `127.0.0.1` 单测；`0.0.0.0` 触发暴露告警；新增 `--host` flag（优先级 flag > env > YAML，有单测）；`examples/09-full-app` 同步为 loopback 默认 + `SERVER_HOST` 放宽，`docker-compose` 的 `SERVER_HOST=0.0.0.0` 现在真实生效。跨主机端到端待环境验证 |
| T2 任务状态跨进程重启 | ✅ 通过 | `RestoreFromStore` 幂等+no-op 单测；`-race -count=3` 绿。**review 修复**：epoch 原仅记录在 must-persist 事件上，而唯一递增 epoch 的 `Acquire` 发的是 observability-only 的 `task.acquired`——重启后会重发已发放的 fencing token，崩溃前持有者可通过所有权校验。改为 epoch 随每个持久化事件记录、restore 扫描全部事件取 max+1，并加回归用例（含 stale holder 被 `ErrNotOwner` 拒绝）；payload 键统一为 `restoreKey*` 常量。`kill -9` 端到端待环境 |
| T3 `run_js` 沙箱 | ✅ 通过 | 采用 B1（移除 `run_js`/`EnableJS`），全仓无残留，`-race` 绿 |
| T4 SDK `create_plan` loop | ✅ 通过 | 采用 A1：SDK kernel 注入 loop 生命周期；`Runtime.Close()` 取消 loop；`-race` 绿。**review 修复**：`syscallKernel` 是私有字段，嵌入方无法列出/停止 loop（serve 路径有此控制面），已导出 `Runtime.LivePlanLoops()` / `StopPlanLoop()`（kernel 未接线时安全返回），并补验收测试 |
| T5 文档指向已删架构 | ✅ 通过 | `docs/agent-birth-capabilities.md` 已替换重写，无失效符号/路径；中英对齐 |
| T6 IPC 死信 | ✅ 通过 | Send/Request 失败进 deadletter 且可观测（DAG 状态段）；`-race` 绿 |
| T7 只读工具清单端点 | ✅ 通过 | `auth_enabled: true` 时读面要求 `PermRead`：`/api/tools`、`/api/mcp/tools`、introspect JSON 流、cost API。**review 修复**：`introspect.ControlServer` 的 `/api/*`（`/api/agents` 活体拓扑、`/api/flight/timeline`、`/api/flight/decisions` 调度决策、`/api/observability/spans`、`/api/runtime/config`、`/api/insights`）原从 handler 末尾无鉴权穿透（探针实测 200），已一并纳入读鉴权；非 `/api` 路径保持开放。矩阵测试覆盖；鉴权落审计 |
| T8 明文弱口令入库 | ✅ 通过 | 已入库配置无 active 明文口令（扫描为空）；`make check` 绿 |
| T9 覆盖率 ≥65% | ⚠️ 部分 | `internal/ares_events` 51% → **71.7%**（≥70% 达标）、`-race -count=5` 绿；**总覆盖率 58.3%**，距 65% 仍差（0% 薄层集中在 `compat/`、`api/evolution`、`examples/`，需专项或按计划决定移除 `compat`） |
| T10 长跑与租户隔离 | ⏳ 待环境 | 依赖真实 PG（`TEST_POSTGRES_DSN`）+ 真实 LLM + 12h soak，本迭代无法在会话内执行 |

**关键结论**：T1–T8 已全部落地且 `make check` 全绿；T9 的关键契约短板
`ares_events` 已达标，但**总覆盖率尚未到 65%**；T10 全部为环境性验收项，需在
具备 PG / LLM / 长跑条件的机器上执行后再定版。

---

## 0. 结论摘要

**当前不建议直接定 GA，建议先发 `v0.3.1-rc`。** 代码质量与内核闭环是真实的，
阻塞项不是代码缺陷，而是**承诺与实现不一致**的边界问题：

| 维度 | 实测结论 |
|------|---------|
| 构建 / 静态检查 | `make check`（vet + staticcheck + golangci-lint + 全量 test）通过 |
| 竞态 | `go test -race -count=1 ./...` 132 包全绿，0 FAIL |
| 门禁 | `make gate`（G1 可达性 / G2 配置契约 / G3 事件契约）通过 |
| 总覆盖率 | **57.8%**（内核关键包 78%–100%，`ares_events` 仅 51%） |
| 生产 TODO/FIXME | 11 处（3 处为已标注技术债，非遗漏） |
| 生产库裸 `panic()` | 0 处（5 处均为 `MustNew`/`quickstart` fail-fast 与 testdata 生成器） |

**阻塞定版的两个 P0：**

1. **任务状态不跨进程重启**——`taskfabric.Fabric` 为纯内存态，`EventStore` 只是
   单向下沉的事件汇，无任何从事件流重建 `tasks` map 的入口。`kill -9` / OOM 后
   READY/LEASED/RUNNING 任务连同 checkpoint 全部丢失。
2. **未鉴权 introspect 面板 + 强制 `0.0.0.0` 绑定**——`ServerConfig.Host` 被标注为
   "display-only"，用户**无法**把服务限制到 localhost，而
   `/api/v1/introspect/eventstream` 返回带完整 payload 的原始事件流。

---

## 1. 验收基线（每个任务完成后都必须重跑）

```bash
# 基础门禁（必过）
make check                       # lint(vet+staticcheck+golangci) + 全量 test
make gate                        # G1 可达性 + G2 配置契约 + G3 事件契约
go build ./... && go vet ./examples/...

# 竞态与稳定性
go test -race -count=1 ./...
go test -race -count=5 ./internal/taskfabric/... ./internal/kernelscheduler/... \
                       ./internal/agentsyscall/... ./internal/aresrecovery/...

# 覆盖率（GA 目标：总覆盖率 ≥ 65%）
make cover
```

**术语约定**：下文「绿」= 上述命令全部零退出码；「新增测试」一律要求
`-race` 通过，且遵循仓库既有的表驱动 + 真实依赖（不引入新 mock 框架）风格。

---

## 2. 任务总表

| # | 级别 | 任务 | 涉及文件 | 预估 |
|---|------|------|---------|------|
| T1 | **P0** | HTTP 绑定地址可配置，默认 `127.0.0.1` | `cmd/ares/serve_routine.go`、`internal/ares_config/config.go` | 0.5 人日 |
| T2 | **P0** | 任务状态跨重启：实现 `RestoreFromStore` 或显式声明非持久 | `internal/taskfabric/*`、`cmd/ares/kernel_loop.go`、`README*.md` | 2–3 人日（方案 A）/ 0.3 人日（方案 B） |
| T3 | **P1** | `run_js` 沙箱校验缺失（CommonJS `require` 可逃逸） | `internal/tools/resources/builtin/execution/code_runner.go` | 0.5 人日 |
| T4 | **P1** | SDK 路径 `create_plan` loop 能力不对等 | `sdk/syscall.go` | 0.3 人日 |
| T5 | **P1** | `docs/agent-birth-capabilities.md` 指向已删除的 leader 架构 | `docs/agent-birth-capabilities*.md` | 0.5 人日 |
| T6 | **P2** | IPC 死信仅记录、无重投、无消费方；且文档状态自相矛盾 | `internal/agentipc/*`、`ares-repair-plan-zh.md`、`cmd/ares/kernel_loop.go` | 1 人日 |
| T7 | **P2** | 只读工具清单端点未鉴权 | `cmd/ares/actions.go` | 0.3 人日 |
| T8 | **P2** | 明文弱口令入库 | `configs/query_rewrite_config.yaml` | 0.1 人日 |
| T9 | **P2** | 覆盖率补齐至 ≥65%（重点 `ares_events`） | `internal/ares_events/*_test.go` | 1–2 人日 |
| T10 | **P2** | 定版前长跑与租户隔离确认 | soak 脚本、`internal/storage/postgres` | 1 人日 |

**关键路径**：T1 → T3 → T2 →（T4、T5 并行）→ `v0.3.1-rc` → T6–T10 → GA。

---

## T1（P0）HTTP 绑定地址可配置，默认 `127.0.0.1`

### 问题证据

`internal/ares_config/config.go:200-202` 把 `Host` 明确标注为 display-only：

```go
// Host is display-only: serve binds ":<port>" (C4); changing the bind
Host string `yaml:"host"`
Port int    `yaml:"port"`
```

`cmd/ares/serve_routine.go:165` 实际绑定：

```go
addr := fmt.Sprintf(":%d", cfg.Server.Port)
```

`:port` 等价于 `0.0.0.0:port`。同时 `internal/introspect/api.go:23-29` 自己写了
安全警告，说明 `/api/v1/introspect/eventstream` 返回原始事件（任务输入、
checkpoint 内容），`/api/v1/introspect/snapshot` 暴露实时调度/租约/agent 状态，
且**该 Handler 不做任何鉴权**。

组合后果：默认 `ares serve` 让同网段任何人可读取任务输入与内部状态，而配置里
改 `host` 无效——用户没有任何收敛手段。

### 怎么做

**Step 1**：改 `internal/ares_config/config.go`
- 删除 "display-only" 注释，改为说明「实际绑定地址；留空默认 `127.0.0.1`」。
- 在配置默认值函数中把 `Server.Host` 默认设为 `"127.0.0.1"`。
- 在配置校验逻辑里加一条**显式告警**（非阻断）：当 `Host` 为
  `0.0.0.0` / `""`（若选择保留空=全绑定语义）/ 公网可路由地址，且
  `Security.AuthEnabled == false` 时，输出 `log.Warn` 说明
  「introspect 读面未鉴权将暴露任务 payload」。

**Step 2**：改 `cmd/ares/serve_routine.go:165`

```go
// 旧
addr := fmt.Sprintf(":%d", cfg.Server.Port)
// 新（示意；Host 为空时回落到 127.0.0.1）
host := cfg.Server.Host
if host == "" {
    host = "127.0.0.1"
}
addr := net.JoinHostPort(host, strconv.Itoa(cfg.Server.Port))
```

同时修正紧随其后的控制台打印（当前硬编码 `http://localhost%s`），改为打印真实
绑定地址，避免绑定 `0.0.0.0` 时输出误导性 URL。

**Step 3**：同步检查是否存在其他 `fmt.Sprintf(":%d"` 形式的绑定点
（`grep -rn 'Sprintf(":%d"' cmd internal`），一并改为走同一个 `Host` 解析逻辑，
避免只修一处而 dashboard / MCP / demo 端口仍全网卡绑定。

**Step 4**：更新 `configs/ares.yaml` 与 `configs/ares.minimal.yaml` 的
`server.host` 注释，写明「默认 127.0.0.1；改为 0.0.0.0 前必须开启
`security.auth_enabled` 并设置 `jwt_secret`」。

**Step 5**：更新 `CHANGELOG.md`，在 `[0.3.1]` 段落的 **Breaking changes** 中记录
「默认绑定地址从 `0.0.0.0` 收敛为 `127.0.0.1`」——这会影响容器部署（需显式设
`host: 0.0.0.0`），必须作为破坏性变更公告。同步检查 `docker-compose.yml` 与
`Dockerfile.demo` 是否需要补 `server.host` 或 `ARES_SERVER_HOST`。

### 验收标准

- [ ] `ares serve` 默认启动后，`lsof -nP -iTCP -sTCP:LISTEN | grep ares` 显示监听
      `127.0.0.1:<port>`，**不是** `*:<port>`。
- [ ] 从另一台主机 `curl http://<本机局域网IP>:<port>/api/v1/introspect/snapshot`
      连接被拒绝。
- [ ] 配置 `server.host: 0.0.0.0` 后，上述跨主机 `curl` 可达（证明配置真正生效，
      而非被忽略）。
- [ ] `server.host: 0.0.0.0` + `auth_enabled: false` 时，启动日志出现暴露告警。
- [ ] 环境变量覆盖路径仍生效（`config.go:598` 的 `cfg.Server.Host = v` 分支）：
      设 `ARES_SERVER_HOST=0.0.0.0` 后绑定随之改变，且新增单测覆盖该分支。
- [ ] 新增单测：`Host` 为空 → 解析出 `127.0.0.1`；`Host` 显式 `0.0.0.0` → 保持
      `0.0.0.0`；IPv6 字面量（`::1`）经 `net.JoinHostPort` 正确加括号。
- [ ] `make check` && `make gate` 绿（注意 G2 配置契约门禁会校验字段语义，
      改动 `Host` 语义后必须同步更新契约测试）。
- [ ] `docker-compose up` 后容器端口仍可从宿主访问（若不可，说明需补 `host` 配置，
      属于本任务范围内的收尾）。

---

## T2（P0）任务状态跨进程重启

### 问题证据

`internal/taskfabric/fabric.go:55-60`：

```go
type Fabric struct {
	mu     sync.Mutex
	tasks  map[string]*Task
	events []TaskEvent
	store  ares_events.EventStore // optional persistent event sink (P2-C)
```

`store` 仅在 `recordLocked`（`fabric.go:622`）中被**写入**。全库搜索
`RestoreFromStore` / `rebuildFabric` / `replayEvents` / `hydrate`，在
`internal/taskfabric` 下**无任何读取重建入口**（`replayEvents` 只存在于
`internal/ares_runtime/manager_lifecycle.go:226`，那是 agent 认知恢复，不是任务状态）。

必须区分两类恢复，避免误判现状：

| 场景 | 现状 | 证据 |
|------|------|------|
| **进程内 agent 死亡** | ✅ 已闭环 | `cmd/ares/kernel_loop.go:297` 周期 `RequeueExpiredLeases()` + 事件驱动双通道，含 `recover()` 与 sweep timeout；租约到期→requeue→checkpoint 续跑 |
| **进程级崩溃（kill -9 / OOM）** | ❌ 未覆盖 | 内存 map 消失，恢复循环扫描的是已不存在的对象 |

### 方案 A（推荐，面向 GA）：实现 `Fabric.RestoreFromStore`

**Step A1**：扩充持久化 payload。当前 `recordLocked` 只写
`task_id / agent_id / origin / state`（`fabric.go:658-663`），不足以重建
`Task`（`internal/taskfabric/task.go:8-48` 有 13 个字段）。至少补齐重建必需集：
`capability`、`priority`、`dependencies`、`deadline`、`retry_policy`、`quantum`、
`created_at`，以及 **`checkpoint`**（`EventTaskCheckpointed` 事件必须带 checkpoint
本体，否则续跑无从恢复）。

- checkpoint 走 `internal/taskfabric/checkpoint_schema.go` 已有的序列化路径
  （`checkpoint_schema.go:167` 注释已声明这是「persistence 的序列化路径」），
  不要新造一套编码。
- payload 字段名与 `ares_events` 事件契约保持一致——**G3 事件契约门禁会校验**，
  改 payload 必须同步更新契约测试。

**Step A2**：新增 `func (f *Fabric) RestoreFromStore(ctx context.Context) error`
- 用 `store.ReadAll(ctx, ReadOptions{...})`（`internal/ares_events/store.go:23`，
  跨流按时间排序）读取全部 `task.*` 事件。
- 按 `StreamID`（= `task.ID`，见 `fabric.go:659`）分组，按版本顺序折叠出终态。
- **只信任 must-persist 事件**：`isMustPersistEvent`（`fabric.go:720`）定义为
  `TaskCreated / TaskCheckpointed / TaskCompleted / TaskFailed / TaskExpired`。
  其余（Ready/Acquired/Started/Yielded/Preempted/Released/Stolen）是
  observability-only，**不可**作为状态重建依据。
- **租约一律不恢复**：重启后旧 `Owner`/`Lease` 必然失效。凡重建出的非终态任务
  统一落到 `READY`，`Owner=""`、`Lease=nil`，保留 `Checkpoint` 与 `Quantum`。
  这样它天然汇入既有的「READY → 调度器 Acquire → 从 checkpoint 续跑」路径，
  不需要新增执行分支。
- **epoch 单调性**：`f.epoch` 必须恢复为「历史最大 epoch + 1」，否则重启后新租约
  的 fencing token 会与崩溃前的重复，栅栏失效（这是本任务最容易出错、也最危险的
  一处）。若事件 payload 未记录 epoch，需在 A1 中一并补上。
- 幂等：对已有任务的 `RestoreFromStore` 二次调用不得重复插入或回退状态。

**Step A3**：在 `cmd/ares/kernel_loop.go` 的内核装配处，于调度器启动**之前**调用
`RestoreFromStore`。顺序至关重要：先恢复，再开 drain，否则调度器会在半空的 fabric
上做出错误决策。失败策略建议 fail-loud（记录并中止启动），避免「静默丢任务」。

**Step A4**：`sdk` 路径同样需要（`sdk/sdk.go` 的 `sdkFabric`）。若 SDK 默认无
`EventStore`，则保持 no-op 但**行为一致**：有 store 就恢复，无 store 就跳过。

**Step A5**：新增测试
- 单测：构造带 must-persist 事件的 store → `RestoreFromStore` → 校验状态、
  checkpoint、epoch 单调性、终态任务不被复活。
- 集成测试（关键）：起 fabric → 建任务 → Acquire → Yield（写 checkpoint）→
  **丢弃整个 Fabric 实例**（模拟进程崩溃）→ 新建 Fabric + `RestoreFromStore` →
  断言任务为 READY 且 checkpoint 内容一致 → 调度器续跑至 Complete。
- 陈旧持有者测试：恢复后用**崩溃前的旧 epoch** 调 `Complete`，必须被栅栏拒绝。

### 方案 B（保底，1 天内可交付）：显式声明非持久语义

若判断 A 的工作量超出本次定版窗口，则**必须**改为明确声明，不能留模糊承诺：

- `README.md` / `README_CN.md` 的 Task Fabric 描述中，把 "Durable Task state
  machine"（`README.md:239` 现用词）改为准确表述：**「进程内持久（durable
  within a process）：状态机 + 租约 + checkpoint 在进程生命周期内可靠；事件流下沉
  到 EventStore 用于审计与回放，但任务状态不跨进程重启恢复」**。
- `CHANGELOG.md` 的 `[0.3.1]` 增 **Known limitations** 段落记录该边界。
- `internal/taskfabric/doc.go` 顶部补包级说明，让读代码的人第一眼看到。
- `docs/` 中所有宣称「崩溃恢复」的位置，明确限定为「进程内 agent 崩溃恢复」。

### 验收标准

**若走方案 A：**
- [ ] 集成测试通过：kill 掉持有 Fabric 的进程/实例后重建，非终态任务全部回到
      READY，`Checkpoint` 与 `Quantum` 逐字段一致。
- [ ] 终态任务（COMPLETED/FAILED）重启后**不被复活**。
- [ ] 恢复后 `f.epoch` > 崩溃前任何已发出的 epoch；用旧 epoch 调
      `Complete`/`Yield`/`Renew` 均返回 owner 校验错误。
- [ ] `RestoreFromStore` 幂等：连续调用两次结果一致。
- [ ] 无 `EventStore` 时为 no-op 且不 panic（SDK 默认路径）。
- [ ] G3 事件契约门禁（`make gate`）随 payload 扩充同步更新并通过。
- [ ] `go test -race -count=5 ./internal/taskfabric/...` 绿。
- [ ] 端到端手工验证：`ares serve` → 提交任务 → 执行中 `kill -9` → 重启 →
      任务出现在 `/api/v1/introspect/snapshot` 且被续跑至完成。

**若走方案 B：**
- [ ] README（中英双版）、CHANGELOG、`taskfabric/doc.go` 三处措辞一致，均不再出现
      无限定词的 "durable" / "崩溃恢复"。
- [ ] 全仓搜索 `crash recovery` / `崩溃恢复` / `durable`，逐处确认已限定语义
      （命令：`grep -rn "崩溃恢复\|crash.recovery\|durable" README*.md docs/ internal/taskfabric/`）。
- [ ] 该限制在 GitHub Release Notes 的 Known limitations 中出现。

---

## T3（P1）`run_js` 沙箱校验缺失

### 问题证据

`code_runner.go:170` 对所有语言调用同一个 `validateCode(code)`，而该函数
（`code_runner.go:317`）是**纯 Python 语义**：`stripPythonComments`、
`importPattern`（匹配 `import x` / `from x import`）、`allowedImports`
（14 个 Python 模块白名单）、`dangerousPatterns`（`__import__`、`eval(`、`exec(`、
`open(` 等 Python 构造）。

JS 分支直接执行（`code_runner.go:406`）：

```go
cmd := exec.CommandContext(ctx, "node", "-e", code) // #nosec G204
```

`require('child_process').execSync('...')` 不命中任何 Python 危险模式，
`require` 也不被 `importPattern` 识别——**白名单对 CommonJS 完全不适用**。
即 `EnableJS(true)` 等价于给 LLM 一个无沙箱的 node RCE。默认关闭
（`code_runner.go:51`）掩盖了这一点，但工具描述里的 "with sandbox constraints"
对 JS 不成立。

### 怎么做（二选一，建议 B1）

**方案 B1（推荐）：移除 `run_js`**
- 删除 `run_js` 分支（`code_runner.go:207-211`）、`runJavaScript`、`EnableJS`、
  `IsJSEnabled`，并从工具 schema 的 `operation` 枚举中移除。
- Python 路径已具备 import 白名单 + 危险模式 + 进程组隔离（`Setpgid`）+
  `PATH`-only 环境 + 临时工作目录，足以覆盖「让 agent 跑代码」的能力展示。
- 在 CHANGELOG 记为 Breaking change（尽管默认关闭，仍属 API 收缩）。

**方案 B2：为 JS 建独立校验**
- 拆分为 `validatePython` / `validateJS`，`validateCode` 按 operation 分派。
- `validateJS` 至少覆盖：`require(...)` 参数必须是**字面量**且在白名单内
  （拒绝 `require(variable)`、模板串、拼接）；禁 `child_process`、`fs`、`net`、
  `http`、`https`、`vm`、`worker_threads`、`process.binding`、`process.env`、
  `globalThis`、`Function(`、`eval(`、动态 `import(`、`--experimental` 类开关。
- 追加 `node` 侧硬约束：`--no-addons`，并考虑 `--permission`（Node 20+ 权限模型）
  彻底关闭 fs/net/child_process，比字符串黑名单可靠得多。
- 注释中明确写清「字符串级校验是纵深防御的最外层，不是唯一防线」，避免后来者
  误以为足够。

### 验收标准

- [ ] 若选 B1：全仓无 `run_js` 残留（`grep -rn "run_js\|EnableJS\|runJavaScript"`
      返回空），schema 枚举同步收缩，`make check` 绿。
- [ ] 若选 B2：新增逃逸用例测试全部被拒——`require('child_process')`、
      `require('fs')`、`require("ch"+"ild_process")`、`require(m)`（变量）、
      `process.env`、`Function('return process')()`、动态 `import('fs')`。
- [ ] 若选 B2：合法用例仍可执行（纯计算、`JSON`、`Math`、字符串处理）。
- [ ] 工具描述与实际防护能力一致：若 JS 保留，描述必须写明具体约束；若移除，
      描述中不再提 JavaScript。
- [ ] `internal/tools/resources/builtin/execution` 包 `-race` 绿。

---

## T4（P1）SDK 路径 `create_plan` loop 能力不对等

### 问题证据

`agentsyscall.WithLoopLifetime` 只在 `cmd/ares/peer_mode.go:261` 注入。
`sdk/syscall.go:126` 的 `agentsyscall.NewKernel(...)` **未传该 option**，因此
SDK 用户的 agent 调 `create_plan` 带 `loop` 参数时命中
`internal/agentsyscall/plan.go:171`：

```go
return nil, errors.New("agentsyscall: plan loop requires a kernel loop lifetime (WithLoopLifetime)")
```

行为本身是正确的（fail loudly，不泄漏 goroutine），问题是**schema 里暴露了
用不了的参数**——`ToolSchemas()`（含 `CreatePlanToolSchema()`）在两条路径是同一份，
SDK 用户看得到 `loop{max_rounds, until}` 却必然报错。

### 怎么做（推荐 A1）

**方案 A1（推荐）：SDK 注入生命周期**
- `sdk/sdk.go:151-155` 已有现成的 `ctx` / `cancel` 字段，注释即写明「governs the
  lifetime of background goroutines ... Cancelled in Close」——正是 loop 需要的语义。
- 在 `sdk/syscall.go` 的 `wireSyscalls()`（`syscall.go:119`）中把 `r.ctx` 传入：
  `agentsyscall.NewKernel(..., agentsyscall.WithLoopLifetime(r.ctx))`。
- 确认 `wireSyscalls` 的调用时机（`ensureScheduler` 内 `schedOnce`）晚于 `r.ctx`
  初始化；若不满足，需调整初始化顺序，**不可**传 `context.Background()`
  （那会让 loop goroutine 脱离 `Close()` 管控，制造泄漏）。
- 复用 Kernel 侧既有的并发上限（`WithMaxPlanLoops`，默认 16）与错误 watcher。

**方案 A2（保底）：SDK schema 收缩**
- 若因初始化顺序无法安全注入，则在 SDK 路径过滤掉 `loop` 参数，使
  schema 与能力一致；并在 `sdk` 文档标注该差异。

### 验收标准

- [ ] 若选 A1：SDK 端到端测试——注册 agent → 触发 `create_plan` 带
      `loop{max_rounds:2, until:"all_succeeded"}` → 两轮均执行 → 正常收敛，
      不再出现 `requires a kernel loop lifetime` 错误。
- [ ] 若选 A1：`Runtime.Close()` 后 loop goroutine 全部退出——用
      `go.uber.org/goleak`（或现有等价手段）断言无泄漏。
- [ ] `LivePlanLoops()` / `StopPlanLoop()` 在 SDK 路径可正常观测与停止。
- [ ] 若选 A2：SDK 路径 schema 中不含 `loop`，且有测试断言两条路径 schema 差异
      是**有意的**而非漂移。
- [ ] `go test -race ./sdk/... ./internal/agentsyscall/...` 绿。

---

## T5（P1）`docs/agent-birth-capabilities.md` 指向已删除架构

### 问题证据

该文档第 5 行（日期标注 2026-08-15，早于 leader 删除）：

> 范围：`serve` 启动创建 **leader / sub** agent 时（`cmd/ares/{serve,agents,tools}.go`
> + `internal/agents/sub` + `internal/ares_memory`）**自动注入**……

实际情况：
- `cmd/ares/agents.go` **不存在**（已确认）。
- `internal/agents/leader` **包已删除**（`git log --diff-filter=D` 显示于
  `3aabb450 refactor(ares): Freeze Agent OS parity architecture` 删除）。
- 文档中仍有 3 处 `leader.NewTaskPlannerWithConfig`、`leader.WithExperienceLocator`
  等已不存在的接线点。

新用户读这份「出生自带能力」清单会按不存在的 API 去找代码。
（对照：`docs/CAPABILITY-MAP.md` 已无 leader 残留，可作为改写参考基准。）

### 怎么做

**Step 1**：按 flat peer + capability 匹配架构重写「范围」段，接线点更新为真实路径
（`cmd/ares/serve.go`、`cmd/ares/peer_mode.go`、`cmd/ares/tools.go`、
`internal/agents/sub`、`internal/ares_memory`）。

**Step 2**：逐条复核表格中每个接线点是否仍存在。**做法：每一行都用
`grep`/LSP 验证符号真实存在**，不存在的删除，被替换的改为新符号。重点复核
原 `leader.*` 三处（任务规划、经验定位、调度器 option）——它们现在的等价物在
`kernelscheduler` / `taskfabric` / `agentsyscall`。

**Step 3**：补上评审中确认为默认开启但文档未列的项，并**标明默认开/关**：

| 能力 | 默认 | 证据位置 |
|------|------|---------|
| 记忆（无 embedding 时降级压缩-only） | 开 | `sdk/quickstart.go:54` |
| 文件工具沙箱（allowedDir 默认 = CWD） | 开 | `builtin.go:126` + `resolveFileToolsAllowedDir()` |
| SSRF 防护（拦 Loopback/Private/LinkLocal） | 开 | `network/ssrf.go:97` |
| 路径穿越防护（`EvalSymlinks` 后再操作，防 TOCTOU） | 开 | `file_tools.go:102` |
| 代码执行（Python / JS） | **关** | `code_runner.go:83-84` |
| HTTP 鉴权 | **关** | `config.go:223`（`auth_enabled` 默认 false） |

**Step 4**：同步英文版 `docs/agent-birth-capabilities.en.md`（文档头部互相引用，
不能只改一侧），并更新两版顶部日期。

**Step 5**：顺带修正 README 的能力宣称口径。评审实测：最小 yaml 是 **7 行有效内容**
（`configs/ares.minimal.yaml:23-26` + provider/model），零参数入口 `sdk.MustNew()`
确实能 3 行跑通简单任务；但**复杂任务**（多步规划 + 多 agent 协作）最短路径是
`examples/01-quickstart` 的约 50 行有效代码、7 个 API（`LoadConfigFile` →
`ToOptions` → `NewRuntime` → `ToolRegistry().Register` → `NewAgent` →
`WithInstruction` → `Run`），SDK 共 34 个 `With*` option、54 个导出符号、35 个示例。
建议口径改为**「简单任务 3 行，复杂任务约 50 行」**，替代含糊的「一个 yaml +
极简接口解决复杂任务」。

### 验收标准

- [ ] 文档内所有代码符号经 `grep` 或 LSP 验证存在；无 `leader.` 前缀残留
      （`grep -n "leader\." docs/agent-birth-capabilities*.md` 返回空）。
- [ ] 无指向不存在文件的路径引用（重点：`cmd/ares/agents.go`）。
- [ ] 每项能力标注默认开/关，且与代码实测一致（尤其代码执行与鉴权为「关」）。
- [ ] 中英双版内容对齐，日期已更新。
- [ ] README 中英双版的能力宣称与实测行数一致。

---

## T6（P2）IPC 死信仅记录、无重投、无消费方

### 问题证据

`internal/agentipc/deadletter.go` 有完整的 `DeadLetterStore`（环形容器、
`Record`、超预算驱逐最旧），`bus.go:81` 暴露 `DeadLetters()` 读面。但
`grep -rn "DeadLetters()" internal cmd sdk`（排除测试）显示：**该方法在生产代码中
零调用者**——既无重投，也无面板/指标消费。

更需要修正的是**文档自相矛盾**：
- `ares-repair-plan-zh.md` §12.5 GAP-3 标记 **「✅ 已落地」**。
- `cmd/ares/kernel_loop.go:3` 自己的 TODO 写着
  `agentipc has no retry/dead-letter semantics ... (repair plan GAP-3)`。

实际状态：**有记录、无重投、无消费**。修复计划的状态标记偏乐观。

### 怎么做

**Step 1（必做，成本最低）**：先让状态诚实。把 `ares-repair-plan-zh.md` GAP-3
从「✅ 已落地」改为「🟡 记录已落地，重投/消费待接」，并保留
`kernel_loop.go:3` 的 TODO（它是对的）。**文档与代码只能有一个真相。**

**Step 2**：接上观测面。在 introspect 只读 API 增加死信快照
（条数、按 from/to/topic 聚合、最近 N 条 reason）。注意该端点同样受 T1 的绑定
收敛保护；若 payload 含任务输入，需与 eventstream 同级别对待。

**Step 3（可选，按需求排期）**：实现重投。建议**不做隐式自动重试**——IPC 语义
是 agent 间消息，盲目重投可能造成重复副作用。推荐显式手动重投端点
（写操作，必须走 `checkAuth`），或带幂等键的有限次退避重试。若本次不做，
则在 CHANGELOG 的 Known limitations 中登记。

### 验收标准

- [ ] `ares-repair-plan-zh.md` GAP-3 状态与代码一致；仓库内不再存在同一议题的
      矛盾记录。
- [ ] 死信可观测：Send/Request 失败后能在 API/面板看到该条目及 reason。
- [ ] 若实现重投：重投端点经过 `checkAuth`（与 `/api/agents/*/kill` 同级），
      有审计日志，且重投次数有上限、耗尽后进终态。
- [ ] 端到端测试：制造投递失败 → 进死信 → 可观测（→ 若实现则重投 → 耗尽终态）。
- [ ] `go test -race ./internal/agentipc/...` 绿。

---

## T7（P2）只读工具清单端点未鉴权

### 问题证据

写操作面已是正确的 deny-by-default：`checkAuth`（`cmd/ares/actions.go:95`）
JWT-或-APIKey 双路径，两者都未配置即 401，用 `subtle.ConstantTimeCompare`
防时序攻击，401/403 语义分明，全部落审计日志；`/api/tasks`、`/api/graphs`、
`/api/agents/*/kill`、`/api/chaos/*`、`/api/tools/call`、`/api/mcp/tools/*/call`
均在 `checkAuth` 之后。**这一层没有问题。**

缺口在只读侧：`GET /api/tools` 与 `GET /api/mcp/tools` 无鉴权，泄露完整工具清单
（含 MCP server 拓扑）。不致命，属攻击面侦察信息。

### 怎么做

- 将两个 `GET` 列表端点纳入鉴权，或至少在 `Security.AuthEnabled == true` 时要求
  读权限（`ares_security.PermRead`）。
- 若为了本地开发便利需保留匿名读，则**必须**依赖 T1 的 `127.0.0.1` 收敛，并在
  代码注释与文档中写明该端点的暴露面。
- 与 introspect 读面（`snapshot` / `eventstream`）采取**一致策略**，避免出现
  「一个端点要鉴权、另一个同等敏感端点不要」的不一致。

### 验收标准

- [ ] `auth_enabled: true` 时，无凭证 `GET /api/tools`、`GET /api/mcp/tools`、
      `/api/v1/introspect/snapshot`、`/api/v1/introspect/eventstream` 均返回 401。
- [ ] 带合法凭证时全部 200。
- [ ] 鉴权决策落审计日志（与写操作同一 sink）。
- [ ] 新增测试覆盖上述矩阵（有/无凭证 × 各端点）。

---

## T8（P2）明文弱口令入库

### 问题证据

`configs/query_rewrite_config.yaml:31` 存在 `password: "postgres"`，且该文件
**已被 git 跟踪**（`git ls-files` 确认）。

对照说明：`configs/ares.local.yaml` 中形如 `sk-BynS7BQ...` 的真实 key
**已被 `.gitignore:22` 排除且无提交历史**——这一项无需处理。

### 怎么做

- 把 `password: "postgres"` 改为占位符（`REPLACE_WITH_YOUR_PASSWORD`，与
  `configs/api_impl.yaml:12` 的 `REPLACE_WITH_YOUR_API_KEY` 风格一致）或改走
  环境变量。
- 顺带扫一遍其他已入库配置中的弱默认凭证
  （`git ls-files 'configs/*' 'examples/**/*.yaml' | xargs grep -n "password:\|api_key:"`），
  统一改为占位符。

### 验收标准

- [ ] 已入库配置中不存在可用的明文口令/密钥。
- [ ] 相关示例仍能按 README 指引在填入凭证后跑通（不能为了清理而破坏示例）。
- [ ] `make check` 绿（配置契约测试可能引用这些文件）。

---

## T9（P2）覆盖率补齐至 ≥65%

### 现状

总覆盖率 **57.8%**。内核关键包已达标：kernelctx 100%、agentloop 97%、
taskfabric 84.5%、agentsyscall 83.6%、kernelscheduler 81.7%、agentfabric 78.1%、
aresrecovery 73.7%。

**短板是 `internal/ares_events`：仅 51%**，而它是持久化与事件契约的基础——
T2 方案 A 的恢复正确性完全依赖它。`compat/` 19 个文件 0% 覆盖率（薄适配层，
生产仅一处引用：`internal/ares_bootstrap/provide_llm.go:57` 注册 LLM provider，
失败只 warn 不阻断），优先级低于 `ares_events`。

### 怎么做

- 优先补 `ares_events`：`Append` 的 OCC 冲突（`ErrVersionConflict`）、`Read`/
  `ReadAll` 的排序与分页边界、`Subscribe` 的 ctx 取消与通道关闭、
  `StreamVersion` 的 `ErrStreamNotFound`、compactable store 的归档与轮转
  （0.3.0 修过 per-stream round 文件覆盖的数据丢失 bug，回归测试必须覆盖）。
- 次优先：`ares_config`（配置契约由门禁保护，但边界解析值得补）。
- `compat/` 视为可选：若决定长期保留，补基本注册/解析测试；若决定移除，
  在 CHANGELOG 中作为 Breaking change 处理。

### 验收标准

- [ ] `make cover` 总覆盖率 ≥ **65%**。
- [ ] `internal/ares_events` 覆盖率 ≥ **70%**。
- [ ] 新增测试全部 `-race` 通过，无 flaky（`-count=5` 连跑稳定）。

---

## T10（P2）定版前长跑与租户隔离确认

### 待验证项（评审中未能覆盖）

1. **12h soak**：本次只跑了 `-race -count=5` 的短重复，`make nightly` 的长跑基线
   未执行。需确认内存平稳、goroutine 数不单调增长、`kill -TERM` 停机日志完整。
2. **`tenantID == ""` 的语义**：`internal/storage/postgres/base_repository.go:85`
   起，`GetByID` / `DeleteByID` / `CountByTenant` 在 `tenantID != ""` 时才追加
   `AND tenant_id = $N`（配合表名白名单防注入）。即**空租户 = 全局访问**。
   这是有意设计还是隐患，取决于调用方是否保证非空——需追全部调用点确认。
3. **真实 LLM 端到端**：评审环境无 API key，未验证真实模型下的完整链路。

### 怎么做

- 跑 `make nightly`（或 `make test-race` + 12h serve 长跑），期间周期采集
  `/debug/pprof/goroutine` 与 RSS，形成基线记录归档到 `benchmarks/`。
- 追 `tenantID` 全部调用点：确认是否存在「本应租户隔离却传空」的路径。若存在，
  改为显式要求非空并返回错误；若空租户确为单机模式的有意语义，在
  `base_repository.go` 包级注释中写明，并补测试固化该约定。
- 用真实 provider 跑 `examples/01-quickstart` 与一个多 agent 协作示例，确认
  syscall（`spawn_agent` / `create_task` / `create_plan`）在真实模型输出下可用。

### 验收标准

- [ ] 12h 长跑：RSS 无持续上涨，goroutine 数收敛，无 panic，`kill -TERM` 优雅停机
      日志完整；基线归档。
- [ ] `tenantID == ""` 语义有明确结论：或收紧为报错，或以注释+测试固化为有意设计。
- [ ] 真实 LLM 下 quickstart 与多 agent 协作示例均跑通，三个 syscall 均被实际触发。

---

## 3. 发布节奏

```
阶段一（rc）：T1 → T3 → T2 →（T4 ∥ T5）
              ↓ 全部验收通过
           打 tag v0.3.1-rc，发布 Release Notes（含 Known limitations）

阶段二（GA）：T6 → T7 → T8 →（T9 ∥ T10）
              ↓ 全部验收通过 + 总覆盖率 ≥65% + 12h soak 基线
           打 tag v0.3.1（GA）
```

**为何 T1/T3 先于 T2**：两者都是小改动、零架构风险，先合掉可以立刻消除最严重的
默认暴露面；T2 无论走 A 还是 B 都需要决策与讨论，不应阻塞安全修复上线。

**每个任务的收尾动作（统一要求）**：
1. `make check` && `make gate` && `go test -race -count=1 ./...` 全绿。
2. 变更涉及配置/事件 payload 时，同步更新 G2/G3 契约测试。
3. 在 `CHANGELOG.md` 的 `[0.3.1]` 段落记录（区分 Fixed / Changed / Breaking /
   Known limitations）。
4. **提交由用户发起**——本计划不包含任何自动 `git commit`。

---

## 4. 非阻塞观察项（记录在案，不阻塞定版）

这些项经核实是**有意设计或可接受现状**，但值得登记以免日后误判为缺陷：

| 项 | 现状与判断 |
|---|---|
| 新旧模块并存三处 | `internal/eval`（被 `ares_eval/evidence_bridge.go` 包着的旧核）、`internal/evolution` 与 `internal/ares_evolution` 双活（均从 `ares_bootstrap` 进入）、`internal/workflow/engine` 仍被 6 个生产文件引用（evolution 的 genome/differ + `serve_live_dag.go` + `arena.go`）。**都是活代码、分工明确**，但边界靠注释维持而非类型系统。与 `taskfabric.PlanLoop` 形成两套 DAG 编排语义并存——建议在 0.4.x 收敛为一套。 |
| `compat/` 层 | 19 个文件、0% 覆盖率、生产仅一处引用且失败只 warn。移除不破坏内核，但会断掉外部生态的 provider 发现约定。保留即可，注意别让它长出新依赖。 |
| `context.Background()` 出现 123 次 | 生产非入口处，其中部分应改为传递 ctx。不阻塞定版，建议 0.4.x 做一轮 ctx 传播审计。 |
| 194 处 `_ =` 忽略错误 | 需逐个判断是否为有意忽略。建议加 lint 规则约束新增。 |
| `fmt.Errorf` 中 `%w` 占比 67%（743/1104） | 错误链基本完整，剩余部分建议渐进补齐。 |
| 生产 TODO 11 处 | 其中 3 处为已标注技术债（scheduler per-agent 队列、evolution genome 维度、postgres schema 版本表），属于「有意记录、暂不做」，非遗漏。 |
| 调度器僵尸清理 | `reconcileFabricDeaths()` 每次 drain 执行，agent 死亡后摘除其静态 executor 注册。设计扎实（注释明确记录了漏掉它会导致「在死 agent 注册上执行任务 + 注册表随 spawn 无界增长」）。**无需改动，仅作记录。** |
| 0.2.x → 0.3.x 功能迁移 | 判定为**完整**。`api/graph`、`api/service/workflow`、`api/client`、`ares workflow run` 是**主动删除**而非遗漏（CHANGELOG 说明 workflow CLI「executed against an empty in-process registry and could never resolve a real workflow」——删除一个从未能工作的命令是正确的）。leader/sub 协议已完整替换为 flat peer + capability 匹配，残留符号在 0.3.0 已 de-leaderize（含 Postgres `leader_checkpoints` → `agent_checkpoints` 幂等迁移）。**缺口只在文档（T5），不在功能。** |

---

## 5. 定版最终检查清单

打 GA tag 前逐项确认：

- [ ] T1–T8 全部完成并验收通过
- [ ] `make check` 绿
- [ ] `make gate` 绿（G1 可达性 / G2 配置契约 / G3 事件契约）
- [ ] `go test -race -count=1 ./...` 132 包全绿
- [ ] `go build ./...` && `go vet ./examples/...` 绿
- [ ] `make cover` 总覆盖率 ≥ 65%，`internal/ares_events` ≥ 70%
- [ ] 12h soak 基线归档，无内存/goroutine 泄漏
- [ ] 默认启动仅监听 `127.0.0.1`，跨主机访问被拒
- [ ] 任务持久化语义与 README/CHANGELOG 文字**完全一致**（无论走 T2-A 还是 T2-B）
- [ ] `docs/agent-birth-capabilities.md`（中英双版）无失效符号与路径
- [ ] 已入库配置无明文可用凭证
- [ ] `CHANGELOG.md` `[0.3.1]` 段落完整：Fixed / Changed / Breaking changes /
      Known limitations 四类齐全
- [ ] `VERSION` 与 tag 一致
- [ ] 仓库内无自相矛盾的状态记录（重点：`ares-repair-plan-zh.md` 的 GAP 表
      与代码 TODO）

---

## 6. 一句话总结

代码是好的，内核闭环是真的——**卡定版的是两处「承诺与实现不一致」**：任务状态
不跨重启，以及用户无法把未鉴权的内省面板关进 localhost。前者可以选择「实现」或
「诚实声明」，后者必须实现。修完这两项加上四个 P1，`v0.3.1` 可发；再补齐覆盖率
与长跑基线，GA 可定。


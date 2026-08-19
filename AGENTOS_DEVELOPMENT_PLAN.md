# AgentOS Development Plan

**Goal**  
Transform the existing ARES runtime into a full‑featured **AgentOS** platform where:

* **Runtime** owns the complete lifecycle of an Agent (creation, scheduling, health‑checking, self‑healing, graceful shutdown).  
* **Agent** behaves like a lightweight thread/worker that can be started, stopped, paused, resumed, and introspected via a unified interface.  

---

## 0. 现状核对（2026-08-18 reconcile）

> 本计划是北向愿景。下方对每个里程碑逐条核对代码库现状（✅ 已实现 / ⚠️ 部分 / 🔲 缺口），
> 避免重复实现已有能力；缺口对应的可执行 TODO 见文末「六、已识别缺口（按优先级）」。
> 核对依据：`internal/agents/base`、`internal/aresrecovery`、`internal/agentfabric`、
> `internal/ares_observability`、`internal/ares_events`、`cmd/ares`、`.github/workflows`。

| 里程碑 | 现状 | 证据 / 说明 |
|---|---|---|
| M1 Agent 接口 & 生命周期 | ✅ | `base.Agent`（ID/Start/Stop/Process/Status）+ `Messenger`/`Heartbeater`/`StatefulAgent`；leader/sub 均实现；kernel 状态机 + 恢复链（lease/requeue） |
| M2 资源调度 & 隔离 | ⚠️ | 配额已实现（`agentfabric.WithResourceBudget`、`EvolutionAwareQuotaManager`）；**cgroup-v2 无**，无 `setrlimit`/RLIMIT 代码 |
| M3 统一事件总线 | ⚠️ | 进程内 `EventStore` + `PluginBus` + `agentipc`（bus/peer registry、json+gzip 演进策略）已存在；**NATS/Redis‑Stream 未引入**（且与 §11 不变量 #10「不提前设计」冲突） |
| M4 健康 & 自愈 | ✅ | recovery loop（lease 过期/失败/抢占）、resurrection 插件、arena 故障注入、`/health` 探针 |
| M5 可观测性 | ✅ | `ares_observability`（otel_tracer/prometheus）、GlobalTracer、`/observability/spans`、dashboard |
| M6 配置热加载 | ✅ | `ConfigStore`（fsnotify + 200ms debounce）+ `/runtime/config` 端点（快照级热重载，见 §6 P1 诚实标注） |
| M7 安全/RBAC | ✅ | `ares_security`：标准库 HS256 JWT + RBAC 矩阵 + 模块化审计（§6 P0） |
| M8 版本化 | ✅ | `VERSION` 文件 + Makefile 注入 `main.version` + `docs/design/versioning.md`（§6 P4） |
| M9 运维文档/Helm | ⚠️ | `docs/operator/README.md` 已建（2026-08-19）；`charts/` Helm 明确不做（§6「明确不做」） |
| M10 CI/多节点 e2e | ⚠️ | `agentos_ci.yml` + codecov 徽章已做（§6 P2）；**多节点（3‑node）集群 e2e 留待真机压测** |
| M11 生产发布 | 🔲 | 依赖上述缺口闭环后的发布节奏 |

---

## 1. Vision & Scope

| Area | Description |
|------|-------------|
| **Agent Abstraction** | Define a **standard `Agent` interface** (`ID()`, `Run()`, `Stop()`, `Health()`, `Meta()`). All current leaders/sub‑agents will implement it. |
| **Lifecycle Management** | Implement a deterministic state machine (INIT → BOOT → RUNNING → SUSPENDED → TERMINATED) with hooks for **Graceful‑Drain, Pause/Resume, Checkpoint/Restore**. |
| **Resource Isolation** | Integrate cgroup‑v2 / quota manager to enforce CPU, memory, GPU, I/O limits per Agent. |
| **Unified Communication** | Introduce a **Pub/Sub bus** (e.g., NATS‑JetStream or Redis‑Stream) with request/response patterns. |
| **Health & Self‑Recovery** | Periodic health polling, configurable thresholds, automatic restart/quarantine, and event publishing. |
| **Observability** | Structured logging (slog) + OpenTelemetry tracing + Prometheus metrics for every Agent and runtime component. |
| **Configuration & Hot‑Reload** | Centralized `ares.yaml` + env overrides; support fsnotify hot‑reload of runtime parameters. |
| **Security & RBAC** | JWT/OAuth2 authentication for API endpoints; role‑based access to destructive operations. |
| **Versioning & Compatibility** | Adopt semantic versioning; deprecation notices for breaking API changes. |
| **Operator Documentation** | Full run‑book: deployment (Docker‑Compose & Helm), runtime tuning, troubleshooting, and upgrade guide. |
| **CI / Test Infrastructure** | Multi‑node e2e suite with fault‑injection, resource‑stress matrices, and coverage reporting. |

---

## 2. Milestones & Timeline

| Milestone | Target Release | Key Deliverables |
|-----------|----------------|-------------------|
| **M0 – Baseline** | v0.1 (now) | Consolidate current `serve` flow; document existing components. |
| **M1 – Agent Interface & Lifecycle** | v0.2 | `internal/agents/base/Agent.go` with `Agent` interface; refactor Leader/Sub‑Agent to implement it; add lifecycle state machine in `runtime.Manager`. |
| **M2 – Resource Scheduler & Isolation** | v0.3 | cgroup‑v2 wrapper; per‑Agent quota manager; health‑check integration with scheduler. |
| **M3 – Unified Event Bus** | v0.4 | `internal/transport/pubsub.go` implementing Publish/Subscribe/Ask/Tell; migrate intra‑agent communication to bus. |
| **M4 – Health & Self‑Recovery** | v0.5 | `internal/runtime/health/monitor.go` + automatic restart logic; expose `/health` and `/metrics` endpoints. |
| **M5 – Observability Stack** | v0.6 | Structured slog logging with `AgentID`, `Component`; OpenTelemetry traces; Prometheus `/metrics` entry point. |
| **M6 – Config Hot‑Reload & Dynamic Tuning** | v0.7 | Viper/spock based config loading; fsnotify watcher; `/runtime/config` API for live changes. |
| **M7 – Security Layer** | v0.8 | JWT middleware for all API routes; RBAC policy definitions; audit logging. |
| **M8 – Versioning & Compatibility Layer** | v0.9 | Semantic version bump; deprecation shim package; migration guide. |
| **M9 – Operator Docs & Examples** | v1.0 | `docs/operator/` with Helm charts, Docker‑Compose files, quick‑start tutorials. |
| **M10 – CI / Test Automation** | v1.1 | GitHub Actions workflow: multi‑node deployment, fault‑injection matrix, performance benchmarks; badge for test coverage. |
| **M11 – Production Release** | v1.2 | Full AgentOS runtime shipped; documentation published; community onboarding. |

---

## 3. Detailed Task Breakdown (Phase‑by‑Phase)

### Phase 1 – Foundations — ✅ 已实现
1. **Define `Agent` interface** (`internal/agents/base/agent.go`). — 已存在：`base.Agent`（ID/Type/Status/Start/Stop/Process/ProcessStream）+ `Messenger`/`Heartbeater`/`StatefulAgent`。
2. Refactor `LeaderAgent` & `SubAgent` structs to embed the interface. — 已实现（`internal/agents/leader`、`internal/agents/sub`）。
3. Add a **lifecycle manager** (`runtime/manager_lifecycle.go`) exposing `Start()`, `Pause()`, `Resume()`, `Stop()`, and health callbacks. — 状态机/生命周期在 `ares_runtime.Manager` + kernel/recovery 链中已实现（Pause/Resume 语义对应 task 的 SUSPENDED/Ready 恢复）。

### Phase 2 – Resource Management — ⚠️ 配额已做，cgroup 缺口
1. Implement **cgroup wrapper** (`internal/runtime/cgroup.go`) that applies CPU, Memory, I/O limits per Agent. — 🔲 缺口：仓库无 cgroup/`setrlimit` 代码。
2. Extend `runtime.QuotaManager` to expose per‑Agent budget Adjust API. — ✅ 已实现：`agentfabric.WithResourceBudget`、`EvolutionAwareQuotaManager`（P5 资源预算）。
3. Integrate quota checks into the **Task Scheduler** before dispatch. — ✅ 已实现：spawn gate + 配额在调度前强制。

### Phase 3 – Communication Bus — ⚠️ 进程内总线已够，分布式总线属「不提前设计」
1. Choose NATS‑JetStream (or Redis‑Stream) as the underlying substrate. — 🔲 未引入（且与 `ares-runtime.md` §11 不变量 #10 冲突，**先不做**，除非出现跨进程真实需求）。
2. Build `pubsub/publisher.go`, `pubsub/subscriber.go`, and **request‑response** helpers. — ⚠️ 等价物已有：`ares_events.EventStore`（pub/sub）+ `ares_runtime.PluginBus` + `agentipc`（bus/peer registry、Send/Register、json+gzip 演进策略）。
3. Replace direct peer‑registry calls with bus‑based messaging. — ✅ peer registry 已通过 evolution-aware IPC 桥接（`wireEvolutionIPC`）。

### Phase 4 – Health & Self‑Recovery — ✅ 已实现
1. Create `health/monitor.go` that polls `runtime.Manager` for each Agent’s `Health()` status. — ✅ recovery loop + resurrection 插件 + arena 故障注入。
2. Define health thresholds (latency, error_rate, CPU usage). — ⚠️ 以 lease/重试阈值为主，CPU 等资源阈值未做（依赖 Phase 2 的 cgroup 缺口）。
3. Hook into `runtime.Manager` to trigger `RestartAgent` on violation. — ✅ 恢复链（lease 过期 requeue / 失败重试 / 抢占）。

### Phase 5 – Observability — ✅ 已实现
1. Add **structured logger** wrapper (`internal/logger/logger.go`) emitting JSON with fields: `timestamp`, `level`, `agent_id`, `component`, `msg`, `trace_id`. — ⚠️ 结构化日志已有（`log/slog`），字段对齐按需补齐。
2. Initialize **OpenTelemetry tracer provider** (`internal/observability/otel_tracer.go`) linking to the tracer set up in recent commit. — ✅ `internal/ares_observability/otel_tracer.go`（已修 schema 冲突）+ GlobalTracer。
3. Export **Prometheus** metrics via `monitoring.NewHTTPServer` (`/metrics`). — ✅ `internal/ares_observability/prometheus.go` + `/observability/spans`。

### Phase 6 – Configuration & Hot‑Reload — ✅ 已实现（快照级，见 §6 P1）
1. Introduce `config/v1/config.go` using Viper + spock for validation. — ✅ `ares_config`（YAML + 校验），未用 Viper/spock（§10.1 无必要不引依赖）。
2. Add `fsnotify` watcher that reloads config and pushes changes to `runtime.ConfigStore`. — ✅ `ConfigStore.Watch`（fsnotify + 200ms debounce，`internal/ares_config/store.go`）。
3. Implement `/runtime/config` REST endpoint returning current config snapshot. — ✅ `internal/monitoring/http_api.go`（脱敏快照 + 历史）。

### Phase 7 – Security & RBAC — ✅ 已实现（§6 P0）
1. Add JWT validation middleware to all HTTP endpoints (`monitoring/httputil/jwt.go`). — ✅ `ares_security` 标准库 HS256 JWT + gin/net-http 中间件；破坏性端点双凭据（API key ∨ JWT）。
2. Define role constants (`admin`, `operator`, `agent`) and map them to API permissions. — ✅ `rbac.go` 角色层级 + read/write/admin 矩阵，默认 deny。
3. Store a minimal **policy engine** (`security/policy.go`). — ✅ `AllowRole`/`HasPermission` + `AuditLogger`（auth 决策 + 破坏性操作审计）。

### Phase 8 – Versioning — ✅ 已实现（§6 P4）
1. Add `VERSION` file and update `go.mod` to embed version. — ✅ `VERSION` + Makefile 注入 `main.version` ldflag。
2. Document deprecation policy in `docs/design/versioning.md`. — ✅ 已建（SemVer + 弃用流程 + 配置兼容 + CHANGELOG 纪律）。
3. Add shim layer to preserve older API signatures where needed. — ⏸ 按需再建（当前 API 面较稳，无破坏性变更待兼容）。

### Phase 9 – Operator Documentation & Examples — ⚠️ 部分（run-book 已建，Helm 明确不做）
1. Write `docs/operator/README.md` covering: Quick‑start with Docker‑Compose; Helm chart directory (`charts/ares-os/`); Config tuning, health checking, and upgrade steps. — ✅ `docs/operator/README.md` 已建（2026-08-19：快速启动/配置/健康/认证/热重载/升级/排障）；
   **`charts/` Helm 明确不做**（§6「明确不做」：无多租户真机消费者，不提前设计）。
2. Add usage examples for custom Agent / Pub‑Sub API / `/metrics`. — ✅ `examples/` 已大量存在（SDK/team/GA/arena 等）；架构图文档 `docs/zh/architecture/ares-architecture.md` 已补。

### Phase 10 – CI / Test Automation — ⚠️ 部分（单进程 e2e 已做，多节点留真机）
1. Add GitHub Actions workflow `.github/workflows/agentos_ci.yml` (3‑node cluster, 1‑200 agents, fault injection, health recovery SLA). — ✅ `agentos_ci.yml`（混沌 e2e + 基准 sanity）；**3‑node 集群 e2e 留待真机压测**（见 §6 P2 说明）。
2. Publish a **coverage badge** and integrate with `codecov`. — ✅ codecov-action + README 徽章。
3. Add performance benchmarks (`benchmarks/benchmark_agent_pool_test.go`). — ✅ `internal/ares_runtime/benchmark_agent_pool_test.go`（并发生命周期 + 复活吞吐）。

---

## 4. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| **Breaking API changes** when introducing `Agent` interface | Runtime may fail to load existing agents | Keep backward‑compatible shim layer; compile‑time check `//go:build compatibility`. |
| **Deadlocks in lifecycle hooks** | Runtime could hang on graceful shutdown | Unit‑test each hook path; enforce timeouts in the state machine. |
| **Resource‑quota mis‑configuration** leading to OOM | Cluster instability | Provide sane defaults; add runtime guardrails that panic if limits are set too low. |
| **Pub/Sub dependency failures** (e.g., NATS down) | Communication loss | Fallback to in‑process event bus for dev mode; auto‑retry logic with exponential back‑off. |
| **Observability overload** (high cardinality metrics) | Dashboard performance degradation | Use restricted label set; implement metric aggregation before export. |
| **Security exposure** via open API endpoints | Unauthorized access | Strict RBAC; default‑deny policy; regular security audit of JWT claims. |

---

## 5. Quick Start for Contributors

```bash
# 1. Clone the repo
git clone https://github.com/Timwood0x10/goagent.git
cd goagent

# 2. Install dependencies
go mod tidy

# 3. Run unit tests
go test ./... -v

# 4. Build the serve binary
go build -o bin/ares ./cmd/ares

# 5. Start a dev runtime (auto‑reload config)
./bin/ares serve -c ares.yaml
```

*All new code should be placed under `internal/` and respect the **golang.org/x/sync/errgroup** pattern for concurrent goroutine orchestration.*  

---

## 6. 已识别缺口（按优先级，2026-08-18 reconcile 后可执行 TODO）

> 只列 🔲 缺口；✅/⚠️ 项按「现状」中的证据已核实为已实现，**不要重复实现**。
> 优先级遵循项目自身纪律（`ares-runtime.md` §11 不变量 #10）：没有真实消费者就不提前设计。

### P0 — 安全层（M7，dashboard/arena 暴露面真实存在，先做）— ✅ 已实现（2026-08-18）
- [x] JWT 校验中间件（`internal/ares_security/jwt.go` + `middleware.go`）——**标准库 HS256** 实现
      （§10.1 优先标准库，不引 golang-jwt），Bearer 解析、exp/iat 校验、常量时间签名比较。
- [x] 角色常量 + RBAC 矩阵（`rbac.go`）：`admin`⊃`operator`⊃`agent`，read/write/admin 权限，
      `AllowRole` 默认 deny。
- [x] 最小策略引擎 + **模块化审计日志**（`audit.go`）：`AuditLogger` 统一结构化记录
      auth 决策 + 破坏性操作（kill/resume/retry、MCP tool call）；token 永不落日志。
- [x] 配置接线：`security.jwt_secret` / `ARES_JWT_SECRET` / `ARES_AUTH_ENABLED`；
      `ares auth token` CLI 签发。
- [x] 测试：`jwt_test.go`（往返/过期/篡改/垃圾）、`rbac_test.go`（矩阵）、
      `middleware_test.go`（allow/deny/审计流）、`audit_test.go`（模块化 sink）、
      `http_api_test.go`（双凭据 API key∨JWT）。
- 设计取舍：JWT 与旧 API key **双凭据并存**（向后兼容）；破坏性端点默认 deny。

### P1 — 配置热加载（M6，已有 MCP 域 watcher 可复用）— ✅ 已实现（2026-08-18）
- [x] 运行时全局 `ConfigStore`（`internal/ares_config/store.go`）：`Current()` / `History()` /
      `Reload(ctx, path)` / `Watch(ctx, path)` 四个方法，fsnotify + 200ms debounce，
      失败 reload 保留上次有效配置并记入历史（上限 20 条）。
- [x] `/runtime/config` REST 端点（`internal/monitoring/http_api.go`）：
      返回脱敏配置快照（`Config.Redacted()` 遮蔽 LLM APIKey/Fallbacks、
      Storage.Password、Security.JWTSecret）+ 变更历史；未挂 store 时不注册（404）。
- [x] serve 接线（`cmd/ares/serve.go` / `serve_routine.go`）：`ares serve --config ares.yaml`
      启动 watcher；minimal `--llm-url` 模式跳过 watcher 但仍提供快照端点。
- [x] 测试：`store_test.go`（reload 成功/失败保旧/历史封顶/watch 重载）、
      `redacted_test.go`（脱敏不篡改原值/空值不误标）、`http_api_test.go`
      （快照脱敏 + 未挂载 404）。
- 设计取舍：store 不向子系统推送变更（无回调、无依赖图），消费方按自身节奏轮询
      `Current()`——保持接口精简，避免过度设计。
- **诚实标注（v0.4.0 code review）**：当前 `ConfigStore` 的**热重载是「快照级」**——
      `Watch`/`Reload` 更新的是 store 内的配置快照，唯一消费方是 `/runtime/config`
      端点（运维观测），**尚未热加载到任何运行子系统**（LLM adapter、kernel 循环、
      agents 等启动时持有各自的 `cfg` 拷贝）。这是刻意的第一版范围：先把
      「当前生效配置 + 失败历史」的观测面做起来，避免过早给子系统加热加载回调。
      待某个子系统出现真实的热更新需求（如 LLM 端点切换、quota 阈值调整）时，
      在 `serve` 的对应循环里轮询 `Current()` 即可增量接入。

### P2 — 故障注入 e2e（M10，已有 ci/integration-test 骨架）— ✅ 已实现（2026-08-18）
- [x] `.github/workflows/agentos_ci.yml`：Chaos CI 工作流——`go test -race -run TestE2EChaosRecovery`
      + agent-pool 基准 sanity（`-benchtime=10x`）上传产物。
- [x] 混沌恢复 e2e（`internal/ares_arena/e2e_chaos_recovery_test.go`）：
      `TestE2EChaosRecovery_InjectKillAndVerifyRestore`（8-agent 池崩溃一半→复活）
      + `TestE2EChaosRecovery_Scale`（16/64/128 池崩溃 25%→复活）。走真实接线
      arena→runtime.Manager→resurrection factory。
- [x] agent-pool 基准（`internal/ares_runtime/benchmark_agent_pool_test.go`）：
      `BenchmarkAgentPoolConcurrentRegisterStartStop`（16/64/256 池并发生命周期）、
      `BenchmarkAgentPoolResurrection`（崩溃重建吞吐）。
- [x] codecov 徽章：`ci.yml` 加 codecov-action 上传 `cover.out`；README 加
      CI / Chaos CI / codecov 三枚徽章。
- 说明：3 节点集群与网络延迟/CPU 过载注入属真实环境压测（需多主机），CI 中以
      单进程内 arena→runtime 恢复链 e2e 覆盖同等的「故障→恢复」断言；多节点形态
      留待真机压测环境，避免在 CI runner 上做脆弱的系统级故障注入（§11 不变量 #10：不提前设计）。

### P3 — 资源隔离（M2，仅在出现多租户/硬隔离需求时做）
- [ ] cgroup‑v2 wrapper（或至少 `setrlimit` 软隔离）——**当前无 cgroup 代码，且无消费方，暂缓**。

### P4 — 版本化收尾（M8，低风险顺手项）— ✅ 已实现（2026-08-18）
- [x] `VERSION` 文件（当前 `0.3.0`）+ Makefile 注入 `main.version` ldflag
      （`make build` 嵌入；`TAG_VERSION` 可覆盖；`ares version` 优先注入值，buildinfo 回退）。
- [x] `docs/design/versioning.md`：SemVer + deprecation 流程（标记/过渡/移除）+ 配置
      schema 兼容 + CHANGELOG 纪律。
- [x] CHANGELOG `[Unreleased]` 补本轮 P0-P4 + 质量修复条目。
- 说明：shim 层按需再建——当前 API 面较稳，无破坏性变更待兼容。

### 明确不做（与 §11 不变量 #10 冲突）
- [ ] NATS‑JetStream / Redis‑Stream 分布式总线（进程内 `EventStore`+`PluginBus`+`agentipc` 已覆盖；跨进程需求出现前不引入）。
- [ ] Helm charts / 多租户调度器 / 完整 Actor 模型（同因暂缓）。

---

## 7. Closing Note

This plan translates the features that **AgentOS** must provide into concrete, incremental milestones that align with the existing ARES architecture. By following the phases above, the repository will evolve from a functional runtime into a production‑grade, self‑managed Agent platform suitable for large‑scale, multi‑tenant workloads.

> **2026-08-18 修订**：本计划经现状核对（§0），多数里程碑已在代码库实现；剩余缺口与优先级见 §6。执行时以 §6 TODO 为准，避免重复实现已有能力。

*Happy coding!* 🚀
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
| M6 配置热加载 | ⚠️ | `ares_mcp/config_watcher.go`（fsnotify，MCP 域）+ workflow reloader；**运行时全局 config store 热加载缺 `/runtime/config` API** |
| M7 安全/RBAC | 🔲 | arena 有 API key 认证；**无 JWT 依赖（无 golang-jwt）、无 RBAC 中间件** |
| M8 版本化 | ⚠️ | `CHANGELOG.md` + `ares version` 存在；**无 `VERSION` 文件、无 deprecation shim 包** |
| M9 运维文档/Helm | ⚠️ | `docker-compose.yml` 已有；**无 `charts/` Helm 目录、无 `docs/operator/` run‑book** |
| M10 CI/多节点 e2e | ⚠️ | `.github/workflows/` 已有 ci/cd/integration-test/release；**无多节点（3‑node）故障注入矩阵、无 coverage badge** |
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

### Phase 6 – Configuration & Hot‑Reload — ⚠️ 局部有，运行时全局缺口
1. Introduce `config/v1/config.go` using Viper + spock for validation. — ⚠️ 配置加载/校验已有（`ares_config`，YAML + 校验），未用 Viper/spock（无必要）。
2. Add `fsnotify` watcher that reloads config and pushes changes to `runtime.ConfigStore`. — ⚠️ 已有 `ares_mcp/config_watcher.go`（fsnotify，MCP 域）+ workflow reloader；**运行时全局 config store 热加载缺口**。
3. Implement `/runtime/config` REST endpoint returning current config snapshot. — 🔲 缺口。

### Phase 7 – Security & RBAC — 🔲 缺口
1. Add JWT validation middleware to all HTTP endpoints (`monitoring/httputil/jwt.go`). — 🔲 无 golang-jwt 依赖、无 JWT 中间件（arena 仅 API key）。
2. Define role constants (`admin`, `operator`, `agent`) and map them to API permissions. — 🔲 缺口。
3. Store a minimal **policy engine** (`security/policy.go`). — 🔲 缺口。

### Phase 8 – Versioning — ⚠️ 部分
1. Add `VERSION` file and update `go.mod` to embed version. — 🔲 无 `VERSION` 文件（`ares version` 已存在，数据源可补）。
2. Document deprecation policy in `docs/design/versioning.md`. — ⚠️ `CHANGELOG.md` 已有；deprecation 策略文档可补。
3. Add shim layer to preserve older API signatures where needed. — 🔲 缺口（现阶段 API 面较稳，按需再建）。

### Phase 9 – Operator Documentation & Examples — ⚠️ 部分
1. Write `docs/operator/README.md` covering: Quick‑start with Docker‑Compose; Helm chart directory (`charts/ares-os/`); Config tuning, health checking, and upgrade steps. — ⚠️ `docker-compose.yml` 已有；**`charts/` Helm 目录、`docs/operator/` run‑book 缺口**。
2. Add usage examples for custom Agent / Pub‑Sub API / `/metrics`. — ⚠️ `examples/` 已大量存在（SDK/team/GA/arena 等）；按需补齐即可。

### Phase 10 – CI / Test Automation — ⚠️ 部分
1. Add GitHub Actions workflow `.github/workflows/agentos_ci.yml` (3‑node cluster, 1‑200 agents, fault injection, health recovery SLA). — ⚠️ 已有 `ci.yml`/`cd.yml`/`integration-test.yml`/`release.yml`；**多节点故障注入矩阵缺口**。
2. Publish a **coverage badge** and integrate with `codecov`. — 🔲 缺口。
3. Add performance benchmarks (`benchmarks/benchmark_agent_pool_test.go`). — ⚠️ `benchmarks/` 与 `internal/agentfabric/benchmark_test.go` 已有；agent‑pool 基准可按需补。

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

### P0 — 安全层（M7，dashboard/arena 暴露面真实存在，先做）
- [ ] 引入 `golang-jwt/jwt`，为 `/console/`、`/observability/*`、arena 等 HTTP 端点加 JWT 校验中间件（`monitoring/httputil/jwt.go`）。
- [ ] 定义角色常量（`admin`/`operator`/`agent`）并映射到 API 权限，默认 deny。
- [ ] 最小策略引擎（`security/policy.go`）+ 审计日志。

### P1 — 配置热加载（M6，已有 MCP 域 watcher 可复用）
- [ ] 把 `ares_mcp/config_watcher.go` 的 fsnotify 模式推广为运行时全局 config store 热加载。
- [ ] 实现 `/runtime/config` REST 端点返回当前配置快照（含变更历史）。

### P2 — 故障注入 e2e（M10，已有 ci/integration-test 骨架）
- [ ] 新增 `.github/workflows/agentos_ci.yml`：3 节点集群 + 随机 1‑200 agent + 网络延迟/CPU 过载注入 + 断言健康恢复/优雅关闭。
- [ ] 接入 codecov 覆盖率徽章。
- [ ] `benchmarks/benchmark_agent_pool_test.go`：agent‑pool 并发基准。

### P3 — 资源隔离（M2，仅在出现多租户/硬隔离需求时做）
- [ ] cgroup‑v2 wrapper（或至少 `setrlimit` 软隔离）——**当前无 cgroup 代码，且无消费方，暂缓**。

### P4 — 版本化收尾（M8，低风险顺手项）
- [ ] 补 `VERSION` 文件并接入 `ares version` 数据源。
- [ ] 写 `docs/design/versioning.md` deprecation 策略（shim 层按需再建）。

### 明确不做（与 §11 不变量 #10 冲突）
- [ ] NATS‑JetStream / Redis‑Stream 分布式总线（进程内 `EventStore`+`PluginBus`+`agentipc` 已覆盖；跨进程需求出现前不引入）。
- [ ] Helm charts / 多租户调度器 / 完整 Actor 模型（同因暂缓）。

---

## 7. Closing Note

This plan translates the features that **AgentOS** must provide into concrete, incremental milestones that align with the existing ARES architecture. By following the phases above, the repository will evolve from a functional runtime into a production‑grade, self‑managed Agent platform suitable for large‑scale, multi‑tenant workloads.

> **2026-08-18 修订**：本计划经现状核对（§0），多数里程碑已在代码库实现；剩余缺口与优先级见 §6。执行时以 §6 TODO 为准，避免重复实现已有能力。

*Happy coding!* 🚀
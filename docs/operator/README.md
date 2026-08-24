# ARES Operator Runbook

> 版本：0.3.0 · 适用命令：`ares serve` / `ares start`
> 本文档是 M9 里程碑交付（AGENTOS_DEVELOPMENT_PLAN.md §6），覆盖：快速启动、配置调优、
> 健康检查、认证、热重载、升级与故障排查。架构总览见
> [docs/zh/architecture/ares-runtime.md](../zh/architecture/ares-runtime.md)。

## 1. 快速启动

### 本地（源码）

```bash
# 1. 前置：本地 LLM（Ollama）
ollama pull llama3.2

# 2. 构建
make build          # 产物 bin/ares，注入 VERSION（0.3.0）

# 3. 准备配置（默认探测 ./ares.yaml）
cp configs/ares.yaml ares.yaml   # 按需修改 llm.provider / server.port

# 4. 启动
bin/ares serve --config ares.yaml
```

### Docker Compose（完整 demo）

```bash
docker compose up -d          # ollama + ares-demo（:8080）
docker compose logs -f ares-demo
```

> Compose 中的 `ollama` 服务含 healthcheck；`ares-demo` 依赖其就绪后才启动。

## 2. 配置调优（configs/ares.yaml）

| 配置段 | 关键项 | 默认 | 说明 |
|---|---|---|---|
| `server` | `host` / `port` | `127.0.0.1:8080` | 监控 console HTTP 监听 |
| `llm` | `provider` / `base_url` / `api_key` / `model` | openai | 主 LLM；`api_key` 可用 `ARES_LLM_API_KEY` 覆盖 |
| `llm.fallbacks` | `provider` / `api_key` / `model` | — | 主 LLM 失败时的备用（错误时自动降级） |
| `kernel` | `resources` / `quota_apply_interval` | 1m | 每 agent 资源预算与配额应用周期 |
| `kernel` | `autopilot` | `false` | 演示任务注入器开关（生产勿开） |
| `security` | `jwt_secret` / `auth_enabled` | 空 / false | JWT 认证（见 §4） |
| `memory` | `archive.enabled` | true | 事件归档（压缩存储） |
| `discovery` | `enabled` | false | 服务发现（可选，需外部依赖） |

**敏感配置纪律**：JWT secret、LLM API key、DB 密码**不要**写进提交到 VCS 的 YAML，
用环境变量注入：`ARES_JWT_SECRET` / `ARES_LLM_API_KEY` / `DB_PASSWORD`。

## 3. 健康检查与可观测

### HTTP 探针（monitoring console，端口来自 `server.port`）

```bash
# 运行时健康（agent 池、状态）
curl -s localhost:8080/api/health | jq

# 运行时配置快照（脱敏）+ 热重载历史
curl -s localhost:8080/api/runtime/config | jq

# 可观测 spans（M4-1）
curl -s localhost:8080/observability/spans | jq

# 进化轨迹（M3-1）与人工反馈（M3-2）
curl -s localhost:8080/evolution/trajectory | jq
curl -s localhost:8080/evolution/feedback | jq
```

> 所有破坏性端点（`/api/agents/:id/kill|resume|retry`、`/api/chaos/*`、
> `/api/tools/call`、`/api/tasks`、`/api/graphs`）默认 **deny-by-default**：
> 未配置凭证时一律 401。权限分级：`/api/tasks` 与 `/api/graphs` 需要
> **write**（operator 及以上；审计动作名 `submit_task` / `submit_graph`）；
> `/api/chaos/*` 与 agent kill 需 **admin**。

### 日志

- 结构化日志（slog）：认证决策（`msg=auth`）与破坏性操作（`msg=action`）带
  subject/role/path 字段；token 永不落日志。
- 优雅关闭：SIGINT/SIGTERM 触发分阶段 shutdown（HTTP → MCP → runtime），
  退出前输出组件快照（`system_runtime snapshot (shutdown)`）。

## 4. 认证（JWT + RBAC）

```bash
# 1. 配置 secret 并启用（环境变量方式）
export ARES_JWT_SECRET=$(openssl rand -hex 32)
export ARES_AUTH_ENABLED=1
bin/ares serve --config ares.yaml

# 2. 签发 token（同一 secret）
export ARES_JWT_SECRET=<同上>
bin/ares auth token --role operator --sub deploy-user --ttl 24h
#   → 输出 HS256 JWT

# 3. 调用破坏性端点
curl -X POST localhost:8080/api/agents/worker-1/kill \
  -H "Authorization: Bearer <token>"
```

角色：`admin`（全部权限，含混沌）、`operator`（写，无混沌）、`agent`（只读）。
兼容：配置了 `ARES_API_KEY` 时，API key 仍可作破坏性端点凭据（双凭据并存）。

## 5. 配置热重载（P1）

- `ares serve --config ares.yaml` 启动 fsnotify watcher（200ms debounce）；
  修改 YAML 后自动 `Reload`：**失败保留上次有效配置**并记入历史，进程不中断。
- 查看历史：`curl -s localhost:8080/api/runtime/config` 的 `history` 字段。
- 范围说明：当前热重载是**快照级**——`/runtime/config` 反映最新配置，但运行子系统
  （LLM adapter、kernel 循环、agents）启动时持有各自拷贝，尚未热切换。
  待具体子系统需要时在其循环内轮询 `ConfigStore.Current()` 增量接入。

## 6. 升级

1. 拉取新版本：`git pull && make build`。
2. 检查 `CHANGELOG.md` 的 `[Unreleased]`：`### Breaking` 段是否影响你的配置。
3. 配置兼容：新增字段有默认值，旧配置可继续解析；删除字段会先弃用（见
   `docs/design/versioning.md`）。
4. 滚动重启：SIGTERM → 等待优雅关闭日志 → 启动新二进制。
5. 验证：`curl /api/health` + `bin/ares version`。

## 7. 故障排查

| 症状 | 排查 |
|---|---|
| 破坏性端点 401 | 未配置 `ARES_JWT_SECRET`/`ARES_API_KEY`（deny-by-default）或 token 过期 |
| 热重载未生效 | 确认 `--config` 指定了文件（自动探测路径已修复）；看 `/api/runtime/config` history 是否有 `reloaded` 记录 |
| 任务失败无重试 | `kernel.go` 的 `RetryPolicy{MaxRetries:2}` 语义：`Attempts < MaxRetries`，1 次失败后 Attempts=1 仍可重试 1 次 |
| LLM 调用全失败 | 检查 `llm.fallbacks`；`createLLMAdapterWithFallback` 返回 `ErrNoLLMAdapter`（`errors.Is` 可检测） |
| agent 卡死 | 看 `/api/health` agent 池；runtime 恢复链（lease 过期 requeue）会自动兜底 |

## 8. 参考

- 架构：`docs/zh/architecture/ares-runtime.md`
- 架构图：`docs/zh/architecture/ares-architecture.md`
- 版本策略：`docs/design/versioning.md`
- CI：`.github/workflows/agentos_ci.yml`（混沌 e2e + 基准）

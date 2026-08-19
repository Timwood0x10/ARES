# ARES Operator Runbook

> Version: 0.3.0 · Commands: `ares serve` / `ares start`
> This document is the M9 milestone deliverable (AGENTOS_DEVELOPMENT_PLAN.md §6),
> covering: quick start, configuration tuning, health checks, authentication,
> hot-reload, upgrades and troubleshooting. For the architecture overview see
> [docs/en/architecture/ares-architecture.md](../en/architecture/ares-architecture.md).

## 1. Quick Start

### Local (source)

```bash
# 1. Prerequisite: local LLM (Ollama)
ollama pull llama3.2

# 2. Build
make build          # output bin/ares, embeds VERSION (0.3.0)

# 3. Prepare config (auto-detects ./ares.yaml by default)
cp configs/ares.yaml ares.yaml   # edit llm.provider / server.port as needed

# 4. Start
bin/ares serve --config ares.yaml
```

### Docker Compose (full demo)

```bash
docker compose up -d          # ollama + ares-demo (:8080)
docker compose logs -f ares-demo
```

> The Compose `ollama` service has a healthcheck; `ares-demo` waits for it
> to become healthy before starting.

## 2. Configuration Tuning (configs/ares.yaml)

| Section | Key field | Default | Notes |
|---|---|---|---|
| `server` | `host` / `port` | `127.0.0.1:8080` | Monitoring console HTTP listen address |
| `llm` | `provider` / `base_url` / `api_key` / `model` | openai | Primary LLM; `api_key` overridable via `ARES_LLM_API_KEY` |
| `llm.fallbacks` | `provider` / `api_key` / `model` | — | Backup LLMs on primary failure (automatic failover) |
| `kernel` | `resources` / `quota_apply_interval` | 1m | Per-agent resource budget and quota application period |
| `kernel` | `autopilot` | `false` | Demo task injector switch (keep off in production) |
| `security` | `jwt_secret` / `auth_enabled` | empty / false | JWT authentication (see §4) |
| `memory` | `archive.enabled` | true | Event archiving (compacted storage) |
| `discovery` | `enabled` | false | Service discovery (optional, external deps) |

**Secret hygiene**: do **not** commit JWT secrets, LLM API keys or DB passwords
to version-controlled YAML. Inject via environment variables:
`ARES_JWT_SECRET` / `ARES_LLM_API_KEY` / `DB_PASSWORD`.

## 3. Health Checks & Observability

### HTTP probes (monitoring console, port from `server.port`)

```bash
# Runtime health (agent pool, statuses)
curl -s localhost:8080/api/health | jq

# Runtime config snapshot (redacted) + hot-reload history
curl -s localhost:8080/api/runtime/config | jq

# Observable spans (M4-1)
curl -s localhost:8080/observability/spans | jq

# Evolution trajectory (M3-1) and human feedback (M3-2)
curl -s localhost:8080/evolution/trajectory | jq
curl -s localhost:8080/evolution/feedback | jq
```

> All destructive endpoints (`/api/agents/:id/kill|resume|retry`,
> `/api/chaos/*`, `/api/tools/call`) are **deny-by-default**: they return 401
> until credentials are configured.

### Logs

- Structured logging (slog): auth decisions (`msg=auth`) and destructive
  actions (`msg=action`) carry subject/role/path fields; tokens are never logged.
- Graceful shutdown: SIGINT/SIGTERM triggers phased teardown
  (HTTP → MCP → runtime) and prints a component snapshot before exit
  (`system_runtime snapshot (shutdown)`).

## 4. Authentication (JWT + RBAC)

```bash
# 1. Configure secret and enable (env-var style)
export ARES_JWT_SECRET=$(openssl rand -hex 32)
export ARES_AUTH_ENABLED=1
bin/ares serve --config ares.yaml

# 2. Mint a token (same secret)
export ARES_JWT_SECRET=<same as above>
bin/ares auth token --role operator --sub deploy-user --ttl 24h
#   → prints an HS256 JWT

# 3. Call a destructive endpoint
curl -X POST localhost:8080/api/agents/worker-1/kill \
  -H "Authorization: Bearer <token>"
```

Roles: `admin` (everything, incl. chaos), `operator` (write, no chaos),
`agent` (read-only). Compatibility: when `ARES_API_KEY` is configured, the API
key still works as a credential on destructive endpoints (dual credential).

## 5. Configuration Hot-Reload (P1)

- `ares serve --config ares.yaml` starts an fsnotify watcher (200ms debounce);
  editing the YAML triggers a `Reload`: **on failure the previous valid config
  is kept** and recorded in history, the process keeps running.
- View history: the `history` field of `curl -s localhost:8080/api/runtime/config`.
- Scope note: hot-reload is currently **snapshot-level** — `/runtime/config`
  reflects the latest config, but running subsystems (LLM adapter, kernel
  loops, agents) hold their own copies at startup and are not hot-swapped yet.
  When a specific subsystem needs it, poll `ConfigStore.Current()` in its loop
  and incrementally wire it in.

## 6. Upgrades

1. Pull the new version: `git pull && make build`.
2. Check `CHANGELOG.md` `[Unreleased]`: does the `### Breaking` section affect
   your config?
3. Config compatibility: new fields have defaults (old configs keep parsing);
   removed fields are deprecated first (see `docs/design/versioning.md`).
4. Rolling restart: SIGTERM → wait for graceful-shutdown logs → start the new
   binary.
5. Verify: `curl /api/health` + `bin/ares version`.

## 7. Troubleshooting

| Symptom | Check |
|---|---|
| Destructive endpoints return 401 | No `ARES_JWT_SECRET`/`ARES_API_KEY` configured (deny-by-default) or token expired |
| Hot-reload does not take effect | Confirm `--config` points at a file (auto-detected path is fixed); check `/api/runtime/config` history for a `reloaded` record |
| Task fails without retry | `kernel.go` `RetryPolicy{MaxRetries:2}` semantics: `Attempts < MaxRetries`; after 1 failure Attempts=1 so 1 retry remains |
| All LLM calls fail | Check `llm.fallbacks`; `createLLMAdapterWithFallback` returns `ErrNoLLMAdapter` (detectable via `errors.Is`) |
| Agent stuck | Look at the `/api/health` agent pool; the runtime recovery chain (lease-expiry requeue) backstops automatically |

## 8. References

- Architecture: `docs/en/architecture/ares-runtime.md`
- Architecture diagrams: `docs/en/architecture/ares-architecture.md`
- Versioning policy: `docs/design/versioning.md`
- CI: `.github/workflows/agentos_ci.yml` (chaos e2e + benchmarks)

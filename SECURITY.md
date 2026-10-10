# Security Policy

## Supported Versions

ARES is pre-1.0 software. We provide security fixes for the **latest minor
release only**.

| Version | Supported          |
|---------|--------------------|
| 0.2.x   | :white_check_mark: |
| < 0.2   | :x:                |

## Reporting a Vulnerability

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please report vulnerabilities to **security@timwood0x10.dev** with:

1. A description of the vulnerability
2. Steps to reproduce
3. Potential impact
4. Suggested fix (if any)

### Response Timeline

- **Acknowledgment**: within 48 hours
- **Initial assessment**: within 7 days
- **Fix or mitigation**: within 30 days for high-severity issues

We appreciate responsible disclosure and will credit reporters in the
CHANGELOG (unless anonymity is preferred).

## Security Considerations

### Configuration Path Traversal

The config loader (`internal/ares_config/config.go`) restricts config file
paths to an allowed directory via `SetAllowedConfigDir()`. **Do not disable
this check in production.**

### Database Access

ARES uses PostgreSQL for persistence. Ensure:

- Database credentials are stored in environment variables or secret managers,
  never in committed config files
- The database user has minimal required privileges
- Production databases use TLS connections

### LLM API Keys

LLM provider API keys (OpenAI, Anthropic, etc.) are read from environment
variables. **Never hardcode API keys in source files or committed YAML
configurations.**

### MCP Tool Execution

MCP tools execute arbitrary commands. Only enable MCP servers from trusted
sources. The `internal/ares_security` package provides authentication (JWT),
authorization (RBAC), audit logging and input sanitization — it does **not**
sandbox tool execution. Constrain MCP stdio servers yourself: launch them
under a least-privilege account or container, and keep the server list to
commands you control. The stdio transport requires an absolute command path
(`mcpclient/stdio.go`) precisely because a bare name would otherwise resolve
against `PATH`.

### Tenancy

ARES is **single-tenant by default**: with no `tenant_id` on a submission,
every task, distilled fact and knowledge recall runs under the `default`
tenant. Tenant isolation is a **per-request opt-in scope** (`tenant_id` on
`POST /api/tasks` / `POST /api/graphs`, or `payload["tenant_id"]` on SDK
submissions) — there is no configuration switch, because the field itself is
the switch.

Two properties operators should rely on:

- **The system never generates a non-default tenant on its own.** The tenant
  is carried on the task's checkpoint envelope, restored into the execution
  context (`tenantctx`) and stamped onto what the request derives
  (planner-grown nodes, `ask_agent` collaboration sessions, distilled
  facts). Every fallback path resolves to `default`.
- **The tenant is Kernel-enforced, not model-enforced.** An LLM-supplied
  `tenant_id` in tool arguments, `create_task` payloads or `ask_agent`
  payloads is overwritten by the executing context's tenant — the same
  contract `Origin` follows. A model cannot self-select the tenant its work
  executes under.

**Isolation is layered — not end-to-end column-level tenancy.** Verified
boundaries as of v0.3.1:

- The **experience/distillation store** isolates at the column level: every
  query in `internal/storage/postgres/repositories/experience_repository.go`
  carries a `tenant_id = $N` predicate, and distillation writes resolve the
  tenant through a context seam (`distillation.WithTenant`) so a runtime
  override reaches the write side.
- The **knowledge side isolates by `namespace`, not by a tenant column.**
  `KnowledgeStore` objects have no `tenant_id` column; `StoreProvider` maps
  tenant → namespace via `namespaceFor` (explicit `Scope.Namespaces` →
  `tenantctx` → provider default). Isolation therefore depends on a namespace
  always being supplied — the underlying `Get`/`Delete`/`Search` carry no
  tenant or namespace predicate of their own.
- **AKG-distilled facts are not tenant-split today**: `akgNamespace` in
  `internal/ares_bootstrap/knowledge_akg.go` is the constant `"default"`, so
  every AKG fact lands in one namespace.

**Warning for multi-tenant deployments**: today the tenant is
*caller-declared* at the HTTP boundary. Any authenticated caller can submit
under any tenant id. A genuine multi-tenant deployment MUST bind the tenant
at the authentication layer (derive it from the JWT principal server-side)
instead of trusting the request body — and the two knowledge-side gaps above
must be closed before the isolation is end to end.

### IPC sender provenance

Agent-to-agent collaboration (`ask_agent` → the `agentipc` bus) follows the
same Kernel-stamped provenance contract as `Task.Origin` and tenancy:

- The `ask_agent` tool arguments carry **no `from` field** (`To`/`Topic`/
  `Payload` only — `internal/agentsyscall/syscall.go`). The binder never
  decodes a sender identity from model output.
- The Kernel stamps `from := kctx.CallerID(ctx)` at the syscall boundary, so
  the bus always sees the executing agent's identity, never a
  model-supplied one. A `{"from": "..."}` key smuggled into the tool-args
  map is ignored.
- Direct in-process `agentipc.Bus.Send(ctx, from, ...)` callers pass `from`
  as a library parameter — that surface trusts its Go callers by
  construction (an in-process caller can already do anything in-process).
  The LLM-reachable boundary is the syscall above, and it is closed.

Locked by `internal/agentsyscall/syscall_from_provenance_test.go`.

### External task surface auth posture

`POST /api/tasks` and `GET /api/tasks/{task_id}` ride the control-plane
route registry:

- **Credential precedence**: `security.api_key` (dedicated HTTP
  control-plane credential, redacted in `Config.Redacted()`) when set; empty
  falls back to `llm.api_key` — the pre-decoupling behavior, kept so
  existing deployments keep working. New deployments SHOULD set
  `security.api_key` so leaking the LLM provider credential no longer hands
  over the HTTP control plane.
- **Write gate** (`POST /api/tasks`): deny-by-default. With no credential
  layer configured every submission is rejected with 401 — loopback
  included. A read-only (agent-role) JWT earns 403, not submission.
- **Read gate** (`GET /api/tasks/{task_id}`): once ANY credential layer is
  configured (JWT / api key / introspect token) it is required on every
  client, loopback included. With no layer configured only direct loopback
  passes (the documented local-dev posture); non-loopback clients get 401
  and proxied-loopback headers are not trusted.
- **`server.default_capability` is audit-only**: the Submitter normalizes
  every submission to the single L2 capability (`ares/plan`); the yaml value
  never routes tasks to a different agent population.
- **Result channel**: `GET /api/tasks/{id}` / `POST ?wait=` surface the
  session answer as `result` on the L2 path (the fabric task's terminal
  answer node), and the failure cause as `error` on FAILED — the quantum
  step error persisted into the checkpoint envelope (`last_error`), falling
  back to cascade provenance (`dependency <id> failed`) or a failed session
  answer node when no cause was persisted. The full checkpoint envelope
  (user profile, strategy attribution, raw payload) stays internal.

Locked by `cmd/ares/agent_routes_external_auth_test.go`,
`cmd/ares/checkauth_failclosed_test.go`, and
`internal/agentsyscall/syscall_from_provenance_test.go`.

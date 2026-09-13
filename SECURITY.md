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
  context (`tenantctx`) and stamped onto everything the request derives
  (planner-grown nodes, `ask_agent` collaboration sessions, distilled
  facts). Every fallback path resolves to `default`.
- **The tenant is Kernel-enforced, not model-enforced.** An LLM-supplied
  `tenant_id` in tool arguments, `create_task` payloads or `ask_agent`
  payloads is overwritten by the executing context's tenant — the same
  contract `Origin` follows. A model cannot self-select the tenant its work
  executes or recalls knowledge under.

**Warning for multi-tenant deployments**: today the tenant is
*caller-declared* at the HTTP boundary. Any authenticated caller can submit
under any tenant id. A genuine multi-tenant deployment MUST bind the tenant
at the authentication layer (derive it from the JWT principal server-side)
instead of trusting the request body. The runtime plumbing is ready for that
swap — only the source of the tenant value changes.

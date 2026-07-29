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
sources. The `internal/ares_security` package provides sandboxing utilities —
use them.

### Quant Trading Module

The quant module (`internal/ares_quant/`) can execute real market operations
if connected to live trading APIs. **Never enable live trading credentials in
development or testing environments.**

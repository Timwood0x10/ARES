# Quick Start

Run the ARES "one interface" golden path in 10 minutes.

**All configuration lives in `ares.yaml`** — the single config entry point (LLM, memory, kernel, external-surface defaults are all in that one file). There are no config flags.

## Prerequisites

- **Go 1.26+**: `go version`
- **LLM backend** (one of, configured in the `llm:` section of ares.yaml):
  - Ollama: `ollama pull llama3.2` (the yaml defaults target this provider/model)
  - Cloud API: fill `llm.provider` / `llm.api_key` / `llm.model` in ares.yaml
- **PostgreSQL + pgvector** (optional): only needed for persistence / distillation / vector retrieval

```bash
git clone https://github.com/Timwood0x10/ares
cd ares
go mod download
```

## Golden path: one interface, two faces, one kernel

Scheduling (kernel / MutableDAG / GA / memory) sits hidden behind the interface — callers never learn it.

### Face 1 — in-process (Go programs, zero HTTP)

```go
package main

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/api"
)

func main() {
	rt := api.MustNew() // reads ./ares.yaml; wires LLM/memory/evolution kernel
	defer rt.Close()

	agent := rt.NewAgent("assistant", api.WithInstruction("You are helpful."))
	result, err := agent.Run(context.Background(), "What is Go concurrency?")
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Output)
}
```

### Face 2 — serve HTTP (non-Go callers / remote hosts)

```bash
ares serve &   # boots the full AgentOS kernel from ares.yaml

# submit: the external minimal body {"query": "..."} — capability defaults
# to server.default_capability from ares.yaml. NOTE: that default is
# AUDIT-ONLY — the Submitter normalizes execution to the single L2
# capability (ares/plan); it does not route to a different peer.
curl -sS -X POST localhost:8080/api/tasks \
  -H "Authorization: Bearer <security.api_key or llm.api_key fallback>" \
  -H 'Content-Type: application/json' \
  -d '{"query":"What is Go concurrency?"}'
# → 202 {"task_id":"...","status":"submitted"}

# read the result: TaskView slim fields + result (the session answer on the
# L2 path; the error field carries the failure cause on FAILED)
curl -sS -H "Authorization: Bearer <same>" localhost:8080/api/tasks/<task_id>

# optional sync wait: ?wait=<dur> blocks until the result channel resolves
# (terminal state AND the session answer where one exists; hard cap 300s;
# empty value uses tasks.wait_timeout, default 60s); on timeout the
# response stays 202 and carries the current state — submission never fails
# on wait expiry
```

The HTTP layer is a thin adapter over the internal submitter — **no scheduling logic lives there**. The HTTP gate credential prefers the dedicated `security.api_key`; when unset it falls back to `llm.api_key` (legacy behavior). **With neither set the write gate answers 401 (deny-by-default, loopback included)** — providers that need no `llm.api_key` (e.g. ollama) must set `security.api_key` first; `ares init` generates one into the template.

### One command (same yaml, human output, zero config flags)

```bash
ares run -c ares.yaml "What is Go concurrency?"
```

## Run examples

Examples live under `examples/_fixtures/`:

```bash
# golden-path example (in-process api/ + the HTTP contract in its header)
go run examples/_fixtures/01-quickstart/main.go

# other examples run by numbered directory, e.g.:
go run examples/_fixtures/03-dag-workflow/main.go
go run examples/_fixtures/31-memory-distillation/main.go
```

`make quickstart` runs 01-quickstart equivalently.

## External-surface sections in ares.yaml

```yaml
server:
  # capability used by POST /api/tasks when the request omits one
  # (default ares/plan) — AUDIT-ONLY: execution is always normalized to
  # the L2 capability; this value does not route to other peers
  default_capability: ares/plan
tasks:
  # default sync-wait for POST /api/tasks?wait= AND the ares run context
  # timeout when set (Go duration; hard cap 300s; run keeps its own larger
  # unset default of 120s)
  wait_timeout: 60s
security:
  # dedicated HTTP control-plane credential (preferred over llm.api_key).
  # With BOTH empty the write gate answers 401; ares init generates a
  # random value into the template
  api_key: ""
memory:
  enabled: true   # sample default on; distillation needs storage+embedding,
                  # RAG prompt injection additionally needs enable_rag
```

`ares init` generates a working ares.yaml template (including a random `security.api_key`); `ares doctor` / `ares status` report the assembled runtime.

## Common issues

- **LLM call fails**: check the `llm:` section (provider/base_url/model/api_key); Ollama needs a local `ollama serve` with the model pulled
- **POST /api/tasks 401**: the write gate is deny-by-default — send `Authorization: Bearer <security.api_key>` (falls back to `llm.api_key` when unset; or configure JWT). With both empty (e.g. the default ollama config) set `security.api_key` in ares.yaml first
- **GET /api/tasks/{id} 401**: once any credential layer is configured the read gate requires it too
- **Task never terminal**: read `state` via GET; the `error` field carries the failure cause. `capability` is audit-only (execution is always normalized to `ares/plan`) and unrelated to `agents.peers` capabilities — do not chase that match

## Next steps

- Architecture overview: [ARCHITECTURE.md](../../../ARCHITECTURE.md)
- Full config reference: [config.yaml guide (EN)](../../articles/en/25-config-yaml-guide.en.md) / [中文](../../articles/zh/25-config-yaml-guide.zh.md)
- Integration guide: [integration-guide.md](../development/integration-guide.md)
- FAQ: [faq.md](faq.md)

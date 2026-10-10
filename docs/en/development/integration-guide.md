# ARES Integration Guide

> **Rewritten for 0.3.2.** The previous revision of this page documented the
> `api/service` facade (`service.NewAgentService`, `service.Config`), which was
> **removed in 0.3.2** — none of those samples compile. This page now lists the
> entry points that actually exist; the old text is in git history.

## Pick an entry point

| What you want | Use | Where |
|---|---|---|
| Embed the runtime in a Go program, config from `ares.yaml` | `sdk.MustNew()` (zero-arg, reads `./ares.yaml`, panics on error) | `sdk/quickstart.go` |
| Same, but handle init errors | `rt, err := sdk.New(opts...)` | `sdk/sdk.go` |
| Same, programmatic options + panic on error | `sdk.NewRuntime(opts...)` | `sdk/sdk.go` |
| Public aliases instead of importing `sdk` | `api.MustNew()` / `api.New(opts...)` / `api.NewRuntime(opts...)` | `api/ares.go` |
| Drive it over HTTP | `POST /api/tasks`, `GET /api/tasks/{id}`, `POST /api/graphs` | [serve walkthrough](../reference/serve-walkthrough.md) |
| Operate a deployment | `ares serve` / `ares run`, everything configured in `ares.yaml` | [operator runbook](../operator/README.en.md) |
| Register tools / capabilities | `sdk.ToolFunc`, `sdk.Tool` | [tool calling cookbook](../cookbook/tool.md) |
| Run and test locally | `make test`, `make ci-test` (CI replica) | [testing guide](./testing-guide.md) |

## Minimal library integration

```go
package main

import (
	"context"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	// Reads ./ares.yaml — the single configuration entry point (no env vars).
	rt := sdk.MustNew()
	defer rt.Close()

	agent := rt.NewAgent("assistant", sdk.WithInstruction("You are helpful."))
	out, err := agent.Run(context.Background(), "summarise this file")
	if err != nil {
		panic(err)
	}
	_ = out
}
```

Programmatic configuration (no `ares.yaml`) uses options instead:

```go
rt := sdk.NewRuntime(
	sdk.WithOllama("llama3.2"),
	sdk.WithDefaultMemory(),
)
defer rt.Close()
```

More runnable code: `examples/_fixtures/*` and `examples/_internal/*` (`make examples` builds all of them).

## Facts this page used to get wrong

- There is **no** `api/service` package: no `service.NewAgentService`, no
  `service.Config`, no `service.ToolConfig`. The `api/` tree keeps only the
  aliases in `api/ares.go`, `options.go`, `types.go`, `errors.go`.
- `MustNew` takes **no arguments**. `sdk.MustNew(sdk.WithOllama(...))` does not
  compile — that is `sdk.NewRuntime(...)` / `sdk.New(...)`.
- `ares.yaml` is the single configuration entry point; there is no environment
  variable override layer.
- Tenant and provenance values come from context, never from the request body
  (see [SECURITY.md](../../../SECURITY.md) → Tenancy).

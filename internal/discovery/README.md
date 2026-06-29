# Service Discovery Engine

## Architecture

```
DiscoveryProvider(s) → Identity Merge → Health Verify → EventBus
```

## Providers

| Provider | Source | Confidence |
|----------|--------|------------|
| ARES Registry | `~/.ares/mcp-registry.json` | 100% |
| Claude | `~/.claude.json`, `.claude/settings.json` | 95% |
| Cursor | `~/.cursor/mcp.json` | 95% |
| VSCode | `.vscode/mcp.json` | 95% |
| Binary Probe | PATH scan + `--help` verification | 80% |

## Events

Events are emitted for every change and can be persisted to any DB:

- `discovery.service.added`
- `discovery.service.updated`
- `discovery.service.removed`
- `discovery.health.changed`
- `discovery.cycle.complete`

## Usage (Public API)

```go
import "github.com/Timwood0x10/ares/api/discovery"

engine := discovery.NewEngine("", nil)

// Events → DB
engine.OnEvent(func(evt discovery.Event) {
    db.Save(evt)
})

// Start periodic discovery
engine.Start(ctx, 5*time.Minute)

// Or manual
engine.DiscoverNow(ctx)
services, _ := engine.List(ctx)
```

## Identity Merge

Same service from multiple sources is merged:

```
codegraph
  sources: ares(100%), binary-probe(80%)
  records: 2
```

## Auto-Tagging

Binary probe runs `--help` and extracts capability/domain tags:

```
codegraph
  tags: [capability:search capability:graph domain:code]
```

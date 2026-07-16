# ares Architecture Deep Dive (XXII): Config System — One YAML, Twelve Modules

Every module needs configuration. LLM needs provider and model. Memory needs history size. Evolution needs population size. Storage needs host and port. When you have twelve modules, you have twelve config files — unless you have a config system.

`internal/ares_config/config.go` (844 lines) and `sdk/config.go` (165 lines) are that system. One YAML file, validated at load time, drives every module.

---

## The Problem: Twelve Config Sources

v0.2.4 had configuration chaos:

| Module | Source | Format |
|--------|--------|--------|
| LLM | Environment variables | `OPENAI_API_KEY=...` |
| Memory | Hardcoded in `main.go` | Go struct literals |
| Evolution | Separate `evolution.yaml` | YAML |
| Storage | `DATABASE_URL` env var | Connection string |
| MCP | Command-line flags | `--mcp-command=...` |

Five sources, three formats, zero validation. A typo in `evolution.yaml`? Silent failure at runtime. A missing `DATABASE_URL`? Cryptic panic in `sql.Open`.

**Honest reflection**: We tried using Viper. It's powerful, but the magic (env binding, remote config, file watching) kept surprising us. A team member spent two hours debugging why his config wasn't loading — Viper was reading a cached copy from a different directory. We went back to `yaml.v3` and explicit loading.

---

## The Design: One Config, Typed, Validated

### The Root Config

```go
// internal/ares_config/config.go
type Config struct {
    Server     ServerConfig       `yaml:"server"`
    LLM        LLMConfig          `yaml:"llm"`
    Agents     AgentsConfig       `yaml:"agents"`
    Tools      ToolsConfig        `yaml:"tools"`
    Prompts    PromptsConfig      `yaml:"prompts"`
    Output     OutputConfig       `yaml:"output"`
    Validation ValidationConfig   `yaml:"validation"`
    Workflow   WorkflowConfig     `yaml:"workflow"`
    Storage    StorageConfig      `yaml:"storage"`
    Memory     MemoryConfig       `yaml:"memory"`
    MCP        MCPConfig          `yaml:"mcp"`
    Dashboard  DashboardAppConfig `yaml:"dashboard"`
    Evolution  EvolutionConfig    `yaml:"evolution"`
}
```

One struct, twelve sections. Each section is a typed struct with `yaml` tags.

### Loading with Path Traversal Protection

```go
// internal/ares_config/config.go
func Load(path string) (*Config, error) {
    // Security: validate path is within allowed directory
    if allowedConfigDir != "" {
        absPath, err := filepath.Abs(path)
        if err != nil {
            return nil, fmt.Errorf("failed to get absolute path: %w", err)
        }
        absDir, err := filepath.Abs(allowedConfigDir)
        if err != nil {
            return nil, fmt.Errorf("failed to get absolute directory: %w", err)
        }
        if !strings.HasPrefix(absPath, absDir+string(filepath.Separator)) {
            return nil, fmt.Errorf("config path %q outside allowed directory", path)
        }
    }
    // ... load and parse YAML ...
}
```

`SetAllowedConfigDir()` restricts where config files can be loaded from. This prevents path traversal attacks — a malicious `../secret.yaml` is rejected before parsing.

**Honest reflection**: We initially used `filepath.Rel` to detect traversal. It worked on macOS but failed on Windows because of path separator differences. The `strings.HasPrefix` check is simpler and cross-platform.

### Typed Validation

Each section has its own validation:

```go
// internal/ares_config/config.go
func (c *Config) Validate() error {
    if err := c.LLM.Validate(); err != nil {
        return fmt.Errorf("llm: %w", err)
    }
    if err := c.Storage.Validate(); err != nil {
        return fmt.Errorf("storage: %w", err)
    }
    if err := c.MCP.Validate(); err != nil {
        return fmt.Errorf("mcp: %w", err)
    }
    // ... validate all sections ...
    return nil
}
```

Validation fails fast. A missing `command` field in an MCP server config produces:

```
mcp: server "filesystem": command is required
```

Not a runtime panic. Not a silent failure. A clear, actionable error message.

---

## The Distillation Threshold (v0.2.8)

The `DistillConfig` gained a `Threshold` field in v0.2.8:

```go
// internal/ares_config/config.go
type DistillConfig struct {
    Enabled     bool   `yaml:"enabled"`
    Storage     string `yaml:"storage"`
    VectorStore bool   `yaml:"vector_store"`
    Prompt      string `yaml:"prompt"`
    // Threshold is the number of conversation rounds that accumulate before
    // distillation fires. 0 preserves legacy ungated behaviour.
    Threshold int `yaml:"threshold"`
}
```

In `ares.yaml`:

```yaml
memory:
  enabled: true
  task_distillation:
    enabled: true
    threshold: 3  # fire distillation every 3 conversation rounds
```

Semantics:
- `0` = ungated (fire every event) — legacy behavior
- `N` = fire every N conversation rounds — throttles distillation

This mirrors the v0.2.4 `examples/knowledge-base/config.yaml` convention. The threshold prevents the distiller from firing on every single conversation event, which would overwhelm the embedding pipeline under load.

**Honest reflection**: The threshold was originally a hardcoded constant (`const distillationThreshold = 3`). Making it YAML-driven was a 10-line change, but it unlocked per-deployment tuning. The lesson: every hardcoded constant is a future config option.

---

## The SDK Config Layer

`sdk/config.go` (165 lines) bridges the raw YAML config to SDK options:

```go
// sdk/config.go
type Config struct {
    LLM        LLMConfig        `yaml:"llm"`
    Memory     MemoryConfig     `yaml:"memory"`
    Evolution  EvolutionConfig  `yaml:"evolution"`
    Knowledge  KnowledgeConfig  `yaml:"knowledge"`
    MCP        MCPConfig        `yaml:"mcp"`
    Tools      ToolsConfig      `yaml:"tools"`
}

func LoadConfigFile(path string) (*Config, error)
func (c *Config) ToOptions() ([]Option, error)
```

`ToOptions()` converts the YAML config to a slice of SDK `Option` functions:

```go
// sdk/config.go (simplified)
func (c *Config) ToOptions() ([]Option, error) {
    var opts []Option

    // LLM
    switch c.LLM.Provider {
    case "openai":
        opts = append(opts, WithOpenAI(c.LLM.Model))
    case "ollama":
        opts = append(opts, WithOllama(c.LLM.Model))
    case "anthropic":
        opts = append(opts, WithAnthropic(c.LLM.Model))
    }

    // Memory
    if c.Memory.Enabled {
        opts = append(opts, WithDefaultMemory())
        if c.Memory.MaxHistory > 0 || c.Memory.MaxSessions > 0 {
            opts = append(opts, WithMemoryConfig(c.Memory.MaxHistory, c.Memory.MaxSessions))
        }
    }

    // Distillation
    if c.Memory.Distillation.Enabled {
        opts = append(opts, WithDistillation(c.Memory.Distillation.Threshold))
    }

    // ... evolution, knowledge, mcp, tools ...

    return opts, nil
}
```

This lets users do:

```go
cfg, _ := ares.LoadConfigFile("ares.yaml")
opts, _ := cfg.ToOptions()
rt := ares.MustNew(opts...)
```

One YAML file drives the entire SDK.

---

## The Zero-Value Philosophy

ares has a config philosophy: **zero means "use the component default."**

```yaml
memory:
  enabled: true
  max_history: 0       # 0 → use memory component default
  max_sessions: 0      # 0 → use memory component default
  distillation_threshold: 0  # 0 → ungated (legacy behavior)
```

This means:
1. You only configure what you want to tune
2. Defaults live in the component, not the config
3. Adding a new config option doesn't break existing configs

**Honest reflection**: The zero-value philosophy has a downside — you can't tell if a user set `max_history: 0` intentionally or just didn't configure it. We considered using `*int` (nil = unset, 0 = explicit zero), but the added complexity wasn't worth it. In practice, "unset" and "zero" mean the same thing: use the default.

---

## Lessons

Configuration is a layer nobody celebrates. You can't demo `Config.Validate()` to investors. But it's the difference between "works on my machine" and "works in production."

The config system is the first thing new users touch (via `ares.yaml`), and the last thing they think about (until something breaks). Making it typed, validated, and zero-value-friendly means users spend less time configuring and more time building.

**The best config system is the one you forget exists.** You write `ares.yaml`, it just works.

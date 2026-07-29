# ares 架构拆解 (XXII)：配置系统——一个 YAML，十二个模块

每个模块都需要配置。LLM 需要 provider 和 model。Memory 需要历史长度。Evolution 需要种群规模。Storage 需要 host 和 port。当你有十二个模块时，你就有十二个配置文件——除非你有配置系统。

`internal/ares_config/config.go`（844 行）和 `sdk/config.go`（165 行）就是这个系统。一个 YAML 文件，加载时校验，驱动每个模块。

---

## 问题：十二个配置源

v0.2.4 配置混乱：

| 模块 | 来源 | 格式 |
|------|------|------|
| LLM | 环境变量 | `OPENAI_API_KEY=...` |
| Memory | 硬编码在 `main.go` | Go 结构体字面量 |
| Evolution | 独立的 `evolution.yaml` | YAML |
| Storage | `DATABASE_URL` 环境变量 | 连接字符串 |
| MCP | 命令行 flag | `--mcp-command=...` |

五个来源，三种格式，零校验。`evolution.yaml` 里一个拼写错误？运行时静默失败。缺 `DATABASE_URL`？`sql.Open` 里诡异的 panic。

**坦诚反思**：我们试过 Viper。它很强大，但魔法太多（环境绑定、远程配置、文件监听）一直给我们惊喜。一个团队成员花了两小时调试为什么配置没加载——Viper 在从另一个目录读缓存。我们回到了 `yaml.v3` 和显式加载。

---

## 设计：一个配置，有类型，有校验

### 根配置

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

一个结构体，十二个 section。每个 section 是带 `yaml` tag 的有类型结构体。

### 带路径遍历保护的加载

```go
// internal/ares_config/config.go
func Load(path string) (*Config, error) {
    // 安全：校验路径在允许的目录内
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
    // ... 加载并解析 YAML ...
}
```

`SetAllowedConfigDir()` 限制配置文件能从哪里加载。这防止路径遍历攻击——恶意的 `../secret.yaml` 在解析前就被拒绝。

**坦诚反思**：我们最初用 `filepath.Rel` 检测遍历。在 macOS 上能用，但在 Windows 上因为路径分隔符差异失败了。`strings.HasPrefix` 检查更简单且跨平台。

### 有类型校验

每个 section 有自己的校验：

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
    // ... 校验所有 section ...
    return nil
}
```

校验快速失败。MCP server config 里缺 `command` 字段会产生：

```
mcp: server "filesystem": command is required
```

不是运行时 panic。不是静默失败。而是清晰、可操作的错误消息。

---

## 蒸馏阈值（v0.2.8）

`DistillConfig` 在 v0.2.8 里加了 `Threshold` 字段：

```go
// internal/ares_config/config.go
type DistillConfig struct {
    Enabled     bool   `yaml:"enabled"`
    Storage     string `yaml:"storage"`
    VectorStore bool   `yaml:"vector_store"`
    Prompt      string `yaml:"prompt"`
    // Threshold 是蒸馏触发前累积的对话轮次数。
    // 0 保留旧的不过门行为。
    Threshold int `yaml:"threshold"`
}
```

在 `ares.yaml` 里：

```yaml
memory:
  enabled: true
  task_distillation:
    enabled: true
    threshold: 3  # 每 3 轮对话触发一次蒸馏
```

语义：
- `0` = 不过门（每个事件都触发）——旧行为
- `N` = 每 N 轮对话触发——节流蒸馏

这模仿了 v0.2.4 `examples/knowledge-base/config.yaml` 的约定。阈值防止蒸馏器对每个对话事件都触发，那会在负载下压垮 embedding pipeline。

**坦诚反思**：阈值最初是硬编码常量（`const distillationThreshold = 3`）。把它改成 YAML 驱动只是 10 行的改动，但解锁了按部署调优。教训：每个硬编码常量都是未来的配置选项。

---

## SDK 配置层

`sdk/config.go`（165 行）把原始 YAML 配置桥接到 SDK 选项：

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

`ToOptions()` 把 YAML 配置转换成 SDK `Option` 函数的切片：

```go
// sdk/config.go（简化）
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

这让用户可以做：

```go
cfg, _ := ares.LoadConfigFile("ares.yaml")
opts, _ := cfg.ToOptions()
rt := ares.MustNew(opts...)
```

一个 YAML 文件驱动整个 SDK。

---

## 零值哲学

ares 有个配置哲学：**零值意味着"用组件默认值"。**

```yaml
memory:
  enabled: true
  max_history: 0       # 0 → 用 memory 组件默认值
  max_sessions: 0      # 0 → 用 memory 组件默认值
  distillation_threshold: 0  # 0 → 不过门（旧行为）
```

这意味着：
1. 你只配置想调的
2. 默认值在组件里，不在配置里
3. 加新配置选项不会破坏现有配置

**坦诚反思**：零值哲学有缺点——你没法分辨用户是故意设 `max_history: 0` 还是根本没配。我们考虑过用 `*int`（nil = 未设，0 = 显式零），但增加的复杂性不值得。实践中"未设"和"零"意味着同一件事：用默认值。

---

## 教训

配置是没人庆祝的层。你不能给投资人演示 `Config.Validate()`。但它是"在我机器上能跑"和"生产能用"的区别。

配置系统是新用户第一个碰的东西（通过 `ares.yaml`），也是最后一个想到的东西（直到出问题）。让它有类型、有校验、零值友好，意味着用户花更少时间配置，更多时间构建。

**最好的配置系统是你忘记它存在的那个。** 你写 `ares.yaml`，它就能跑。

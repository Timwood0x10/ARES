# ARES 开发计划：极致降低上手门槛

> 核心理念：冻结新 Feature，专注 Framework Maturity
> 目标：5 分钟从零跑到 Hello World

---

## Phase 0 — 基础设施（已完成 ✅）

- [x] 顶层 `ares` 包骨架 — `ares.go` + `options.go`
- [x] LLM 多 Provider — OpenAI / Ollama / Anthropic / OpenRouter
- [x] Tool 调用循环 — ReAct 循环实现
- [x] Memory 集成 — `WithDefaultMemory()` 选项已有，`memsvc.New(nil)` 对接完成
- [x] 编译验证 — `go build ./...` + `go vet ./...` + `go test -short ./...` 均无错
- [x] Trace 日志 — `[ares:trace]` 每步打印
- [x] 清理 `runner/` 临时目录 — 统一走顶层 `ares` 包

---

## Phase 1 — 5 分钟上手闭环（本周，P0）

### 1.1 极简 Quickstart 示例

- [x] 创建 `examples/quickstart/main.go`
  - 配置 LLM（OpenAI / Ollama / DeepSeek 等）
  - 简单 Tool（计算器）
  - 运行 + 输出结果
  - 开启 Memory

**验收：** `go build ./examples/quickstart/...` 编译通过

### 1.2 一键启动脚本

- [x] `Makefile` 添加 `make quickstart` 目标
  ```makefile
  quickstart:  ## 5 分钟快速开始
      go run examples/quickstart/main.go
  ```

### 1.3 README 大重构 —— 暂缓

- [ ] 顶部 "🚀 5 分钟快速开始" 区块（复制粘贴代码）
- [ ] "📊 特性对比" 表（LangGraph / AutoGen / trpc-agent-go）
- [ ] 安装命令简化：`go install`
- [ ] 清晰定位："谁适合使用 ARES"
  - 需要长期运行、高容错、自我优化的 Agent 系统
  - 喜欢 Go 原生、追求极致控制和韧性的开发者

---

## Phase 2 — 示例生态（已完成 ✅）

每个示例配 README + docker-compose（如果需要），按使用频率排序：

- [x] `examples/quickstart` — 最简 Chat（≤20 行）
- [x] `examples/tool-calling` — 计算器 + 搜索（≤60 行）
- [x] `examples/dag-workflow` — MutableDAG 线性/条件流程（≤130 行）
- [x] `examples/multi-agent` — Leader/Sub + Failover（≤60 行）
- [x] `examples/evolution-demo` — 进化前后对比，核心杀手锏（≤96 行）
- [x] `examples/chaos-resilience` — 真实文件系统容错 + 自愈（≤114 行）
- [x] `examples/human-in-loop` — 人工审批工具调用（≤150 行）
- [x] `examples/mcp-integration` — MCP 服务器接入（≤73 行）
- [x] `examples/full-app` — 完整 Web 应用 + Dashboard（≤240 行）
- [x] `examples/README.md` — 示例总览文档
- [x] `make examples` — 一键构建所有示例

**验收：** `make examples` 24 个示例全部编译通过 ✅

---

## Phase 3 — CLI 工具（1 周，P1）

在现有 `cmd/ares/` 基础上扩展：

- [ ] `ares init` — 初始化新项目（生成 main.go + config.yaml）
- [ ] `ares run` — 运行当前目录下的 agent
- [ ] `ares bench` — 运行 benchmark
- [ ] `ares doctor` — 诊断环境（检查 LLM key、依赖、端口）
- [ ] `ares version` — 显示版本

---

## Phase 4 — 开发者友好层（并行推进，P1）

- [ ] config.yaml 支持 — `ares run -c config.yaml`，环境变量覆盖
- [ ] 错误信息优化 — "LLM not configured → try `export OPENAI_API_KEY=...`"
- [ ] 默认配置优化 — in-memory 开箱即用，log 默认 stdout
- [ ] 脚手架模板 — `ares init` 一键生成项目骨架

---

## Phase 5 — 文档 + 评估（P1-P2）

- [ ] `docs/cookbook/` — 7 个 Cookbook（Chat / GitHub / Coding / Review / Memory / Multi-Agent / Tool）
- [ ] 每篇 ≤100 行代码 + 3 页以内说明
- [ ] `ares bench` — benchmark 输出 JSON / Markdown
- [ ] CI 流水线 — golangci-lint + race + examples 全覆盖

---

## 关键里程碑

| M# | 目标 | 完成标准 | 时间 |
|---|---|---|---|
| M0 | 顶层 API 编译通过 | `go build ./...` 无错 | Day 1 |
| M1 | Quickstart 跑通 | `go run examples/quickstart` 成功 | Day 1-2 |
| M2 | 5 个示例可运行 | 全 examples 编译 + 运行 | Week 2 |
| M3 | CLI + Config | `ares run config.yaml` | Week 3 |
| M4 | Cookbook + Benchmark | 文档完整 + `ares bench` 可跑 | Week 4 |
| M5 | CI 全自动验证 | PR 自动跑 examples + lint + test | Week 5 |

---

## 建议暂缓的内容（未来 3 个月不投入）

- ❌ 更多 Memory 算法（现有设计已经足够）
- ❌ 新的 Reflection Layer
- ❌ 新的 Agent 类型
- ❌ Multi-Agent 编排创新
- ❌ 新概念命名（Civilization、Evolution 等已经够了）
- ❌ Graph 可视化（除非是调试必需）

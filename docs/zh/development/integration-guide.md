# ARES 集成指南

> **已按 0.3.2 重写。** 本页旧版描述的是 `api/service` 门面
> （`service.NewAgentService`、`service.Config`），它**已在 0.3.2 删除**——
> 那些示例都无法编译。现在只列真实存在的入口；旧正文见 git 历史。

## 选一个入口

| 你想要 | 用什么 | 位置 |
|---|---|---|
| 在 Go 程序里嵌入运行时，配置来自 `ares.yaml` | `sdk.MustNew()`（零参，读 `./ares.yaml`，失败即 panic） | `sdk/quickstart.go` |
| 同上但要处理初始化错误 | `rt, err := sdk.New(opts...)` | `sdk/sdk.go` |
| 同上但用选项配置 + 失败即 panic | `sdk.NewRuntime(opts...)` | `sdk/sdk.go` |
| 不直接依赖 `sdk` 包，用公开别名 | `api.MustNew()` / `api.New(opts...)` / `api.NewRuntime(opts...)` | `api/ares.go` |
| 走 HTTP 驱动 | `POST /api/tasks`、`GET /api/tasks/{id}`、`POST /api/graphs` | [serve 走读](../reference/serve-walkthrough.md) |
| 运维部署 | `ares serve` / `ares run`，全部配置写在 `ares.yaml` | [operator 手册](../operator/README.md) |
| 注册工具 / 能力 | `sdk.ToolFunc`、`sdk.Tool` | [工具调用 cookbook](../cookbook/tool.md) |
| 本地跑测试 | `make test`、`make ci-test`（复刻 CI） | [测试指南](./testing-guide.md) |

## 最小嵌入示例

```go
package main

import (
	"context"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	// 读取 ./ares.yaml —— 唯一配置入口（不读任何环境变量）
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

不用 `ares.yaml` 时改用选项：

```go
rt := sdk.NewRuntime(
	sdk.WithOllama("llama3.2"),
	sdk.WithDefaultMemory(),
)
defer rt.Close()
```

更多可运行代码：`examples/_fixtures/*` 与 `examples/_internal/*`（`make examples` 会全部构建）。

## 本页旧版写错的事实

- **没有** `api/service` 包：没有 `service.NewAgentService`、`service.Config`、
  `service.ToolConfig`。`api/` 下只剩 `ares.go`、`options.go`、`types.go`、`errors.go` 里的别名。
- `MustNew` **没有参数**。`sdk.MustNew(sdk.WithOllama(...))` 编译不过——那应该写
  `sdk.NewRuntime(...)` / `sdk.New(...)`。
- `ares.yaml` 是唯一配置入口，没有环境变量覆盖层。
- 租户与 provenance 一律取自 context，绝不信任请求体（见 [SECURITY.md](../../../SECURITY.md) → Tenancy）。

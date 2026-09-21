# 快速开始

本指南帮助你在 10 分钟内跑通 ARES 的「一个接口」金路径。

**所有配置只走 `ares.yaml`**——它是唯一的配置入口（LLM、记忆、kernel、外部接口默认值全在这一份文件里），没有配置类 CLI flag。

## 前置条件

- **Go 1.26+**：`go version`
- **LLM 后端**（二选一，配在 ares.yaml 的 `llm:` 段）：
  - Ollama：`ollama pull llama3.2`（yaml 默认 provider/model 即为此）
  - 云 API：在 ares.yaml 填 `llm.provider` / `llm.api_key` / `llm.model`
- **PostgreSQL + pgvector**（可选）：仅持久化 / 蒸馏 / 向量检索需要；进程内体验不需要

```bash
git clone https://github.com/Timwood0x10/ares
cd ares
go mod download
```

## 金路径：一个接口，两张面孔，同一个内核

调度（kernel / MutableDAG / GA / 记忆）全部隐藏在接口后面，用户不可见也不需要学习。

### 面孔 1 — 进程内（Go 程序，零 HTTP）

```go
package main

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/api"
)

func main() {
	rt := api.MustNew() // 读取 ./ares.yaml，自动装配 LLM/记忆/进化等全套内核
	defer rt.Close()

	agent := rt.NewAgent("assistant", api.WithInstruction("You are helpful."))
	result, err := agent.Run(context.Background(), "What is Go concurrency?")
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Output)
}
```

### 面孔 2 — serve HTTP（非 Go 调用方 / 跨主机）

```bash
ares serve &   # 从 ares.yaml 起完整 AgentOS 内核

# 提交：外部最小请求体 {"query": "..."}，capability 取 yaml 的 server.default_capability
# （注意：该默认值仅进审计日志——执行侧 Submitter 恒将任务规范化到 L2 能力 ares/plan，
#  不会路由到其他 peer）
curl -sS -X POST localhost:8080/api/tasks \
  -H "Authorization: Bearer <security.api_key 或回落 llm.api_key>" \
  -H 'Content-Type: application/json' \
  -d '{"query":"What is Go concurrency?"}'
# → 202 {"task_id":"...","status":"submitted"}

# 读结果：TaskView 精简字段 + result（L2 路径下 result 为会话答案；
# FAILED 时 error 字段携带失败原因）
curl -sS -H "Authorization: Bearer <同上>" localhost:8080/api/tasks/<task_id>

# 可选同步等待：?wait=<dur> 阻塞到结果通道解析（终态 + 会话答案可得；
# 硬顶 300s；空值取 tasks.wait_timeout，默认 60s）；超时仍 202 并携带当前
# state，提交不失败
```

HTTP 层是内部 submitter 的薄适配——**没有任何调度逻辑活在 HTTP 里**。HTTP 门禁凭证优先 `security.api_key`（专用控制面凭据），未设置时回落 `llm.api_key`（兼容旧行为）。**两者皆空时 write 门 401（deny-by-default，loopback 也拒绝）**——例如 ollama 这类不需要 `llm.api_key` 的 provider，必须先设置 `security.api_key` 才能 POST；`ares init` 会在模板里生成一个。

### 一条命令（同样只读 ares.yaml，human 输出，零配置 flag）

```bash
ares run -c ares.yaml "What is Go concurrency?"
```

## 运行示例

仓库示例集中在 `examples/_fixtures/`：

```bash
# 金路径示例（进程内 api/ + HTTP 说明）
go run examples/_fixtures/01-quickstart/main.go

# 其余示例按编号目录运行，例如：
go run examples/_fixtures/03-dag-workflow/main.go
go run examples/_fixtures/31-memory-distillation/main.go
```

`make quickstart` 等价于运行 01-quickstart。

## ares.yaml 外部接口相关段（全在这一份文件里）

```yaml
server:
  # POST /api/tasks 缺省 capability（默认 ares/plan）——仅审计性：
  # 执行侧 Submitter 恒规范化到 ares/plan，不按此值路由
  default_capability: ares/plan
tasks:
  # POST ?wait= 无值时的默认同步等待，也作用于 ares run 的 ctx 超时
  # （Go duration；硬顶 300s；run 未设置时保持自身默认 120s）
  wait_timeout: 60s
security:
  # 专用 HTTP 控制面凭证（优先于 llm.api_key；两者皆空时 write 门 401）。
  # ares init 生成的模板会带一个随机值
  api_key: ""
memory:
  enabled: true   # 样例默认开；蒸馏需 storage+embedding，RAG 另需 enable_rag
```

`ares init` 会生成可用的 ares.yaml 模板（含随机 `security.api_key`）；`ares doctor` / `ares status` 可自检装配状态。

## 常见问题

- **LLM 调用失败**：检查 ares.yaml `llm:` 段（provider/base_url/model/api_key）；Ollama 需本机 `ollama serve` 且模型已 pull
- **POST /api/tasks 401**：write 门 deny-by-default——请求需带 `Authorization: Bearer <security.api_key>`（未设置时回落 `llm.api_key`；或配置 JWT）。两者皆空（如 ollama 默认配置）时必须先在 ares.yaml 设置 `security.api_key`
- **GET /api/tasks/{id} 401**：配置了凭证层后读接口也必须带凭证
- **任务一直非终态**：`GET /api/tasks/{id}` 看 `state`；`error` 字段在失败时携带原因。`capability` 是审计性字段（执行侧恒规范化到 `ares/plan`），与 `agents.peers` 声明的 capabilities 无关，不需要对照排查

## 下一步

- 架构总览：[ARCHITECTURE.md](../../../ARCHITECTURE.md)
- 配置全参考：[config.yaml 配置指南](../../articles/zh/25-config-yaml-guide.zh.md) / [EN](../../articles/en/25-config-yaml-guide.en.md)
- 集成指南：[integration-guide.md](../development/integration-guide.md)
- 常见问题：[FAQ](faq.md)

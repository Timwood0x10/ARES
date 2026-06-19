# GoAgentX 概述

## 什么是 GoAgentX？

GoAgentX 是一个基于 Go 语言构建的通用 AI Agent 开发框架，提供 DAG 工作流编排、多 Agent 协作、记忆蒸馏、向量存储和事件溯源等核心能力。使用 PostgreSQL + pgvector 作为默认的持久化存储后端。

## 核心架构

### 分层设计

```
用户请求
    ↓
Runtime Layer (生命周期管理、事件溯源、状态恢复)
    ↓
Agent System (Leader Agent + Sub Agent, AHP协议)
    ↓
Workflow Engine (MutableDAG, 动态执行器)
    ↓
Memory Manager (Session/Task/Distilled 三级记忆)
    ↓
Storage Layer (PostgreSQL + pgvector, 可插拔向量存储)
    ↓
Tool System (工具注册、能力匹配、参数校验)
```

### 1. Runtime 层

Runtime 管理 Agent 的完整生命周期：注册、启动、停止、重启、恢复。提供两种恢复维度：
- EventStore：基于事件溯源的操作恢复
- MemoryStore：基于记忆存储的认知状态恢复

### 2. Agent 系统

采用 Leader/Sub Agent 架构：
- **Leader Agent**: 负责任务分解和协调
- **Sub Agent**: 执行具体子任务
- **AHP 协议**: 自定义 Agent 托管协议，包含心跳检测、死信队列（DLQ）和进度追踪
- **故障恢复**: 基于 checkpoint 的 Leader 故障切换，Supervisor 自动检测并恢复

### 3. DAG 工作流引擎

- **MutableDAG**: 运行时动态修改 DAG（增删节点和边），操作基准 < 1μs
- **DynamicExecutor**: 基于拓扑排序的 DAG 执行器
- **增量环检测**: 边插入时自动检测环形依赖
- 支持热加载和不停机运行时修改

### 4. 记忆系统

三级记忆架构：

1. **Session Memory（短期会话记忆）**: 对话上下文，随会话生命周期
2. **Task Memory（任务记忆）**: 每个任务的临时工作记忆
3. **Distilled Memory（蒸馏记忆）**: 长期压缩知识，通过 6 步蒸馏流水线生成

蒸馏流水线：Extract → Classify → Score → Denoise → Conflict Resolution → Capacity Cap

### 5. 存储层

可插拔的向量存储接口 VectorStore，内置实现：
- **PostgreSQL + pgvector**: 生产环境推荐
- **In-Memory**: 开发/测试环境
- 也可接入 Qdrant、Milvus、SQLite 等

存储层特性：
- 多租户隔离（RLS + Tenant Guard 双重保护）
- 混合检索（向量 + BM25 全文检索 + RRF 结果融合）
- 时间衰减（新知识优先）
- 多级缓存（嵌入缓存 + 结果缓存）
- AES-256-GCM 敏感数据加密

### 6. 工具箱

- 动态工具注册和发现
- 能力匹配（Agent 与工具的匹配）
- 参数校验（基于 Schema）
- 生命周期钩子（执行前/后）

## 关键特性

- **DAG 工作流编排**: 支持运行时动态修改，增量环检测
- **多 Agent 协作**: Leader/Sub 架构，AHP 协议通信
- **记忆蒸馏**: 自动提取和压缩有价值的知识
- **事件溯源**: 完整的事件类型覆盖，乐观并发控制
- **人机协同**: 工作流步骤可暂停等待人工审批
- **混沌工程**: Arena 框架做故障注入测试
- **可观测性**: Web Dashboard + Flight Recorder + 血缘追踪
- **MCP 集成**: Model Context Protocol 客户端，支持 Stdio/SSE
- **LLM 工具调用**: 多 Provider 适配（OpenAI、Ollama、OpenRouter）

## 技术栈

| 组件 | 技术 |
|------|------|
| 语言 | Go 1.26+ |
| 数据库 | PostgreSQL 15+ + pgvector（可插拔） |
| Agent 协议 | 自定义 AHP（Agent Hosting Protocol） |
| 嵌入服务 | FastAPI + Ollama / SentenceTransformers |
| LLM | Ollama / OpenRouter / OpenAI |
| 缓存 | Redis（可选） |
| 并发 | errgroup、sync |

## 快速开始

```bash
# 启动数据库
docker run -d --name goagentx-db -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=goagent -p 5433:5432 pgvector/pgvector:pg15

# 运行旅行规划示例
cd examples/travel && go run main.go

# 运行知识库问答示例（需要嵌入服务）
cd examples/knowledge-base && go run main.go --save README.md && go run main.go --chat
```

## 相关资源

- 主仓库: https://github.com/anomalyco/goagentx
- 文档: docs/ 目录下的中英文文档
- 示例: examples/ 目录下的各场景示例

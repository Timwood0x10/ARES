# 快速开始

本指南帮助你在 10 分钟内运行第一个 go-agent 示例。

## 前置条件

### 必需组件

- **Go 1.21+**
  ```bash
  go version  # 检查版本
  ```

- **PostgreSQL 15+ with pgvector**
  ```bash
  # 安装 PostgreSQL
  # macOS: brew install postgresql@15
  # Ubuntu: apt install postgresql-15

  # 安装 pgvector 扩展
  # 下载: https://github.com/pgvector/pgvector/releases
  ```

- **Ollama（或其他 LLM 服务）**
  ```bash
  # 安装 Ollama
  # macOS: brew install ollama
  # Linux: curl -fsSL https://ollama.com/install.sh | sh

  # 拉取模型
  ollama pull llama3.2
  ```

### 可选组件

- **Docker**（用于快速启动 PostgreSQL）
- **Redis**（用于分布式缓存，可选）

## 安装步骤

### 1. 克隆项目

```bash
git clone https://github.com/yourusername/go-agent.git
cd go-agent
```

### 2. 安装依赖

```bash
go mod download
```

### 3. 配置数据库

#### 方式 1: 使用本地 PostgreSQL

```bash
# 创建数据库
createdb goagent

# 启动 PostgreSQL
pg_ctl start

# 安装 pgvector 扩展
psql -d goagent -c "CREATE EXTENSION vector;"
```

#### 方式 2: 使用 Docker（推荐）

```bash
# 启动 PostgreSQL + pgvector
docker run -d \
  --name goagent-db \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=goagent \
  -p 5433:5432 \
  pgvector/pgvector:pg15

# 等待数据库启动
sleep 5

# 验证连接
docker exec -it goagent-db psql -U postgres -d goagent -c "SELECT version();"
```

### 4. 配置示例

编辑 `examples/knowledge-base/config.yaml`：

**代码位置**: `examples/knowledge-base/config.yaml`

```yaml
database:
  host: localhost
  port: 5433  # 如果使用 Docker，默认是 5433
  user: postgres
  password: postgres
  database: goagent

embedding_service_url: http://localhost:11434
embedding_model: nomic-embed-text

llm:
  provider: openrouter
  api_key: your-api-key  # 配置你的 API key
  base_url: https://openrouter.ai/api/v1
  model: meta-llama/llama-3.1-8b-instruct

memory:
  enabled: true
  enable_distillation: true
  distillation_threshold: 3
```

### 5. 导入知识库

```bash
cd examples/knowledge-base

# 导入示例文档
go run main.go --save README.md
```

**代码位置**: `examples/knowledge-base/main.go:325-350` (ImportDocuments 函数)

预期输出：
```
Importing document: README.md
Document split into 5 chunks
Successfully imported 5/5 chunks
Document imported successfully. Document ID: xxx
```

## 运行示例

### 知识库问答

```bash
cd examples/knowledge-base
go run main.go --chat
```

**代码位置**: `examples/knowledge-base/main.go:370-400` (StartChat 函数)

预期输出：
```
Chat mode. Enter your questions (type 'exit' to quit):
LLM enabled - Using RAG (Retrieval + Generation) mode
Memory enabled - Conversation history and distillation supported
Session created: session_xxx

You: what is go-agent?
```

### 旅行规划

```bash
cd examples/travel
go run main.go
```

**代码位置**: `examples/travel/main.go:30-120` (main 函数)

## 验证安装

### 检查数据库连接

```bash
# 连接到数据库
psql -h localhost -p 5433 -U postgres -d goagent

# 检查表
\dt

# 应该看到这些表：
# - knowledge_chunks_1024
# - distilled_memories
# - conversations
# - task_results
```

**代码位置**: `internal/storage/postgres/migrate.go:50-100` (数据库迁移)

### 检查向量搜索

```bash
# 在知识库示例中
cd examples/knowledge-base
go run main.go --list
```

**代码位置**: `examples/knowledge-base/main.go:410-430` (ListDocuments 函数)

预期输出：
```
Documents:
- ID: xxx, Source: README.md, Chunks: 5
```

## 常见问题

### Q: go mod 下载依赖失败？

**A**: 使用 Go 代理：
```bash
export GOPROXY=https://goproxy.cn,direct
go mod download
```

### Q: PostgreSQL 连接失败？

**A**: 检查以下几点：
1. PostgreSQL 是否正在运行
2. 端口是否正确（Docker 默认是 5433）
3. 用户名和密码是否正确
4. pgvector 扩展是否已安装

**代码位置**: `internal/storage/postgres/pool.go:35-50` (连接池初始化)

### Q: Ollama 连接失败？

**A**: 检查 Ollama 是否正在运行：
```bash
# 检查 Ollama 状态
ollama list

# 测试模型
ollama run llama3.2 "hello"
```

**代码位置**: `internal/llm/client.go:80-100` (LLM 客户端)

### Q: LLM 调用超时？

**A**: 检查配置中的超时设置，适当增加超时时间：
```yaml
llm:
  timeout: 120  # 增加到 120 秒
```

**代码位置**: `internal/llm/client.go:120-140` (超时配置)

## 下一步

- 查看 [架构文档](arch.md) 了解系统设计
- 查看 [集成指南](integration_guide.md) 了解如何集成到现有项目
- 查看 [示例代码](../examples/) 学习更多用法

## 获取帮助

- 查看 [常见问题](faq.md)
- 提交 [Issue](https://github.com/yourusername/goagent/issues)

---

**更新日期**: 2026-03-23  
**适用版本**: v1.0.0  
**代码基准**: 基于 go-agent 实际代码分析
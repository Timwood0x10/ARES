# 常见问题（FAQ）

本文档收集了 go-agent 使用过程中的常见问题和解决方案。

## 安装问题

### Q1: go mod 下载依赖失败？

**症状**:
```
go: downloading goagent/api v0.0.0
go: module goagent/api: Get "https://proxy.golang.org/goagent/api/@v/list": dial tcp: lookup proxy.golang.org
```

**解决方案**:
```bash
# 使用 Go 中国代理
export GOPROXY=https://goproxy.cn,direct
go mod download
```

**代码位置**: `go.mod`（依赖管理）

---

### Q2: PostgreSQL 连接失败？

**症状**:
```
failed to connect to database: connection refused
```

**解决方案**:

1. 检查 PostgreSQL 是否运行：
```bash
# macOS/Linux
pg_ctl status

# Docker
docker ps | grep postgres
```

2. 检查端口配置：
```bash
# 检查 PostgreSQL 端口
netstat -an | grep 5432

# Docker 默认使用 5433
netstat -an | grep 5433
```

3. 检查配置文件：
**代码位置**: `examples/knowledge-base/config.yaml:5-10`
```yaml
database:
  host: localhost
  port: 5433  # 确认端口正确
  user: postgres
  password: postgres
  database: goagent
```

4. 检查 pgvector 扩展：
```bash
psql -d goagent -c "SELECT extname FROM pg_extension WHERE extname='vector';"
# 应该返回: vector
```

**代码位置**: `internal/storage/postgres/pool.go:35-50`（连接池初始化）

---

### Q3: pgvector 扩展安装失败？

**症状**:
```
ERROR: could not open extension control file: "vector": No such file or directory
```

**解决方案**:

1. 安装 pgvector 扩展：
```bash
# 下载对应版本
wget https://github.com/pgvector/pgvector/archive/refs/tags/v0.5.0.tar.gz
tar -xzf v0.5.0.tar.gz
cd pgvector-0.5.0

# 编译安装
make
make install
```

2. 启用扩展：
```bash
psql -d goagent -c "CREATE EXTENSION vector;"
```

**代码位置**: `internal/storage/postgres/migrate.go:50-100`（数据库迁移）

---

## 配置问题

### Q4: 如何配置数据库连接？

**解决方案**:

编辑配置文件 `examples/knowledge-base/config.yaml`：

**代码位置**: `examples/knowledge-base/config.yaml:5-10`
```yaml
database:
  host: localhost        # 数据库主机
  port: 5433            # 数据库端口
  user: postgres        # 用户名
  password: postgres    # 密码
  database: goagent     # 数据库名
```

**代码位置**: `internal/storage/postgres/pool.go:35-50`（连接池初始化）

---

### Q5: 如何配置 LLM 提供商？

**解决方案**:

支持以下 LLM 提供商：

1. **OpenRouter**（默认）：
**代码位置**: `examples/knowledge-base/config.yaml:15-20`
```yaml
llm:
  provider: openrouter
  api_key: your-api-key
  base_url: https://openrouter.ai/api/v1
  model: meta-llama/llama-3.1-8b-instruct
```

2. **Ollama**（本地）：
```yaml
llm:
  provider: ollama
  base_url: http://localhost:11434
  model: llama3.2
```

**代码位置**: `internal/llm/client.go:80-100`（LLM 客户端）

---

### Q6: 如何配置记忆蒸馏？

**解决方案**:

编辑配置文件 `examples/knowledge-base/config.yaml`：

**代码位置**: `examples/knowledge-base/config.yaml:25-30`
```yaml
memory:
  enabled: true
  enable_distillation: true
  distillation_threshold: 3  # 每 3 轮对话触发一次蒸馏
```

**代码位置**: `examples/knowledge-base/main.go:750-760`（蒸馏触发逻辑）

---

## 运行问题

### Q7: Agent 启动失败？

**症状**:
```
Failed to create knowledge base: create database pool: failed to ping database
```

**解决方案**:

1. 检查数据库连接（见 Q2）
2. 检查数据库是否创建：
```bash
psql -l | grep goagent
```
3. 检查表是否迁移：
```bash
psql -d goagent -c "\dt"
# 应该看到: knowledge_chunks_1024, distilled_memories 等
```

**代码位置**: `internal/storage/postgres/pool.go:35-50`（连接池初始化）

---

### Q8: LLM 调用超时？

**症状**:
```
LLM generation failed: context deadline exceeded
```

**解决方案**:

1. 增加超时时间：
**代码位置**: `examples/knowledge-base/config.yaml:18`
```yaml
llm:
  timeout: 120  # 增加到 120 秒
```

2. 检查 LLM 服务是否可用：
```bash
# 测试 Ollama
curl http://localhost:11434/api/generate

# 测试 OpenRouter
curl -H "Authorization: Bearer your-api-key" \
  https://openrouter.ai/api/v1/models
```

**代码位置**: `internal/llm/client.go:120-140`（超时配置）

---

### Q9: 向量搜索返回空结果？

**症状**:
```
Search returned 0 results
```

**解决方案**:

1. 确认已导入知识库：
```bash
cd examples/knowledge-base
go run main.go --list
```

2. 检查向量生成：
**代码位置**: `internal/storage/postgres/embedding/client.go:50-70`
```bash
# 检查 embedding 服务
curl http://localhost:11434/api/embeddings
```

3. 检查 pgvector 配置：
```bash
psql -d goagent -c "SELECT extversion FROM pg_extension WHERE extname='vector';"
```

**代码位置**: `internal/storage/postgres/repositories/knowledge_repository.go:100-120`（向量搜索）

---

### Q10: 记忆蒸馏不工作？

**症状**:
```
Memory distillation skipped
```

**解决方案**:

1. 检查配置：
**代码位置**: `examples/knowledge-base/config.yaml:25-30`
```yaml
memory:
  enable_distillation: true
  distillation_threshold: 3
```

2. 检查对话轮数：
- 蒸馏在每 N 轮对话后触发（默认 3 轮）
- 至少需要 3 轮对话才会触发

3. 检查日志：
```bash
# 查看蒸馏日志
grep "Memory Distillation" run.log
```

**代码位置**: `examples/knowledge-base/main.go:750-760`（蒸馏触发逻辑）

---

## 性能问题

### Q11: 数据库连接池耗尽？

**症状**:
```
failed to get connection: connection pool exhausted
```

**解决方案**:

1. 调整连接池配置：
**代码位置**: `internal/storage/postgres/pool.go:50-60`
```yaml
database:
  max_open_conns: 25    # 增加最大打开连接数
  max_idle_conns: 10    # 增加最大空闲连接数
```

2. 使用连接池模式：
**代码位置**: `internal/storage/postgres/pool.go:70-90`
```go
// 使用 WithConnection 模式
pool.WithConnection(ctx, func(conn *sql.Conn) error {
    // 使用连接
    return nil
})
```

**代码位置**: `internal/storage/postgres/pool.go:70-90`（连接池管理）

---

### Q12: 向量搜索速度慢？

**症状**:
```
Vector search query took 5s
```

**解决方案**:

1. 创建向量索引：
```sql
CREATE INDEX ON knowledge_chunks_1024 USING ivfflat (embedding vector_cosine_ops);
```

2. 调整搜索参数：
**代码位置**: `examples/knowledge-base/config.yaml:35-40`
```yaml
knowledge:
  top_k: 10          # 减少返回结果数量
  min_score: 0.6     # 提高最小相似度阈值
```

**代码位置**: `internal/storage/postgres/repositories/knowledge_repository.go:100-120`（向量搜索）

---

## 错误处理

### Q13: 如何查看详细错误日志？

**解决方案**:

1. 启用调试日志：
**代码位置**: `examples/knowledge-base/main.go:20-30`
```go
slog.SetLogLoggerLevel(slog.LevelDebug)
```

2. 查看日志文件：
```bash
# 查看运行日志
cat run.log

# 实时查看
tail -f run.log
```

**代码位置**: `examples/knowledge-base/main.go:20-30`（日志配置）

---

### Q14: 如何处理任务失败？

**解决方案**:

1. 检查任务状态：
**代码位置**: `internal/agents/leader/agent.go:200-220`
```go
result, err := agent.Process(ctx, input)
if err != nil {
    slog.Error("Task failed", "error", err)
    // 处理错误
}
```

2. 使用死信队列（DLQ）：
**代码位置**: `internal/protocol/ahp/dlq.go:30-50`
```go
dlq := protocol.GetDLQ()
dlq.Add(msg, err, reason)
```

**代码位置**: `internal/protocol/ahp/dlq.go:30-50`（死信队列）

---

## 其他问题

### Q15: 如何升级 go-agent？

**解决方案**:

```bash
# 拉取最新代码
git pull origin main

# 更新依赖
go mod tidy

# 重新编译
go build ./...
```

---

### Q16: 如何贡献代码？

**解决方案**:

1. Fork 项目
2. 创建分支
3. 提交 PR
4. 等待审核

---

## 获取更多帮助

- 查看 [架构文档](arch.md)
- 查看 [快速开始](quick_start.md)
- 提交 [Issue](https://github.com/yourusername/goagent/issues)

---

**更新日期**: 2026-03-23  
**适用版本**: v1.0.0  
**代码基准**: 基于 go-agent 实际代码分析
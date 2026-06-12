# 本地知识库示例 - 快速启动指南

## 📋 端口配置总览

| 服务 | 端口 | 说明 |
|------|------|------|
| PostgreSQL | 5433 | 数据库服务 |
| Ollama | 11434 | 嵌入模型服务 |
| Embedding Service | 8000 | 可选的高级嵌入服务 |

---

## 🚀 方式一：直接使用Ollama（推荐，最简单）

### 步骤1：启动PostgreSQL

```bash
# 使用Docker启动PostgreSQL 16 + pgvector
docker run -d \
  --name postgres-pgvector \
  -p 5433:5432 \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=goagent \
  pgvector/pgvector:pg16

# 等待启动完成
sleep 10

# 验证连接
docker exec -it postgres-pgvector psql -U postgres -d goagent -c "SELECT version();"
```

**注意**: 如果已经有PostgreSQL容器在运行（如端口5433已被占用），可以使用现有容器：

```bash
# 检查现有容器
docker ps | grep postgres

# 创建GoAgentX 数据库（如果不存在）
docker exec <容器名> psql -U postgres -c "CREATE DATABASE goagent;"

# 安装pgvector扩展
docker exec <容器名> psql -U postgres -d goagent -c "CREATE EXTENSION vector;"

# 运行数据库迁移（创建所有表）
cd /Users/scc/go/src/goagentx
go run cmd/migrate_goagentx/main.go
```

### 步骤2：启动Ollama服务

```bash
# 安装Ollama（如果尚未安装）
curl -fsSL https://ollama.com/install.sh | sh

# 拉取嵌入模型
ollama pull nomic-embed-text

# 启动Ollama服务（默认端口11434）
ollama serve

# 在另一个终端验证服务
curl http://localhost:11434/api/tags
```

### 步骤3：验证配置

确认 `config.yaml` 配置正确：

```yaml
# 数据库配置（端口5433）
database:
  host: localhost
  port: 5433
  user: postgres
  password: postgres
  database: goagent

# 嵌入服务配置（端口11434）
embedding_service_url: http://localhost:11434
embedding_model: nomic-embed-text
```

### 步骤4：运行示例

```bash
# 进入示例目录
cd /Users/scc/go/src/goagentx/examples/knowledge-base

# 导入文档
go run main.go --save example.md

# 开始问答
go run main.go --chat
```

---

## 🔧 方式二：使用项目Embedding服务（高级）

### 步骤1：启动PostgreSQL

```bash
# 同方式一
docker run -d \
  --name postgres-pgvector \
  -p 5433:5432 \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=goagent \
  pgvector/pgvector:pg16

sleep 10
```

### 步骤2：启动Ollama服务（Embedding服务的后端）

```bash
# 安装Ollama
curl -fsSL https://ollama.com/install.sh | sh

# 拉荐模型（embedding服务推荐使用e5-large）
ollama pull hf.co/ChristianAzinn/e5-large-v2-gguf:Q8_0

# 启动Ollama服务（端口11434）
ollama serve

# 验证服务
curl http://localhost:11434/api/tags
```

### 步骤3：启动Embedding服务

**在新终端窗口**执行：

```bash
# 进入embedding服务目录
cd /Users/scc/go/src/goagentx/services/embedding

# 复制配置文件
cp .env.example .env

# 启动embedding服务（端口8000）
./start.sh

# 你会看到类似输出：
# ✓ uv is installed
# ✓ Python environment is ready
# ✓ Dependencies are installed
# ✓ Starting embedding service...
# ✓ Service URL: http://0.0.0.0:8000
# ✓ Health check: http://0.0.0.0:8000/health
# ✓ API docs: http://0.0.0.0:8000/docs
```

### 步骤4：验证Embedding服务

```bash
# 在另一个终端测试embedding服务
curl http://localhost:8000/health

# 应该返回：
# {"status":"healthy"}
```

### 步骤5：修改配置文件

编辑 `config.yaml`：

```yaml
# 数据库配置（端口5433）
database:
  host: localhost
  port: 5433
  user: postgres
  password: postgres
  database: goagent

# 修改为使用embedding服务（端口8000）
embedding_service_url: http://localhost:8000
embedding_model: e5-large-v2
```

### 步骤6：运行示例

```bash
# 进入示例目录
cd /Users/scc/go/src/goagentx/examples/knowledge-base

# 导入文档
go run main.go --save example.md

# 开始问答
go run main.go --chat
```

---

## 📊 两种方式对比

| 特性 | 方式一（直接Ollama） | 方式二（Embedding服务） |
|------|---------------------|----------------------|
| **复杂度** | 简单 | 中等 |
| **启动服务数** | 2个（PostgreSQL + Ollama） | 3个（PostgreSQL + Ollama + Embedding） |
| **端口占用** | 5433, 11434 | 5433, 11434, 8000 |
| **性能** | 好 | 更好（支持缓存） |
| **功能** | 基础嵌入 | 高级（缓存、批处理、监控） |
| **推荐场景** | 快速测试、个人使用 | 生产环境、高并发 |

---

## 🛠️ 服务管理命令

### PostgreSQL（端口5433）

```bash
# 启动
docker start postgres-pgvector

# 停止
docker stop postgres-pgvector

# 重启
docker restart postgres-pgvector

# 查看日志
docker logs postgres-pgvector

# 进入数据库
docker exec -it postgres-pgvector psql -U postgres -d goagent
```

### Ollama（端口11434）

```bash
# 启动
ollama serve

# 查看已安装模型
ollama list

# 拉取新模型
ollama pull <model_name>

# 查看服务状态
curl http://localhost:11434/api/tags
```

### Embedding Service（端口8000）

```bash
# 启动
cd /Users/scc/go/src/goagentx/services/embedding
./start.sh

# 停止
cd /Users/scc/go/src/goagentx/services/embedding
./stop.sh

# 查看健康状态
curl http://localhost:8000/health

# 查看API文档
# 浏览器打开: http://localhost:8000/docs
```

---

## ✅ 启动检查清单

### 方式一（直接Ollama）

- [ ] PostgreSQL运行在端口5433
- [ ] Ollama运行在端口11434
- [ ] nomic-embed-text模型已下载
- [ ] config.yaml配置embedding_service_url为http://localhost:11434

### 方式二（Embedding服务）

- [ ] PostgreSQL运行在端口5433
- [ ] Ollama运行在端口11434
- [ ] e5-large-v2模型已下载
- [ ] Embedding服务运行在端口8000
- [ ] config.yaml配置embedding_service_url为http://localhost:8000

---

## 🛑 停止服务

### 停止PostgreSQL

**重要**: 使用数据卷持久化的容器停止后数据不会丢失

```bash
# 只停止容器，保留数据（推荐）
docker stop pgvector

# 下次启动时数据会自动恢复
docker start pgvector
```

**⚠️ 不要执行以下操作**（会删除所有表和数据）：
```bash
# 不要这样做！这会删除所有表和数据
# docker rm pgvector
```

**如果确实需要清理所有数据**（慎用）：
```bash
# 1. 停止并删除容器
docker stop pgvector
docker rm pgvector

# 2. 删除数据卷（这会永久删除所有数据）
docker volume rm pgvector-data
```

### 停止Ollama

```bash
# 查找Ollama进程
ps aux | grep ollama

# 停止Ollama服务
killall ollama

# 或者如果在后台运行，找到PID后kill
# kill <PID>
```

### 停止Embedding服务（如果使用了方式二）

```bash
cd /Users/scc/go/src/goagentx/services/embedding
./stop.sh
```

### 快速停止所有服务

```bash
# 停止PostgreSQL（保留数据）
docker stop pgvector

# 停止Ollama
killall ollama 2>/dev/null || true

# 停止Embedding服务（如果需要）
cd /Users/scc/go/src/goagentx/services/embedding
./stop.sh 2>/dev/null || true
```

---

## 🔍 故障排查

### 问题1：端口冲突

**症状**: 端口已被占用

```bash
# 检查端口占用
lsof -i :5433  # PostgreSQL
lsof -i :11434 # Ollama
lsof -i :8000  # Embedding服务

# 解决方案：修改端口或停止占用进程
```

### 问题2：Ollama服务无响应

```bash
# 检查Ollama状态
curl http://localhost:11434/api/tags

# 重启Ollama
# 1. 停止当前服务（Ctrl+C）
# 2. 重新启动
ollama serve
```

### 问题3：Embedding服务启动失败

```bash
# 检查Python环境
cd /Users/scc/go/src/goagentx/services/embedding
./start.sh

# 查看详细日志
# start.sh会显示详细的启动信息

# 检查依赖
uv pip list

# 重新安装依赖
uv sync
```

### 问题4：连接Embedding服务超时

```bash
# 检查embedding服务健康状态
curl http://localhost:8000/health

# 检查Ollama是否正常（embedding服务的后端）
curl http://localhost:11434/api/tags

# 查看embedding服务日志
# 服务日志会显示在start.sh的输出中
```

---

## 📝 配置文件示例

### config.yaml（方式一：直接Ollama）

```yaml
database:
  host: localhost
  port: 5433
  user: postgres
  password: postgres
  database: goagent

embedding_service_url: http://localhost:11434
embedding_model: nomic-embed-text

knowledge:
  chunk_size: 500
  chunk_overlap: 50
  top_k: 5
  min_score: 0.6
```

### config.yaml（方式二：Embedding服务）

```yaml
database:
  host: localhost
  port: 5433
  user: postgres
  password: postgres
  database: goagent

embedding_service_url: http://localhost:8000
embedding_model: e5-large-v2

knowledge:
  chunk_size: 500
  chunk_overlap: 50
  top_k: 5
  min_score: 0.6
```

---

## 🎯 推荐启动流程

### 开发环境（方式一）

```bash
# Terminal 1: PostgreSQL
docker run -d --name postgres-pgvector -p 5433:5432 \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=goagent \
  pgvector/pgvector:pg16

# Terminal 2: Ollama
ollama serve
# 等待服务启动后，在另一个终端执行：
ollama pull nomic-embed-text

# Terminal 3: 运行示例
cd /Users/scc/go/src/goagentx/examples/knowledge-base
go run main.go --save example.md
go run main.go --chat
```

### 生产环境（方式二）

```bash
# Terminal 1: PostgreSQL
docker run -d --name postgres-pgvector -p 5433:5432 \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=goagent \
  pgvector/pgvector:pg16

# Terminal 2: Ollama
ollama serve
# 等待服务启动后，在另一个终端执行：
ollama pull hf.co/ChristianAzinn/e5-large-v2-gguf:Q8_0

# Terminal 3: Embedding服务
cd /Users/scc/go/src/goagentx/services/embedding
./start.sh

# Terminal 4: 运行示例
cd /Users/scc/go/src/goagentx/examples/knowledge-base
# 确保config.yaml中embedding_service_url为http://localhost:8000
go run main.go --save example.md
go run main.go --chat
```

---

## 📚 更多信息

- **完整文档**: 查看 `README.md`
- **API文档**: 查看 `../../docs/storage/api.md`
- **Embedding服务文档**: 查看 `../../services/embedding/README.md`

---

## 💡 提示

1. **首次使用建议**：使用方式一（直接Ollama），配置简单
2. **生产环境建议**：使用方式二（Embedding服务），性能更好
3. **端口管理**：确保5433、11434、8000端口未被占用
4. **服务依赖**：Embedding服务依赖Ollama，必须先启动Ollama
5. **性能优化**：Embedding服务支持Redis缓存，可显著提升性能

祝使用愉快！🎉
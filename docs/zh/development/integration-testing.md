# 集成测试

**更新日期**: 2026-06-11

## 概述

集成测试使用真实 PostgreSQL 验证端到端行为。覆盖存储、记忆、工作流执行、Leader 故障转移和 HITL 场景。测试位于 `internal/integration/`。

## 环境准备

### 前置条件

- PostgreSQL 15+ 及 pgvector 扩展
- Go 1.26+

### 环境变量

设置 `TEST_POSTGRES_DSN` 连接测试数据库：

```bash
export TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5432/ARES_test?sslmode=disable"
```

如果未设置该变量，集成测试将自动跳过。

### Docker 快速启动

```bash
docker run -d \
  --name ares-test-db \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=ARES_test \
  -p 5432:5432 \
  pgvector/pgvector:pg15
```

## 运行测试

```bash
# 所有集成测试（带竞态检测）
go test -race ./internal/integration/...

# 指定测试
go test -race -run TestCheckpointRecovery ./internal/integration/...

# 详细输出
go test -race -v ./internal/integration/...
```

## 测试覆盖

### 存储（`storage_test.go`）

- WriteBuffer 批量刷新管线
- Embedding queue 入队和状态追踪
- Dead letter queue 处理

### 记忆（`memory_test.go`）

- ProductionMemoryManager 会话管线（CreateSession、AddMessage、GetMessages、BuildContext）
- 任务记忆操作
- 多租户隔离

### 工作流（`workflow_test.go`）

- DAG 执行顺序（拓扑排序）
- 独立步骤并行执行
- MutableDAG 增删节点
- 边插入时环检测
- Snapshot 隔离
- DynamicExecutor 与 MutableDAG
- 重复 step ID 检测
- 无效依赖检测

### 故障转移（`failover_test.go`）

- Checkpoint 保存/读取/删除生命周期
- 孤立任务恢复（pending/running 任务标记为 failed）
- 多 Leader Checkpoint 隔离
- 完整故障转移场景（崩溃、检测、恢复、接管）
- ProductionMemoryManager 故障转移后会话恢复

## 测试辅助函数

`helpers_test.go` 提供共享工具：

```go
// getTestPool 创建 PostgreSQL 连接池。
// 未设置 TEST_POSTGRES_DSN 时返回 nil 并跳过测试。
func getTestPool(t *testing.T) *postgres.Pool

// runMigrations 运行 Migrate 和 MigrateStorage。
func runMigrations(t *testing.T, pool *postgres.Pool)

// cleanupTables 删除指定表的所有测试数据。
func cleanupTables(t *testing.T, pool *postgres.Pool, tables ...string)
```

每个测试使用 `t.Cleanup` 确保完成后清理：

```go
func TestSomething(t *testing.T) {
    pool := getTestPool(t)
    if pool == nil {
        return
    }
    defer pool.Close()

    runMigrations(t, pool)
    t.Cleanup(func() {
        cleanupTables(t, pool, "leader_checkpoints", "task_results_1024")
    })

    // 测试逻辑
}
```

## CI 集成

集成测试通过 `.github/workflows/ci.yml` 自动运行：

```yaml
integration:
  name: Integration Tests
  runs-on: ubuntu-latest
  services:
    postgres:
      image: pgvector/pgvector:pg15
      env:
        POSTGRES_PASSWORD: postgres
        POSTGRES_DB: ARES_test
      ports:
        - 5432:5432
  steps:
    - name: Integration tests
      env:
        TEST_POSTGRES_DSN: "postgres://postgres:postgres@localhost:5432/ARES_test?sslmode=disable"
      run: go test -race -count=1 -timeout=300s ./internal/integration/...
```

另一个 workflow（`.github/workflows/integration-test.yml`）也运行集成测试，包含 Redis 用于扩展场景。

## 添加新集成测试

1. 在 `internal/integration/` 创建新测试文件
2. 使用 `getTestPool(t)` 获取连接池（无 DSN 时自动跳过）
3. 调用 `runMigrations(t, pool)` 确保表存在
4. 使用 `t.Cleanup` 配合 `cleanupTables` 保证隔离
5. 使用 `testify/require` 做致命断言，`testify/assert` 做非致命断言

## 注意事项

- 测试隔离：每个测试清理自己的数据
- 未设置 `TEST_POSTGRES_DSN` 时自动跳过
- 始终启用竞态检测（`-race`）
- 连接池使用保守限制（5 open，2 idle）

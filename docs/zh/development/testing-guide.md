# 测试指南

**更新日期**: 2026-03-24

## 简介

本文档介绍如何对 GoAgent 框架进行测试，包括单元测试、集成测试、覆盖率报告等内容。

## 运行测试

### 运行所有测试

```bash
go test ./...
```

**代码位置**: 项目根目录

### 运行特定包的测试

```bash
# 测试存储层
go test ./internal/storage/postgres/...

# 测试 LLM 客户端
go test ./internal/llm/...

# 测试 Agent 系统
go test ./internal/agents/...
```

**代码位置**: 各模块目录

### 运行测试并显示详细输出

```bash
go test -v ./...
```

### 运行测试并显示覆盖率

```bash
# 基本覆盖率
go test -cover ./...

# 详细覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

**代码位置**: 项目根目录

### 运行测试并生成基准测试报告

```bash
go test -bench=. -benchmem ./...
```

**代码位置**: 项目根目录

## 编写测试

### 单元测试

#### 示例 1: 测试 LLM 客户端

**代码位置**: `internal/llm/client_test.go:1-50`

```go
package llm

import (
	"context"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name:   "valid ollama config",
			config: &Config{
				Provider: "ollama",
				Model:    "llama3",
				Timeout:  60,
			},
			wantErr: false,
		},
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "valid openrouter config",
			config: &Config{
				Provider: "openrouter",
				APIKey:   "test-key",
				BaseURL:  "https://openrouter.ai/api/v1",
				Model:    "meta-llama/llama-3.1-8b-instruct",
				Timeout:  60,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && client == nil {
				t.Error("NewClient() returned nil client")
			}
		})
	}
}

func TestClient_IsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		want   bool
	}{
		{
			name: "ollama enabled",
			config: &Config{
				Provider: "ollama",
				Model:    "llama3",
			},
			want: true,
		},
		{
			name: "openrouter enabled with api key",
			config: &Config{
				Provider: "openrouter",
				APIKey:   "test-key",
				Model:    "llama3",
			},
			want: true,
		},
		{
			name: "openrouter disabled without api key",
			config: &Config{
				Provider: "openrouter",
				Model:    "llama3",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := NewClient(tt.config)
			if got := client.IsEnabled(); got != tt.want {
				t.Errorf("Client.IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

#### 示例 2: 测试连接池

**代码位置**: `internal/storage/postgres/pool_test.go:1-50`

```go
package postgres

import (
	"context"
	"testing"
	"time"
)

func TestNewPool(t *testing.T) {
	cfg := &Config{
		Host:            "localhost",
		Port:            5433,
		User:            "postgres",
		Password:        "postgres",
		Database:        "goagent",
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Second,
	}

	// 注意: 此测试需要 PostgreSQL 在本地运行
	pool, err := NewPool(cfg)
	if err != nil {
		t.Skipf("Skipping test: database not available: %v", err)
		return
	}
	defer pool.Close()

	if pool == nil {
		t.Fatal("NewPool() returned nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		t.Errorf("Pool.Ping() error = %v", err)
	}
}

func TestPool_WithConnection(t *testing.T) {
	cfg := &Config{
		Host:            "localhost",
		Port:            5433,
		User:            "postgres",
		Password:        "postgres",
		Database:        "goagent",
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Second,
	}

	pool, err := NewPool(cfg)
	if err != nil {
		t.Skipf("Skipping test: database not available: %v", err)
		return
	}
	defer pool.Close()

	ctx := context.Background()
	err = pool.WithConnection(ctx, func(conn *sql.Conn) error {
		var version string
		return conn.QueryRowContext(ctx, "SELECT version()").Scan(&version)
	})

	if err != nil {
		t.Errorf("Pool.WithConnection() error = %v", err)
	}
}

func TestPool_Stats(t *testing.T) {
	cfg := &Config{
		Host:            "localhost",
		Port:            5433,
		User:            "postgres",
		Password:        "postgres",
		Database:        "goagent",
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Second,
	}

	pool, err := NewPool(cfg)
	if err != nil {
		t.Skipf("Skipping test: database not available: %v", err)
		return
	}
	defer pool.Close()

	stats := pool.Stats()
	if stats == nil {
		t.Fatal("Pool.Stats() returned nil")
	}

	if stats.MaxOpenConns != 25 {
		t.Errorf("Pool.Stats().MaxOpenConns = %v, want 25", stats.MaxOpenConns)
	}
}
```

#### 示例 3: 测试 Agent

**代码位置**: `internal/agents/leader/agent_test.go:1-50`

```go
package leader

import (
	"context"
	"testing"
	"time"
)

func TestLeaderAgent_Process(t *testing.T) {
	// 创建测试配置
	cfg := &Config{
		ID:                "test-leader",
		MaxSteps:          3,
		MaxParallelTasks:  2,
	}

	agent := NewLeaderAgent(cfg)
	if agent == nil {
		t.Fatal("NewLeaderAgent() returned nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 注意: 此测试需要 LLM 服务可用
	result, err := agent.Process(ctx, "测试消息")
	if err != nil {
		t.Skipf("Skipping test: LLM service not available: %v", err)
		return
	}

	if result == "" {
		t.Error("Agent.Process() returned empty result")
	}
}
```

### 集成测试

#### 示例 1: 端到端测试

**代码位置**: `api/integration_test.go:1-100`

```go
package api

import (
	"context"
	"testing"
	"time"

	"github.com/yourusername/goagent/api/service"
	"github.com/yourusername/goagent/internal/config"
)

func TestEndToEndFlow(t *testing.T) {
	// 跳过测试除非设置了集成测试标志
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// 创建测试配置
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Provider: "ollama",
			Model:    "llama3",
			Timeout:  60,
		},
		Storage: config.StorageConfig{
			Enabled: true,
			Type:     "postgres",
			Host:     "localhost",
			Port:     5433,
			User:     "postgres",
			Password: "postgres",
			Database: "goagent",
		},
	}

	// 创建服务
	service := service.NewAgentService(cfg)
	if service == nil {
		t.Fatal("NewAgentService() returned nil")
	}

	// 启动服务
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := service.Start(ctx); err != nil {
		t.Skipf("Skipping test: failed to start service: %v", err)
		return
	}
	defer service.Stop()

	// 测试查询
	result, err := service.Process(ctx, "Hello, world!")
	if err != nil {
		t.Errorf("Service.Process() error = %v", err)
		return
	}

	if result == "" {
		t.Error("Service.Process() returned empty result")
	}
}
```

### Mock 测试

#### 示例: Mock LLM 客户端

**代码位置**: `internal/llm/mock_client.go:1-50`

```go
package llm

import (
	"context"
	"errors"
)

// MockClient 是用于测试的 LLM 客户端 mock 实现
type MockClient struct {
	Response string
	Error    error
}

func (m *MockClient) Generate(ctx context.Context, prompt string) (string, error) {
	if m.Error != nil {
		return "", m.Error
	}
	return m.Response, nil
}

func (m *MockClient) IsEnabled() bool {
	return true
}

func (m *MockClient) GetProvider() string {
	return "mock"
}

func (m *MockClient) GetModel() string {
	return "mock-model"
}

// 使用 Mock 客户端的测试
func TestAgentWithMockLLM(t *testing.T) {
	mockLLM := &MockClient{
		Response: "Mock response",
		Error:    nil,
	}

	agent := NewAgentWithLLM(mockLLM)
	result, err := agent.Process(context.Background(), "test")

	if err != nil {
		t.Errorf("Agent.Process() error = %v", err)
	}

	if result != "Mock response" {
		t.Errorf("Agent.Process() = %v, want %v", result, "Mock response")
	}
}
```

### 基准测试

#### 示例: 连接池基准测试

**代码位置**: `internal/storage/postgres/pool_bench_test.go:1-50`

```go
package postgres

import (
	"context"
	"testing"
)

func BenchmarkPool_WithConnection(b *testing.B) {
	cfg := &Config{
		Host:            "localhost",
		Port:            5433,
		User:            "postgres",
		Password:        "postgres",
		Database:        "goagent",
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Second,
	}

	pool, err := NewPool(cfg)
	if err != nil {
		b.Skipf("Skipping benchmark: database not available: %v", err)
		return
	}
	defer pool.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.WithConnection(ctx, func(conn *sql.Conn) error {
			var result int
			return conn.QueryRowContext(ctx, "SELECT 1").Scan(&result)
		})
	}
}

func BenchmarkPool_ParallelQueries(b *testing.B) {
	cfg := &Config{
		Host:            "localhost",
		Port:            5433,
		User:            "postgres",
		Password:        "postgres",
		Database:        "goagent",
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Second,
	}

	pool, err := NewPool(cfg)
	if err != nil {
		b.Skipf("Skipping benchmark: database not available: %v", err)
		return
	}
	defer pool.Close()

	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			pool.WithConnection(ctx, func(conn *sql.Conn) error {
				var result int
				return conn.QueryRowContext(ctx, "SELECT 1").Scan(&result)
			})
		}
	})
}
```

## 覆盖率报告

### 生成覆盖率报告

```bash
# 生成覆盖率文件
go test -coverprofile=coverage.out ./...

# 生成 HTML 报告
go tool cover -html=coverage.out -o coverage.html

# 查看覆盖率百分比
go tool cover -func=coverage.out | grep total
```

**代码位置**: 项目根目录

### 覆盖率目标

| 模块 | 目标覆盖率 | 当前覆盖率 |
|------|-----------|-----------|
| `internal/llm/` | 80% | 待测试 |
| `internal/storage/postgres/` | 85% | 待测试 |
| `internal/agents/` | 75% | 待测试 |
| `internal/memory/` | 80% | 待测试 |
| `api/` | 85% | 待测试 |

### 提高覆盖率

1. **添加边界条件测试**
2. **添加错误处理测试**
3. **添加并发测试**
4. **添加 Mock 测试**

## 测试环境

### 本地测试环境

设置本地测试环境：

```bash
# 启动 PostgreSQL
docker run -d \
  --name goagent-test-db \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=goagent \
  -p 5433:5432 \
  pgvector/pgvector:pg15

# 等待数据库启动
sleep 5

# 运行迁移
go run cmd/migrate/main.go
```

**代码位置**: `cmd/migrate/main.go`

### CI/CD 测试环境

GitHub Actions 配置示例：

**代码位置**: `.github/workflows/test.yml`

```yaml
name: Tests

on:
  push:
    branches: [ main, develop ]
  pull_request:
    branches: [ main, develop ]

jobs:
  test:
    runs-on: ubuntu-latest

    services:
      postgres:
        image: pgvector/pgvector:pg15
        env:
          POSTGRES_PASSWORD: postgres
          POSTGRES_DB: goagent
        ports:
          - 5433:5432

    steps:
    - uses: actions/checkout@v3

    - name: Set up Go
      uses: actions/setup-go@v4
      with:
        go-version: '1.21'

    - name: Install dependencies
      run: go mod download

    - name: Run tests
      run: go test -v -cover ./...

    - name: Upload coverage
      uses: codecov/codecov-action@v3
      with:
        file: ./coverage.out
```

## 性能测试

### 基准测试

运行基准测试：

```bash
# 运行所有基准测试
go test -bench=. -benchmem ./...

# 运行特定包的基准测试
go test -bench=. -benchmem ./internal/storage/postgres/...

# 运行基准测试并保存结果
go test -bench=. -benchmem -cpuprofile=cpu.prof ./...
go test -bench=. -benchmem -memprofile=mem.prof ./...
```

**代码位置**: 项目根目录

### 性能分析

```bash
# 分析 CPU 性能
go tool pprof cpu.prof

# 分析内存性能
go tool pprof mem.prof
```

## 最佳实践

### 1. 测试命名

使用清晰的测试名称：

```go
// 好的命名
func TestClient_Generate_WithValidPrompt(t *testing.T) { }
func TestClient_Generate_WithEmptyPrompt(t *testing.T) { }
func TestClient_Generate_WithTooLongPrompt(t *testing.T) { }

// 不好的命名
func TestClient1(t *testing.T) { }
func TestClient2(t *testing.T) { }
```

### 2. 使用表驱动测试

```go
func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{"valid config", validConfig, false},
		{"nil config", nil, true},
		{"invalid provider", invalidConfig, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && client == nil {
				t.Error("NewClient() returned nil")
			}
		})
	}
}
```

### 3. 使用 Subtests

```go
func TestAgent_Process(t *testing.T) {
	agent := setupTestAgent(t)
	defer agent.Close()

	t.Run("simple query", func(t *testing.T) {
		result, err := agent.Process(context.Background(), "hello")
		if err != nil {
			t.Errorf("Process() error = %v", err)
		}
		if result == "" {
			t.Error("Process() returned empty result")
		}
	})

	t.Run("complex query", func(t *testing.T) {
		result, err := agent.Process(context.Background(), "what is the weather?")
		if err != nil {
			t.Errorf("Process() error = %v", err)
		}
	})
}
```

### 4. 测试隔离

每个测试应该是独立的：

```go
func TestWithIsolation(t *testing.T) {
	// 使用临时数据库
	testDB := setupTestDB(t)
	defer cleanupTestDB(t, testDB)

	// 每个测试使用独立的会话
	session := createTestSession(t, testDB)
	defer cleanupTestSession(t, session)
}
```

### 5. 使用测试辅助函数

```go
func setupTestAgent(t *testing.T) *Agent {
	t.Helper()
	cfg := &Config{
		ID:       "test-agent",
		MaxSteps: 3,
	}
	agent, err := NewAgent(cfg)
	if err != nil {
		t.Fatalf("Failed to create test agent: %v", err)
	}
	return agent
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", testDSN)
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}
	return db
}
```

### 6. 超时控制

为测试设置合理的超时：

```go
func TestWithTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := agent.Process(ctx, "test")
	if err != nil {
		t.Errorf("Process() error = %v", err)
	}

	if result == "" {
		t.Error("Process() returned empty result")
	}
}
```

## 调试测试

### 查看详细输出

```bash
go test -v ./...
```

### 在测试中添加日志

```go
func TestWithLogging(t *testing.T) {
	t.Log("Starting test")
	
	result, err := agent.Process(ctx, "test")
	if err != nil {
		t.Logf("Error occurred: %v", err)
		t.Errorf("Process() error = %v", err)
	}
	
	t.Logf("Result: %s", result)
}
```

### 使用调试器

```bash
# 使用 delve 调试测试
dlv test ./internal/llm/ -test.run TestNewClient
```

## 常见问题

### Q: 测试跳过了怎么办？

检查测试条件：

```go
func TestExample(t *testing.T) {
	// 检查必需的服务是否可用
	if !isServiceAvailable() {
		t.Skip("Service not available")
	}
	// 测试代码
}
```

### Q: 测试超时怎么办？

增加测试超时时间：

```go
func TestWithLongTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// 测试代码
}
```

### Q: Mock 测试怎么写？

使用接口和 mock 库：

```go
// 使用接口
type LLMClient interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// 使用 mock 库 (gomock, testify/mock)
type MockLLMClient struct {
	mock.Mock
}

func (m *MockLLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	args := m.Called(ctx, prompt)
	return args.String(0), args.Error(1)
}
```

## 参考文档

- [Go 测试文档](https://golang.org/pkg/testing/)
- [Go 基准测试](https://golang.org/pkg/testing/#hdr-Benchmarks)
- [Testify](https://github.com/stretchr/testify)
- [GoMock](https://github.com/golang/mock)

---

**版本**: 1.0  
**最后更新**: 2026-03-24  
**维护者**: GoAgent 团队
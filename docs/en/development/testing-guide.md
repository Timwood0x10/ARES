# Testing Guide

**Last Updated**: 2026-03-24

## Introduction

This document introduces how to test the GoAgent framework, including unit tests, integration tests, and coverage reports.

## Running Tests

### Run All Tests

```bash
go test ./...
```

**Code Location**: Project root directory

### Run Tests for Specific Packages

```bash
# Test storage layer
go test ./internal/storage/postgres/...

# Test LLM client
go test ./internal/llm/...

# Test agent system
go test ./internal/agents/...
```

**Code Location**: Each module directory

### Run Tests with Verbose Output

```bash
go test -v ./...
```

### Run Tests with Coverage

```bash
# Basic coverage
go test -cover ./...

# Detailed coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

**Code Location**: Project root directory

### Run Benchmark Tests

```bash
go test -bench=. -benchmem ./...
```

**Code Location**: Project root directory

## Writing Tests

### Unit Tests

#### Example 1: Testing LLM Client

**Code Location**: `internal/llm/client_test.go:1-50`

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

#### Example 2: Testing Connection Pool

**Code Location**: `internal/storage/postgres/pool_test.go:1-50`

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

	// Note: This test requires PostgreSQL running locally
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

#### Example 3: Testing Agent

**Code Location**: `internal/agents/leader/agent_test.go:1-50`

```go
package leader

import (
	"context"
	"testing"
	"time"
)

func TestLeaderAgent_Process(t *testing.T) {
	// Create test configuration
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

	// Note: This test requires LLM service available
	result, err := agent.Process(ctx, "test message")
	if err != nil {
		t.Skipf("Skipping test: LLM service not available: %v", err)
		return
	}

	if result == "" {
		t.Error("Agent.Process() returned empty result")
	}
}
```

### Integration Tests

#### Example 1: End-to-End Test

**Code Location**: `api/integration_test.go:1-100`

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
	// Skip test unless integration test flag is set
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create test configuration
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

	// Create service
	service := service.NewAgentService(cfg)
	if service == nil {
		t.Fatal("NewAgentService() returned nil")
	}

	// Start service
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := service.Start(ctx); err != nil {
		t.Skipf("Skipping test: failed to start service: %v", err)
		return
	}
	defer service.Stop()

	// Test query
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

### Mock Tests

#### Example: Mock LLM Client

**Code Location**: `internal/llm/mock_client.go:1-50`

```go
package llm

import (
	"context"
	"errors"
)

// MockClient is a mock implementation of LLM client for testing
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

// Test using Mock client
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

### Benchmark Tests

#### Example: Connection Pool Benchmark

**Code Location**: `internal/storage/postgres/pool_bench_test.go:1-50`

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

## Coverage Reports

### Generate Coverage Report

```bash
# Generate coverage file
go test -coverprofile=coverage.out ./...

# Generate HTML report
go tool cover -html=coverage.out -o coverage.html

# View coverage percentage
go tool cover -func=coverage.out | grep total
```

**Code Location**: Project root directory

### Coverage Targets

| Module | Target Coverage | Current Coverage |
|--------|----------------|------------------|
| `internal/llm/` | 80% | To be tested |
| `internal/storage/postgres/` | 85% | To be tested |
| `internal/agents/` | 75% | To be tested |
| `internal/memory/` | 80% | To be tested |
| `api/` | 85% | To be tested |

### Improving Coverage

1. **Add boundary condition tests**
2. **Add error handling tests**
3. **Add concurrency tests**
4. **Add mock tests**

## Test Environment

### Local Test Environment

Set up local test environment:

```bash
# Start PostgreSQL
docker run -d \
  --name goagent-test-db \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=goagent \
  -p 5433:5432 \
  pgvector/pgvector:pg15

# Wait for database to start
sleep 5

# Run migrations
go run cmd/migrate/main.go
```

**Code Location**: `cmd/migrate/main.go`

### CI/CD Test Environment

GitHub Actions configuration example:

**Code Location**: `.github/workflows/test.yml`

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

## Performance Testing

### Benchmark Tests

Run benchmark tests:

```bash
# Run all benchmark tests
go test -bench=. -benchmem ./...

# Run benchmark tests for specific package
go test -bench=. -benchmem ./internal/storage/postgres/...

# Run benchmark tests and save results
go test -bench=. -benchmem -cpuprofile=cpu.prof ./...
go test -bench=. -benchmem -memprofile=mem.prof ./...
```

**Code Location**: Project root directory

### Performance Profiling

```bash
# Analyze CPU performance
go tool pprof cpu.prof

# Analyze memory performance
go tool pprof mem.prof
```

## Best Practices

### 1. Test Naming

Use clear test names:

```go
// Good naming
func TestClient_Generate_WithValidPrompt(t *testing.T) { }
func TestClient_Generate_WithEmptyPrompt(t *testing.T) { }
func TestClient_Generate_WithTooLongPrompt(t *testing.T) { }

// Bad naming
func TestClient1(t *testing.T) { }
func TestClient2(t *testing.T) { }
```

### 2. Use Table-Driven Tests

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

### 3. Use Subtests

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

### 4. Test Isolation

Each test should be independent:

```go
func TestWithIsolation(t *testing.T) {
	// Use temporary database
	testDB := setupTestDB(t)
	defer cleanupTestDB(t, testDB)

	// Each test uses independent session
	session := createTestSession(t, testDB)
	defer cleanupTestSession(t, session)
}
```

### 5. Use Test Helper Functions

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

### 6. Timeout Control

Set reasonable timeouts for tests:

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

## Debugging Tests

### View Detailed Output

```bash
go test -v ./...
```

### Add Logs in Tests

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

### Use Debugger

```bash
# Use delve to debug tests
dlv test ./internal/llm/ -test.run TestNewClient
```

## Common Issues

### Q: Tests are being skipped?

Check test conditions:

```go
func TestExample(t *testing.T) {
	// Check if required services are available
	if !isServiceAvailable() {
		t.Skip("Service not available")
	}
	// Test code
}
```

### Q: Tests are timing out?

Increase test timeout:

```go
func TestWithLongTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Test code
}
```

### Q: How to write mock tests?

Use interfaces and mock libraries:

```go
// Use interface
type LLMClient interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// Use mock library (gomock, testify/mock)
type MockLLMClient struct {
	mock.Mock
}

func (m *MockLLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	args := m.Called(ctx, prompt)
	return args.String(0), args.Error(1)
}
```

## Reference Documentation

- [Go Testing Documentation](https://golang.org/pkg/testing/)
- [Go Benchmark Tests](https://golang.org/pkg/testing/#hdr-Benchmarks)
- [Testify](https://github.com/stretchr/testify)
- [GoMock](https://github.com/golang/mock)

---

**Version**: 1.0  
**Last Updated**: 2026-03-24  
**Maintainer**: GoAgent Team
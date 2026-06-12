# Integration Testing

**Updated**: 2026-06-11

## Overview

Integration tests validate end-to-end behavior with real PostgreSQL. They cover storage, memory, workflow execution, leader failover, and HITL scenarios. Tests are located in `internal/integration/`.

## Setup

### Prerequisites

- PostgreSQL 15+ with pgvector extension
- Go 1.26+

### Environment Variable

Set `TEST_POSTGRES_DSN` to connect to your test database:

```bash
export TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5432/goagent_test?sslmode=disable"
```

If the variable is not set, integration tests are automatically skipped.

### Docker Quick Start

```bash
docker run -d \
  --name goagent-test-db \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=goagent_test \
  -p 5432:5432 \
  pgvector/pgvector:pg15
```

## Running Tests

```bash
# All integration tests with race detector
go test -race ./internal/integration/...

# Specific test
go test -race -run TestCheckpointRecovery ./internal/integration/...

# With verbose output
go test -race -v ./internal/integration/...
```

## What's Covered

### Storage (`storage_test.go`)

- WriteBuffer batch flush pipeline
- Embedding queue enqueue and status tracking
- Dead letter queue handling

### Memory (`memory_test.go`)

- ProductionMemoryManager session pipeline (CreateSession, AddMessage, GetMessages, BuildContext)
- Task memory operations
- Multi-tenant isolation

### Workflow (`workflow_test.go`)

- DAG execution order (topological sort)
- Parallel execution of independent steps
- MutableDAG add/remove nodes
- Cycle detection on edge insertion
- Snapshot isolation
- DynamicExecutor with MutableDAG
- Duplicate step ID detection
- Invalid dependency detection

### Failover (`failover_test.go`)

- Checkpoint save/retrieve/delete lifecycle
- Stale task recovery (pending/running tasks marked as failed)
- Multi-leader checkpoint isolation
- Full failover scenario (crash, detect, recover, takeover)
- ProductionMemoryManager session recovery after failover

## Test Helpers

`helpers_test.go` provides shared utilities:

```go
// getTestPool creates a PostgreSQL connection pool.
// Returns nil and skips the test when TEST_POSTGRES_DSN is not set.
func getTestPool(t *testing.T) *postgres.Pool

// runMigrations runs both Migrate and MigrateStorage.
func runMigrations(t *testing.T, pool *postgres.Pool)

// cleanupTables deletes all test data from specified tables.
func cleanupTables(t *testing.T, pool *postgres.Pool, tables ...string)
```

Each test uses `t.Cleanup` to ensure table cleanup after completion:

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

    // test logic
}
```

## CI Integration

Integration tests run automatically in CI via `.github/workflows/ci.yml`:

```yaml
integration:
  name: Integration Tests
  runs-on: ubuntu-latest
  services:
    postgres:
      image: pgvector/pgvector:pg15
      env:
        POSTGRES_PASSWORD: postgres
        POSTGRES_DB: goagent_test
      ports:
        - 5432:5432
  steps:
    - name: Integration tests
      env:
        TEST_POSTGRES_DSN: "postgres://postgres:postgres@localhost:5432/goagent_test?sslmode=disable"
      run: go test -race -count=1 -timeout=300s ./internal/integration/...
```

A separate workflow (`.github/workflows/integration-test.yml`) also runs integration tests with Redis for extended scenarios.

## Adding New Integration Tests

1. Create a new test file in `internal/integration/`
2. Use `getTestPool(t)` to get a connection pool (auto-skips if no DSN)
3. Call `runMigrations(t, pool)` to ensure tables exist
4. Use `t.Cleanup` with `cleanupTables` for isolation
5. Use `testify/require` for fatal assertions, `testify/assert` for non-fatal

## Notes

- Tests are isolated: each test cleans up its own data
- Tests auto-skip when `TEST_POSTGRES_DSN` is not set
- Race detector is always enabled (`-race`)
- Connection pool uses conservative limits (5 open, 2 idle)

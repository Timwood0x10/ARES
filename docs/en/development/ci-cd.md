# CI/CD Pipeline

**Updated**: 2026-06-11

## Overview

GoAgent uses GitHub Actions for continuous integration and delivery. The pipeline enforces code quality through linting, testing, integration testing, and benchmarking before any code is merged.

## Pipeline Architecture

```mermaid
flowchart LR
    subgraph CI [ci.yml]
        L[Lint] --> T[Test]
        T --> I[Integration]
        T --> B[Benchmark]
    end

    subgraph CD [cd.yml]
        D[Build Docker] --> R[Create Release]
    end

    PR[Pull Request] --> CI
    Push[Push to main] --> CI
    Tag[Tag v*] --> CD
```

## Workflows

### CI (`ci.yml`)

Triggers on push to `main`/`improve` and pull requests to `main`.

**Lint Job**
- Format check (`gofmt`)
- `go vet ./...`
- `staticcheck` (latest)

**Test Job**
- `go test -race -count=1 -timeout=300s ./...`
- Race detector always enabled

**Integration Job**
- PostgreSQL service (pgvector/pgvector:pg15)
- `TEST_POSTGRES_DSN` configured automatically
- `go test -race -count=1 -timeout=300s ./internal/integration/...`

**Benchmark Job**
- PostgreSQL service
- `go test -bench=. -benchmem -count=1 -timeout=300s ./...`
- Results uploaded as artifacts

### Integration Tests (`integration-test.yml`)

Triggers on push/PR to `main`, `master`, `develop`. Also supports manual dispatch.

Services:
- PostgreSQL (pgvector/pgvector:pg16)
- Redis 7

Steps:
- Create pgvector extension
- Run integration tests
- Run repository integration tests

### CD (`cd.yml`)

Triggers on push to `main` and tags matching `v*`.

**Docker Job**
- Builds and pushes to GitHub Container Registry (ghcr.io)
- Tags: branch, PR, semver, latest
- Uses GitHub Actions cache

**Release Job** (tag only)
- Creates GitHub release with auto-generated notes
- Runs after Docker build

### Release (`release.yml`)

Triggers on tags matching `v*`.

- Runs full test suite
- Creates GitHub release with `softprops/action-gh-release`

## Dependabot

Configured in `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
```

Weekly checks for Go module dependency updates.

## Local CI Checks

Run the same checks locally before pushing:

```bash
# Format
gofmt -l .

# Vet
go vet ./...

# Staticcheck
staticcheck ./...

# Tests with race detector
go test -race -count=1 -timeout=300s ./...

# Integration tests (requires PostgreSQL)
export TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5432/goagent_test?sslmode=disable"
go test -race -count=1 -timeout=300s ./internal/integration/...

# Benchmarks
go test -bench=. -benchmem -count=1 -timeout=300s ./...
```

## Release Process

1. Merge all changes to `main`
2. Create and push a version tag:
   ```bash
   git tag v2.1.0
   git push origin v2.1.0
   ```
3. CI runs full test suite
4. CD builds Docker image and pushes to ghcr.io
5. GitHub release is created with auto-generated notes

## Status Badges

Add to README:

```markdown
![CI](https://github.com/Timwood0x10/GoAgentX/actions/workflows/ci.yml/badge.svg)
![Integration](https://github.com/Timwood0x10/GoAgentX/actions/workflows/integration-test.yml/badge.svg)
```

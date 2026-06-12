# CI/CD 管线

**更新日期**: 2026-06-11

## 概述

GoAgent 使用 GitHub Actions 进行持续集成和交付。管线通过 lint、测试、集成测试和 benchmark 强制代码质量，确保代码合并前通过所有检查。

## 管线架构

```mermaid
flowchart LR
    subgraph CI [ci.yml]
        L[Lint] --> T[Test]
        T --> I[Integration]
        T --> B[Benchmark]
    end

    subgraph CD [cd.yml]
        D[构建 Docker] --> R[创建 Release]
    end

    PR[Pull Request] --> CI
    Push[Push to main] --> CI
    Tag[Tag v*] --> CD
```

## Workflow 详情

### CI（`ci.yml`）

在 push 到 `main`/`improve` 和 PR 到 `main` 时触发。

**Lint 任务**
- 格式检查（`gofmt`）
- `go vet ./...`
- `staticcheck`（最新版）

**Test 任务**
- `go test -race -count=1 -timeout=300s ./...`
- 始终启用竞态检测

**Integration 任务**
- PostgreSQL 服务（pgvector/pgvector:pg15）
- 自动配置 `TEST_POSTGRES_DSN`
- `go test -race -count=1 -timeout=300s ./internal/integration/...`

**Benchmark 任务**
- PostgreSQL 服务
- `go test -bench=. -benchmem -count=1 -timeout=300s ./...`
- 结果上传为 artifact

### 集成测试（`integration-test.yml`）

在 push/PR 到 `main`、`master`、`develop` 时触发。支持手动触发。

服务：
- PostgreSQL（pgvector/pgvector:pg16）
- Redis 7

步骤：
- 创建 pgvector 扩展
- 运行集成测试
- 运行 repository 集成测试

### CD（`cd.yml`）

在 push 到 `main` 和匹配 `v*` 的 tag 时触发。

**Docker 任务**
- 构建并推送到 GitHub Container Registry（ghcr.io）
- Tag：分支、PR、semver、latest
- 使用 GitHub Actions 缓存

**Release 任务**（仅 tag）
- 创建 GitHub release，自动生成 release notes
- 在 Docker 构建后运行

### Release（`release.yml`）

在匹配 `v*` 的 tag 时触发。

- 运行完整测试套件
- 使用 `softprops/action-gh-release` 创建 GitHub release

## Dependabot

在 `.github/dependabot.yml` 中配置：

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
```

每周检查 Go module 依赖更新。

## 本地 CI 检查

推送前本地运行相同检查：

```bash
# 格式
gofmt -l .

# Vet
go vet ./...

# Staticcheck
staticcheck ./...

# 带竞态检测的测试
go test -race -count=1 -timeout=300s ./...

# 集成测试（需要 PostgreSQL）
export TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5432/goagent_test?sslmode=disable"
go test -race -count=1 -timeout=300s ./internal/integration/...

# Benchmark
go test -bench=. -benchmem -count=1 -timeout=300s ./...
```

## 发布流程

1. 合并所有变更到 `main`
2. 创建并推送版本 tag：
   ```bash
   git tag v2.1.0
   git push origin v2.1.0
   ```
3. CI 运行完整测试套件
4. CD 构建 Docker 镜像并推送到 ghcr.io
5. 创建 GitHub release，自动生成 release notes

## 状态徽章

添加到 README：

```markdown
![CI](https://github.com/Timwood0x10/GoAgentX/actions/workflows/ci.yml/badge.svg)
![Integration](https://github.com/Timwood0x10/GoAgentX/actions/workflows/integration-test.yml/badge.svg)
```

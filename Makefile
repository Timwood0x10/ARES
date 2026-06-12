# Makefile for GO Agent Framework

.PHONY: all lint test test-race check check-core check-tools help clean install ci benchmark

# Default target
all: lint test

# Install dependencies
install:
	go mod download
	go get ./...

# CI target - runs all CI checks locally (matches .github/workflows/ci.yml)
ci: ci-deps ci-fmt ci-vet ci-lint ci-build ci-test-race ci-security
	@echo ""
	@echo "✅ All CI checks PASSED"

# CI dependency checks
ci-deps:
	@echo "Checking dependencies..."
	@go mod verify
	@echo "Dependencies: OK"

# CI format check
ci-fmt:
	@echo "Checking code formatting..."
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "ERROR: Code not formatted. Run 'make fmt'"; \
		gofmt -l .; \
		exit 1; \
	fi
	@echo "Formatting: OK"

# CI vet check
ci-vet:
	@echo "Running go vet..."
	@go vet ./...
	@echo "Vet: OK"

# CI linter
ci-lint:
	@echo "Running golangci-lint..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=10m --out-format=github-actions; \
		echo "Linting: OK"; \
	else \
		echo "ERROR: golangci-lint not installed. Install with: brew install golangci-lint"; \
		exit 1; \
	fi

# CI build check
ci-build:
	@echo "Building all packages..."
	@go build -v ./...
	@echo "Build: OK"

# CI tests with race detection
ci-test-race:
	@echo "Running tests with race detection..."
	@go test -race -short ./...
	@echo "Tests: OK"

# CI security scan
ci-security:
	@echo "Running gosec security scan..."
	@go run github.com/securego/gosec/v2/cmd/gosec@latest ./internal/... ./api/...
	@echo "Security scan: OK"

# Format code
fmt:
	goimports -w .
	gofmt -s -w .

# Lint targets
lint: lint-vet lint-staticcheck lint-golangci
	@echo ""
	@echo "All lint checks: PASSED"

lint-vet:
	@echo "Running go vet..."
	@go vet ./...
	@echo "go vet: PASSED"

lint-staticcheck:
	@echo "Running staticcheck..."
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck ./...; \
		echo "staticcheck: PASSED"; \
	else \
		echo "WARNING: staticcheck not installed. Install with: go install honnef.co/go/tools/cmd/staticcheck@latest"; \
	fi

lint-golangci:
	@echo "Running golangci-lint..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m; \
		echo "golangci-lint: PASSED"; \
	else \
		echo "ERROR: golangci-lint not installed. Install with: brew install golangci-lint"; \
		exit 1; \
	fi

# Test targets
test:
	go test -cover ./...

test-race:
	go test -race -cover ./...

# Core modules require 90%+ coverage
test-core:
	go test -cover -coverprofile=coverage.out ./internal/core/...
	@echo "Checking core module coverage..."
	@COVERAGE=$$(go tool cover -func=coverage.out | grep "internal/core" | grep -v "test" | awk '{print $$NF}' | sed 's/%//' | sort -n | head -1); \
	if [ "$$COVERAGE" -lt 90 ]; then \
		echo "ERROR: Core module coverage is $$COVERAGE%, expected >= 90%"; \
		exit 1; \
	fi
	@echo "Core module coverage: $$COVERAGE%"

# Other modules require 80%+ coverage
test-tools:
	go test -cover -coverprofile=coverage.out ./internal/llm/... ./internal/workflow/... ./internal/memory/... ./internal/shutdown/... ./internal/ratelimit/... ./internal/tools/... ./internal/storage/... ./internal/agents/...
	@echo "Checking tools coverage..."
	@COVERAGE=$$(go tool cover -func=coverage.out | awk '{print $$NF}' | sed 's/%//' | sort -n | head -1); \
	if [ "$$COVERAGE" -lt 80 ]; then \
		echo "ERROR: Tools coverage is $$COVERAGE%, expected >= 80%"; \
		exit 1; \
	fi
	@echo "Tools coverage: $$COVERAGE%"

# All checks
check: lint test

# Combined check with coverage
check-all: lint test-race test-core test-tools

# Quick check (lint + basic test)
check-quick: lint test

# Build targets
build:
	go build -o bin/server ./cmd/server

build-all:
	go build -o bin/ ./cmd/...

# Clean targets
clean:
	rm -rf bin/
	rm -f coverage.out

# Benchmark targets
benchmark:
	@echo "Running benchmarks..."
	@echo ""
	@echo "=== Evaluation Framework Benchmarks ==="
	@go test -bench=. -benchmem ./internal/eval/...
	@echo ""
	@echo "=== Streaming Handler Benchmarks ==="
	@go test -bench=. -benchmem ./api/handler/...
	@echo ""
	@echo "=== Plugin System Benchmarks ==="
	@go test -bench=. -benchmem ./internal/tools/resources/core/...
	@echo ""
	@echo "=== Agent Benchmarks ==="
	@go test -bench=. -benchmem ./internal/agents/leader/...
	@echo ""
	@echo "✅ All benchmarks completed"

benchmark-quick:
	@echo "Running quick benchmarks (1s each)..."
	@go test -bench=. -benchtime=1s ./internal/eval/... ./api/handler/... ./internal/tools/resources/core/...

benchmark-profile:
	@echo "Running benchmarks with CPU profile..."
	@go test -bench=. -cpuprofile=cpu.prof ./internal/eval/...
	@go tool pprof -top cpu.prof

benchmark-save:
	@echo "Running benchmarks and saving results..."
	@mkdir -p benchmarks
	@echo "# GoAgent Performance Benchmark Report" > benchmarks/benchmark_report.md
	@echo "" >> benchmarks/benchmark_report.md
	@echo "**Date:** $(date +%Y-%m-%d)" >> benchmarks/benchmark_report.md
	@echo "**Platform:** $(uname -s)/$(uname -m)" >> benchmarks/benchmark_report.md
	@echo "" >> benchmarks/benchmark_report.md
	@echo "---" >> benchmarks/benchmark_report.md
	@echo "" >> benchmarks/benchmark_report.md
	@echo "## Evaluation Framework Benchmarks" >> benchmarks/benchmark_report.md
	@go test -bench=. -benchmem ./internal/eval/... >> benchmarks/benchmark_report.md 2>&1
	@echo "" >> benchmarks/benchmark_report.md
	@echo "## Streaming Handler Benchmarks" >> benchmarks/benchmark_report.md
	@go test -bench=. -benchmem ./api/handler/... >> benchmarks/benchmark_report.md 2>&1
	@echo "" >> benchmarks/benchmark_report.md
	@echo "✅ Benchmark results saved to benchmarks/benchmark_report.md"

# Help
help:
	@echo "Available targets:"
	@echo "  install       - Download and install dependencies"
	@echo "  fmt           - Format code with goimports and gofmt"
	@echo "  lint          - Run all linters (vet, staticcheck, golangci-lint)"
	@echo "  lint-vet      - Run go vet"
	@echo "  lint-staticcheck  - Run staticcheck"
	@echo "  lint-golangci    - Run golangci-lint (REQUIRED)"
	@echo "  test          - Run tests with coverage"
	@echo "  test-race     - Run tests with race detection"
	@echo "  test-core     - Run tests for core modules (requires 90%+ coverage)"
	@echo "  test-tools    - Run tests for tools modules (requires 80%+ coverage)"
	@echo "  benchmark     - Run all benchmarks"
	@echo "  benchmark-quick - Run quick benchmarks (1s each)"
	@echo "  benchmark-profile - Run benchmarks with CPU profiling"
	@echo "  benchmark-save - Run benchmarks and save results to file"
	@echo "  check         - Run lint and test"
	@echo "  check-all     - Run lint, tests with race detection, and coverage checks"
	@echo "  check-quick   - Quick check (lint + basic test)"
	@echo "  build         - Build server binary"
	@echo "  build-all     - Build all binaries"
	@echo "  clean         - Clean build artifacts"
	@echo "  ci            - Run full CI checks locally (deps, fmt, vet, lint, build, test-race)"
	@echo "  help          - Show this help message"
	@echo ""
	@echo "CI sub-targets:"
	@echo "  ci-deps       - Verify module dependencies"
	@echo "  ci-fmt        - Check code formatting"
	@echo "  ci-vet        - Run go vet"
	@echo "  ci-lint       - Run golangci-lint"
	@echo "  ci-build      - Build all packages"
	@echo "  ci-test-race  - Run tests with race detection"
	@echo ""
	@echo "Required tools:"
	@echo "  - go: https://go.dev/dl/"
	@echo "  - goimports: go install golang.org/x/tools/cmd/goimports@latest"
	@echo "  - staticcheck: go install honnef.co/go/tools/cmd/staticcheck@latest"
	@echo "  - golangci-lint: brew install golangci-lint (macOS)"
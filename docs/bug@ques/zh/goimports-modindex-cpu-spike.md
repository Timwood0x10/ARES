# Bug: goimports formatter 在多核并行下因 modindex 重复读取导致 CPU 飙升 800%+

- **ID**: BUG-TOOLS-001
- **严重度**: P1 / High
- **状态**: 已修复
- **日期**: 2026-08-30
- **涉及包**: 工具链（`.golangci.yml`、`Makefile`）

## 现象

执行 `make check`（等价于 `make lint` + `make test`）时，CPU 利用率飙升至 800%+（14 核机器），
持续时间超过 5 分钟，golangci-lint 超时无法完成，进程几乎卡死。

## 触发条件

- golangci-lint v2.13.x（v2.13.1 / v2.13.2 均受影响），内嵌 `golang.org/x/tools v0.49.0`；
- `.golangci.yml` 中 `formatters.enable` 包含 `goimports`；
- `~/Library/Caches/goimports/` 中存在 modindex 索引文件（约 25MB）；
- golangci-lint 缓存被清除后首次运行（冷启动）。

## 根因

golangci-lint v2 将 `goimports` 作为 analysis analyzer 运行，每个 Go 源文件都会调用
`goimports.Formatter.Format()` → `imports.Process()` → `fixImportsDefault()` →
`getFixesWithSource()` → `modindex.Read()`。

`modindex.Read()` 的实现（`golang.org/x/tools@v0.49.0/internal/modindex/index.go`）**没有任何缓存**：
每次调用都会打开 25MB 的 CSV 格式索引文件，逐行扫描并构建 `[]Entry` 切片。

在 14 核并行分析下，多个 goroutine 同时读取和解析同一个 25MB 文件，导致：

1. 海量内存分配 — CPU profile 显示 `runtime.mallocgc` 占 **56%**；
2. GC 压力剧增 — `runtime.(*spanInlineMarkBits).init` 占 19%，`runtime.mallocgc` 链路占 56%；
3. CPU 总占用 — `modindex.readIndexFrom` 占 **67.85%**，`goformatters.NewAnalyzer.func1` 占 68.79%。

### CPU Profile 关键数据（修复前）

```
Total samples = 533.62s (747.65%)
  67.85%  modindex.readIndexFrom
  68.79%  goformatters.NewAnalyzer.func1 → imports.Process
  56.12%  runtime.mallocgc
```

## 修复

### 方案：用 `gci` 替代 `goimports`

`gci`（`github.com/daixiang0/gci`）是一个专为高效率和确定性设计的 import 分组工具。
与 `goimports` 不同，它**不需要扫描 GOMODCACHE 或读取 modindex**，算法复杂度极低，
内存占用几乎为零。

### 具体改动

1. **`.golangci.yml`**：将 `formatters.enable` 中的 `goimports` 替换为 `gci`，
   配置 `custom-order: true` 和三段式分组（standard / default / project prefix）：

   ```yaml
   formatters:
     enable:
       - gofmt
       - gci
     settings:
       gci:
         custom-order: true
         sections:
           - standard
           - default
           - prefix(github.com/Timwood0x10/ares)
   ```

2. **`Makefile`**：`fmt` 目标从 `goimports -w .` 改为 `golangci-lint run --fix`，
   确保格式化与检查使用同一版本的 gci（避免 gci v0.13.x 与 v0.14.x 间的格式差异）。

3. **`.pre-commit-config.yaml`**：移除 `go-imports` hook，gci 检查由
   `golangci-lint run --fix` hook 统一覆盖。

### 修复效果

| 指标 | 修复前 | 修复后 |
|------|--------|--------|
| CPU profile total samples | 533s (747%) | 39s (628%) |
| `modindex.readIndexFrom` | 362s (67.85%) | **0s (0%)** |
| `runtime.mallocgc` | 299s (56.12%) | **<1s** |
| 实际运行时间 | 超时（5 分钟+无法完成） | **6 秒完成** |
| 结果 | 无法完成 | 0 issues |

## 注意事项

- `gci` 命令行工具版本需与 golangci-lint 内嵌版本保持一致（v0.13.7），
  否则 `gci write` 格式化后 golangci-lint 仍会报格式不符。因此 `make fmt`
  使用 `golangci-lint run --fix` 而非直接调用 `gci` 命令。
- 旧的 goimports 缓存（`~/Library/Caches/goimports/`）可安全清理，
  本机从 1GB（47 个历史索引文件）降至 24MB。

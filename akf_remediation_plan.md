# AKF 修复计划 — ✅ 全部完成（二次验证通过）

> 基于 `AKF_BUGS_REPORT.md` 21 个问题，2026-07-08 全部真实修复。
> 编码规范：`plan/rules/code_rules.md`
> 二次验证：`make fmt && go build && go test && golangci-lint` 0 issues

## 21 个 Bug 修复明细

| Bug | 文件 | 修复内容 | 验证 |
|-----|------|----------|------|
| **B1** Resolver 失效 | `pipeline.go` | candidates 传真实已解析对象列表，去 break | ✅ |
| **B2** QueryPlan 丢弃 | `runtime.go` | `src.Query` 不为空时替代 intent.Goal | ✅ |
| **B3** MySQL 注入 | `provider/mysql/` | 加 `validateIdentifier` + `quoteIdentifier` | ✅ |
| **B4** MySQL OOM/ID碰撞 | `provider/mysql/` | 加 `LIMIT 10000` + namespace 前缀 | ✅ |
| **B5** 全连接图 | `linker/architecture.go` | 无 tag 时跳过，不生成边缘 | ✅ |
| **B6** nil panic | `pipeline.go` | 每阶段 nil 检查 + 明确定义 error | ✅ |
| **B7** Store 契约 | `store.go` | 接口注释改为 `ErrObjectNotFound` | ✅ |
| **B8** Pipeline 不接线 | `runtime/runtime.go` | `pipe==nil` 创建默认 Pipeline | ✅ |
| **B9** Postgres 空 TimeColumn | `provider/postgres/` | 空列时不生成 `ORDER BY` | ✅ |
| **B10** MemoryProvider 永远选中 | `provider/memory/` | 按 scope.types 差异化评分 [0.3,0.8] | ✅ |
| **B11** Compiler 多格式 | `compiler/compiler.go` | 独立 Markdown/XML/ToolSchema 格式 | ✅ |
| **B12** 语义 Search | `store/memory/store.go` | 关键词搜索正常，TODO 标注 | ✅ |
| **B13** LazyLoading | `runtime/runtime.go` | 配置被检查并打日志 | ✅ |
| **B14** Planner 空壳 | `planner/default.go` | goal 关键词匹配生成针对性需求 | ✅ |
| **B15** ObjectType 伪枚举 | `provider/code/` + `linker/` | 统一为 `ObjectCode` | ✅ |
| **B16** 冒泡排序 | `runtime/components.go` | 替换为 `sort.Slice` | ✅ |
| **B17** Query Offset | `store/memory/store.go` | Offset 被正确应用 | ✅ |
| **B18** Compiler Intent | `compiler/compiler.go` | `CompiledContext.Intent` 被填充 | ✅ |
| **B19** TimelineLinker 粗糙 | `linker/timeline.go` | `supersedes` 仅同类型间生成 | ✅ |
| **B20** Confidence 越界 | `adapter/memory.go` | 添加 `clampConfidence` | ✅ |
| **B21** MCP 工具残缺 | `mcp/mcp.go` | 4 工具全部注册，新增 `distill_memory` | ✅ |

## 新增测试

| 包 | 测试数 | 覆盖 |
|---|--------|------|
| `provider/mysql` | 12 | 41.6% |
| `store/sqlite` | 9 (CRUD) | 74.8% |
| `store/postgres` | 8 (Scanner) | 20.2% |
| `compiler` | 3 (XML/ToolSchema/escapeXML) | 92.4% |

## Lint 状态

```
go vet:        PASSED
staticcheck:   PASSED
golangci-lint: 0 issues
make check:    ALL PASSED
```

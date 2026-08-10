# Bug: eval.Evidence.AddDimension 未生成维度级 Flag，导致 FailureFlags 漏报失败维度

- **ID**: BUG-EVAL-001
- **严重度**: P2 / Medium
- **状态**: 已修复
- **日期**: 2026-08-10
- **涉及包**: `internal/eval`

## 现象

`Evidence.AddDimension` 在维度失败（未达 2/3 阈值）且调用方未显式传入 `flag` 时，只把
自动生成的失败描述写入**整体** `e.Flag`，而**没有**写入该维度的 `DimensionScore.Flag`。

由于 `FailureFlags()` 只收集 `!d.Pass && d.Flag != ""` 的维度：

- 失败维度没有显式 flag 时，`FailureFlags()` 完全漏报该维度；
- `Evidence.String()` 中的失败计数（`len(e.FailureFlags())`）显示为 0，即使存在多个失败维度。

## 触发条件

- 调用 `AddDimension(name, score, max, evidence, "")`，且 `score < max*2/3`（维度失败但未传 flag）。

## 根因

`AddDimension` 原实现：

```go
e.Dimensions = append(e.Dimensions, DimensionScore{
    ...
    Flag: flag, // flag 为空时维度级 Flag 保持空
})
if !pass && flag == "" {
    e.Flag = fmt.Sprintf("%s below threshold (%d/%d)", name, score, max)
}
```

自动生成的失败描述只写入整体 `e.Flag`，维度自身的 `Flag` 字段仍为空，与
`FailureFlags` 的收集条件不匹配。

## 修复

在 append 之前为失败且 flag 为空的维度生成自动 flag，并让整体 `e.Flag` 记录第一个失败维度：

```go
if !pass && flag == "" {
    flag = fmt.Sprintf("%s below threshold (%d/%d)", name, score, max)
}
e.Dimensions = append(e.Dimensions, DimensionScore{..., Flag: flag})
if !pass && e.Flag == "" {
    e.Flag = flag
}
```

## 回归测试

- `TestEvidence_FailureFlags/collects_non-empty_flags_only`：混合显式/自动 flag 的失败维度，
  断言 `FailureFlags()` 返回全部 3 个失败维度。
- `TestEvidence_String/failing_evidence_shows_failure_count`：两个失败维度，断言
  `String()` 显示 `fail(2 failures)`。
- `TestAddDimension_ExplicitFlagWins`：显式 flag 时整体 `e.Flag` 记录第一个失败维度。

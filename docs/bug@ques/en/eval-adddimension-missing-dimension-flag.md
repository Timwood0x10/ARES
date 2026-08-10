# Bug: eval.Evidence.AddDimension misses dimension-level flag, FailureFlags loses failing dimensions

- **ID**: BUG-EVAL-001
- **Severity**: P2 / Medium
- **Status**: fixed
- **Date**: 2026-08-10
- **Package**: `internal/eval`

## Symptom

When a dimension fails (below the 2/3 threshold) and the caller does not pass an explicit
`flag`, `Evidence.AddDimension` writes the auto-generated failure description only into the
**overall** `e.Flag`, not into the dimension's `DimensionScore.Flag`.

Because `FailureFlags()` only collects dimensions where `!d.Pass && d.Flag != ""`:

- A failing dimension without an explicit flag is completely missed by `FailureFlags()`.
- The failure count in `Evidence.String()` (`len(e.FailureFlags())`) shows 0 even when
  several dimensions failed.

## Trigger Conditions

- Calling `AddDimension(name, score, max, evidence, "")` with `score < max*2/3`
  (dimension fails without an explicit flag).

## Root Cause

Original `AddDimension` implementation:

```go
e.Dimensions = append(e.Dimensions, DimensionScore{
    ...
    Flag: flag, // stays empty when flag is empty
})
if !pass && flag == "" {
    e.Flag = fmt.Sprintf("%s below threshold (%d/%d)", name, score, max)
}
```

The auto-generated failure description was written only to the overall `e.Flag`; the
dimension's own `Flag` field remained empty, which does not match `FailureFlags`'
collection condition.

## Fix

Generate an auto flag for failing dimensions before appending, and let the overall
`e.Flag` record the first failing dimension:

```go
if !pass && flag == "" {
    flag = fmt.Sprintf("%s below threshold (%d/%d)", name, score, max)
}
e.Dimensions = append(e.Dimensions, DimensionScore{..., Flag: flag})
if !pass && e.Flag == "" {
    e.Flag = flag
}
```

## Regression Tests

- `TestEvidence_FailureFlags/collects_non-empty_flags_only`: mixed explicit/auto flags,
  asserts `FailureFlags()` returns all 3 failing dimensions.
- `TestEvidence_String/failing_evidence_shows_failure_count`: two failing dimensions,
  asserts `String()` shows `fail(2 failures)`.
- `TestAddDimension_ExplicitFlagWins`: with an explicit flag, the overall `e.Flag`
  records the first failing dimension.

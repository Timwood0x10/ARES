# Bug: goimports formatter causes 800%+ CPU via modindex re-reads under multi-core parallelism

- **ID**: BUG-TOOLS-001
- **Severity**: P1 / High
- **Status**: fixed
- **Date**: 2026-08-30
- **Package**: tooling (`.golangci.yml`, `Makefile`)

## Symptom

Running `make check` (equivalent to `make lint` + `make test`) caused CPU utilization
to spike to 800%+ on a 14-core machine. golangci-lint would hang for 5+ minutes and
eventually time out without completing.

## Trigger Conditions

- golangci-lint v2.13.x (both v2.13.1 and v2.13.2 are affected), embedding
  `golang.org/x/tools v0.49.0`;
- `.golangci.yml` has `goimports` in `formatters.enable`;
- A modindex payload file (~25MB) exists in `~/Library/Caches/goimports/`;
- golangci-lint cache is cold (first run after `golangci-lint cache clean`).

## Root Cause

In golangci-lint v2, `goimports` runs as an analysis analyzer. Each Go source file
triggers `goimports.Formatter.Format()` → `imports.Process()` → `fixImportsDefault()` →
`getFixesWithSource()` → `modindex.Read()`.

The `modindex.Read()` implementation (`golang.org/x/tools@v0.49.0/internal/modindex/index.go`)
has **no caching whatsoever**: every call opens the ~25MB CSV-format index file, scans it
line-by-line, and builds a `[]Entry` slice from scratch.

With 14-way parallel analysis, multiple goroutines simultaneously read and parse the same
25MB file, causing:

1. Massive memory allocation — CPU profile shows `runtime.mallocgc` at **56%**;
2. GC pressure — `runtime.(*spanInlineMarkBits).init` at 19%, `mallocgc` chain at 56%;
3. Total CPU — `modindex.readIndexFrom` at **67.85%**, `goformatters.NewAnalyzer.func1` at 68.79%.

### CPU Profile Highlights (before fix)

```
Total samples = 533.62s (747.65%)
  67.85%  modindex.readIndexFrom
  68.79%  goformatters.NewAnalyzer.func1 → imports.Process
  56.12%  runtime.mallocgc
```

## Fix

### Approach: replace `goimports` with `gci`

`gci` (`github.com/daixiang0/gci`) is an import-grouping tool designed for efficiency
and determinism. Unlike `goimports`, it **does not scan GOMODCACHE or read modindex at
all**, resulting in near-zero memory overhead.

### Changes

1. **`.golangci.yml`**: replaced `goimports` with `gci` in `formatters.enable`,
   configured with `custom-order: true` and three-way section grouping:

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

2. **`Makefile`**: `fmt` target changed from `goimports -w .` to `golangci-lint run --fix`,
   ensuring the formatter and checker use the same gci version (avoids format differences
   between gci v0.13.x and v0.14.x).

3. **`.pre-commit-config.yaml`**: removed `go-imports` hook; gci checking is covered by
   the `golangci-lint run --fix` hook.

### Results

| Metric | Before | After |
|--------|--------|-------|
| CPU profile total samples | 533s (747%) | 39s (628%) |
| `modindex.readIndexFrom` | 362s (67.85%) | **0s (0%)** |
| `runtime.mallocgc` | 299s (56.12%) | **<1s** |
| Wall time | timeout (5 min+, never completes) | **6 seconds** |
| Result | incomplete | 0 issues |

## Notes

- The `gci` CLI tool version must match the version embedded in golangci-lint (v0.13.7).
  Using a newer `gci` (e.g. v0.14.0) to format code will cause golangci-lint to report
  format mismatches. Therefore `make fmt` uses `golangci-lint run --fix` rather than
  calling `gci` directly.
- The old goimports cache (`~/Library/Caches/goimports/`) can be safely cleaned.
  On this machine it was reduced from 1GB (47 historical index files) to 24MB.

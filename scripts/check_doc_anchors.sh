#!/usr/bin/env bash
# check_doc_anchors.sh — documentation drift detector (A2, 0.3.2).
#
# Scans project *.md files for `file.go:NNN` anchors and warns when:
#   - the referenced source file cannot be resolved, or
#   - the anchor line number exceeds the file's current line count, i.e. the
#     reference went stale after a refactor (the A2 failure mode: kernel code
#     split into scheduler_dispatch/execute/quantum.go while the docs kept
#     pointing at the old single-file line numbers).
#
# Anchors resolve in order: repo-root-relative, then relative to the markdown
# file's directory, then by unique basename. Ambiguous bare filenames (e.g.
# lifecycle.go in several packages) are skipped — guessing would only produce
# false positives.
#
# Performance (REVIEW-2026-10-07 H1): the first cut forked ~3 processes per
# anchor (~7700 forks over ~2500 anchors) and could not finish in 90s. Two O(1)
# indexes are now built once (basename->path, path->line-count) and BOTH
# resolution and the stale check run in a SINGLE awk pass over the collected
# anchors, so the per-anchor shell work is one grep-backed line read with zero
# subprocesses.
#
# plan/archive is pruned (REVIEW-2026-10-07 M1): those are historical reviews
# whose anchors are EXPECTED stale after refactors; flagging them is noise.
#
# Warnings only (exit 0) so it can land in CI before the tree is clean.
#
# Usage: scripts/check_doc_anchors.sh [repo_root]

set -u

ROOT="${1:-$(cd "$(dirname "$0")/.." && pwd)}"
MAX_REPORT=50

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
INDEX="$TMP/index.tsv"  # basename<TAB>absolute-path
LINES="$TMP/lines.tsv"  # absolute-path<TAB>line-count
CHECKS="$TMP/checks.tsv" # mdfile<TAB>mddir<TAB>ref<TAB>lineno
WARNS="$TMP/warnings.txt"
: > "$CHECKS"

# Shared prune: dot-dirs (.git …), vendor/node_modules, and plan/archive.
FIND_BASE=(find "$ROOT" \( -name '.*' -o -name vendor -o -name node_modules \
    -o -path "$ROOT/plan/archive" \) -prune -o)

# basename -> path, one awk pass (no per-file `basename` fork).
"${FIND_BASE[@]}" -name '*.go' -print 2>/dev/null |
    awk -F/ '{ print $NF "\t" $0 }' > "$INDEX"

# path -> line count, one wc pass over every Go file.
"${FIND_BASE[@]}" -name '*.go' -print0 2>/dev/null |
    xargs -0 wc -l 2>/dev/null |
    awk '$2 != "total" { print $2 "\t" $1 }' > "$LINES"

# Collect raw anchors. Resolution is deferred to the awk pass below.
while IFS= read -r mdfile; do
    mddir="${mdfile%/*}"
    while IFS= read -r match; do
        ref="${match%:*}"
        lineno="${match##*:}"
        case "$lineno" in '' | *[!0-9]*) continue ;; esac
        printf '%s\t%s\t%s\t%s\n' "$mdfile" "$mddir" "$ref" "$lineno" >> "$CHECKS"
    done < <(grep -oE '[A-Za-z0-9_/.-]+\.go:[0-9]+' "$mdfile" 2>/dev/null || true)
done < <("${FIND_BASE[@]}" -name '*.md' -print 2>/dev/null)

# One pass: load the two indexes, then resolve + stale-check every anchor.
# Resolution mirrors the original three-step fallback:
#   1. repo-root-relative, 2. markdown-dir-relative,
#   3. unique suffix / unique basename. Ambiguous matches are skipped silently.
awk -F'\t' -v IDX="$INDEX" -v LINF="$LINES" -v ROOT="$ROOT" '
    FILENAME == IDX {
        paths[$2] = 1
        cnt[$1]++
        uniq[$1] = $2
        all[++n] = $2
        next
    }
    FILENAME == LINF { lines[$1] = $2; next }
    {
        md = $1; dir = $2; ref = $3; ln = $4 + 0
        resolved = ""
        if (index(ref, "/") > 0) {
            if (paths[ROOT "/" ref]) {
                resolved = ROOT "/" ref
            } else if (paths[dir "/" ref]) {
                resolved = dir "/" ref
            } else {
                suf = "/" ref
                found = 0
                for (i = 1; i <= n; i++) {
                    p = all[i]
                    if (substr(p, length(p) - length(suf) + 1) == suf) {
                        if (++found > 1) break
                        resolved = p
                    }
                }
                if (found != 1) { resolved = "" } # none, or ambiguous -> skip
            }
        } else {
            if (cnt[ref] > 1) next           # ambiguous bare name -> skip silently
            resolved = uniq[ref]             # unique, or "" when absent
        }
        if (resolved == "") {
            printf "warn: %s references unresolved file: %s\n", md, ref
            next
        }
        t = lines[resolved]
        if (t != "" && ln > t + 0)
            printf "warn: %s -> %s:%d exceeds %d lines (stale anchor)\n", md, ref, ln, t
    }
' "$INDEX" "$LINES" "$CHECKS" > "$WARNS"

WARNINGS="$(awk 'END { print NR }' "$WARNS")"
if [ "$WARNINGS" -gt 0 ]; then
    head -n "$MAX_REPORT" "$WARNS"
    if [ "$WARNINGS" -gt "$MAX_REPORT" ]; then
        echo "doc-anchor check: $WARNINGS warning(s) total, first $MAX_REPORT shown"
    else
        echo "doc-anchor check: $WARNINGS warning(s) — review stale references"
    fi
fi

# Warnings only for now; tighten to exit 1 in a future iteration.
exit 0

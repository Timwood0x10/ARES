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
# Local-only and superseded trees are pruned: the whole plan/ scratch tree
# (gitignored, author-local) and docs/archive (historical docs kept for
# reference) — their anchors are EXPECTED stale after refactors, so flagging
# them is noise.
#
# Hard gate (exit 1): the tree is clean as of 0.3.2, so a stale anchor fails CI
# instead of rotting silently. When a deliberately-historical document is
# introduced, move it into docs/archive rather than loosening this check.
#
# What this check CANNOT do: prove an anchor still points at the thing the prose
# claims. It only proves the file resolves and the line exists. That limit is not
# fixable by a stricter rule — 46 of this repo's anchors legitimately point at a
# doc-comment line (the convention is "cite the line that defines or documents
# the thing", which includes a specific sentence inside a comment block), so any
# content rule would be ~30% false positives. The 2026-10-10 round proved the
# failure mode: four refs kept passing while pointing into the middle of a
# function's comment block.
#
# `--show` is the answer to that: it prints every anchor next to the line it
# resolves to, turning the human review into one screen. Run it whenever anchors
# are added or the cited code moves; the check itself still runs afterwards.
#
# Usage: scripts/check_doc_anchors.sh [--show] [repo_root]

set -u

SHOW=0
ROOT=""
for arg in "$@"; do
    case "$arg" in
        --show) SHOW=1 ;;
        *) ROOT="$arg" ;;
    esac
done
if [ -z "$ROOT" ]; then
    ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fi
MAX_REPORT=50

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
INDEX="$TMP/index.tsv"  # basename<TAB>absolute-path
LINES="$TMP/lines.tsv"  # absolute-path<TAB>line-count
CHECKS="$TMP/checks.tsv" # mdfile<TAB>mddir<TAB>ref<TAB>lineno
WARNS="$TMP/warnings.txt"
SHOWOUT="$TMP/show.tsv"   # mdfile<TAB>resolved:line<TAB>cited-line-content
: > "$CHECKS"
: > "$SHOWOUT"

# Shared prune: dot-dirs (.git …), vendor/node_modules, the whole local-only
# plan/ scratch tree (gitignored: its anchors are author-local notes, and a
# stale one there says nothing about the repository), and docs/archive
# (superseded history kept for reference — its anchors are expected to rot).
FIND_BASE=(find "$ROOT" \( -name '.*' -o -name vendor -o -name node_modules \
    -o -path "$ROOT/plan" -o -path "$ROOT/docs/archive" \) -prune -o)

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
awk -F'\t' -v IDX="$INDEX" -v LINF="$LINES" -v ROOT="$ROOT" \
    -v SHOW="$SHOW" -v SHOWOUT="$SHOWOUT" '
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
            if (SHOW) {
                mdp = md; sub(ROOT "/", "", mdp)
                printf "%s\t%s:%d\t%s\n", mdp, ref, ln, "[unresolved]" >> SHOWOUT
            }
            next
        }
        t = lines[resolved]
        if (t != "" && ln > t + 0)
            printf "warn: %s -> %s:%d exceeds %d lines (stale anchor)\n", md, ref, ln, t
        if (SHOW) {
            content = ""
            if ((getline content < resolved) > 0) {
                k = 1
                while (k < ln) {
                    if ((getline content < resolved) <= 0) break
                    k++
                }
            }
            close(resolved)
            if (t != "" && ln > t + 0) content = "[stale: file has " t " lines]"
            # Indented source lines start with a tab; left as-is they would be
            # eaten by the tab-separated print below and show up as empty.
            gsub(/\t/, "  ", content)
            mdp = md; sub(ROOT "/", "", mdp)
            printf "%s\t%s:%d\t%s\n", mdp, ref, ln, content >> SHOWOUT
        }
    }
' "$INDEX" "$LINES" "$CHECKS" > "$WARNS"

if [ "$SHOW" = "1" ]; then
    echo "doc-anchor review: every anchor against the line it resolves to."
    echo "This check proves a line EXISTS; whether it is still the line the prose"
    echo "claims is a read, not a rule. Cited lines that look like prose you did"
    echo "not intend are the drift to fix."
    echo
    sort "$SHOWOUT" |
        awk -F'\t' '{ printf "%-44s -> %-50s | %s\n", $1, $2, $3 }'
    echo
fi

WARNINGS="$(awk 'END { print NR }' "$WARNS")"
if [ "$WARNINGS" -gt 0 ]; then
    head -n "$MAX_REPORT" "$WARNS"
    if [ "$WARNINGS" -gt "$MAX_REPORT" ]; then
        echo "doc-anchor check: $WARNINGS stale reference(s), first $MAX_REPORT shown" >&2
    else
        echo "doc-anchor check: $WARNINGS stale reference(s)" >&2
    fi
    echo "Fix the anchor (file.go:NNN) or prune the file in this script (see the header)." >&2
    exit 1
fi

echo "doc-anchor check: OK — no stale file.go:NNN references"
exit 0

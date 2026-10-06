#!/usr/bin/env bash
# check_doc_anchors.sh — documentation drift detector (A2, 0.3.2).
#
# Scans *.md files for `file.go:NNN` style anchors and warns when the
# referenced file does not exist. This catches stale line-number
# references left over from refactors.
#
# Usage: scripts/check_doc_anchors.sh [repo_root]
# Exit code: 0 (warnings only, CI gate can be tightened later)

set -u

ROOT="${1:-$(cd "$(dirname "$0")/.." && pwd)}"
WARNINGS=0

# Find all .md files and scan for file:line anchors like `path/to/file.go:123`
while IFS= read -r mdfile; do
    # Extract file.go:NNN patterns
    while IFS= read -r match; do
        filepart="${match%:*}"
        fullpath="$ROOT/$filepart"

        if [ ! -f "$fullpath" ]; then
            echo "warn: $mdfile references missing file: $filepart"
            WARNINGS=$((WARNINGS + 1))
        fi
    done < <(grep -oE '[a-zA-Z0-9_/.-]+\.go:[0-9]+' "$mdfile" 2>/dev/null || true)
done < <(find "$ROOT" -name '*.md' -not -path '*/.git/*' -not -path '*/vendor/*' 2>/dev/null)

if [ "$WARNINGS" -gt 0 ]; then
    echo "doc-anchor check: $WARNINGS warning(s) — review stale references"
fi

# Warnings only for now; tighten to exit 1 in a future iteration
exit 0

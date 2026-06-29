#!/usr/bin/env bash
#
# stop_interview_demo.sh - Stop SearXNG and interview-demo
#
# Usage:
#   ./scripts/stop_interview_demo.sh
#

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

log_info()  { printf "${GREEN}[INFO]${NC}  %s\n" "$*"; }
log_error() { printf "${RED}[ERROR]${NC} %s\n" "$*"; }

# ── Stop interview-demo processes ───────────────
INTERVIEW_PIDS=$(pgrep -f "interview-demo" 2>/dev/null || true)
if [ -n "$INTERVIEW_PIDS" ]; then
  log_info "Stopping interview-demo..."
  echo "$INTERVIEW_PIDS" | xargs kill 2>/dev/null || true
fi

# ── Stop SearXNG ────────────────────────────────
log_info "Stopping SearXNG..."
docker compose stop searxng 2>/dev/null || true

log_info "Done. SearXNG container preserved (no re-download needed)."

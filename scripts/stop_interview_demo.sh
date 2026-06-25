#!/usr/bin/env bash
#
# stop_interview_demo.sh - Stop SearXNG and clean up
#
# Stops the SearXNG container started by start_interview_demo.sh.
#
# Usage:
#   ./scripts/stop_interview_demo.sh [--clean]
#
# Options:
#   --clean    Also remove the SearXNG volume (clears cache)
#

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { printf "${GREEN}[INFO]${NC}  %s\n" "$*"; }
log_warn()  { printf "${YELLOW}[WARN]${NC}  %s\n" "$*"; }
log_error() { printf "${RED}[ERROR]${NC} %s\n" "$*"; }
log_step()  { printf "\n${CYAN}══════ %s ══════${NC}\n" "$*"; }

log_step "Stopping SearXNG"

if [ "${1:-}" = "--clean" ]; then
  log_info "Stopping and removing searxng container + volume..."
  docker compose down -v searxng 2>/dev/null || true
  log_info "SearXNG stopped and data volume removed."
else
  log_info "Stopping searxng container (data preserved)..."
  docker compose stop searxng 2>/dev/null || true
  log_info "SearXNG stopped."
  log_warn "Data volume preserved. Use --clean to clear cache."
fi

log_info "Done."

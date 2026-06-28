#!/usr/bin/env bash
#
# start_interview_demo.sh - One-click startup script
#
# 1. Starts SearXNG (Docker Compose)
# 2. Waits for SearXNG health check
# 3. Starts the Interview Agent CLI
#
# Usage:
#   ./scripts/start_interview_demo.sh
#
# Environment Variables:
#   CONFIG_PATH  - Path to server.yaml (default: examples/interview-demo/config/server.yaml)
#   SEARXNG_URL  - SearXNG base URL (default: http://localhost:5605)
#

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

# ── Colors ──────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

log_info()  { printf "${GREEN}[INFO]${NC}  %s\n" "$*"; }
log_warn()  { printf "${YELLOW}[WARN]${NC}  %s\n" "$*"; }
log_error() { printf "${RED}[ERROR]${NC} %s\n" "$*"; }
log_step()  { printf "\n${CYAN}══════ %s ══════${NC}\n" "$*"; }

# ── Cleanup on exit ─────────────────────────────
cleanup() {
  log_step "Shutting down"
  log_info "Stopping SearXNG..."
  docker compose stop searxng 2>/dev/null || true
  log_info "Done."
}
trap cleanup EXIT

# ── 1. Check Docker ────────────────────────────
log_step "Checking Docker"
if ! command -v docker &>/dev/null; then
  log_error "Docker is not installed. Install Docker Desktop: https://docs.docker.com/get-docker/"
  exit 1
fi

if ! docker info &>/dev/null; then
  log_error "Docker daemon is not running. Start Docker Desktop first."
  exit 1
fi
log_info "Docker is running."

# ── 2. Start SearXNG ───────────────────────────
log_step "Starting SearXNG"
log_info "Bringing up searxng service..."

docker compose up -d searxng

log_info "Waiting for SearXNG to be ready..."
MAX_RETRIES=30
RETRY=0
while [ $RETRY -lt $MAX_RETRIES ]; do
  if curl -sf "http://localhost:5605/search?q=health&format=json" >/dev/null 2>&1; then
    log_info "SearXNG is ready on http://localhost:5605"
    break
  fi
  RETRY=$((RETRY + 1))
  if [ $RETRY -eq $MAX_RETRIES ]; then
    log_error "SearXNG did not start within ${MAX_RETRIES}s. Check: docker compose logs searxng"
    exit 1
  fi
  sleep 1
done
log_info "$(curl -sf "http://localhost:5605/search?q=health&format=json" | python3 -c "import sys,json;d=json.load(sys.stdin);print(f'Query: {d.get(\"query\",\"?\")}, Results: {len(d.get(\"results\",[]))}')" 2>/dev/null || echo "SearXNG responding")"

# ── 3. Build and start the Interview Agent ──────
log_step "Starting Interview Agent"

CONFIG_PATH="${CONFIG_PATH:-examples/interview-demo/config/server.yaml}"
if [ ! -f "$CONFIG_PATH" ]; then
  log_error "Config not found: $CONFIG_PATH"
  exit 1
fi
export CONFIG_PATH

log_info "Building interview-demo..."
if ! go build -o /tmp/interview-demo ./examples/interview-demo/; then
  log_error "Build failed."
  exit 1
fi
log_info "Build succeeded."

echo ""
echo "============================================================"
echo "  Interview Agent is ready!"
echo "  Type a technical interview question to search and analyze."
echo "  Type 'caps' to see capabilities, 'exit' to quit."
echo "============================================================"
echo ""

# Run the binary (subshell so trap handles docker stop)
/tmp/interview-demo

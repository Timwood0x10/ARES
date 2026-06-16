#!/bin/bash
#
# GoAgentX Chaos Demo — MCP Dashboard
#
# Starts the demo and tails logs. Kills existing process on the same port.
# Usage:
#   ./run.sh [config-path]           # start + tail logs
#   ./run.sh --stop                  # stop running instance
#   ./run.sh --status                # check if running
#
set -e

CONFIG="${1:-./examples/mcp-dashboard/config.yaml}"
DIR="$(cd "$(dirname "$0")" && pwd)"
LOG="$DIR/run.log"
PIDFILE="/tmp/mcp-dashboard.pid"

# Colors.
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# Extract port from config.
ADDR=$(grep 'addr:' "$CONFIG" | head -1 | sed 's/.*: *//' | tr -d '"' | tr -d "'")
ADDR="${ADDR:-:8090}"
PORT="${ADDR#:}"

# ---- Helpers ----
info()  { echo -e "${CYAN}${BOLD}==>${NC} ${BOLD}$1${NC}"; }
ok()    { echo -e " ${GREEN}✓${NC} $1"; }
warn()  { echo -e " ${YELLOW}⚠${NC} $1"; }
fail()  { echo -e " ${RED}✗${NC} $1"; }

cleanup() {
    if [ -f "$PIDFILE" ]; then
        OLD_PID=$(cat "$PIDFILE" 2>/dev/null || echo "")
        if [ -n "$OLD_PID" ] && kill -0 "$OLD_PID" 2>/dev/null; then
            warn "Stopping existing process (PID $OLD_PID)..."
            kill "$OLD_PID" 2>/dev/null || true
            sleep 1
        fi
        rm -f "$PIDFILE"
    fi
}

# ---- Commands ----
if [ "$1" = "--stop" ]; then
    cleanup
    ok "Stopped"
    exit 0
fi

if [ "$1" = "--status" ]; then
    if [ -f "$PIDFILE" ]; then
        PID=$(cat "$PIDFILE" 2>/dev/null || echo "")
        if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
            ok "Running (PID $PID, http://localhost:$PORT)"
            exit 0
        fi
        rm -f "$PIDFILE"
    fi
    info "Not running"
    exit 0
fi

# ---- Pre-flight checks ----
echo -e "\n${CYAN}${BOLD}═══ GoAgentX Chaos Demo ═══${NC}\n"

# Check port.
if lsof -i ":$PORT" >/dev/null 2>&1; then
    warn "Port $PORT is in use. Use '$0 --stop' first or wait."
    warn "Continuing will attempt to start anyway."
    echo ""
fi

# Build.
info "Building..."
BUILD_OUT=$(go build -o /tmp/mcp-dashboard ./examples/mcp-dashboard/ 2>&1) || {
    fail "Build failed:"
    echo "$BUILD_OUT"
    exit 1
}
ok "Built to /tmp/mcp-dashboard"

# Start.
info "Starting on port $PORT..."
/tmp/mcp-dashboard -config "$CONFIG" -log "$LOG" > "$LOG" 2>&1 &
PID=$!
echo "$PID" > "$PIDFILE"

# Wait for startup.
sleep 2
if ! kill -0 "$PID" 2>/dev/null; then
    fail "Process died immediately. Check logs:"
    tail -5 "$LOG"
    exit 1
fi
ok "Started (PID $PID)"

# Register cleanup on exit.
trap 'cleanup; echo -e "${GREEN}Demo stopped${NC}"' EXIT INT TERM

echo ""
echo -e "  ${BOLD}Dashboard:${NC}  http://localhost:$PORT"
echo -e "  ${BOLD}Log file:${NC}   $LOG"
echo -e "  ${BOLD}Stop:${NC}       $0 --stop  (or Ctrl+C to stop + exit)"
echo ""
echo -e "  ${YELLOW}Tips:${NC}"
echo -e "    - Open the Dashboard and click ☠Leader to see self-healing"
echo -e "    - Click agent nodes to select, then use KILL / Pause / Slow"
echo -e "    - Watch the Event Log for real-time recovery timeline"
echo ""

# Open browser (macOS only, best-effort).
if command -v open >/dev/null 2>&1; then
    open "http://localhost:$PORT" 2>/dev/null || true
fi

# Tail logs.
echo -e "${CYAN}${BOLD}─── Live Log ──────────────────────────────────────${NC}"
tail -f "$LOG"

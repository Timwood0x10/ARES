#!/bin/bash
# ares Quantitative Trading Demo — one-click runner.
set -e

CONFIG="${1:-./examples/quant-trading/config.yaml}"
DIR="$(cd "$(dirname "$0")" && pwd)"
LOG="$DIR/run.log"
PIDFILE="/tmp/quant-trading.pid"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
info() { echo -e "${CYAN}${BOLD}==>${NC} ${BOLD}$1${NC}"; }
ok()   { echo -e " ${GREEN}✓${NC} $1"; }
warn() { echo -e " ${YELLOW}⚠${NC} $1"; }
fail() { echo -e " ${RED}✗${NC} $1"; }

ADDR=$(grep 'addr:' "$CONFIG" | head -1 | sed 's/.*: *//' | tr -d '"' | tr -d "'")
ADDR="${ADDR:-:8092}"; PORT="${ADDR#:}"

cleanup() {
    if [ -f "$PIDFILE" ]; then
        OLD_PID=$(cat "$PIDFILE" 2>/dev/null || echo "")
        if [ -n "$OLD_PID" ] && kill -0 "$OLD_PID" 2>/dev/null; then
            warn "Stopping (PID $OLD_PID)..."; kill "$OLD_PID" 2>/dev/null || true; sleep 1
        fi
        rm -f "$PIDFILE"
    fi
}

if [ "$1" = "--stop" ]; then cleanup; ok "Stopped"; exit 0; fi
if [ "$1" = "--status" ]; then
    if [ -f "$PIDFILE" ]; then
        PID=$(cat "$PIDFILE" 2>/dev/null || echo "")
        if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then ok "Running (PID $PID, http://localhost:$PORT)"; exit 0; fi
        rm -f "$PIDFILE"
    fi
    info "Not running"; exit 0
fi

echo -e "\n${CYAN}${BOLD}═══ ares Quant Trading Demo ═══${NC}\n"

# Install mcp-null helper.
info "Building mcp-null..."
go install ./cmd/mcp-null/ 2>/dev/null || true

# Build.
info "Building quant trading demo..."
BUILD_OUT=$(go build -o /tmp/quant-trading ./examples/quant-trading/ 2>&1) || { fail "Build failed:"; echo "$BUILD_OUT"; exit 1; }
ok "Built to /tmp/quant-trading"

# Start.
info "Starting on port $PORT..."
/tmp/quant-trading -config "$CONFIG" -log "$LOG" > "$LOG" 2>&1 &
PID=$!; echo "$PID" > "$PIDFILE"
sleep 3
if ! kill -0 "$PID" 2>/dev/null; then fail "Process died:"; tail -5 "$LOG"; exit 1; fi
ok "Started (PID $PID)"

trap 'cleanup; echo -e "${GREEN}Demo stopped${NC}"' EXIT INT TERM
echo ""; echo -e "  ${BOLD}Dashboard:${NC}  http://localhost:$PORT"; echo -e "  ${BOLD}Log:${NC}         $LOG"; echo ""
if command -v open >/dev/null 2>&1; then open "http://localhost:$PORT" 2>/dev/null || true; fi
echo -e "${CYAN}${BOLD}─── Live Log ─────────────────────────────────${NC}"
tail -f "$LOG"

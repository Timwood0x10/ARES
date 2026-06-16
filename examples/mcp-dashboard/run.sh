#!/bin/bash
set -e

CONFIG="${1:-./examples/mcp-dashboard/config.yaml}"
ADDR=$(grep 'addr:' "$CONFIG" | head -1 | sed 's/.*: *//' | tr -d '"' | tr -d "'")
ADDR="${ADDR:-:8090}"
PORT="${ADDR#:}"
DIR="$(cd "$(dirname "$0")" && pwd)"
LOG="$DIR/run.log"

echo "Building..."
go build -o /tmp/mcp-dashboard ./examples/mcp-dashboard/

echo "Starting in background..."
/tmp/mcp-dashboard -config "$CONFIG" -log "$LOG" > "$LOG" 2>&1 &
PID=$!

sleep 3

echo ""
echo "PID:      $PID"
echo "Dashboard: http://localhost:$PORT"
echo "Logs:      tail -f $LOG"
echo "Stop:      kill $PID"
echo ""

open "http://localhost:$PORT" 2>/dev/null || true

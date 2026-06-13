#!/bin/bash
set -e

TARGET="${1:-.}"
CONFIG="${2:-./examples/mcp-dashboard/config.yaml}"
ADDR=$(grep 'addr:' "$CONFIG" | head -1 | sed 's/.*: *//' | tr -d '"' | tr -d "'")
ADDR="${ADDR:-:8090}"
PORT="${ADDR#:}"
LOG=/tmp/mcp-dashboard.log

echo "Building..."
go build -o /tmp/mcp-dashboard ./examples/mcp-dashboard/

echo "Starting in background..."
/tmp/mcp-dashboard -config "$CONFIG" -target "$TARGET" > "$LOG" 2>&1 &
PID=$!

sleep 3

echo ""
echo "PID:      $PID"
echo "Dashboard: http://localhost:$PORT"
echo "Logs:      tail -f $LOG"
echo "Stop:      kill $PID"
echo ""

open "http://localhost:$PORT" 2>/dev/null || true

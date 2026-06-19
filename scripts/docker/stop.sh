#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

echo "=== Stopping GoAgent Docker services ==="
docker compose -f "$ROOT/docker-compose.yml" down

echo ""
echo "✅ All services stopped. Data preserved."

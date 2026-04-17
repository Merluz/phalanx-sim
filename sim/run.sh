#!/usr/bin/env bash
set -euo pipefail

PROFILE="${1:-dashboard-dev}"
NODES="${2:-500}"
PORT="${PORT:-8090}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

case "$PROFILE" in
  build)
    go build ./cmd/sim ./cmd/dashboard
    ;;
  dashboard-dev)
    go run ./cmd/dashboard -listen ":${PORT}" -log-mode progress
    ;;
  dashboard-quiet)
    go run ./cmd/dashboard -listen ":${PORT}" -log-mode quiet
    ;;
  sim-smoke)
    go run ./cmd/sim \
      -n "${NODES}" \
      -async-spawn=true \
      -spawn-parallelism=0 \
      -stats-every=2s \
      -udp-demo=true \
      -udp-sender=random \
      -udp-interval=1s
    ;;
  sim-load)
    go run ./cmd/sim \
      -n "${NODES}" \
      -async-spawn=true \
      -spawn-parallelism=0 \
      -stats-every=5s \
      -udp-demo=false \
      -run-for=45s
    ;;
  *)
    echo "Unknown profile: ${PROFILE}" >&2
    echo "Valid profiles: build, dashboard-dev, dashboard-quiet, sim-smoke, sim-load" >&2
    exit 1
    ;;
esac

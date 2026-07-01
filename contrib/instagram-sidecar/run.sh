#!/usr/bin/env bash
# Start the zcoms Instagram sidecar and wait until it is healthy.
#
#   ./run.sh            # build + start via docker compose, wait for /health
#   ./run.sh --local    # run with a local Python venv (no Docker)
#
# The sidecar listens on 127.0.0.1:8099. Point the daemon at a non-default host
# with ZC_INSTAGRAM_SIDECAR (e.g. http://127.0.0.1:9000).
set -euo pipefail
cd "$(dirname "$0")"

PORT="${PORT:-8099}"
URL="http://127.0.0.1:${PORT}/health"

wait_healthy() {
  echo "waiting for the sidecar to become healthy at ${URL} ..."
  for _ in $(seq 1 30); do
    if curl -fsS "${URL}" >/dev/null 2>&1; then
      echo "sidecar healthy."
      return 0
    fi
    sleep 1
  done
  echo "sidecar did not become healthy in time" >&2
  return 1
}

if [[ "${1:-}" == "--local" ]]; then
  python3 -m venv .venv
  # shellcheck disable=SC1091
  source .venv/bin/activate
  pip install --quiet --upgrade pip
  pip install --quiet -r requirements.txt
  uvicorn app:app --host 127.0.0.1 --port "${PORT}" &
  wait_healthy
  wait
else
  docker compose up -d --build
  wait_healthy
fi

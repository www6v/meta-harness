#!/usr/bin/env bash
set -euo pipefail

# Start the Codex app-server bridge on :8092.
#
# The bridge exposes POST /codex/turn — the Go harness Codex client calls
# this endpoint to run a turn against a remote codex app-server. The
# bridge speaks JSON-RPC 2.0 over WebSocket and (optionally) routes the
# connection through an SSH tunnel when the WS port is only reachable
# via the bastion.
#
# Common env vars (defaults in codex_bridge.py):
#   CODEX_WS_URL         ws://127.0.0.1:8765  (direct mode)
#   CODEX_WS_TOKEN       Bearer token (optional)
#   CODEX_SSH_HOST       124.221.28.203       (enable SSH tunnel)
#   CODEX_SSH_USER       root
#   CODEX_SSH_PASSWORD   1qaZxsw@
#   CODEX_REMOTE_PORT    8765
#   CODEX_LISTEN         127.0.0.1:8092

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXT_DIR="$(cd "${ROOT_DIR}/../meta-harness-ext" && pwd)"
cd "${EXT_DIR}/ssh"

export CODEX_LISTEN="${CODEX_LISTEN:-127.0.0.1:8092}"
export CODEX_WS_URL="${CODEX_WS_URL:-ws://127.0.0.1:8765}"
export CODEX_WS_TOKEN="${CODEX_WS_TOKEN:-codex-poc-token-2024}"
export CODEX_SSH_HOST="${CODEX_SSH_HOST:-124.221.28.203}"
export CODEX_SSH_USER="${CODEX_SSH_USER:-root}"
export CODEX_SSH_PASSWORD="${CODEX_SSH_PASSWORD:-1qaZxsw@}"
export CODEX_REMOTE_PORT="${CODEX_REMOTE_PORT:-8765}"

# Prefer the meta-harness python venv if available (has websockets, paramiko).
META_HARNESS_DIR="${ROOT_DIR}"
if [[ -x "${META_HARNESS_DIR}/harness/.venv/bin/python" ]]; then
  PY="${META_HARNESS_DIR}/harness/.venv/bin/python"
elif [[ -x "${META_HARNESS_DIR}/harness/.venv/Scripts/python.exe" ]]; then
  PY="${META_HARNESS_DIR}/harness/.venv/Scripts/python.exe"
else
  PY="${PYTHON:-python3}"
fi

echo "codex-bridge listening on ${CODEX_LISTEN}"
echo "  ws_url=${CODEX_WS_URL} ssh=${CODEX_SSH_HOST}:${CODEX_REMOTE_PORT}"

# Run as a package so relative imports (`from .codex_client import ...`) resolve.
# Use Python -c to set sys.path explicitly for Windows compatibility
PY_PARENT_DIR="$(cygpath -w "${ROOT_DIR}/.." 2>/dev/null || echo "${ROOT_DIR}/..")"
# Convert backslashes to forward slashes for Python string
PY_PARENT_DIR="${PY_PARENT_DIR//\\/\/}"
exec "${PY}" -c "
import sys
sys.path.insert(0, '${PY_PARENT_DIR}')
import runpy
runpy.run_module('meta_harness_ext.ssh.codex_bridge', run_name='__main__', alter_sys=True)
"

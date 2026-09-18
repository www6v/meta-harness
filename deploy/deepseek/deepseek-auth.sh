#!/usr/bin/env bash
# DSH credential setup & connectivity verification.
#
# deepseek-harness is deployed independently (outside this project).
# This script handles the integration points between it and meta-harness:
#   1. setup-auth — sync DSH browser-session credentials to /root/.dsh/
#   2. verify    — check DSH ↔ meta-harness connectivity
#   3. status    — read-only overview of the integration state
#
# Usage: ./start-deepseek.sh {setup-auth|verify|status}
#
# Prerequisites:
#   - deepseek-harness container running (name: deepseek-harness)
#   - Both containers on the same Docker network (deepseek-harness-network)
#   - DSH_ALLOW_WILDCARD_HOST=1 (or --trusted-host includes deepseek-harness)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DSH_CONTAINER="deepseek-harness"
DSH_NETWORK="deepseek-harness-network"
CRED_DIR="/root/.dsh"
CRED_FILE="${CRED_DIR}/.credentials.yaml"
DSH_PORT="${DSH_HOST_PORT:-3080}"

# ---------- setup-auth: sync DSH credentials ----------
cmd_setup_auth() {
  echo "=== Setting up DSH credentials ==="
  mkdir -p "${CRED_DIR}"

  # Check container is running
  if ! docker ps --filter "name=${DSH_CONTAINER}" --format '{{.Names}}' 2>/dev/null | grep -q .; then
    echo "[ERROR] ${DSH_CONTAINER} container not running"
    return 1
  fi

  # Wait for DSH to be reachable
  echo "Waiting for DSH on port ${DSH_PORT}..."
  for i in $(seq 1 30); do
    if curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${DSH_PORT}/" 2>/dev/null | grep -qE '^(200|302|303|401)$'; then
      echo "[OK] DSH reachable"
      break
    fi
    if [[ $i -eq 30 ]]; then
      echo "[ERROR] DSH not reachable after 60s"
      return 1
    fi
    sleep 2
  done

  # If container already has credentials, just sync
  if docker exec "${DSH_CONTAINER}" test -f /app/.dsh/.credentials.yaml 2>/dev/null; then
    docker cp "${DSH_CONTAINER}:/app/.dsh/.credentials.yaml" "${CRED_FILE}"
    echo "[OK] Credentials synced from container to ${CRED_FILE}"
    return 0
  fi

  # No credentials yet — login via token to generate them
  echo "No credentials found, logging in via DSH token..."
  local token
  token=$(docker logs "${DSH_CONTAINER}" 2>&1 | grep -oP 'token=\K[a-zA-Z0-9_-]+' | head -1)
  if [[ -z "${token}" ]]; then
    echo "[ERROR] Could not extract token from DSH logs"
    return 1
  fi

  local http_status
  http_status=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${DSH_PORT}/?token=${token}")
  echo "Token login: HTTP ${http_status}"

  if [[ "${http_status}" != "303" && "${http_status}" != "302" && "${http_status}" != "200" ]]; then
    echo "[ERROR] Token login failed: HTTP ${http_status}"
    return 1
  fi

  # Wait for credentials file to be created inside container
  echo "Waiting for credentials file..."
  for i in $(seq 1 15); do
    if docker exec "${DSH_CONTAINER}" test -f /app/.dsh/.credentials.yaml 2>/dev/null; then
      docker cp "${DSH_CONTAINER}:/app/.dsh/.credentials.yaml" "${CRED_FILE}"
      echo "[OK] Credentials created and synced to ${CRED_FILE}"
      return 0
    fi
    sleep 2
  done

  echo "[ERROR] Credentials file not created after 30s"
  return 1
}

# ---------- verify: full integration check ----------
cmd_verify() {
  echo "=== DSH ↔ meta-harness connectivity ==="

  local ok=0

  # DSH container
  if docker ps --filter "name=${DSH_CONTAINER}" --format '{{.Names}}' 2>/dev/null | grep -q .; then
    echo "[OK] ${DSH_CONTAINER} running"
  else
    echo "[FAIL] ${DSH_CONTAINER} not running"
    ok=1
  fi

  # meta-harness container
  if docker ps --filter "name=deploy-meta-harness-1" --format '{{.Names}}' 2>/dev/null | grep -q .; then
    echo "[OK] meta-harness running"
  else
    echo "[WARN] meta-harness not running (use deploy/docker.sh up)"
  fi

  # Same network
  if docker inspect "${DSH_CONTAINER}" --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}' 2>/dev/null | grep -q "${DSH_NETWORK}"; then
    echo "[OK] ${DSH_CONTAINER} on ${DSH_NETWORK}"
  else
    echo "[FAIL] ${DSH_CONTAINER} not on ${DSH_NETWORK}"
    ok=1
  fi

  # DNS
  if docker exec deploy-meta-harness-1 getent hosts "${DSH_CONTAINER}" 2>/dev/null | grep -q .; then
    echo "[OK] DNS: deepseek-harness resolvable from meta-harness"
  else
    echo "[FAIL] DNS: deepseek-harness NOT resolvable from meta-harness"
    ok=1
  fi

  # Credentials
  if [[ -f "${CRED_FILE}" ]]; then
    echo "[OK] Credentials: ${CRED_FILE} exists"
  else
    echo "[FAIL] Credentials: ${CRED_FILE} missing (run: $0 setup-auth)"
    ok=1
  fi

  # Proxy
  local proxy_status
  proxy_status=$(curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:8787/api/session/create \
    -H 'Content-Type: application/json' -d '{}' 2>/dev/null || echo "000")
  if [[ "${proxy_status}" == "200" ]]; then
    echo "[OK] DSH proxy: auth working (HTTP ${proxy_status})"
  elif [[ "${proxy_status}" == "401" ]]; then
    echo "[FAIL] DSH proxy: auth failed (HTTP ${proxy_status})"
    ok=1
  else
    echo "[WARN] DSH proxy: HTTP ${proxy_status}"
  fi

  echo ""
  echo "Harness config: $(curl -s http://127.0.0.1:8787/v1/config/harnesses 2>/dev/null || echo 'unreachable')"

  return $ok
}

# ---------- status: read-only overview ----------
cmd_status() {
  echo "--- Container ---"
  docker ps -a --filter "name=${DSH_CONTAINER}" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null || echo "(not found)"

  echo ""
  echo "--- Network ---"
  docker inspect "${DSH_CONTAINER}" --format '{{range $k,$v := .NetworkSettings.Networks}}  {{$k}}: {{$v.IPAddress}}{{println}}{{end}}' 2>/dev/null || echo "  (not running)"

  echo ""
  echo "--- Credentials ---"
  if [[ -f "${CRED_FILE}" ]]; then
    ls -la "${CRED_FILE}"
  else
    echo "  Not found. Run: $0 setup-auth"
  fi

  echo ""
  echo "--- Token URL ---"
  docker logs "${DSH_CONTAINER}" 2>&1 | grep -o 'http://[^ ]*token=[^ )]*' | head -1 || echo "  (not available)"
}

case "${1:-help}" in
  setup-auth) cmd_setup_auth ;;
  verify)     cmd_verify ;;
  status)     cmd_status ;;
  help|*)
    echo "Usage: $0 {setup-auth|verify|status}"
    echo ""
    echo "  setup-auth  Sync DSH browser-session credentials to ${CRED_FILE}"
    echo "  verify      Check DSH ↔ meta-harness full connectivity"
    echo "  status      Read-only overview (container, network, credentials)"
    echo ""
    echo "Deploy order:"
    echo "  1. [externally] Start deepseek-harness container"
    echo "  2. $0 setup-auth   # sync credentials"
    echo "  3. ./deploy/docker.sh up       # start meta-harness"
    echo "  4. $0 verify       # check everything"
    ;;
esac
#!/usr/bin/env bash
# ============================================================================
# deploy-meta-harness.sh — 完整部署脚本（含 DeepSeek Harness 联动）
#
# 功能：
#   1. 启动/重建 deepseek-harness 容器（含正确的 --trusted-host 和网络别名）
#   2. 自动登录 DSH web 获取 browser-session 凭证
#   3. 将凭证同步到宿主机 /root/.dsh/.credentials.yaml
#   4. 通过 docker compose 启动 meta-harness + oma-auth
#   5. 验证全链路连通性
#
# 用法：
#   ./deploy/deploy-meta-harness.sh              # 完整部署
#   ./deploy/deploy-meta-harness.sh --restart    # 仅重启 meta-harness
#   ./deploy/deploy-meta-harness.sh --verify     # 仅验证
#
# 依赖：
#   - docker, docker compose
#   - deploy-dsh 镜像（deepseek-harness）
#   - 宿主机 /root/.dsh/ 目录（脚本自动创建）
# ============================================================================

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_DIR="${ROOT_DIR}/deploy"
COMPOSE_FILE="${DEPLOY_DIR}/docker-compose.yml"
CRED_DIR="/root/.dsh"
CRED_FILE="${CRED_DIR}/.credentials.yaml"
DSH_NETWORK="deepseek-harness-network"
DSH_CONTAINER="deepseek-harness"
DSH_IMAGE="deploy-dsh"
DSH_PORT="3080"

# ---------- 工具函数 ----------
log()  { echo "[$(date '+%H:%M:%S')] $*"; }
fail() { log "ERROR: $*"; exit 1; }

# ---------- 启动/重建 deepseek-harness ----------
ensure_dsh() {
  log "=== 检查 deepseek-harness 容器 ==="

  # 确保网络存在
  if ! docker network inspect "${DSH_NETWORK}" &>/dev/null; then
    log "创建 Docker 网络: ${DSH_NETWORK}"
    docker network create "${DSH_NETWORK}"
  fi

  # 确保凭证目录存在
  mkdir -p "${CRED_DIR}"

  # 检查是否已运行且配置正确
  local need_recreate=true
  if docker inspect "${DSH_CONTAINER}" &>/dev/null; then
    local current_trusted
    current_trusted=$(docker inspect "${DSH_CONTAINER}" --format '{{range .Config.Cmd}}{{.}} {{end}}' 2>/dev/null || true)
    # 检查 --trusted-host 是否包含 deepseek-harness
    if echo "${current_trusted}" | grep -q 'trusted-host.*deepseek-harness'; then
      # 检查是否在正确的网络上
      if docker inspect "${DSH_CONTAINER}" --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}' 2>/dev/null | grep -q "${DSH_NETWORK}"; then
        log "deepseek-harness 已存在且配置正确，跳过重建"
        need_recreate=false
      fi
    fi
  fi

  if $need_recreate; then
    log "重建 deepseek-harness 容器..."
    docker stop "${DSH_CONTAINER}" 2>/dev/null || true
    docker rm "${DSH_CONTAINER}" 2>/dev/null || true

    docker run -d \
      --name "${DSH_CONTAINER}" \
      --network "${DSH_NETWORK}" \
      --network-alias "${DSH_CONTAINER}" \
      --network-alias dsh \
      --network-alias oma-deepseek \
      -e DEEPSEEK_API_KEY= \
      -e DEEPSEEK_BASE_URL= \
      -e DSH_HOME=/app/.dsh \
      -e DSH_ALLOW_WILDCARD_HOST=1 \
      -e TZ=UTC \
      -e NODE_ENV=production \
      -e DSH_WEB_PORT="${DSH_PORT}" \
      -p "${DSH_PORT}:${DSH_PORT}" \
      -v /home/ubuntu/.pi/agent:/root/.pi/agent \
      -v "${ROOT_DIR}/data:/data" \
      "${DSH_IMAGE}" \
      node apps/cli/lib/bin.js web \
        --host 0.0.0.0 \
        --port "${DSH_PORT}" \
        --no-open \
        --trusted-host 124.221.28.203 \
        --trusted-host deepseek-harness \
        --trusted-host oma-deepseek \
        --trusted-host 127.0.0.1
  fi

  # 等待健康检查通过
  log "等待 deepseek-harness 就绪..."
  for i in $(seq 1 30); do
    if curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${DSH_PORT}/" 2>/dev/null | grep -qE '^(200|302|303|401)$'; then
      log "deepseek-harness 已就绪"
      return 0
    fi
    sleep 2
  done
  fail "deepseek-harness 启动超时"
}

# ---------- DSH 凭证生成 ----------
ensure_dsh_credentials() {
  log "=== 检查 DSH 凭证 ==="

  # 检查容器内凭证是否已存在
  local container_cred_exists
  container_cred_exists=$(docker exec "${DSH_CONTAINER}" test -f /app/.dsh/.credentials.yaml && echo "yes" || echo "no")

  if [[ "${container_cred_exists}" == "yes" ]]; then
    # 直接同步
    docker cp "${DSH_CONTAINER}:/app/.dsh/.credentials.yaml" "${CRED_FILE}"
    log "从容器同步凭证到 ${CRED_FILE}"
    return 0
  fi

  # 凭证不存在，需要登录生成
  log "凭证不存在，通过 token 登录 DSH web 生成..."

  # 获取 token
  local token
  token=$(docker logs "${DSH_CONTAINER}" 2>&1 | grep -oP 'token=\K[a-zA-Z0-9_-]+' | head -1)
  if [[ -z "${token}" ]]; then
    fail "无法从 DSH 日志提取 token"
  fi

  # 访问 token URL 触发登录
  local http_status
  http_status=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${DSH_PORT}/?token=${token}")
  log "Token 登录返回: HTTP ${http_status}"

  if [[ "${http_status}" != "303" && "${http_status}" != "302" && "${http_status}" != "200" ]]; then
    fail "Token 登录失败: HTTP ${http_status}"
  fi

  # 等待凭证文件生成
  log "等待凭证文件生成..."
  for i in $(seq 1 15); do
    if docker exec "${DSH_CONTAINER}" test -f /app/.dsh/.credentials.yaml 2>/dev/null; then
      docker cp "${DSH_CONTAINER}:/app/.dsh/.credentials.yaml" "${CRED_FILE}"
      log "凭证已生成并同步到 ${CRED_FILE}"
      return 0
    fi
    sleep 2
  done

  fail "凭证文件生成超时"
}

# ---------- 部署 meta-harness ----------
deploy_meta_harness() {
  log "=== 通过 docker compose 部署 meta-harness ==="

  cd "${DEPLOY_DIR}"

  # 确保用最新的 .env（Docker 网络内部地址）
  if ! grep -q 'OMA_DEEPSEEK_GATEWAY_URL=http://deepseek-harness:3080' "${ROOT_DIR}/.env"; then
    log "警告: .env 中 OMA_DEEPSEEK_GATEWAY_URL 不是 http://deepseek-harness:3080"
    log "  当前值: $(grep 'OMA_DEEPSEEK_GATEWAY_URL' "${ROOT_DIR}/.env" || echo '未设置')"
    log "  Docker 部署需要使用 Docker 网络内部地址"
  fi

  docker compose up -d

  log "等待 meta-harness 就绪..."
  sleep 5

  # 检查日志中的 DSH 认证状态
  if docker logs deploy-meta-harness-1 --tail 20 2>&1 | grep -q 'successfully loaded auth cookie'; then
    log "meta-harness DSH 认证 OK"
  else
    log "警告: 未能确认 DSH 认证状态，查看日志:"
    docker logs deploy-meta-harness-1 --tail 10 2>&1 || true
  fi
}

# ---------- 验证 ----------
verify() {
  log "=== 验证部署 ==="

  # 网络验证
  log "网络状态:"
  docker inspect "${DSH_CONTAINER}" --format '  deepseek-harness: {{range $k,$v := .NetworkSettings.Networks}}{{$k}}({{$v.IPAddress}}) {{end}}' 2>/dev/null || echo "  deepseek-harness: 未运行"
  docker inspect deploy-meta-harness-1 --format '  meta-harness:      {{range $k,$v := .NetworkSettings.Networks}}{{$k}}({{$v.IPAddress}}) {{end}}' 2>/dev/null || echo "  meta-harness:      未运行"

  # DNS 验证
  log "DNS 解析:"
  if docker exec deploy-meta-harness-1 getent hosts "${DSH_CONTAINER}" 2>/dev/null; then
    log "  deepseek-harness 可解析 ✓"
  else
    log "  deepseek-harness 不可解析 ✗"
  fi

  # API 验证
  log "API 状态:"
  local harness_cfg
  harness_cfg=$(curl -s http://127.0.0.1:8787/v1/config/harnesses 2>/dev/null || echo '{"error":"unreachable"}')
  log "  GET /v1/config/harnesses: ${harness_cfg}"

  # Proxy 验证
  log "DSH Proxy:"
  local proxy_status
  proxy_status=$(curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:8787/api/session/create \
    -H 'Content-Type: application/json' -d '{}' 2>/dev/null || echo "000")
  if [[ "${proxy_status}" == "200" ]]; then
    log "  POST /api/session/create: ${proxy_status} (认证通过) ✓"
  elif [[ "${proxy_status}" == "401" ]]; then
    log "  POST /api/session/create: ${proxy_status} (认证失败) ✗"
  else
    log "  POST /api/session/create: ${proxy_status}"
  fi
}

# ---------- 主入口 ----------
case "${1:-}" in
  --restart)
    log "仅重启 meta-harness..."
    cd "${DEPLOY_DIR}"
    docker compose restart meta-harness
    sleep 3
    verify
    ;;
  --verify)
    verify
    ;;
  --dsh-only)
    ensure_dsh
    ensure_dsh_credentials
    log "DSH 就绪，跳过 meta-harness 部署"
    ;;
  *)
    ensure_dsh
    ensure_dsh_credentials
    deploy_meta_harness
    verify
    log "=== 部署完成 ==="
    ;;
esac
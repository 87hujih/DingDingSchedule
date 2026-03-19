#!/bin/bash

# Schedule Server 应急一键部署脚本
# 用途：同步部署资产并在服务器上部署指定镜像 tag

set -euo pipefail

SERVER_HOST="${SERVER_HOST:-106.52.42.194}"
SERVER_USER="${SERVER_USER:-root}"
SERVER_PORT="${SERVER_PORT:-22}"
SERVER_DIR="${SERVER_DIR:-/opt/schedule_server}"
CONTAINER_NAME="${CONTAINER_NAME:-schedule-server}"
IMAGE_TAG="${1:-${IMAGE_TAG:-latest}}"
IMAGE_REPO="${IMAGE_REPO:-}"
TOTAL_STEPS=4

if [ -n "${GHCR_USERNAME:-}" ] && [ -n "${GHCR_TOKEN:-}" ]; then
    TOTAL_STEPS=5
fi

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_step() {
    echo ""
    echo -e "${BLUE}=========================================="
    echo -e "  $1"
    echo -e "==========================================${NC}"
    echo ""
}

print_success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

detect_image_repo() {
    if [ -n "${IMAGE_REPO}" ]; then
        return
    fi

    local remote owner
    remote="$(git remote get-url origin 2>/dev/null || true)"
    owner="$(printf '%s' "${remote}" | sed -nE 's#.*github\.com[:/]+([^/]+)/[^/.]+(\.git)?#\1#p' | tr '[:upper:]' '[:lower:]')"

    if [ -n "${owner}" ]; then
        IMAGE_REPO="ghcr.io/${owner}/schedule-server"
        print_warning "未显式设置 IMAGE_REPO，已根据 origin 推断为 ${IMAGE_REPO}"
        return
    fi

    print_error "无法自动推断 IMAGE_REPO，请手动设置 IMAGE_REPO=ghcr.io/<github-owner>/schedule-server"
    exit 1
}

check_local_requirements() {
    local asset

    for asset in ssh scp curl; do
        if ! command -v "${asset}" >/dev/null 2>&1; then
            print_error "缺少本地命令: ${asset}"
            exit 1
        fi
    done

    for asset in deploy.sh docker-compose.prod.yml .env.prod.example; do
        if [ ! -f "${asset}" ]; then
            print_error "缺少部署资产: ${asset}"
            exit 1
        fi
    done
}

check_ssh_connection() {
    print_step "1/${TOTAL_STEPS} 检查 SSH 连接"

    if ssh -p "${SERVER_PORT}" -o BatchMode=yes -o ConnectTimeout=5 "${SERVER_USER}@${SERVER_HOST}" "echo ok" >/dev/null 2>&1; then
        print_success "SSH 连接正常"
        return
    fi

    print_error "SSH 连接失败，请先确认密钥登录或改用 Xshell 手工应急部署"
    exit 1
}

sync_assets() {
    print_step "2/${TOTAL_STEPS} 同步部署资产"

    ssh -p "${SERVER_PORT}" "${SERVER_USER}@${SERVER_HOST}" "mkdir -p '${SERVER_DIR}' '${SERVER_DIR}/configs' '${SERVER_DIR}/logs' '${SERVER_DIR}/uploads'"
    scp -P "${SERVER_PORT}" deploy.sh docker-compose.prod.yml .env.prod.example "${SERVER_USER}@${SERVER_HOST}:${SERVER_DIR}/"

    print_success "部署资产已同步到 ${SERVER_USER}@${SERVER_HOST}:${SERVER_DIR}"
}

login_ghcr_if_needed() {
    if [ -z "${GHCR_USERNAME:-}" ] || [ -z "${GHCR_TOKEN:-}" ]; then
        return
    fi

    print_step "3/${TOTAL_STEPS} 登录 GHCR"
    printf '%s' "${GHCR_TOKEN}" | ssh -p "${SERVER_PORT}" "${SERVER_USER}@${SERVER_HOST}" "docker login ghcr.io -u '${GHCR_USERNAME}' --password-stdin"
    print_success "GHCR 登录完成"
}

remote_deploy() {
    local step_label
    step_label="3/${TOTAL_STEPS} 远程部署镜像"
    if [ "${TOTAL_STEPS}" -eq 5 ]; then
        step_label="4/${TOTAL_STEPS} 远程部署镜像"
    fi

    print_step "${step_label}"

    ssh -p "${SERVER_PORT}" "${SERVER_USER}@${SERVER_HOST}" \
        "set -euo pipefail; \
        cd '${SERVER_DIR}'; \
        if [ ! -f '.env.prod' ]; then cp '.env.prod.example' '.env.prod'; echo '.env.prod 不存在，已用模板初始化，请补全后重试。'; exit 1; fi; \
        if [ ! -f 'configs/prod.yaml' ]; then echo '缺少 configs/prod.yaml，请先补齐生产配置。'; exit 1; fi; \
        chmod +x deploy.sh; \
        IMAGE_REPO='${IMAGE_REPO}' IMAGE_TAG='${IMAGE_TAG}' ./deploy.sh deploy"

    print_success "镜像部署命令执行完成"
}

verify_deployment() {
    local step_label
    step_label="4/${TOTAL_STEPS} 验证部署结果"
    if [ "${TOTAL_STEPS}" -eq 5 ]; then
        step_label="5/${TOTAL_STEPS} 验证部署结果"
    fi

    print_step "${step_label}"

    ssh -p "${SERVER_PORT}" "${SERVER_USER}@${SERVER_HOST}" "docker ps --filter 'name=${CONTAINER_NAME}' --format '{{.Names}} {{.Status}}'"
    ssh -p "${SERVER_PORT}" "${SERVER_USER}@${SERVER_HOST}" "curl -fsS http://localhost:26665/health" >/dev/null

    print_success "健康检查通过"
}

show_result() {
    echo ""
    echo -e "${GREEN}部署完成${NC}"
    echo "服务器: ${SERVER_USER}@${SERVER_HOST}"
    echo "目录: ${SERVER_DIR}"
    echo "镜像: ${IMAGE_REPO}:${IMAGE_TAG}"
    echo ""
    echo "常用命令:"
    echo "  ssh -p ${SERVER_PORT} ${SERVER_USER}@${SERVER_HOST} 'cd ${SERVER_DIR} && ./deploy.sh status'"
    echo "  ssh -p ${SERVER_PORT} ${SERVER_USER}@${SERVER_HOST} 'cd ${SERVER_DIR} && ./deploy.sh logs'"
    echo "  回滚: IMAGE_REPO=${IMAGE_REPO} ./one-click-deploy.sh <old-sha>"
    echo ""
}

main() {
    detect_image_repo
    check_local_requirements
    check_ssh_connection
    sync_assets
    login_ghcr_if_needed
    remote_deploy
    verify_deployment
    show_result
}

trap 'print_error "部署过程中发生错误"; exit 1' ERR

main "$@"

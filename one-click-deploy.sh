#!/bin/bash

# Schedule Server 一键源码部署脚本
# 用途：本地打包源码，上传服务器后在服务器本地构建镜像并启动容器

set -euo pipefail

SERVER_HOST="${SERVER_HOST:-106.52.42.194}"
SERVER_USER="${SERVER_USER:-root}"
SERVER_PORT="${SERVER_PORT:-22}"
SERVER_PARENT_DIR="${SERVER_PARENT_DIR:-/opt}"
SERVER_DIR="${SERVER_DIR:-${SERVER_PARENT_DIR}/schedule_server}"
PACKAGE_NAME="schedule_server_deploy.tar.gz"
PACKAGE_DIR="schedule_server"
CONTAINER_NAME="${CONTAINER_NAME:-schedule-server}"
TOTAL_STEPS=4

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

check_local_requirements() {
    local asset

    for asset in ssh scp tar bash; do
        if ! command -v "${asset}" >/dev/null 2>&1; then
            print_error "缺少本地命令: ${asset}"
            exit 1
        fi
    done

    for asset in pack-for-deploy.sh deploy-legacy.sh Dockerfile; do
        if [ ! -f "${asset}" ]; then
            print_error "缺少部署文件: ${asset}"
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

    print_error "SSH 连接失败，请先确认密钥登录或改用 Xshell 手工部署"
    exit 1
}

build_source_bundle() {
    print_step "2/${TOTAL_STEPS} 打包源码部署包"

    bash ./pack-for-deploy.sh

    if [ ! -f "${PACKAGE_NAME}" ]; then
        print_error "未生成部署包: ${PACKAGE_NAME}"
        exit 1
    fi

    print_success "源码部署包已生成: ${PACKAGE_NAME}"
}

sync_and_deploy() {
    print_step "3/${TOTAL_STEPS} 上传并远程部署"

    ssh -p "${SERVER_PORT}" "${SERVER_USER}@${SERVER_HOST}" "mkdir -p '${SERVER_PARENT_DIR}'"
    scp -P "${SERVER_PORT}" "${PACKAGE_NAME}" "${SERVER_USER}@${SERVER_HOST}:${SERVER_PARENT_DIR}/"

    ssh -p "${SERVER_PORT}" "${SERVER_USER}@${SERVER_HOST}" \
        "set -euo pipefail; \
        cd '${SERVER_PARENT_DIR}'; \
        rm -rf '${PACKAGE_DIR}'; \
        tar -xzf 'schedule_server_deploy.tar.gz'; \
        cd 'schedule_server'; \
        sed -i 's/\r\$//' 'deploy.sh'; \
        chmod +x deploy.sh; \
        mkdir -p logs uploads; \
        ./deploy.sh deploy"

    print_success "远程源码部署命令执行完成"
}

verify_deployment() {
    print_step "4/${TOTAL_STEPS} 验证部署结果"

    ssh -p "${SERVER_PORT}" "${SERVER_USER}@${SERVER_HOST}" "docker ps --filter 'name=${CONTAINER_NAME}' --format '{{.Names}} {{.Status}}'"
    ssh -p "${SERVER_PORT}" "${SERVER_USER}@${SERVER_HOST}" "curl -fsS http://localhost:26665/health" >/dev/null

    print_success "健康检查通过"
}

show_result() {
    echo ""
    echo -e "${GREEN}部署完成${NC}"
    echo "服务器: ${SERVER_USER}@${SERVER_HOST}"
    echo "目录: ${SERVER_DIR}"
    echo "部署方式: 源码包上传后在服务器本地构建"
    echo ""
    echo "常用命令:"
    echo "  ssh -p ${SERVER_PORT} ${SERVER_USER}@${SERVER_HOST} 'cd ${SERVER_DIR} && ./deploy.sh status'"
    echo "  ssh -p ${SERVER_PORT} ${SERVER_USER}@${SERVER_HOST} 'cd ${SERVER_DIR} && ./deploy.sh logs'"
    echo "  回滚: 切回旧代码版本后重新执行 ./one-click-deploy.sh"
    echo ""
}

main() {
    if [ "$#" -gt 0 ]; then
        print_warning "当前已恢复源码部署流程，位置参数将被忽略: $*"
    fi

    check_local_requirements
    check_ssh_connection
    build_source_bundle
    sync_and_deploy
    verify_deployment
    show_result
}

trap 'print_error "部署过程中发生错误"; exit 1' ERR

main "$@"

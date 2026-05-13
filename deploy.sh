#!/bin/bash

# Schedule Server 部署脚本
# 用途：自动化部署和管理生产容器；镜像由 CI 构建并发布

set -euo pipefail

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 配置变量
CONTAINER_NAME="${CONTAINER_NAME:-schedule-server}"
COMPOSE_FILE="${DEPLOY_COMPOSE_FILE:-docker-compose.prod.yml}"
ENV_FILE="${DEPLOY_ENV_FILE:-.env.prod}"
LOCAL_IMAGE_TAG="${LOCAL_IMAGE_TAG:-schedule-server:local}"
IMAGE_PULL_RETRIES="${IMAGE_PULL_RETRIES:-3}"
IMAGE_PULL_RETRY_DELAY_SECONDS="${IMAGE_PULL_RETRY_DELAY_SECONDS:-15}"

# 打印带颜色的消息
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查 Docker 是否安装
check_docker() {
    if ! command -v docker &> /dev/null; then
        log_error "Docker 未安装，请先安装 Docker"
        exit 1
    fi
    log_info "Docker 版本: $(docker --version)"
}

# 检查 docker compose 是否可用
check_compose() {
    if ! docker compose version >/dev/null 2>&1; then
        log_error "Docker Compose 插件不可用，请先安装 docker compose"
        exit 1
    fi
}

# 检查部署文件
check_deploy_files() {
    if [ ! -f "${COMPOSE_FILE}" ]; then
        log_error "未找到部署文件: ${COMPOSE_FILE}"
        exit 1
    fi

    if [ ! -f "${ENV_FILE}" ]; then
        log_error "未找到环境文件: ${ENV_FILE}"
        if [ -f ".env.prod.example" ]; then
            log_warn "请先基于 .env.prod.example 创建 ${ENV_FILE}"
        fi
        exit 1
    fi

    load_env_paths

    if [ ! -f "${CONFIG_DIR:-./configs}/prod.yaml" ]; then
        log_error "未找到生产配置: ${CONFIG_DIR:-./configs}/prod.yaml"
        log_warn "请先将生产配置放到 ${CONFIG_DIR:-./configs}/prod.yaml"
        exit 1
    fi
}

load_env_paths() {
    local key value

    while IFS='=' read -r key value; do
        case "${key}" in
            ""|\#*)
                continue
                ;;
        esac

        value="${value%$'\r'}"
        value="${value%\"}"
        value="${value#\"}"

        case "${key}" in
            CONFIG_DIR)
                if [ -z "${CONFIG_DIR:-}" ]; then
                    CONFIG_DIR="${value}"
                fi
                ;;
            LOG_DIR)
                if [ -z "${LOG_DIR:-}" ]; then
                    LOG_DIR="${value}"
                fi
                ;;
            UPLOAD_DIR)
                if [ -z "${UPLOAD_DIR:-}" ]; then
                    UPLOAD_DIR="${value}"
                fi
                ;;
        esac
    done < "${ENV_FILE}"
}

compose() {
    if [ -n "${IMAGE_REPO:-}" ]; then
        export IMAGE_REPO
    fi
    if [ -n "${IMAGE_TAG:-}" ]; then
        export IMAGE_TAG
    fi
    docker compose -f "${COMPOSE_FILE}" --env-file "${ENV_FILE}" "$@"
}

resolve_image_ref() {
    if [ -z "${IMAGE_REPO:-}" ] || [ -z "${IMAGE_TAG:-}" ]; then
        return 1
    fi

    printf '%s:%s' "${IMAGE_REPO}" "${IMAGE_TAG}"
}

should_skip_image_pull() {
    case "${SKIP_IMAGE_PULL:-}" in
        1|true|TRUE|yes|YES)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

ensure_local_image_available() {
    local image_ref

    if ! image_ref="$(resolve_image_ref)"; then
        log_error "SKIP_IMAGE_PULL 已开启，但 IMAGE_REPO 或 IMAGE_TAG 未提供。"
        return 1
    fi

    if ! docker image inspect "${image_ref}" >/dev/null 2>&1; then
        log_error "SKIP_IMAGE_PULL 已开启，但本地未找到镜像 ${image_ref}。"
        return 1
    fi

    log_info "检测到本地镜像 ${image_ref}，跳过远端镜像拉取。"
}

retry_compose_pull() {
    local attempt=1

    while [ "${attempt}" -le "${IMAGE_PULL_RETRIES}" ]; do
        if compose pull; then
            return 0
        fi

        if [ "${attempt}" -lt "${IMAGE_PULL_RETRIES}" ]; then
            log_warn "拉取镜像失败（第 ${attempt}/${IMAGE_PULL_RETRIES} 次），${IMAGE_PULL_RETRY_DELAY_SECONDS} 秒后重试..."
            sleep "${IMAGE_PULL_RETRY_DELAY_SECONDS}"
        fi

        attempt=$((attempt + 1))
    done

    log_error "镜像拉取连续失败，请检查服务器到 ghcr.io 的网络连通性、GHCR 凭据和目标镜像标签后重试。"
    return 1
}

remove_conflicting_container() {
    if docker inspect "${CONTAINER_NAME}" >/dev/null 2>&1; then
        log_warn "发现已有同名容器 ${CONTAINER_NAME}，准备清理后再由 compose 接管..."
        docker stop "${CONTAINER_NAME}" || true
        docker rm "${CONTAINER_NAME}" || true
    fi
}

MONITOR_PORTS="9090 3000 9093 8065"

# 检查端口是否被占用
port_in_use() {
    local port="$1"
    if command -v fuser >/dev/null 2>&1; then
        fuser "${port}/tcp" >/dev/null 2>&1
    elif command -v ss >/dev/null 2>&1; then
        ss -tln "sport = :${port}" 2>/dev/null | grep -q ":${port}"
    elif command -v netstat >/dev/null 2>&1; then
        netstat -tlnp 2>/dev/null | grep -q ":${port} "
    else
        return 1
    fi
}

# 杀掉占用指定端口的进程
kill_port_process() {
    local port="$1" pid=""
    if command -v fuser >/dev/null 2>&1; then
        fuser -k "${port}/tcp" 2>/dev/null || true
    elif command -v ss >/dev/null 2>&1; then
        pid="$(ss -tlnp "sport = :${port}" 2>/dev/null | grep -oP 'pid=\K[0-9]+' | head -1 || true)"
        if [ -n "${pid}" ] && [ "${pid}" -ne 1 ]; then
            kill "${pid}" 2>/dev/null || true
        fi
    fi
}

# 释放监控栈端口（不影响 API 端口 26665）
free_monitor_ports() {
    local port cid

    for port in ${MONITOR_PORTS}; do
        if port_in_use "${port}"; then
            log_warn "端口 ${port} 被占用，尝试释放..."

            cid="$(docker ps --filter "publish=${port}" --format '{{.ID}}' | head -1 || true)"
            if [ -n "${cid}" ]; then
                docker stop "${cid}" && docker rm "${cid}" || true
            fi

            kill_port_process "${port}"
            sleep 1

            if port_in_use "${port}"; then
                log_error "端口 ${port} 释放失败，可能需要手动处理"
            fi
        fi
    done
}

# 构建本地调试镜像
build_local_image() {
    log_info "开始构建本地调试镜像..."
    docker build -t "${LOCAL_IMAGE_TAG}" .
    log_info "镜像构建完成: ${LOCAL_IMAGE_TAG}"
}

# 拉取并启动生产镜像
deploy_stack() {
    mkdir -p "${LOG_DIR:-./logs}" "${UPLOAD_DIR:-./uploads}" "${CONFIG_DIR:-./configs}"

    if should_skip_image_pull; then
        ensure_local_image_available
    else
        log_info "开始拉取镜像..."
        retry_compose_pull
    fi

    remove_conflicting_container
    log_info "清理监控栈..."
    compose stop prometheus grafana alertmanager webhook-dingtalk 2>/dev/null || true
    compose rm -f prometheus grafana alertmanager webhook-dingtalk 2>/dev/null || true
    free_monitor_ports
    log_info "拉取监控栈镜像..."
    compose pull --ignore-pull-failures || true
    log_info "启动所有容器..."
    if ! compose up -d; then
        log_error "compose up 失败，尝试恢复 API 服务..."
        compose up -d schedule-server || true
        exit 1
    fi

    if ! curl -fsS --max-time 3 http://localhost:${HOST_PORT:-26665}/health >/dev/null 2>&1; then
        log_warn "API 健康检查未通过，尝试重启 API 服务..."
        compose restart schedule-server || true
    fi
}

# 查看容器状态
check_status() {
    log_info "容器状态:"
    compose ps

    log_info "\n最近日志:"
    docker logs --tail 50 "${CONTAINER_NAME}" 2>/dev/null || true
}

# 清理旧镜像
cleanup_images() {
    log_info "清理未使用的镜像..."
    docker image prune -f
}

# 主函数
main() {
    case "${1:-deploy}" in
        build)
            check_docker
            build_local_image
            ;;
        deploy)
            check_docker
            check_compose
            check_deploy_files
            deploy_stack
            sleep 5
            check_status
            cleanup_images
            log_info "部署完成！访问地址: http://localhost:26665"
            ;;
        restart)
            check_docker
            check_compose
            check_deploy_files
            compose restart
            log_info "容器已重启"
            ;;
        stop)
            check_docker
            check_compose
            check_deploy_files
            compose stop
            log_info "容器已停止"
            ;;
        start)
            check_docker
            check_compose
            check_deploy_files
            compose up -d
            log_info "容器已启动"
            ;;
        logs)
            check_docker
            check_compose
            check_deploy_files
            compose logs -f "${CONTAINER_NAME}"
            ;;
        status)
            check_docker
            check_compose
            check_deploy_files
            check_status
            ;;
        config)
            check_docker
            check_compose
            check_deploy_files
            compose config
            ;;
        clean)
            check_docker
            check_compose
            check_deploy_files
            compose down --remove-orphans || true
            docker image rm "${LOCAL_IMAGE_TAG}" || true
            log_info "清理完成"
            ;;
        *)
            echo "用法: $0 {build|deploy|restart|stop|start|logs|status|config|clean}"
            echo ""
            echo "命令说明:"
            echo "  build   - 构建本地调试镜像"
            echo "  deploy  - 拉取镜像并按生产 compose 启动"
            echo "  restart - 重启容器"
            echo "  stop    - 停止容器"
            echo "  start   - 启动容器"
            echo "  logs    - 查看实时日志"
            echo "  status  - 查看容器状态"
            echo "  config  - 展开并检查生产 compose 配置"
            echo "  clean   - 清理容器和镜像"
            exit 1
            ;;
    esac
}

main "$@"

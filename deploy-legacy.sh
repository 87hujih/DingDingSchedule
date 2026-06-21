#!/bin/bash

# Schedule Server 旧版源码部署脚本
# 用途：在服务器本地构建镜像并直接启动容器，不依赖 compose 插件

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

IMAGE_NAME="schedule-server"
CONTAINER_NAME="schedule-server"
VERSION=$(date +%Y%m%d-%H%M%S)

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

check_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        log_error "Docker 未安装，请先安装 Docker"
        exit 1
    fi
    log_info "Docker 版本: $(docker --version)"
}

prepare_runtime_dirs() {
    mkdir -p configs logs uploads
}

build_image() {
    log_info "开始构建 Docker 镜像..."
    docker build -t ${IMAGE_NAME}:${VERSION} -t ${IMAGE_NAME}:latest .
    log_info "镜像构建完成: ${IMAGE_NAME}:${VERSION}"
}

stop_old_container() {
    if [ "$(docker ps -aq -f name=${CONTAINER_NAME})" ]; then
        log_warn "停止旧容器..."
        docker stop ${CONTAINER_NAME} || true
        docker rm ${CONTAINER_NAME} || true
        log_info "旧容器已删除"
    fi
}

start_container() {
    log_info "启动新容器..."
    docker run -d \
        --name ${CONTAINER_NAME} \
        --restart unless-stopped \
        -p 26665:26665 \
        -e CONFIG_ENV=prod \
        -e CONFIG_PATH=/app/configs \
        -e TZ=Asia/Shanghai \
        -v $(pwd)/configs:/app/configs:ro \
        -v $(pwd)/logs:/app/logs \
        -v $(pwd)/uploads:/app/uploads \
        ${IMAGE_NAME}:latest

    log_info "容器启动成功"
}

check_status() {
    log_info "容器状态:"
    docker ps -f name=${CONTAINER_NAME}

    log_info "\n最近日志:"
    docker logs --tail 50 ${CONTAINER_NAME} 2>/dev/null || true
}

cleanup_images() {
    log_info "清理未使用的镜像..."
    docker image prune -f
}

main() {
    case "${1:-deploy}" in
        build)
            check_docker
            prepare_runtime_dirs
            build_image
            ;;
        deploy)
            check_docker
            prepare_runtime_dirs
            build_image
            stop_old_container
            start_container
            sleep 3
            check_status
            cleanup_images
            log_info "部署完成！访问地址: http://localhost:26665"
            ;;
        restart)
            docker restart ${CONTAINER_NAME}
            log_info "容器已重启"
            ;;
        stop)
            docker stop ${CONTAINER_NAME}
            log_info "容器已停止"
            ;;
        start)
            docker start ${CONTAINER_NAME}
            log_info "容器已启动"
            ;;
        logs)
            docker logs -f ${CONTAINER_NAME}
            ;;
        status)
            check_status
            ;;
        clean)
            stop_old_container
            docker rmi ${IMAGE_NAME}:latest || true
            log_info "清理完成"
            ;;
        *)
            echo "用法: $0 {build|deploy|restart|stop|start|logs|status|clean}"
            echo ""
            echo "命令说明:"
            echo "  build   - 仅构建镜像"
            echo "  deploy  - 完整部署（构建+停止旧容器+启动新容器）"
            echo "  restart - 重启容器"
            echo "  stop    - 停止容器"
            echo "  start   - 启动容器"
            echo "  logs    - 查看实时日志"
            echo "  status  - 查看容器状态"
            echo "  clean   - 清理容器和镜像"
            exit 1
            ;;
    esac
}

main "$@"

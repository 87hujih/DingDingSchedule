#!/bin/bash

# Schedule Server 部署脚本
# 用途：自动化构建、部署和管理 Docker 容器

set -e  # 遇到错误立即退出

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 配置变量
IMAGE_NAME="schedule-server"
CONTAINER_NAME="schedule-server"
VERSION=$(date +%Y%m%d-%H%M%S)

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

# 构建镜像
build_image() {
    log_info "开始构建 Docker 镜像..."
    docker build -t ${IMAGE_NAME}:${VERSION} -t ${IMAGE_NAME}:latest .
    log_info "镜像构建完成: ${IMAGE_NAME}:${VERSION}"
}

# 停止并删除旧容器
stop_old_container() {
    if [ "$(docker ps -aq -f name=${CONTAINER_NAME})" ]; then
        log_warn "停止旧容器..."
        docker stop ${CONTAINER_NAME} || true
        docker rm ${CONTAINER_NAME} || true
        log_info "旧容器已删除"
    fi
}

# 启动新容器
start_container() {
    log_info "启动新容器..."
    docker run -d \
        --name ${CONTAINER_NAME} \
        --restart unless-stopped \
        -p 26665:26665 \
        -e ENV=prod \
        -v $(pwd)/logs:/app/logs \
        -v $(pwd)/uploads:/app/uploads \
        ${IMAGE_NAME}:latest

    log_info "容器启动成功"
}

# 查看容器状态
check_status() {
    log_info "容器状态:"
    docker ps -f name=${CONTAINER_NAME}

    log_info "\n最近日志:"
    docker logs --tail 50 ${CONTAINER_NAME}
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
            build_image
            ;;
        deploy)
            check_docker
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

#!/bin/bash

# 打包部署文件脚本
# 只打包部署所需的文件，排除开发文件和敏感信息

set -e

PACK_NAME="schedule_server_deploy.tar.gz"

echo "开始打包部署文件..."

# 创建临时目录
TEMP_DIR=$(mktemp -d)
PROJECT_DIR="$TEMP_DIR/schedule_server"
mkdir -p "$PROJECT_DIR"

# 复制必要的文件和目录
echo "复制源代码..."
cp -r cmd internal pkg inits global config "$PROJECT_DIR/"

# 复制配置文件（只复制生产配置）
echo "复制配置文件..."
mkdir -p "$PROJECT_DIR/configs"
cp configs/prod.yaml "$PROJECT_DIR/configs/"

# 复制 Go 模块文件
echo "复制 Go 模块文件..."
cp go.mod go.sum "$PROJECT_DIR/"

# 复制部署文件
echo "复制部署文件..."
cp Dockerfile docker-compose.yml deploy.sh "$PROJECT_DIR/"
cp .dockerignore "$PROJECT_DIR/"

# 复制文档
echo "复制文档..."
cp DEPLOYMENT.md QUICKSTART.md DEPLOYMENT_CHECKLIST.md "$PROJECT_DIR/" 2>/dev/null || true

# 创建必要的目录
mkdir -p "$PROJECT_DIR/logs"
mkdir -p "$PROJECT_DIR/uploads"

# 打包
echo "打包中..."
cd "$TEMP_DIR"
tar -czf "$PACK_NAME" schedule_server/

# 移动到原目录
mv "$PACK_NAME" "$OLDPWD/"

# 清理临时目录
rm -rf "$TEMP_DIR"

echo "打包完成: $PACK_NAME"
echo "文件大小: $(du -h $PACK_NAME | cut -f1)"

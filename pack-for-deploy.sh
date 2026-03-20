#!/bin/bash

# 打包部署文件脚本
# 用途：重新生成旧版源码部署包，供 one-click-deploy.sh 上传到服务器后本地构建

set -euo pipefail

PACK_NAME="schedule_server_deploy.tar.gz"

echo "开始打包部署文件..."

TEMP_DIR=$(mktemp -d)
PROJECT_DIR="$TEMP_DIR/schedule_server"
mkdir -p "$PROJECT_DIR"

echo "复制源代码..."
cp -r cmd internal pkg inits global config "$PROJECT_DIR/"

echo "复制配置文件..."
mkdir -p "$PROJECT_DIR/configs"
cp configs/prod.yaml "$PROJECT_DIR/configs/"

echo "复制 Go 模块文件..."
cp go.mod go.sum "$PROJECT_DIR/"

echo "复制部署文件..."
cp Dockerfile docker-compose.yml "$PROJECT_DIR/"
cp deploy-legacy.sh "$PROJECT_DIR/deploy.sh"
cp .dockerignore "$PROJECT_DIR/"

echo "创建运行目录..."
mkdir -p "$PROJECT_DIR/logs"
mkdir -p "$PROJECT_DIR/uploads"

echo "打包中..."
cd "$TEMP_DIR"
tar -czf "$PACK_NAME" schedule_server/

mv "$PACK_NAME" "$OLDPWD/"
rm -rf "$TEMP_DIR"

echo "打包完成: $PACK_NAME"
echo "文件大小: $(du -h "$OLDPWD/$PACK_NAME" | cut -f1)"

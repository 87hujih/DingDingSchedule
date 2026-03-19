#!/bin/bash

# 打包部署资产脚本
# 用途：生成仅包含生产部署资产的应急压缩包，不再打包应用源码

set -euo pipefail

PACK_NAME="${PACK_NAME:-schedule_server_ops_bundle.tar.gz}"
ROOT_DIR="$(pwd)"
TEMP_DIR="$(mktemp -d)"
BUNDLE_DIR="${TEMP_DIR}/schedule_server_ops_bundle"

mkdir -p "${BUNDLE_DIR}/docs/deployment"

copy_if_exists() {
    if [ -f "$1" ]; then
        cp "$1" "$2"
    fi
}

echo "开始打包部署资产..."

copy_if_exists "deploy.sh" "${BUNDLE_DIR}/deploy.sh"
copy_if_exists "docker-compose.prod.yml" "${BUNDLE_DIR}/docker-compose.prod.yml"
copy_if_exists ".env.prod.example" "${BUNDLE_DIR}/.env.prod.example"
copy_if_exists "ONE_CLICK_DEPLOY_GUIDE.md" "${BUNDLE_DIR}/ONE_CLICK_DEPLOY_GUIDE.md"
copy_if_exists "XSHELL_DEPLOY_COMPLETE_GUIDE.md" "${BUNDLE_DIR}/XSHELL_DEPLOY_COMPLETE_GUIDE.md"
copy_if_exists "docs/deployment/production.md" "${BUNDLE_DIR}/docs/deployment/production.md"
copy_if_exists "docs/deployment/emergency.md" "${BUNDLE_DIR}/docs/deployment/emergency.md"

cat > "${BUNDLE_DIR}/README.md" <<'EOF'
# Schedule Server 部署资产包

本压缩包只包含生产部署与回滚所需的运维资产：

- deploy.sh
- docker-compose.prod.yml
- .env.prod.example
- 部署说明文档

以下文件不会被打包，必须在服务器上单独准备：

- /opt/schedule_server/.env.prod
- /opt/schedule_server/configs/prod.yaml

日常正式发布请优先使用 GitHub Actions；本压缩包仅用于手工应急部署。
EOF

tar -czf "${TEMP_DIR}/${PACK_NAME}" -C "${TEMP_DIR}" "schedule_server_ops_bundle"
mv "${TEMP_DIR}/${PACK_NAME}" "${ROOT_DIR}/"
rm -rf "${TEMP_DIR}"

echo "打包完成: ${PACK_NAME}"
echo "说明: 当前资产包不再包含应用源码，只用于应急同步部署文件。"

# 使用 Xshell + Xftp 的应急部署流程

## 定位

本文档仅用于“手工应急部署”或“手工回滚”。

正式生产发布流程已经切换为 GitHub Actions + GHCR 镜像部署，日常上线请优先参考：

- `docs/deployment/production.md`

当 GitHub Actions 不可用，或者你需要在服务器上手工部署指定镜像 tag 时，再使用本文件。

## 准备工作

### 本地工具

- Xshell：用于 SSH 登录服务器。
- Xftp：用于上传部署资产。
- Git Bash：用于执行 `./pack-for-deploy.sh`（可选）。

### 服务器要求

服务器需要满足：

- 已安装 Docker 和 `docker compose`。
- 已存在 `/opt/schedule_server/.env.prod`。
- 已存在 `/opt/schedule_server/configs/prod.yaml`。
- 若 GHCR 镜像为私有，服务器已能执行 `docker login ghcr.io`。

## 第一步：准备部署资产

推荐在仓库根目录执行：

```bash
./pack-for-deploy.sh
```

脚本会生成：

```text
schedule_server_ops_bundle.tar.gz
```

该压缩包只包含部署所需资产，不再包含应用源码。

## 第二步：通过 Xftp 上传

1. 连接服务器。
2. 进入 `/opt/schedule_server/`。
3. 上传 `schedule_server_ops_bundle.tar.gz`。

如果你不想使用压缩包，也可以直接上传以下文件：

- `deploy.sh`
- `docker-compose.prod.yml`
- `.env.prod.example`

## 第三步：通过 Xshell 解压并更新部署资产

登录服务器后执行：

```bash
cd /opt/schedule_server
tar -xzf schedule_server_ops_bundle.tar.gz
cp -f schedule_server_ops_bundle/deploy.sh .
cp -f schedule_server_ops_bundle/docker-compose.prod.yml .
cp -n schedule_server_ops_bundle/.env.prod.example .env.prod
chmod +x deploy.sh
mkdir -p configs logs uploads
```

然后确认生产配置仍然存在：

```bash
ls /opt/schedule_server/configs/prod.yaml
```

## 第四步：部署指定镜像版本

推荐使用 commit SHA 作为镜像版本号：

```bash
cd /opt/schedule_server
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server IMAGE_TAG=<commit-sha> ./deploy.sh deploy
```

如果只是临时使用最新镜像：

```bash
cd /opt/schedule_server
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server IMAGE_TAG=latest ./deploy.sh deploy
```

## 第五步：验证

```bash
cd /opt/schedule_server
./deploy.sh status
curl -fsS http://localhost:26665/health
```

如果需要查看更详细日志：

```bash
cd /opt/schedule_server
./deploy.sh logs
```

## 手工回滚

回滚方式与部署一致，只是把 `IMAGE_TAG` 换成旧版本：

```bash
cd /opt/schedule_server
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server IMAGE_TAG=<old-sha> ./deploy.sh deploy
```

## 常见问题

### `.env.prod` 不存在

先初始化模板：

```bash
cd /opt/schedule_server
cp .env.prod.example .env.prod
```

然后补全真实生产值。

### `configs/prod.yaml` 不存在

`deploy.sh` 现在会在部署前直接校验该文件。缺失时请先补齐：

```text
/opt/schedule_server/configs/prod.yaml
```

### 拉镜像权限不足

在服务器上先执行：

```bash
echo "<token>" | docker login ghcr.io -u "<username>" --password-stdin
```

再重新运行部署命令。

## 相关文档

- 正式发布流程：`docs/deployment/production.md`
- 一键应急脚本：`ONE_CLICK_DEPLOY_GUIDE.md`
- 应急与回滚说明：`docs/deployment/emergency.md`

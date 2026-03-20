# 一键部署使用指南

## 定位

本文件现在只用于说明本地应急部署脚本 `one-click-deploy.sh` 的使用方式。

正式生产发布流程已经调整为：

1. Pull Request / 普通 push 先经过 `.github/workflows/ci.yml`。
2. 合并到 `master` 或手动触发 `.github/workflows/deploy.yml`。
3. GitHub Actions 构建并推送 GHCR 镜像。
4. 生产机只拉取镜像并通过 `docker compose` 启动。

如果只是日常上线，请优先使用 GitHub Actions，不要再走“本地打包源码上传到服务器再构建”的旧流程。

当前 GitHub Actions 部署支持：

- 优先使用 `SERVER_SSH_KEY`
- 未配置私钥时退回使用 `SERVER_PASSWORD`
- 如果两者都缺失，workflow 会在远程步骤前直接失败

## 适用场景

以下情况才建议使用 `one-click-deploy.sh`：

- GitHub Actions 临时不可用。
- 需要手工回滚到某个历史镜像 tag。
- 需要从本地快速补发部署资产到服务器。

## 前置条件

- 本地已安装 Git Bash，并可用 `ssh` / `scp` / `curl`。
- 已能通过 SSH 访问服务器。
- 服务器已安装 Docker 和 `docker compose`。
- 服务器已准备 `/opt/schedule_server/.env.prod`。
- 服务器已准备 `/opt/schedule_server/configs/prod.yaml`。
- 如果 GHCR 镜像为私有，服务器已登录 GHCR，或本地设置了 `GHCR_USERNAME` 与 `GHCR_TOKEN`。

## 推荐用法

```bash
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server ./one-click-deploy.sh <commit-sha>
```

说明：

- `<commit-sha>` 建议使用 GitHub Actions 中对应发布版本的提交 SHA。
- 如果省略参数，脚本默认部署 `latest`。
- 如果未显式设置 `IMAGE_REPO`，脚本会尝试根据 `git remote origin` 自动推断。

## 脚本会做什么

`one-click-deploy.sh` 会自动完成以下步骤：

1. 检查 SSH 连通性。
2. 把 `deploy.sh`、`docker-compose.prod.yml`、`.env.prod.example` 上传到 `/opt/schedule_server`。
3. 如提供 GHCR 凭证，则在服务器执行 `docker login ghcr.io`。
4. 在服务器执行：

```bash
IMAGE_REPO=<repo> IMAGE_TAG=<tag> ./deploy.sh deploy
```

5. 校验服务器本地健康检查 `http://localhost:26665/health`。

## 回滚

回滚时重新执行脚本并传入旧镜像 tag：

```bash
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server ./one-click-deploy.sh <old-sha>
```

## 常见问题

### 0. GitHub Actions 报 SSH 凭据缺失

正式部署 workflow 现在要求以下 secret：

- `SERVER_HOST`
- `SERVER_USER`
- `SERVER_SSH_KEY` 或 `SERVER_PASSWORD`

可选：

- `SERVER_SSH_PASSPHRASE`

如果看到以下报错：

```text
Either SERVER_SSH_KEY or SERVER_PASSWORD must be configured
```

说明仓库侧还没有配置可用的远程认证信息。

### 1. `.env.prod` 不存在

脚本会自动把 `.env.prod.example` 复制为 `.env.prod`，然后终止部署。你需要登录服务器补全真实生产值后再重新执行。

### 2. `configs/prod.yaml` 不存在

`deploy.sh` 会在启动前直接校验：

```text
/opt/schedule_server/configs/prod.yaml
```

缺失时需要先补齐生产配置，再重新部署。

### 3. GHCR 拉镜像失败

如果镜像为私有，请先确保服务器可登录 GHCR。也可以在本地临时设置：

```bash
export GHCR_USERNAME=<username>
export GHCR_TOKEN=<token>
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server ./one-click-deploy.sh <commit-sha>
```

### 4. 健康检查失败

先登录服务器执行：

```bash
cd /opt/schedule_server
./deploy.sh status
./deploy.sh logs
```

## 相关文档

- 正式发布流程：`docs/deployment/production.md`
- 应急与回滚：`docs/deployment/emergency.md`
- Xshell / Xftp 手工流程：`XSHELL_DEPLOY_COMPLETE_GUIDE.md`

# 生产部署说明

## 目标流程

当前仓库的正式生产部署流程统一为：

1. 开发分支或 Pull Request 先通过 `.github/workflows/ci.yml`。
2. 合并到 `master` 或手动触发 `.github/workflows/deploy.yml`。
3. GitHub Actions 构建镜像并推送到 `ghcr.io/<github-owner>/schedule-server`。
4. Workflow 通过 SSH 将 `deploy.sh`、`docker-compose.prod.yml`、`.env.prod.example` 同步到 `/opt/schedule_server`。
5. 服务器只执行 `docker compose pull` 和 `docker compose up -d`，不再现场重建源码。

`ONE_CLICK_DEPLOY_GUIDE.md` 与 `XSHELL_DEPLOY_COMPLETE_GUIDE.md` 仅保留为应急手工方案，不再作为日常正式发布流程。

## 仓库侧要求

需要配置以下 GitHub Secrets：

- `SERVER_HOST`: 生产服务器地址。
- `SERVER_USER`: 生产服务器 SSH 用户。
- `SERVER_SSH_KEY`: 用于部署的私钥。
- `GHCR_USERNAME`: 可选；当 GHCR 包为私有时使用。
- `GHCR_TOKEN`: 可选；当 GHCR 包为私有时使用。

默认会使用 GitHub 自带的 `GITHUB_TOKEN` 将镜像推送到 GHCR。

## 服务器初始化

生产机需要预先具备以下条件：

- 已安装 Docker 和 `docker compose` 插件。
- 已创建目录 `/opt/schedule_server`。
- 已准备 `/opt/schedule_server/.env.prod`。
- 已准备 `/opt/schedule_server/configs/prod.yaml`。
- 如果镜像仓库为私有，服务器已能登录 GHCR，或者在 workflow 中提供了 `GHCR_USERNAME` / `GHCR_TOKEN`。

推荐目录结构：

```text
/opt/schedule_server
├── .env.prod
├── .env.prod.example
├── deploy.sh
├── docker-compose.prod.yml
├── configs/
│   └── prod.yaml
├── logs/
└── uploads/
```

可以先用仓库里的 `.env.prod.example` 初始化：

```bash
cd /opt/schedule_server
cp .env.prod.example .env.prod
```

然后补全 `.env.prod` 中的生产值，并将真实生产配置写入 `configs/prod.yaml`。

## 发布方式

### 日常发布

日常发布只建议使用两种入口：

- 向 `master` 推送已通过 CI 的提交。
- 在 GitHub Actions 页面手动触发 `Deploy to Server`。

部署成功后，工作流会对 `http://<SERVER_HOST>:26665/health` 执行健康检查。

### 指定版本发布

`deploy.yml` 会把镜像同时打上两类 tag：

- `${GITHUB_SHA}`
- `latest`

如果需要明确版本，优先使用 commit SHA 作为部署和回滚的版本号。

## 服务器侧常用命令

```bash
cd /opt/schedule_server
docker compose -f docker-compose.prod.yml --env-file .env.prod ps
docker compose -f docker-compose.prod.yml --env-file .env.prod logs -f schedule-server
./deploy.sh status
./deploy.sh config
```

## 回滚

推荐回滚方式是重新部署上一版镜像 tag：

```bash
cd /opt/schedule_server
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server IMAGE_TAG=<old-sha> ./deploy.sh deploy
```

如果需要从本地机器执行，也可以使用：

```bash
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server ./one-click-deploy.sh <old-sha>
```

## 验收清单

每次生产发布至少确认以下事项：

- GitHub Actions 中 `CI` 与 `Deploy to Server` 结果为成功。
- 服务器上 `docker compose ... ps` 显示容器处于运行状态。
- `http://<SERVER_HOST>:26665/health` 返回成功。
- 关键业务接口或管理后台能完成最小人工验证。

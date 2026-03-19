# 应急部署与回滚说明

## 适用场景

以下情况才建议使用本文件中的应急流程：

- GitHub Actions 无法访问服务器。
- 需要手工回滚到某个历史镜像 tag。
- 需要在服务器上快速补发 `deploy.sh` 或 `docker-compose.prod.yml`。

标准发布流程仍然以 [生产部署说明](production.md) 为准。

## 方案 A：本地一键应急部署

仓库根目录的 `one-click-deploy.sh` 会执行以下动作：

1. 检查 SSH 连通性。
2. 同步 `deploy.sh`、`docker-compose.prod.yml`、`.env.prod.example` 到 `/opt/schedule_server`。
3. 可选执行 GHCR 登录。
4. 远程执行 `IMAGE_REPO=<repo> IMAGE_TAG=<tag> ./deploy.sh deploy`。
5. 验证服务器本地健康检查。

推荐命令：

```bash
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server ./one-click-deploy.sh <commit-sha>
```

如果省略 `<commit-sha>`，脚本会默认部署 `latest`。

### 可用环境变量

- `SERVER_HOST`: 默认 `106.52.42.194`
- `SERVER_USER`: 默认 `root`
- `SERVER_PORT`: 默认 `22`
- `SERVER_DIR`: 默认 `/opt/schedule_server`
- `IMAGE_REPO`: 默认尝试从 `git remote origin` 推断
- `IMAGE_TAG`: 默认 `latest`
- `GHCR_USERNAME`: 可选
- `GHCR_TOKEN`: 可选

## 方案 B：Xshell / Xftp 手工部署

如果本地脚本不可用，可以手工执行：

1. 在本地运行 `./pack-for-deploy.sh`，生成仅包含部署资产的 `schedule_server_ops_bundle.tar.gz`。
2. 使用 Xftp 将压缩包上传到 `/opt/schedule_server/`。
3. 使用 Xshell 登录服务器并执行：

```bash
cd /opt/schedule_server
tar -xzf schedule_server_ops_bundle.tar.gz
cp -n schedule_server_ops_bundle/.env.prod.example .env.prod
cp -f schedule_server_ops_bundle/deploy.sh .
cp -f schedule_server_ops_bundle/docker-compose.prod.yml .
chmod +x deploy.sh
```

4. 确认 `configs/prod.yaml` 存在：

```bash
ls /opt/schedule_server/configs/prod.yaml
```

5. 拉起指定版本：

```bash
cd /opt/schedule_server
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server IMAGE_TAG=<commit-sha> ./deploy.sh deploy
```

## 回滚

回滚本质上就是重新部署旧镜像 tag：

```bash
cd /opt/schedule_server
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server IMAGE_TAG=<old-sha> ./deploy.sh deploy
```

如果只需要从本地执行，也可以直接：

```bash
IMAGE_REPO=ghcr.io/<github-owner>/schedule-server ./one-click-deploy.sh <old-sha>
```

## 常用排查命令

```bash
cd /opt/schedule_server
./deploy.sh status
./deploy.sh logs
docker compose -f docker-compose.prod.yml --env-file .env.prod ps
curl -fsS http://localhost:26665/health
```

## 常见故障

### `.env.prod` 不存在

首次部署时可以先从模板生成：

```bash
cd /opt/schedule_server
cp .env.prod.example .env.prod
```

生成后必须立刻补全真实生产值。

### `configs/prod.yaml` 不存在

`deploy.sh` 现在会在启动前直接校验这个文件。缺失时，请先把生产配置写到：

```text
/opt/schedule_server/configs/prod.yaml
```

### GHCR 拉镜像失败

如果仓库镜像为私有，请先登录：

```bash
echo "<token>" | docker login ghcr.io -u "<username>" --password-stdin
```

然后重新执行部署命令。

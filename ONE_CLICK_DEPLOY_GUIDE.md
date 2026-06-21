# 一键部署使用指南

## 部署方式说明

本文档恢复的是项目原来的部署方法：

1. 在本地准备 `schedule_server_deploy.tar.gz` 源码部署包。
2. 将部署包上传到服务器。
3. 在服务器解压源码包并进入项目目录。
4. 执行 `./deploy.sh deploy`，由服务器本地构建镜像并直接启动容器。

这套流程不依赖 GitHub Actions，也不依赖 GHCR 镜像仓库，核心就是“本地打包源码，上传服务器后现场构建部署”。

## 前置条件

- 本地已安装 Git Bash，且可用 `tar`、`scp`、`ssh`。
- 服务器已安装 Docker。
- 服务器可开放并访问 `26665` 端口。
- 部署包中已包含生产配置 `configs/prod.yaml`。
- 服务器上有可写的部署目录，例如 `/opt/schedule_server`。

## 第一步：准备源码部署包

原来的部署包应命名为：

```text
schedule_server_deploy.tar.gz
```

包内目录结构应类似：

```text
schedule_server/
├── cmd/
├── internal/
├── pkg/
├── inits/
├── global/
├── config/
├── configs/prod.yaml
├── go.mod
├── go.sum
├── Dockerfile
├── docker-compose.yml
├── deploy.sh
├── .dockerignore
├── logs/
└── uploads/
```

也就是说，部署包里必须带完整应用源码、`Dockerfile`、`deploy.sh` 和生产配置。原来的思路不是上传镜像，而是把这些文件传到服务器后再构建。

如果你当前手里没有旧版 `pack-for-deploy.sh` 产物，也可以按上面的目录结构自行准备这个压缩包；关键是保证服务源码和 `configs/prod.yaml` 一起进入包内。

这里说的 `deploy.sh`，指的是旧源码部署包里随包上传的脚本版本；它会在服务器本地执行 `docker build`。当前仓库根目录里的 `deploy.sh` 已经切换到镜像拉取流程，不属于本文恢复的这套旧部署方法。

## 第二步：上传到服务器

以下命令以 `/opt` 为例：

```bash
scp schedule_server_deploy.tar.gz root@<server-host>:/opt/
```

如果你习惯先上传到别的目录也可以，最终只要能在服务器解压出 `schedule_server/` 目录即可。

## 第三步：在服务器解压

登录服务器后执行：

```bash
cd /opt
rm -rf schedule_server
tar -xzf schedule_server_deploy.tar.gz
cd schedule_server
chmod +x deploy.sh
```

解压后目录中应能看到 `Dockerfile`、`deploy.sh`、`cmd/`、`internal/`、`pkg/` 和 `configs/prod.yaml`。

## 第四步：执行部署

在服务器项目目录执行：

```bash
./deploy.sh deploy
```

`deploy.sh deploy` 原来的行为是：

1. 在服务器本地执行 `docker build` 构建 `schedule-server` 镜像。
2. 停止并删除旧容器 `schedule-server`。
3. 通过 `docker run` 启动新容器。
4. 挂载当前目录下的 `logs/` 与 `uploads/`。
5. 输出容器状态并清理未使用镜像。

## 第五步：验证部署结果

部署完成后可执行：

```bash
docker ps -f name=schedule-server
curl -f http://localhost:26665/health
./deploy.sh status
```

如果需要看实时日志：

```bash
./deploy.sh logs
```

## 回滚方式

原来的回滚方式不是切镜像 tag，而是重新上传旧版本源码部署包，再次执行：

```bash
./deploy.sh deploy
```

只要压缩包里的源码版本回到目标版本，服务器重新构建后就会回滚到对应代码。

## 常见问题

### 1. `configs/prod.yaml` 缺失

这是原部署方式里最容易漏掉的文件。部署包里如果没有它，容器虽然可能构建成功，但服务会因为生产配置缺失而无法正常启动。

### 2. Docker 构建失败

先确认服务器当前目录里确实有完整源码，而不只是几个运维脚本。原来的部署方式要求在服务器本地构建镜像，缺少 `cmd/`、`internal/`、`pkg/` 或 `go.mod` 都会失败。

### 3. 端口已被占用

旧容器未清理或服务器上有其他服务占用了 `26665` 时，新容器会启动失败。可先执行：

```bash
docker ps -a
docker logs schedule-server
```

### 4. 日志或上传目录权限异常

原来的部署脚本会挂载当前目录的 `logs/` 和 `uploads/`。如果目录不存在或权限不对，建议先手工确认：

```bash
mkdir -p logs uploads
ls -ld logs uploads
```

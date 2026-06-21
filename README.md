# Schedule Server

一个面向教育/培训场景的多租户排课与考勤后端，基于 Go 构建，深度集成钉钉开放平台，并支持通过钉钉 Stream 接入 AI 助手能力。

当前仓库已经覆盖的主链路包括：钉钉登录与组织同步、课表导入与查询、实时/最终考勤快照、请假审批同步、休息日与作息配置、群考勤推送，以及基于工具调用的自然语言查询与管理操作。

## 核心能力

- 多租户架构：基于 GORM 插件对带 `tenant_id` 的模型自动注入租户隔离，适合单库多租户场景。
- 课表管理：支持 Excel `.xls/.xlsx` 导入、按周查询、手动增删改查、从其他用户复制课表。
- 考勤计算：支持实时视图、最终结算、周排行、考勤文本生成、管理员补签和人工覆盖。
- 请假同步：通过钉钉 Stream 接收审批事件，落库后参与考勤计算。
- 作息与休息日：支持学期、节次、上学/假期模式、个人休息日和开关配置。
- 群消息场景：支持群考勤自动推送订阅，并按部门过滤推送范围。
- AI 助手：通过钉钉聊天调用课表、考勤、请假、统计分析和部分管理工具。
- 部署链路：已提供 CI、GHCR 镜像发布、生产 compose 部署和应急源码包部署脚本。

## 技术栈

- Go 1.24
- Gin
- GORM + MySQL
- Viper
- Zap + Lumberjack
- JWT
- robfig/cron
- DingTalk Open Platform / DingTalk Stream SDK
- GoAdmin
- excelize / xls

## 系统组成

### HTTP API

服务启动入口为 `cmd/main.go -> inits.Init() -> app.RunServer()`，HTTP 路由统一挂载在 `/api` 下，当前主要模块包括：

- `/auth`：钉钉登录
- `/users`、`/search`、`/departments`：用户与组织查询
- `/schedules`：课表导入、查询、复制与维护
- `/attendance`：时段状态、考勤详情、快照、排行、文本、补签
- `/semesters`、`/schedule`、`/rest-day`：学期、作息与休息日配置
- `/admin/*`：用户/部门同步与审计日志
- `/health`：健康检查

### AI Agent

当 `dingtalk.stream_mode=true` 时，服务进程会同时启动钉钉 Stream 客户端、AI Agent 和考勤调度器。当前 Agent 通过工具调用支持：

- 个人课表查询
- 指定时间段空闲人员查询
- 当前节次/指定节次考勤查询
- 考勤文本生成与周排行查询
- 个人请假与休息日查询
- 群考勤推送订阅/取消/状态查询
- 管理员补签
- 按部门的统计分析与交叉分析

### 调度与数据

- 启动时会自动执行 `AutoMigrate`
- 当前自动迁移包含租户、用户、部门、课表、请假、考勤快照、人工补签覆盖、群订阅、Agent 调用日志等核心表
- 考勤调度器会按节次动态装配 cron 任务，并在上课后延迟执行实时统计，30 分钟后执行最终结算

## 目录结构

```text
schedule_server/
├── cmd/                    # 程序入口
├── config/                 # 配置结构定义
├── configs/                # YAML 运行配置（本地/生产）
├── docs/                   # 部署与专题文档
├── global/                 # 全局 DB / Logger / Config
├── inits/                  # 配置、日志、数据库、迁移初始化
├── internal/
│   ├── agent/              # AI Agent 与工具注册
│   ├── app/                # Router、服务启动、依赖装配
│   ├── handler/            # HTTP Handler
│   ├── middleware/         # JWT、审计、权限控制
│   ├── model/              # GORM 模型
│   ├── repository/         # 数据访问与租户隔离插件
│   ├── scheduler/          # 考勤调度器
│   └── service/            # 业务服务
├── pkg/
│   ├── dingtalk/           # 钉钉 API / Stream 封装
│   ├── jwt/                # JWT 工具
│   ├── scheduleparse/      # 课表文件解析
│   ├── scheduleutil/       # 节次时间计算
│   └── weekutil/           # 周次工具
├── scripts/                # 迁移和运维辅助脚本
├── .github/workflows/      # CI / CD 工作流
├── deploy.sh               # 正式部署脚本
├── one-click-deploy.sh     # 应急源码包部署脚本
└── docker-compose*.yml     # 本地/生产 compose 资产
```

## 快速开始

### 1. 环境要求

- Go 1.24+
- MySQL 8+
- Docker / Docker Compose（可选）
- 钉钉应用凭据、JWT 密钥、数据库账号、LLM Key（按需提供）

### 2. 准备配置

应用通过 `CONFIG_ENV` 读取 `configs/<env>.yaml`，默认环境名为 `dev`。也可以通过 `CONFIG_PATH` 指定额外的配置目录。

建议本地开发至少准备：

- `configs/dev.yaml`
- 可用的 MySQL 数据库
- 钉钉与 LLM 相关配置（如只验证基础接口，可先关闭不需要的集成能力）

安全要求：

- 不要提交真实数据库、钉钉、JWT 或 LLM 凭据
- 生产环境请使用独立的 `configs/prod.yaml` 和 `.env.prod`

### 3. 本地运行

```bash
make run
```

等价命令：

```bash
go run ./cmd/main.go
```

说明：

- 当前代码在未显式设置 `CONFIG_ENV` 时会默认读取 `configs/dev.yaml`
- 服务启动后可通过 `http://localhost:26665/health` 检查健康状态

### 4. 常用开发命令

```bash
make build        # 编译二进制到 bin/schedule_server
make run          # 本地启动
make test         # 运行全部 Go 测试
make docker-build # 构建本地镜像
make docker-run   # 使用 docker compose 启动
make docker-stop  # 停止 compose 服务
golangci-lint run # 运行 lint
```

## Docker 与部署

### 本地容器运行

仓库提供了 [`docker-compose.yml`](./docker-compose.yml)，默认会：

- 暴露 `26665`
- 挂载 `./configs` 到容器内 `/app/configs`
- 挂载 `./logs` 和 `./uploads`
- 使用 `CONFIG_ENV=prod` 读取容器内配置

启动命令：

```bash
docker compose up -d
```

### 正式部署

当前正式链路是：

1. `.github/workflows/ci.yml` 在 PR 和非 `master` push 上执行测试、lint、构建和 Docker 构建校验
2. `.github/workflows/deploy.yml` 在 `master` push 或手动触发时构建镜像并推送到 GHCR
3. Workflow 通过 SSH 将 `deploy.sh`、`docker-compose.prod.yml`、`.env.prod.example` 同步到服务器
4. 服务器在 `/opt/schedule_server` 下执行 `docker compose pull && docker compose up -d`

生产 compose 依赖：

- `.env.prod`
- `configs/prod.yaml`
- Docker + `docker compose`

更详细的生产说明见：

- [`docs/deployment/production.md`](./docs/deployment/production.md)
- [`docs/deployment/emergency.md`](./docs/deployment/emergency.md)

### 应急部署

仓库仍保留 [`one-click-deploy.sh`](./one-click-deploy.sh) 作为应急入口。它会在本地打包源码部署包、上传到服务器，并在服务器本地构建和启动容器。

这条链路适合：

- GitHub Actions 无法访问服务器
- 需要快速手工部署
- 需要在特殊环境下绕过正式镜像链路

## 业务能力概览

### 课表

- Excel 导入与全量覆盖
- 按周查询个人课表
- 查询指定周次/节次的空闲人员
- 管理员维护课程与复制课表

### 考勤

- 实时考勤详情
- 最终快照结算
- 请假扣减
- 迟到/缺勤统计
- 周排行与文本生成
- 人工补签与人工覆盖
- 群自动推送订阅

### AI

Agent 当前更适合处理以下自然语言问题：

- “我这周三第几节有课？”
- “这周谁周二下午没课？”
- “今天第一节哪些人未到？”
- “帮我生成第一节考勤文本”
- “订阅这个群的自动考勤推送”

## 开发说明

- 仓库约定：Handler 放在 `internal/handler`，业务规则放在 `internal/service`，数据访问放在 `internal/repository`
- 提交前建议执行 `gofmt`、`goimports`、`make test`
- 对于部署、考勤和 Agent 相关改动，优先参考 `docs/` 与 `tasks/` 中已有记录，避免和现有流程漂移

## 安全提示

- 不要在 README、Issue、日志或示例配置中暴露真实凭据
- 钉钉、数据库、JWT、LLM 等敏感配置应通过本地私有配置或服务器注入管理
- 如果你计划将仓库公开，请先彻底检查 `configs/`、`.env*`、部署脚本和历史提交中的敏感信息

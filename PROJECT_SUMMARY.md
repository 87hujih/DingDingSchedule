# 排班考勤管理系统

## 项目概述

这是一个基于 Go 语言开发的**多租户排班考勤管理系统**，深度集成钉钉开放平台，为教育培训机构提供课表管理、考勤统计、请假审批等核心功能。系统采用 SaaS 架构，支持多个企业独立使用，数据完全隔离。

## 核心功能

### 1. 多租户管理
- 支持多个企业（tenant）独立使用
- 数据完全隔离，通过 GORM 插件自动实现
- 每个租户独立配置钉钉应用凭证

### 2. 钉钉集成
- **用户登录**：基于钉钉扫码登录，自动同步用户信息
- **组织架构同步**：自动同步钉钉部门和用户数据
- **考勤查询**：调用钉钉考勤 API 获取请假记录
- **审批回调**：实时接收请假审批结果，自动更新系统数据

### 3. 课表管理
- 支持多种格式导入：xls、xlsx、html
- 自动解析课程信息（课程名、教师、时间、地点等）
- 按用户维度管理课表，支持覆盖更新

### 4. 考勤统计
- 基于课表计算应到人员（候选人 - 忙碌人）
- 支持按部门筛选考勤人员
- 自动查询请假人员，判断时间重叠
- 灵活的时间窗口配置（支持自定义作息表）

### 5. 管理后台
- 集成 GoAdmin 框架，提供可视化管理界面
- 支持租户、用户、部门、课程等数据管理

## 技术栈

### 核心框架
- **Web 框架**：Gin v1.11.0
- **ORM**：GORM v1.31.1 + MySQL Driver v1.6.0
- **配置管理**：Viper v1.21.0
- **日志**：Zap v1.27.1 + Lumberjack v2.2.1（日志轮转）
- **认证**：JWT（golang-jwt/jwt/v5 v5.3.0）

### 业务依赖
- **钉钉 SDK**：自研封装（pkg/dingtalk），支持 AccessToken 自动刷新
- **文件解析**：
  - excelize v2.8.1（xlsx）
  - extrame/xls v0.0.1（xls）
  - goquery v1.11.0（HTML）
- **管理后台**：GoAdmin v1.2.26
- **工具库**：
  - mozillazg/go-pinyin v0.21.0（拼音转换）
  - go-playground/validator/v10 v10.28.0（参数校验）

### 开发环境
- Go 1.24.0+
- MySQL 5.7+

## 项目结构

```
schedule_server/
├── cmd/                    # 启动入口
│   └── main.go            # 调用 inits.Init() + app.RunServer()
├── config/                 # 配置结构定义
│   └── config.go          # Config/Server/Database/DingTalk/JWT 等
├── configs/                # 配置文件
│   └── dev.yaml           # 开发环境配置
├── global/                 # 全局单例
│   └── global.go          # AppConfig/DB/Log
├── inits/                  # 初始化逻辑
│   ├── init.go            # 总入口：配置→日志→DB→迁移→插件
│   ├── config.go          # Viper 加载配置
│   ├── logger.go          # Zap 日志初始化
│   └── database.go        # GORM 连接 + AutoMigrate
├── internal/               # 核心业务代码
│   ├── app/               # HTTP 服务器与路由
│   │   ├── app.go         # 启动与优雅关闭
│   │   ├── router.go      # 依赖注入 + 路由注册
│   │   └── routes_*.go    # 按模块拆分路由
│   ├── handler/           # HTTP Handler 层
│   │   ├── auth_handler.go
│   │   ├── user_handler.go
│   │   ├── department_handler.go
│   │   ├── schedule_handler.go
│   │   ├── attendance_handler.go
│   │   └── dingtalk_callback_handler.go
│   ├── service/           # 业务逻辑层
│   │   ├── dingtalk_client_manager.go  # 按租户缓存钉钉 client
│   │   ├── attendance_service.go       # 考勤计算
│   │   ├── schedule_service.go         # 课表导入
│   │   └── leave_sync_service.go       # 请假同步
│   ├── repository/        # 数据访问层
│   │   ├── tenant_gorm_plugin.go       # 多租户隔离插件
│   │   ├── tenant_scope.go             # 租户作用域工具
│   │   └── *_repository.go             # 各实体仓储
│   ├── model/             # GORM 数据模型
│   │   ├── tenant.go      # 租户（存储钉钉凭证）
│   │   ├── user.go        # 用户（含拼音索引）
│   │   ├── department.go  # 部门
│   │   ├── course.go      # 课程
│   │   └── leave_approval.go  # 请假审批记录
│   ├── middleware/        # 中间件
│   │   └── jwt_auth.go    # JWT 校验 + tenant_id 注入
│   ├── tenantctx/         # 租户上下文封装
│   ├── dto/               # 数据传输对象
│   ├── response/          # 统一响应格式
│   ├── consts/            # 常量（角色等）
│   └── adminui/           # GoAdmin 集成
├── pkg/                    # 可复用工具包
│   ├── dingtalk/          # 钉钉 API 封装
│   │   ├── client.go      # AccessToken 管理
│   │   ├── user.go        # 用户 API
│   │   ├── department.go  # 部门 API
│   │   ├── attendance.go  # 考勤 API
│   │   ├── approval.go    # 审批 API
│   │   └── callback_crypto.go  # 回调加解密
│   ├── scheduleparse/     # 课表解析
│   ├── jwt/               # JWT 工具
│   ├── weekutil/          # 周次工具
│   └── pinyinutil/        # 拼音转换
├── docs/                   # 文档
│   ├── roadmap.md         # 架构扫描与路线图
│   ├── dingtalk_login_flow.md
│   └── dingtalk_callback_setup.md
├── go.mod                  # 依赖管理
└── go.sum
```

## 核心业务逻辑

### 多租户隔离机制

系统采用三层防护确保租户数据隔离：

#### 1. JWT 中间件（middleware/jwt_auth.go）
- 解析 token 获取 `tenant_id`
- 注入到 `request.Context()` 中
- 所有需要鉴权的接口自动获得租户上下文

#### 2. Context 传播（tenantctx/tenantctx.go）
```go
// 注入租户 ID
ctx = tenantctx.WithTenantID(ctx, tenantID)

// 获取租户 ID
tenantID, ok := tenantctx.TenantIDFrom(ctx)

// 跳过租户隔离（用于迁移/后台任务）
ctx = tenantctx.WithSkipTenantScope(ctx)
```

#### 3. GORM 插件（repository/tenant_gorm_plugin.go）
- **Query/Update/Delete**：自动追加 `WHERE tenant_id = ?`
- **Create/Update**：强制写入/覆盖 `tenant_id`
- 仅对包含 `tenant_id` 字段的模型生效
- 支持通过 context 跳过隔离

### 钉钉集成

#### Client 管理（service/dingtalk_client_manager.go）
- 按租户缓存 `dingtalk.Client`（每个租户独立 token 缓存）
- 支持通过 `tenant_id` 或 `corp_id` 获取 client
- 配置变更时可失效缓存

#### AccessToken 自动刷新（pkg/dingtalk/client.go）
```go
// 提前 5 分钟刷新 token
if time.Now().After(c.tokenExpireTime.Add(-5 * time.Minute)) {
    c.refreshAccessToken()
}
```
- 双重检查锁避免并发重复刷新
- 自动重试机制

#### 回调处理（handler/dingtalk_callback_handler.go）
1. 验签 + AES 解密
2. 异步处理审批回调（goroutine）
3. 幂等设计：通过 `(tenant_id, process_instance_id)` 唯一约束

### 考勤计算逻辑

#### 应到人员计算（service/attendance_service.go）
```
应到人员 = 候选人集合 - 忙碌人集合

- 候选人：参与考勤的用户（status=1），可按部门筛选
- 忙碌人：该时段有课的用户（结合 weekList 过滤）
```

#### 请假人员计算
1. 调用钉钉 API 获取请假记录
2. 通过时间重叠判断（半开区间 `[start, end)`）
3. 仅返回与课节时间窗口重叠的请假

#### 时间窗口计算
基于配置的作息表（`config.Schedule.Periods`）动态计算课节时间：
```yaml
schedule:
  periods:
    - name: "第1节"
      start: "08:00"
      end: "08:45"
    - name: "第2节"
      start: "08:55"
      end: "09:40"
```

### 课表导入流程

1. 上传文件（xls/xlsx/html）
2. 统一转换为 xlsx 格式（`scheduleparse.ConvertToXLSX`）
3. 解析课程数据（`scheduleparse.ParseCourses`）
4. 覆盖写入数据库（`courseRepo.ReplaceByUser`）

## 快速开始

### 1. 环境准备

```bash
# 安装 Go 1.24+
go version

# 安装 MySQL 5.7+
mysql --version
```

### 2. 配置文件

复制配置模板并修改：
```bash
cp configs/dev.yaml configs/local.yaml
```

编辑 `configs/local.yaml`：
```yaml
server:
  port: 8080
  mode: debug  # debug/release

database:
  host: localhost
  port: 3306
  username: root
  password: your_password
  dbname: schedule_server
  charset: utf8mb4

jwt:
  secret: your_jwt_secret
  expire_hours: 168  # 7天

dingtalk:
  # 默认租户配置（可选）
  app_key: your_app_key
  app_secret: your_app_secret
  agent_id: your_agent_id
```

### 3. 初始化数据库

```bash
# 创建数据库
mysql -u root -p -e "CREATE DATABASE schedule_server CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"

# 启动服务（自动执行 AutoMigrate）
go run cmd/main.go --config configs/local.yaml
```

### 4. 访问服务

- API 服务：http://localhost:8080
- 管理后台：http://localhost:8080/admin（如果启用）

## 配置说明

### 核心配置项

| 配置项 | 说明 | 示例 |
|--------|------|------|
| `server.port` | HTTP 服务端口 | 8080 |
| `server.mode` | 运行模式 | debug/release |
| `database.*` | MySQL 连接配置 | - |
| `jwt.secret` | JWT 签名密钥 | 随机字符串 |
| `jwt.expire_hours` | Token 有效期（小时） | 168 |
| `dingtalk.*` | 钉钉应用凭证 | - |
| `schedule.periods` | 作息表配置 | 见上文 |

### 日志配置

日志文件位置：`logs/app.log`

配置项（代码中硬编码，可改为配置文件）：
- 最大文件大小：100MB
- 保留文件数：30 个
- 保留天数：7 天
- 是否压缩：是

## API 概览

### 认证相关
- `POST /api/auth/dingtalk/login` - 钉钉扫码登录
- `POST /api/auth/refresh` - 刷新 Token

### 用户管理
- `GET /api/users` - 获取用户列表
- `GET /api/users/:id` - 获取用户详情
- `POST /api/users/sync` - 同步钉钉用户

### 部门管理
- `GET /api/departments` - 获取部门列表
- `POST /api/departments/sync` - 同步钉钉部门

### 课表管理
- `POST /api/schedules/upload` - 上传课表文件
- `GET /api/schedules` - 查询课表
- `DELETE /api/schedules/:id` - 删除课程

### 考勤统计
- `GET /api/attendance/available` - 查询应到人员
- `GET /api/attendance/leave` - 查询请假人员

### 钉钉回调
- `POST /api/dingtalk/callback` - 接收钉钉事件回调

## 部署建议

### 生产环境配置

1. **修改运行模式**
```yaml
server:
  mode: release
```

2. **使用环境变量管理敏感信息**
```bash
export DB_PASSWORD=xxx
export JWT_SECRET=xxx
export DINGTALK_APP_SECRET=xxx
```

3. **配置反向代理（Nginx）**
```nginx
server {
    listen 80;
    server_name your-domain.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

4. **使用进程管理工具（systemd）**
```ini
[Unit]
Description=Schedule Server
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/path/to/schedule_server
ExecStart=/path/to/schedule_server/bin/server --config configs/prod.yaml
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

### 性能优化

1. **数据库连接池**
```go
// 在 inits/database.go 中配置
sqlDB.SetMaxIdleConns(10)
sqlDB.SetMaxOpenConns(100)
sqlDB.SetConnMaxLifetime(time.Hour)
```

2. **启用 GORM 预编译**
```go
db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
    PrepareStmt: true,
})
```

3. **添加数据库索引**
- `users` 表：`(tenant_id, dingtalk_user_id)` 唯一索引
- `courses` 表：`(tenant_id, user_id, week_day)` 复合索引
- `leave_approvals` 表：`(tenant_id, process_instance_id)` 唯一索引

## 注意事项

### 安全风险（P0）

1. **配置文件包含明文密钥**
   - 问题：`configs/dev.yaml` 可能包含敏感信息
   - 建议：使用环境变量或密钥管理服务（如 AWS Secrets Manager）

2. **启动时 AutoMigrate 生产风险**
   - 问题：自动迁移可能导致数据丢失或结构异常
   - 建议：引入 migration 工具（如 golang-migrate），手动管理数据库版本

### 代码改进建议（P1）

1. **回调异步使用 request context**
   - 问题：`go func() { ... }` 中使用 `c.Request.Context()` 可能被提前 cancel
   - 建议：使用 `context.Background()` 或独立的 context

2. **JWT 管理分散**
   - 问题：middleware 和 service 各自初始化 JWT 工具
   - 建议：统一在 `global` 包中初始化

3. **缺少单元测试**
   - 建议：为核心业务逻辑（考勤计算、课表解析）添加测试

### 功能扩展建议

1. **数据库迁移工具**：引入 golang-migrate 管理数据库版本
2. **API 文档**：集成 Swagger 自动生成 API 文档
3. **监控告警**：接入 Prometheus + Grafana
4. **缓存层**：引入 Redis 缓存热点数据（如部门树、用户列表）
5. **消息队列**：使用 RabbitMQ/Kafka 处理异步任务（如批量同步）

## 关键设计亮点

1. **多租户隔离**：通过 GORM 插件实现自动隔离，避免业务代码遗漏
2. **钉钉 Client 缓存**：按租户维度复用 client，token 自动刷新
3. **拼音索引**：用户姓名自动生成全拼和首字母索引，支持快速搜索
4. **幂等设计**：请假审批通过唯一约束保证重复回调不产生脏数据
5. **时间窗口计算**：基于配置的作息表动态计算课节时间，灵活适配不同学校


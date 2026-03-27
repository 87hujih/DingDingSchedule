# 后台操作知识说明

## 适用范围

本文档描述当前系统中 GoAdmin 后台的用途、启用方式、初始化前置条件、入口路径、常见页面和常见报错，适合回答“后台怎么打开、为什么打不开、需要先做什么初始化”这类问题。

## 来源

- `docs/adminui.md`
- `internal/adminui/adminui.go`
- `internal/app/router.go`
- `config/config.go`

## 核心结论

- 当前后台基于 GoAdmin，挂载在现有 Gin 服务里，与业务 API 共用同一个 HTTP 服务。
- 只有当 `goadmin.enable=true` 时，后台才会挂载。
- 后台不是零配置启动，首次启用前需要先初始化 goadmin 系统表。
- 默认后台入口是 `/<url_prefix>`，通常是 `/admin`。

## 后台是干什么的

当前后台主要用于：

- 进入 GoAdmin 管理界面
- 查看和维护租户等后台表
- 管理部分基础配置
- 作为运维和管理入口，而不是普通用户入口

它不是面向学生或老师的业务前台，而是面向管理员和运维人员的后台。

## 后台怎么启用

要启用后台，至少需要满足两个条件：

### 条件 1：打开配置开关

配置项是：

- `goadmin.enable=true`

常见相关配置还包括：

- `goadmin.url_prefix`
- `goadmin.theme`
- `goadmin.language`
- `goadmin.store_path`
- `goadmin.store_prefix`

### 条件 2：数据库中已经有 goadmin 系统表

首次启用前，需要先执行初始化 SQL。

当前代码会检查以下系统表是否存在，例如：

- `goadmin_users`
- `goadmin_menu`
- `goadmin_roles`
- `goadmin_permissions`

如果缺少这些表，后台不会正常挂载。

## 后台从哪里进入

当前后台挂载路径由 `goadmin.url_prefix` 决定。

如果没有特殊配置，默认前缀是：

- `admin`

因此默认入口通常是：

- `/admin`

当前实现还专门补了一个入口跳转：

- 访问 `/admin` 时，会重定向到 `/admin/info/tenants`

这意味着，登录成功后通常会落到租户管理页面。

## 后台和业务 API 是什么关系

它们共用同一个 HTTP 服务：

- 同端口
- 同进程
- 同一个 Gin 路由引擎

只是路径不同：

- 业务接口主要在 `/api/...`
- 后台入口主要在 `/<url_prefix>`

## 首次启用前要做什么

首次启用后台，推荐按这个顺序做：

1. 打开 `goadmin.enable`
2. 配置好 `url_prefix`、语言、主题和上传目录
3. 在目标数据库执行 goadmin 初始化 SQL
4. 启动服务
5. 打开后台入口确认是否可访问

## 常见页面是做什么的

### `/admin/info/tenants`

这是当前实现里最直接的后台落地页。

它主要用于查看和维护租户信息，例如：

- 企业标识
- 应用凭据
- 状态

### `/admin/info/schedule_periods`

用于维护作息时段配置。

常见操作包括：

- 配置 `school` 模式时段
- 配置 `holiday` 模式时段
- 控制时段是否启用

### `/admin/info/schedule_settings`

用于维护当前生效模式和相关开关配置。

### 其他后台表

随着 Generator 注册的表增加，后台还可以扩展出更多 CRUD 页面。

## 上传目录和静态资源是怎么处理的

后台会把上传目录挂成静态资源路由。

如果配置为空，默认值通常是：

- `store_path = ./uploads`
- `store_prefix = uploads`

这意味着后台相关上传资源通常通过 `/uploads/...` 访问。

## 常见报错和排查方法

### 启动时报 GoAdmin 系统表缺失

这是最常见的问题。

说明数据库里没有初始化 goadmin 所需的系统表。需要先执行初始化 SQL，再启动服务。

### 访问 `/admin` 返回 404

优先检查：

1. `goadmin.enable` 是否真的开启
2. `url_prefix` 是否不是 `admin`
3. 服务启动时是否因为 GoAdmin 初始化失败直接退出

### 登录成功后页面不对

当前实现里，默认入口会跳转到：

- `/admin/info/tenants`

如果你改了前缀，实际路径会随之变化。

### 上传资源访问异常

优先检查：

1. `store_path` 是否存在
2. `store_prefix` 是否配置正确
3. 运行用户是否有对应目录读写权限

## 常见理解误区

### 后台是不是单独的服务

不是。

当前后台挂在现有 Gin 服务上，不是独立进程。

### 只开配置就能直接用后台吗

不行。

如果数据库里没有 goadmin 系统表，开关打开后也无法正常初始化。

### `/admin` 是不是业务 API

不是。

业务 API 主要走 `/api/...`，`/admin` 是后台管理入口。

## 当前实现边界

- 当前后台建立在 GoAdmin 之上，不是完全自定义后台
- 当前后台是否启用由配置决定
- 当前后台落地页默认是租户管理页，而不是业务首页

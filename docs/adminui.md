# AdminUI（GoAdmin）接入说明与代码解读

本文解释 `internal/adminui/adminui.go` 的设计目的、运行流程、配置项含义，以及如何扩展后台页面（新增 CRUD 表）。

> 结论：`adminui.Mount(r)` 会把 GoAdminGroup 的 `go-admin` 管理后台挂载到现有的 `gin.Engine` 上（与业务 API 共用同一个 HTTP 服务），并根据 `configs/*.yaml` 的 `goadmin` 配置启用/关闭。

---

## 1. 入口在哪里？整体链路是什么？

项目在 `internal/app/router.go` 初始化路由：

- 先创建 `r := gin.Default()` 并注册业务 API（`/api/...`）
- 再根据配置决定是否挂载 go-admin：
  - `if global.AppConfig.GoAdmin.Enable { adminui.Mount(r) }`

因此 go-admin 与业务 API：

- **同端口**、**同进程**、**同一个 gin.Engine**
- go-admin 的路由统一在 `/<url_prefix>` 下（默认 `/admin`）

---

## 2. 如何启用（运行前置条件）

### 2.1 打开开关

在 `configs/dev.yaml`：

```yaml
goadmin:
  enable: true
  url_prefix: "admin"
  theme: "adminlte"
  language: "cn"
  store_path: "./uploads"
  store_prefix: "uploads"
```

对应结构体定义在 `config/config.go` 的 `type GoAdmin struct`。

### 2.2 初始化 go-admin 系统表（只需一次）

首次启用前，需要在目标 MySQL 数据库执行：

- `docs/goadmin_mysql_init.sql`

原因：`adminui.Mount` 里会调用 `ensureGoAdminBootstrapTables(global.DB)` 检查 `goadmin_users`、`goadmin_menu` 等表是否存在，缺表直接返回错误并提示你先初始化。

> 注意：`docs/goadmin_mysql_init.sql` 含 `DROP TABLE`，会删除已有 `goadmin_*` 表，执行前务必确认。

默认账号（SQL 脚本注释也会写）：

- 用户名：`admin`
- 密码：`admin`

---

## 3. `internal/adminui/adminui.go` 逐段解读

文件核心是：

- `func Mount(r *gin.Engine, gens ...table.GeneratorList) (*engine.Engine, error)`
- 若干辅助函数：`normalizeLanguage`、`normalizeTheme`、`ensureGoAdminBootstrapTables`

### 3.1 `Mount` 的参数与返回值

```go
func Mount(r *gin.Engine, gens ...table.GeneratorList) (*engine.Engine, error)
```

- `r`：业务侧创建的 `*gin.Engine`，go-admin 会把自己的路由注册到它上面
- `gens ...table.GeneratorList`：额外的表生成器（扩展点）；会在内置 `tables.Generators` 之后追加注册
- 返回值：
  - `*engine.Engine`：go-admin 引擎对象（可用于后续扩展/调试）
  - `error`：初始化失败（例如缺 goadmin 系统表、DB 为空、go-admin 初始化失败）

特别行为：

- 如果 `global.AppConfig.GoAdmin.Enable == false`，`Mount` **返回 `(nil, nil)`**。
  - 这让调用方把它当作“可选模块”，无需特殊处理。

### 3.2 前置校验：gin / 配置开关 / DB

`Mount` 开头依次检查：

- `r == nil`：直接报错
- `!global.AppConfig.GoAdmin.Enable`：直接返回 `nil, nil`
- `global.DB == nil`：直接报错
- `ensureGoAdminBootstrapTables(global.DB)`：检查 goadmin 系统表是否存在

这些校验把错误尽量提前暴露：

- 没初始化系统表时，你会得到明确提示去执行 `docs/goadmin_mysql_init.sql`

### 3.3 uploads 静态资源挂载

`Mount` 会把上传目录做成静态资源：

- `store_path` 为空则默认 `./uploads`
- `store_prefix` 为空则默认 `uploads`
- 实际注册：
  - `r.Static("/"+storePrefix, storePath)`

效果举例：

- 配置 `store_prefix: uploads` + `store_path: ./uploads`
- 那么 `GET /uploads/a.png` 会映射到磁盘 `./uploads/a.png`

注意点：

- 这是 **gin 的静态资源路由**，通常不做鉴权；如果你允许上传敏感文件，需要评估访问控制策略。

### 3.4 go-admin 配置组装（`goadmincfg.Config`）

`Mount` 会把你项目的配置映射成 go-admin 的配置：

- 主题：`normalizeTheme(global.AppConfig.GoAdmin.Theme)`
  - 当前支持：`adminlte` / `sword`；空值默认 `adminlte`
- 语言：`normalizeLanguage(global.AppConfig.GoAdmin.Language)`
  - 当前兜底：空值或非法值 -> `cn`
- 后台 URL 前缀：`url_prefix`，默认 `admin`
  - 最终挂载路径是 `/<url_prefix>`

数据库部分：

- go-admin 连接同一个 MySQL（使用 `global.AppConfig.Database.DSN()`）
- 连接池参数：`MaxIdleConns/MaxOpenConns/ConnMaxLifetime`
  - `ConnMaxLifetime` 会从 `database.conn_max_lifetime` 解析（失败则默认 1 小时）

环境/调试：

- `Debug`：当 `server.mode == debug` 时开启
- `Env`：当 `env == prod` 时设为 go-admin 的 `EnvProd`，否则默认本地

### 3.5 注册模板组件与主题

```go
template.AddComp(chartjs.NewChart())
```

- adminlte 主题会使用 chart 组件，这里显式添加以避免页面组件缺失。

另外：

- `_ "github.com/GoAdminGroup/themes/sword"`

这是 Go 的匿名导入：目的不是直接引用符号，而是让该包的 `init()` 执行，从而把主题注册到 go-admin。

### 3.6 初始化并挂载到 gin

关键链路：

```go
eng := engine.Default()
err := eng.AddConfig(&goCfg).
  AddAdapter(new(ada.Gin)).
  AddGenerators(tables.Generators).
  AddGenerators(gens...).
  Use(r)
```

含义：

- `AddConfig`：注入 go-admin 配置
- `AddAdapter`：声明使用 gin 作为 Web 框架
- `AddGenerators`：注册“后台页面/CRUD 表”的生成器
  - 先注册 `internal/adminui/tables` 内置的
  - 再注册调用方额外传入的
- `Use(r)`：真正把 go-admin 路由注册到你的 `gin.Engine`

### 3.7 为什么要补 `GET /admin` 入口重定向

代码里额外做了：

- `GET /<url_prefix>` -> `302` 重定向到 `/<url_prefix>/info/tenants`

https://docs.qq.com/doc/DZWhNb0dvaUdJVWpo怎

---

## 4. `internal/adminui/tables/tenants.go`：CRUD 页面是怎么来的？

go-admin 的“业务页面”通常由 `table.GeneratorList` 提供：

```go
var Generators = table.GeneratorList{
  "tenants": GetTenantTable,
}
```

- key：`"tenants"`
- value：生成该资源页面的函数 `GetTenantTable(ctx)`

常见访问路径（以当前默认 `url_prefix: admin` 为例）：

- `/admin/info/tenants`

### 4.1 列表页（Info）配置

`tenantTable.GetInfo()` 添加字段：

- `ID`（可排序）
- `CorpID/Name`（可 like 过滤）
- `Status`（显示转换 + 下拉过滤）
- `CreatedAt/UpdatedAt`

并指定：

- `info.SetTable("tenants")`：对应 MySQL 表名

### 4.2 表单页（Form）配置

`tenantTable.GetForm()` 添加字段：

- `corp_id/app_key/app_secret/agent_id/status` 等
- `ID` 在创建时禁用、更新时不可编辑

并且有一个非常关键的处理：

```go
formList.SetPreProcessFn(func(values form.Values) form.Values {
  // insert/update 时补齐 created_at/updated_at
})
```

原因：

- go-admin 的写入不一定走你项目里的 GORM model hooks（例如 `BeforeCreate`/`BeforeUpdate`）
- 所以这里手动补 `created_at` / `updated_at`，确保数据完整。

---

## 5. 如何扩展：新增一张后台 CRUD 表

### 5.1 新增 generator

在 `internal/adminui/tables/` 新建一个文件（例如 `users.go`），写一个类似 `GetUserTable` 的函数，并把它加入 `Generators`：

```go
var Generators = table.GeneratorList{
  "tenants": GetTenantTable,
  "users":   GetUserTable,
}
```

> 注意：当前 `Generators` 变量在 `tenants.go` 里定义；如果你要维护多个表，通常会把 `Generators` 独立到一个文件里统一管理，避免多人改同一个文件冲突。

### 5.2 配置左侧菜单入口

go-admin 的左侧菜单来自 `goadmin_menu`。

- 如果你访问 `/admin/info/users` 能打开，但左侧菜单没有入口：
  - 进入 go-admin 的 `Menu` 管理
  - 新增菜单项，`uri` 填 `/info/users`

`docs/goadmin.md` 里也提到：`/info/tenants` 的菜单项需要在 Menu 管理里配置。

---

## 6. 常见问题（排错清单）

1) 启动时报缺 goadmin 系统表

- 现象：`Mount` 返回 error，提示缺 `goadmin_users` 等表
- 处理：执行 `docs/goadmin_mysql_init.sql`（注意含 DROP TABLE）

2) 访问 `/admin` 404

- 正常情况下本项目已补了 `/admin -> /admin/info/tenants` 重定向
- 若你改了 `url_prefix` 或想改默认落地页：
  - 修改 `adminui.go` 里 `c.Redirect(302, mountPath+"/info/tenants")` 的目标

3) `created_at/updated_at` 没自动更新

- go-admin 写库路径不一定触发 GORM 自动时间戳
- 按当前做法：在对应表的 `Form` 里用 `SetPreProcessFn` 补齐

---

## 7. 快速验证步骤

- 确认 `configs/dev.yaml`：`goadmin.enable: true`
- 确认数据库已执行 `docs/goadmin_mysql_init.sql`
- 启动服务后访问：
  - `http://localhost:<port>/admin`
  - 默认账号：`admin/admin`
- tenants 管理页：
  - `http://localhost:<port>/admin/info/tenants`

---

## 参考文件

- `internal/adminui/adminui.go`
- `internal/adminui/tables/tenants.go`
- `internal/app/router.go`
- `config/config.go`
- `configs/dev.yaml`
- `docs/goadmin.md`
- `docs/goadmin_mysql_init.sql`

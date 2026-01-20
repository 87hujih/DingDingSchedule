# GoAdmin 后台（tenants 管理）

## 1) 首次初始化（只需一次）

在目标 MySQL 数据库执行：`docs/goadmin_mysql_init.sql`

> 注意：该 SQL 含 `DROP TABLE`，会删除已存在的 `goadmin_*` 表，请谨慎执行。

## 2) 启动与访问

- 配置：`configs/dev.yaml` -> `goadmin.enable: true`
- 启动服务后访问：`http://localhost:<port>/admin`
- 默认账号：`admin` / `admin`

## 3) tenants 管理入口

- 直接访问：`/admin/info/tenants`
- 如果左侧菜单没有入口：在 `Menu` 管理里新增一个菜单项，`uri` 填 `/info/tenants`



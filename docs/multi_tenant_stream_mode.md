# 多租户 Stream 模式配置指南

## 概述

系统现已支持多租户 Stream 模式，可以同时为多个企业（租户）提供钉钉事件推送服务。每个租户使用独立的 Stream 客户端连接，互不干扰。

## 架构说明

### 配置来源

- **旧版本**：从 `configs/dev.yaml` 读取单一企业的配置
- **新版本**：从数据库 `tenants` 表读取所有活跃租户的配置

### 工作原理

1. 服务启动时，`StreamClientManager` 从数据库读取所有 `status=1` 的租户
2. 为每个租户创建独立的 `StreamClient` 实例
3. 每个客户端使用对应租户的 `app_key`、`app_secret`、`corp_id`
4. 所有客户端并行运行，独立接收各自企业的钉钉事件

## 数据库配置

### 1. 租户表结构

```sql
CREATE TABLE `tenants` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `corp_id` varchar(64) NOT NULL COMMENT '企业标识（钉钉 CorpID）',
  `name` varchar(128) DEFAULT NULL COMMENT '企业名称',
  `app_key` varchar(100) NOT NULL COMMENT '钉钉应用 AppKey',
  `app_secret` varchar(255) NOT NULL COMMENT '钉钉应用 AppSecret',
  `agent_id` varchar(100) NOT NULL COMMENT '钉钉应用 AgentID',
  `status` int DEFAULT '1' COMMENT '状态：1=启用，0=禁用',
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uni_tenants_corp_id` (`corp_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 2. 添加租户

```sql
-- 示例：添加乐知教育租户
INSERT INTO tenants (corp_id, name, app_key, app_secret, agent_id, status, created_at, updated_at)
VALUES (
  'dinge292658c9243df4235c2f4657eb6378f',  -- 企业 CorpID
  '乐知教育',                                -- 企业名称
  'dingvtmiaxzmsya4ymqs',                   -- AppKey
  'pJUpxMOzibe5hs7Zge3vB9hsnh_5b8_HXkA6MF0nc41QZBKHQUnH_HVA76t8yuP-',  -- AppSecret
  '4250011931',                             -- AgentID
  1,                                        -- 启用状态
  NOW(),
  NOW()
);

-- 示例：添加第二个租户
INSERT INTO tenants (corp_id, name, app_key, app_secret, agent_id, status, created_at, updated_at)
VALUES (
  'dingxxxxxxxxxxxxxxxx',  -- 第二个企业的 CorpID
  '示例企业',
  'dingxxxxxxxx',          -- 第二个企业的 AppKey
  'xxxxxxxxxxxxxxxx',      -- 第二个企业的 AppSecret
  '1234567890',            -- 第二个企业的 AgentID
  1,
  NOW(),
  NOW()
);
```

### 3. 禁用租户

```sql
-- 禁用某个租户的 Stream 客户端（需要重启服务生效）
UPDATE tenants SET status = 0 WHERE corp_id = 'dingxxxxxxxxxxxxxxxx';
```

## 配置文件变更

### configs/dev.yaml

```yaml
dingtalk:
  stream_mode: true  # 启用 Stream 模式
  # 以下配置已废弃，凭证从数据库读取
  app_key: ""
  app_secret: ""
  agent_id: ""
  corp_id: ""
```

**重要提示**：
- `stream_mode: true` 仍然需要保留，用于控制是否启用 Stream 功能
- 具体的应用凭证不再从配置文件读取，而是从数据库 `tenants` 表读取

## 运行时管理

### 启动流程

1. 服务启动时，`StreamClientManager.StartAll()` 被调用
2. 从数据库查询所有 `status=1` 的租户
3. 为每个租户创建独立的 Stream 客户端
4. 每个客户端在独立的 goroutine 中运行

### 日志输出

```
INFO  准备启动 2 个租户的 Stream 客户端
INFO  启动租户 Stream 客户端  tenantID=1 corpID=dinge292658c9243df4235c2f4657eb6378f appKey=dingvtmiaxzmsya4ymqs
INFO  启动租户 Stream 客户端  tenantID=2 corpID=dingxxxxxxxxxxxxxxxx appKey=dingxxxxxxxx
```

### 停止流程

1. 服务收到停止信号（SIGINT/SIGTERM）
2. `StreamClientManager.StopAll()` 被调用
3. 取消所有租户的 context，触发客户端优雅关闭
4. 等待 2 秒让 SDK 完成清理工作

## 动态管理（未来扩展）

当前版本需要重启服务才能加载新租户。未来可以扩展以下功能：

### 1. 热加载新租户

```go
// 在管理接口中调用
streamMgr.StartForTenant(ctx, newTenant, eventHandler)
```

### 2. 热停止租户

```go
// 禁用租户时调用
streamMgr.StopForTenant(tenantID)
```

### 3. 重启租户客户端

```go
// 更新租户配置后调用
streamMgr.RestartForTenant(ctx, tenantID, eventHandler)
```

## 故障处理

### 问题：某个租户的客户端启动失败

**现象**：日志中出现 "启动租户 Stream 客户端失败"

**原因**：
- AppKey/AppSecret 配置错误
- 网络连接问题
- 钉钉服务端限流

**处理**：
- 检查数据库中该租户的配置是否正确
- 其他租户的客户端不受影响，会继续正常运行

### 问题：所有租户都无法启动

**现象**：日志中出现 "没有找到活跃的租户"

**原因**：
- 数据库中没有 `status=1` 的租户记录
- 数据库连接失败

**处理**：
- 检查数据库连接
- 确认 `tenants` 表中有启用的租户记录

## 性能考虑

### 资源消耗

- 每个租户一个长连接（WebSocket）
- 每个客户端约占用 1-2MB 内存
- 建议：单实例支持 100 个租户以内

### 扩展性

如果租户数量超过 100 个，建议：
1. 使用多个服务实例，每个实例负责部分租户
2. 通过配置或数据库字段控制租户分配
3. 使用负载均衡器分发流量

## 迁移指南

### 从单租户迁移到多租户

1. **备份配置**：保存 `configs/dev.yaml` 中的钉钉配置

2. **插入租户记录**：
   ```sql
   INSERT INTO tenants (corp_id, name, app_key, app_secret, agent_id, status)
   VALUES (
     '原配置文件中的 corp_id',
     '企业名称',
     '原配置文件中的 app_key',
     '原配置文件中的 app_secret',
     '原配置文件中的 agent_id',
     1
   );
   ```

3. **更新配置文件**：清空 `dev.yaml` 中的钉钉凭证（可选）

4. **重启服务**：新版本会自动从数据库读取配置

5. **验证**：检查日志确认 Stream 客户端正常启动

## 相关代码

- `internal/service/stream_client_manager.go` - Stream 客户端管理器
- `internal/app/app.go` - 服务启动逻辑
- `pkg/dingtalk/stream.go` - Stream 客户端实现
- `internal/model/tenant.go` - 租户模型

## 常见问题

**Q: 是否需要为每个租户配置不同的钉钉应用？**
A: 是的。每个企业（租户）需要在钉钉开放平台创建独立的应用，获取各自的 AppKey/AppSecret。

**Q: 可以多个租户共用一个钉钉应用吗？**
A: 不可以。钉钉应用是企业级的，一个应用只能属于一个企业。

**Q: 添加新租户后需要重启服务吗？**
A: 当前版本需要重启。未来可以通过管理接口实现热加载。

**Q: 如何测试多租户 Stream 模式？**
A: 在数据库中添加测试租户记录，重启服务，观察日志输出确认客户端启动成功。

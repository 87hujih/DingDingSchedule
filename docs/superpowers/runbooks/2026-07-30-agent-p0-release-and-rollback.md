# Agent P0 灰度发布与回滚 Runbook

## 1. 适用范围

本 Runbook 只用于 P0 Agent runtime hardening 发布。它不授权自动修改生产配置、执行 DDL、切换流量或清理 workflow 数据。

生产 `protocol_live` 的最终状态必须是：

```yaml
llm:
  protocol_mode: protocol_live
  deterministic_compiler_mode: short_circuit
  intent_context_enabled: true
  workflow_store: database
  workflow_migration: false
  log_payloads: false
```

`shadow` 仅允许作为有截止时间的迁移态。禁止长期运行，也禁止同一服务池长期混用 `memory`、`shadow` 和 `database` 主存储实例。

## 2. 发布前硬门禁

以下任一项不满足时停止发布：

- 已评审并执行 `scripts/migrations/20260730_create_agent_workflows.sql`。
- 已评审并执行 `scripts/migrations/20260730_create_agent_write_ledgers.sql`。
- `go test ./... -count=1`、Agent/repository/app race、`go vet ./...` 和 lint 全部通过。
- 两连接 CAS 测试证明同一 version 只有一个写入成功。
- reservation 失败时业务 executor 调用次数为 0。
- `result_recorded` 恢复只 finalize，不重放业务写。
- 过期 `executing` 能通过新 token takeover，并由业务幂等账本收敛。
- start/cancel 在进程重启和请求重放后不产生重复副作用。
- 生产配置已在宿主机和容器内分别核验，且 fingerprint 一致。

生产配置核验命令：

```bash
CONFIG_ENV=prod CONFIG_PATH=/opt/schedule_server/configs ./schedule_server agent-config-check
docker exec schedule-server /app/schedule_server agent-config-check
```

首次 P0 发布由部署工作流在替换容器前运行：

```bash
/app/schedule_server agent-release-prepare
/app/schedule_server agent-release-check
```

`agent-release-prepare` 只补充缺失的保守灰度值（compiler `observe`、context 关闭、workflow `shadow`），保留显式配置，并执行仓库内两份 `CREATE TABLE IF NOT EXISTS` 评审 DDL。配置发生变化时会在同目录保留 `prod.yaml.pre-agent-p0-<run-id>.bak`；任一配置校验、数据库连接、DDL 或表权限检查失败时自动恢复该备份，旧容器不会被替换。此自动准备流程仅由本次明确授权的 P0 发布使用，不代表后续可绕过 DDL 或配置评审。

输出只能包含 environment、mode、timeout、model、store、context flag 和 fingerprint。不得出现 API key、DingTalk secret 或带 credential 的 URL。

## 3. Compiler 灰度

按 `observe -> fallback -> short_circuit` 逐阶段推进，每阶段至少覆盖 3 次 semantic-v1 固定评测和一个真实峰值观察窗口。

进入下一阶段的门槛：

- 写操作 false positive 为 0。
- catalog 外 operation 为 0。
- 固定 exact 样本准确率为 100%。
- 核心 operation recall 相对基线下降不超过 1 个百分点。
- timeout/transport/invalid output 均只走已定义的安全降级，不执行低置信写操作。

任一门槛失败时退回上一 compiler mode；不要同时改 context 或 workflow store。

## 4. Context 灰度

按“固定评测 -> 测试租户 -> 单个真实群 -> 全量”推进。每阶段只修改 `intent_context_enabled`，保持 compiler mode 和 workflow store 不变。

进入下一阶段的门槛：

- operation macro-F1 相对基线下降不超过 1 个百分点。
- 指代、省略和改口样本准确率相对基线提升至少 10 个百分点。
- normalized slot F1 不低于 0.85。
- hard-negative 准确率下降不超过 1 个百分点。
- 写操作 false positive 为 0。
- readiness 和日志中不出现原始历史、tool 内容、内部 ID 或 credential。

## 5. WorkflowStore 灰度

### 5.1 Shadow

1. 所有实例部署同一 DB-capable 版本。
2. 设置 `workflow_store=shadow`、`workflow_migration=true` 和明确的 RFC3339 截止时间。
3. 运行至少 24 小时并覆盖一个完整业务峰值。
4. 检查 shadow mismatch、DB mirror error 和 codec error。

切换 database primary 的门槛：

- shadow mismatch 为 0。
- DB store error 接近 0，且不存在持续错误窗口。
- 没有无法解码的 snapshot/execution。
- recovery 扫描可以跨租户发现候选，但执行时重新绑定 tenant context。

### 5.2 Database primary

1. 暂停 Agent 新消息入口并 drain 当前处理。
2. 确认没有未解释的 `executing` 或 `recovery_required`：

```sql
SELECT execution_status, COUNT(*)
FROM agent_workflows
GROUP BY execution_status;
```

3. 备份宿主机生产配置并记录原 fingerprint。
4. 将整个 Agent 服务池统一切换为 `workflow_store=database`；不要滚动混部 memory-primary 与 database-primary。
5. 运行 `agent-config-check`，记录新 fingerprint 后再启动。
6. 验证 `/ready` 为 200，`/internal/readiness` 中 store 为 `database` 且 fingerprint 与发布记录一致。
7. 在测试租户执行 read、start、重复 start、cancel、重复 cancel、进程重启续接和过期恢复 smoke。

## 6. 运行时故障处理

数据库探针失败时：

- `/ready` 返回 503。
- Agent 聊天准入返回“智能助手暂时不可用，请稍后重试”，不进入 compiler/workflow/executor。
- 现有 recovery loop 停止取得新的安全进展，待数据库恢复后继续。
- 不得临时切换到 memory；这会形成双主并破坏 fencing。

恢复数据库后确认 readiness 自动恢复，并检查：

```sql
SELECT tenant_id, conversation_id, actor_user_id, version,
       execution_status, lease_expires_at, updated_at
FROM agent_workflows
WHERE execution_status IN ('executing', 'recovery_required', 'result_recorded')
ORDER BY updated_at;
```

## 7. 回滚

应用行为回归但数据库正常时：

1. 停止 Agent 新消息并 drain。
2. 回滚到仍支持 versioned DB store、reservation、fencing 和 recovery 的最近版本。
3. 保持 `workflow_store=database`，不要回退为 memory。
4. 对整个 Agent 服务池同步操作，核对旧/新 fingerprint。
5. 复跑 restart、result-recorded finalize 和请求重放 smoke。

数据库不可用时：

1. 保持 Agent readiness=false，不接收新 Agent 请求。
2. 修复数据库或连接，不删除 `agent_workflows`、`agent_write_ledgers`。
3. 检查并恢复 `executing`、`recovery_required`、`result_recorded`。
4. readiness 恢复后逐步恢复流量。

在完整观察窗口结束前不回滚 DDL、不清空 workflow 表、不手工改 execution token/version。DDL 清理必须作为独立变更评审。

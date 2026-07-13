# Agent 订阅部门选择修复设计

## 背景

2026-07-13 生产 `agent_call_logs` 显示，群考勤订阅请求能通过 protocol、权限和 workflow 创建，但在部门实体与候选选择阶段卡住。典型链路是：

1. `请将26暑期智能体开发训练营部门的考勤抄送到本群`
2. Agent 返回部门候选列表。
3. 用户回复 `3. 26暑期智能体开发训练营`。
4. workflow 仍停在 `collect_departments`，executor 未执行。
5. 用户改回 `全部人员`也无法继续，并收到与真实缺失字段不符的提示。

生产镜像与 `origin/master@6ec7664` 一致。失败窗口内没有 LLM timeout、数据库错误、workflow 冲突或钉钉回复失败。

## 目标

- 用户在首条请求中给出唯一部门名时，允许直接解析可信部门 ID 并执行订阅。
- 用户按 Agent 展示格式回复 `序号. 部门名`时，能稳定选中对应候选并执行订阅。
- 在 `collect_departments` 阶段回复 `全部人员`时，能安全切换为全员订阅并执行。
- 无法解析部门时，提示用户选择部门，不再误报“选择订阅范围”。
- 保持租户隔离、权限策略、trusted params、write guard、幂等 ledger 和 workflow fencing 不变。

## 方案

采用局部确定性修复，不修改 LLM prompt，不放宽全局实体匹配。

### 1. 部门名规范化

部门 resolver 保留完整原文精确匹配，再增加部门专用变体：仅剔除末尾语义后缀 `部门`。例如 `26暑期智能体开发训练营部门` 可与 `26暑期智能体开发训练营` 匹配。该规则不进入用户名或其他实体 resolver，避免扩大误匹配面。

### 2. 候选选择解析

候选 resolver 支持纯序号、精确标签以及 Agent 展示格式 `3. 标签`。当同时存在序号和标签时，必须校验二者指向同一候选；不一致时返回可观测的 mismatch，不能仅信任序号而选错部门。租户 ID 校验仍由 `validateCandidateTenant` 完成。

### 3. workflow 范围切换

`collect_departments` 先识别明确的 `全部人员` / `全部` 范围。切换为 `scope=all` 时必须清空 workflow 中旧的部门 ID 及 `dept_ids` trusted param，然后进入 `ready_to_execute`。这保证用户能从部门选择阶段安全改为全员订阅。

### 4. 错误提示

workflow 解析失败时，ResponseModel 携带当前 operation 和真实 `MissingFields`。对 `subscription.start + dept_names` 渲染为“请从部门选项中选择，或输入准确部门名”；`scope` 缺失仍保留现有范围提示。

## 测试与验收

- 单元测试覆盖部门后缀规范化、`3. 标签` 选择、序号/标签不一致拒绝和跨租户拒绝。
- pipeline 测试回放生产对话，断言订阅 executor 被调用、可信 `dept_ids` 正确、结果为成功且 workflow 被清除。
- pipeline 测试覆盖 `collect_departments -> 全部人员`，断言执行参数不含部门 ID。
- renderer 测试覆盖 `dept_names` 与 `scope` 两类不同提示。
- 定向测试、`go test ./internal/agent/... -count=1`、`go test ./... -count=1`、race 测试、build、lint 和 `git diff --check` 全部通过后才能推送。

## 部署影响

修复不包含 schema 或配置变更。推送到 `origin/master` 会触发生产部署；回滚方式为恢复上一个镜像 `6ec7664`。部署后应用同一组真实群聊消息回放，并核对 `agent_call_logs.executor_status=success`、workflow 清除及群订阅记录。

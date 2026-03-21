# Attendance Realtime Manual Sign Override Design

## 背景

当前实时考勤详情 `GET /api/admin/attendance/record/detail` 在未 finalize 时走现场计算，
不依赖 `attendance_records` 快照，因此返回的 `record_id` 会是 `0`。
而现有代签接口 `POST /api/admin/attendance/record/sign` 只接受 `record_id`，并且在服务层直接按 `attendance_records.id` 查找并改写已落库快照。

这造成两个直接问题：

- 实时考勤阶段无法代签，因为还没有稳定可用的 `record_id`
- 即使强行让实时接口提前生成 `record_id`，也无法自然表达“管理员人工修正优先于后续真实迟到打卡”这一规则

用户已确认新的目标行为：

1. 在实时考勤阶段允许代签
2. 代签成功后，后续 `/detail` 立即把该用户并入 `on_time`
3. 不需要在详情页区分“真实签到”和“代签签到”，统一并入现有 `on_time`
4. 如果后续又同步到了该用户这节课的真实迟到打卡，最终结果仍以代签为准，保留在 `on_time`

## 目标

在不破坏现有“实时视图 + 最终快照”架构的前提下，引入一层可持久化的人工覆盖状态，使系统满足：

- 实时阶段可执行代签，不依赖 `attendance_records.record_id`
- 代签后，管理端实时详情立即反映到 `on_time`
- 最终结算时，人工代签覆盖真实迟到/未到结果
- 已 finalize 的历史节次仍可通过原有代签能力修正最终快照
- 为后续审计与扩展保留独立的人工操作记录

## 非目标

- 不改动群推送触发时机与推送文案
- 不在管理端详情中新增“人工代签”专门分组或特殊展示标签
- 不引入 Redis、内存状态或其他非持久化覆盖层
- 不在本次设计中扩展“人工请假”“撤销代签”等其他人工修正规则
- 不重构 `attendance_records` 为草稿/正式双状态模型

## 推荐方案

采用“独立人工覆盖表 + 实时/结算双端合并”的方案。

核心原则：

- `attendance_records` 继续表示某节次的最终聚合快照，不承担实时草稿职责
- 管理员实时代签写入独立的人工覆盖表，而不是要求先存在快照记录
- 实时详情在现场计算结果上叠加人工覆盖后返回
- 最终结算在落库前先合并人工覆盖，因此人工代签优先于后续真实迟到打卡
- 对于已经 finalize 的历史记录，保留现有按快照主键代签的路径，以最小改动兼容现有能力

这是和当前代码结构最匹配的方案：

- 不破坏 `attendance_records` 作为最终口径来源的语义
- 不需要把 `/detail` 的 `record_id` 变成实时阶段的强依赖
- 能自然表达“人工修正优先”的业务规则

## 方案取舍

### 方案 A：实时阶段提前创建 `attendance_records` 占位记录

做法：

- 实时查询或实时第一次代签时，先创建一条快照记录
- 后续代签继续复用 `record_id + SignForUsers`

优点：

- 复用现有代签入口表面上最直接

缺点：

- 混淆“最终快照”和“实时草稿”的语义
- 需要给 `attendance_records` 再增加草稿/正式状态，否则 `/snapshot` 与统计口径会被污染
- 不能自然表达“后续真实迟到打卡仍不覆盖人工代签”，最终仍需要额外覆盖层

不采用。

### 方案 B：独立人工覆盖表，实时查询和最终结算都合并覆盖

做法：

- 新增人工覆盖表，唯一键按 `tenant_id + date + section + user_id`
- 实时代签写入该表
- `/detail` 实时视图和最终结算都读取该表并执行相同合并规则

优点：

- 边界清晰，职责稳定
- 满足实时显示与最终覆盖两类需求
- 后续如果需要支持撤销或其他人工修正，也有自然扩展位

缺点：

- 需要新增数据模型、仓储和合并逻辑

本次采用。

### 方案 C：用缓存或进程内状态保存实时人工代签

优点：

- 实现速度快

缺点：

- 服务重启丢数据
- 多实例不一致
- 最终结算时容易漏合并
- 不适合作为管理员修正的事实来源

不采用。

## 数据模型

建议新增 `attendance_manual_overrides` 表，用于存储人工修正事实。

建议字段：

- `id`
- `tenant_id`
- `date`
- `week`（服务端根据 `date` 推导后落库，作为冗余字段保留，不属于接口请求契约）
- `section`
- `user_id`
- `override_type`
- `operator_id`
- `applied_at`
- `created_at`
- `updated_at`
- `deleted_at`

唯一约束建议为：

- `tenant_id + date + section + user_id`

本次只定义一种 `override_type`：

- `force_on_time`

设计原则：

- 表中保存的是“人工覆盖事实”，不是最终聚合结果
- 对同一用户同一节次重复代签应执行 upsert，保持幂等
- 即使详情页不展示人工标签，后台仍保留 `operator_id` 与 `applied_at` 便于审计

## 接口设计

### 详情接口

`GET /api/admin/attendance/record/detail`

行为调整：

- 当 `view_mode=current` 时：
  - 先按现有逻辑实时计算 `on_time / late / leave / not_arrived / rest_day / has_course`
  - 再加载当前节次的人工覆盖记录并合并
  - 合并后返回给前端
- 当 `view_mode=final` 时：
  - 仍以快照为主
  - 必须继续把人工覆盖合并进最终结果，确保 `/detail` 与 `/snapshot` 一致

详情接口不新增人工分组字段；被人工代签的用户直接并入 `on_time`。

### 代签接口

当前接口 `POST /api/admin/attendance/record/sign` 的请求体只有：

- `record_id`
- `target_user_ids`

为了兼容实时阶段，建议扩展为兼容式请求：

- 保留 `record_id`
- 新增 `date`

- 新增 `section`
- 保留 `target_user_ids`

建议规则：

- 如果传了有效 `record_id`，优先按快照记录反查槽位信息，再执行代签
- 如果未传 `record_id`，则要求 `date + section` 完整可用，由服务端推导 `week` 并直接对该节次写入人工覆盖
- 这样旧调用方不需要立刻改动，新调用方可以在实时阶段不依赖 `record_id`

### 快照读取接口

`GET /api/admin/attendance/record/snapshot`

建议行为：

- 继续从 `attendance_records` 读取最终快照
- 若已经存在人工覆盖表中的记录，也要在返回前合并覆盖，保证最终详情读取和快照读取一致

## 核心业务规则

### 人工覆盖优先级

对单个用户单个节次，优先级调整为：

- 有课：不进入考勤候选集合
- 休息日：不计入应到
- 请假：不允许通过本次“代签到 on_time”能力覆盖
- 人工代签 `force_on_time`：覆盖真实迟到和真实未到
- 真实准时 / 真实迟到 / 未到：仅在没有人工覆盖时使用

换句话说：

- `force_on_time` 会把用户最终归入 `on_time`
- 即使后续真实打卡时间落在迟到区间，最终仍保留在 `on_time`

### 实时详情合并规则

对实时计算出的结果执行后处理：

1. 加载当前节次人工覆盖记录
2. 只处理 `override_type=force_on_time`
3. 对每个命中的用户：
   - 从 `late` 中移除
   - 从 `not_arrived` 中移除
   - 若已在 `on_time` 中则保持不变
   - 若不在 `on_time` 中则追加到 `on_time`
4. 更新 `statistics.on_time / statistics.late / statistics.not_arrived`

`on_time.check_time` 的展示值建议直接使用 `applied_at`，这样不需要扩展现有详情结构。

### 最终结算合并规则

`FinalizeAttendanceRecord` 在现场计算出最终 `on_time / late / not_arrived` 后、写入 `attendance_records` 前，也执行同样的覆盖合并规则。

这样可以保证：

- 实时看到的结果和最终快照口径一致
- 人工代签优先于后续真实迟到打卡

### 已 finalize 节次的修正

对已经 finalize 且已有快照记录的节次，建议继续兼容现有路径：

- 若请求传入 `record_id`，在更新快照的同时，也 upsert 一条人工覆盖记录
- 这样历史快照被立即修正，后续任何重新读取快照或重算逻辑也都能保持一致

## 服务与职责边界

### `AttendanceRecordService`

需要新增或调整的职责：

- 提供按 `date + section` 的实时代签入口，不再强制依赖 `record_id`；`week` 由服务端根据日期推导或做一致性校验
- 将“加载人工覆盖并合并结果”封装为独立 helper，供实时详情、最终结算和快照读取复用
- 保持现有 `SignForUsers(record_id)` 路径兼容，但内部逐步收口到统一覆盖逻辑

### 新仓储

建议新增 `AttendanceManualOverrideRepository`，职责只包括：

- `UpsertForceOnTime(...)`
- `ListByDateSection(...)`


### Handler

`AttendanceRecordHandler.SignForUser` 需要支持两类调用：

- 历史/快照代签：传 `record_id`
- 实时节次代签：传 `date + section`

Handler 只负责参数校验和分流，不承担覆盖规则判断。

## 兼容策略

### 对现有调用方的兼容

- 旧前端若继续传 `record_id`，行为保持可用
- 新前端若在实时详情页中发起代签，可以改为传 `date + section`
- agent 现有“补签”链路当前通过“日期 + 节次找快照记录 ID”工作；若目标节次尚未 finalize，则可逐步切换为直接传 `date + section`

### 对返回结构的兼容

- `/detail` 继续使用现有 `on_time` 列表，不新增 `manual_on_time`
- 前端无需为“代签”新增特殊展示逻辑
- `record_id` 可以继续保留当前语义：实时视图可能为 `0`，不再把“实时可代签”建立在它之上

## 风险与约束

### 1. 请假/休息日/有课用户被误代签

这是最需要防止的错误覆盖。

实现时必须显式校验：

- 用户必须属于当前节次 `should_attend`
- 用户不能位于 `leave`
- 用户不能位于 `rest_day`
- 用户不能位于 `has_course`

否则应返回业务错误，而不是静默写覆盖。

### 2. 同一用户重复代签

重复代签应视为幂等成功。

否则前端和管理员体验会很差，也不利于重试。

### 3. 最终快照与实时详情不一致

如果实时详情合并了人工覆盖，但最终结算没有合并，就会导致管理员看到的实时结果和最终结果冲突。

因此覆盖合并逻辑必须抽成同一个 helper，在两个路径共用。

### 4. 历史代签后的重新读取一致性

如果已 finalize 的节次只改了 `attendance_records` 而没落人工覆盖记录，后续一旦重算或改为统一走覆盖层，就可能丢失历史人工修正。

因此历史代签也建议同时 upsert 覆盖表。

## 测试策略

至少覆盖以下场景：

1. 实时节次在没有 `record_id` 时也能成功代签
2. 实时代签后再次调用 `/detail`，用户立即从 `late` 或 `not_arrived` 转入 `on_time`
3. 同一用户重复代签是幂等成功
4. 请假、休息日、有课用户代签被拒绝
5. 最终结算时存在人工代签覆盖，最终快照写入 `on_time`，而不是 `late` / `not_arrived`
6. 人工代签后，即使后续出现真实迟到打卡，最终仍保持 `on_time`
7. 已 finalize 节次通过旧 `record_id` 路径代签后，`/detail` 与 `/snapshot` 返回一致
8. 部门过滤下，人工覆盖用户若不属于当前部门，不应错误泄露到过滤结果中

## 实施边界

本设计对应的实现范围包括：

- 新增人工覆盖模型与仓储
- 扩展代签请求结构与 handler 校验
- 在实时详情、最终结算、快照读取中合并人工覆盖
- 保留现有快照代签能力并向统一覆盖逻辑收口
- 补充上述关键回归测试

不包括：

- 群推送文案或时机调整
- 管理端页面交互重设计
- 新的人工覆盖类型（如强制请假、撤销代签）
- 历史数据迁移或回填


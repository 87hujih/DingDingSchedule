# Attendance Realtime Manual Sign Override Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让实时考勤阶段支持代签，并在 `/detail` 中立即反映到 `on_time`，同时保证最终结算和历史快照仍以人工代签结果为准。

**Architecture:** 保持 `attendance_records` 继续承担最终快照职责，新增独立的人工覆盖持久层来表达 `force_on_time` 事实。服务层把“代签入口解析”和“人工覆盖结果合并”收口为复用 helper，由实时详情、最终结算和历史快照读取共享同一套规则，避免口径漂移。

**Tech Stack:** Go, GORM, Gin, SQLite 测试夹具, 现有 `AttendanceRecordService`

---

## Environment Note

- 在当前 Codex Windows 沙箱中，`go test` 前先初始化本地缓存：

Run:
`if (-not (Test-Path .\.cache\go-build)) { New-Item -ItemType Directory -Force .\.cache\go-build | Out-Null }; $env:GOCACHE = (Convert-Path .\.cache\go-build)`

## File Map

- Create: `docs/superpowers/plans/2026-03-21-attendance-realtime-manual-sign-override-plan.md`
  - 记录本次实施步骤。
- Create: `internal/model/attendance_manual_override.go`
  - 定义人工代签覆盖模型和表名。
- Create: `internal/repository/attendance_manual_override_repository.go`
  - 提供人工覆盖记录的 upsert 和按节次查询能力。
- Create: `internal/dto/sign_attendance_test.go`
  - 锁定“`record_id` 或 `date + section` 二选一”的请求契约。
- Modify: `internal/dto/sign_attendance.go`
  - 扩展代签请求结构并提供请求校验 helper。
- Modify: `internal/repository/repository.go`
  - 把人工覆盖仓储接入统一仓储集合。
- Modify: `inits/database.go`
  - 将人工覆盖表纳入 `AutoMigrate`。
- Modify: `internal/service/attendance_record_service.go`
  - 注入人工覆盖仓储，新增实时节次代签入口、覆盖合并 helper，并让实时详情、最终结算、快照读取和历史代签共享同一套覆盖逻辑。
- Modify: `internal/service/service.go`
  - 在主服务装配处注入人工覆盖仓储。
- Modify: `internal/app/app.go`
  - 在 agent / scheduler 用到的 `AttendanceRecordService` 构造路径里注入人工覆盖仓储。
- Modify: `internal/service/attendance_record_service_test.go`
  - 增加实时代签、覆盖优先级、历史快照一致性的失败回归测试，并更新 sqlite 夹具。
- Modify: `internal/handler/attendance_record_handler.go`
  - 在绑定 JSON 后调用请求校验 helper，再委派 service。
- Modify: `tasks/todo.md`
  - 记录实施状态、验证命令和复盘。

### Task 1: 锁定代签请求契约

**Files:**
- Create: `internal/dto/sign_attendance_test.go`
- Modify: `internal/dto/sign_attendance.go`

- [ ] **Step 1: 写请求契约失败测试**

新增 `TestSignForUserRequestValidate`，至少覆盖：
- 传 `record_id + target_user_ids` 时通过
- 不传 `record_id` 但传 `date + section + target_user_ids` 时通过
- `record_id` 缺失且 `date` 或 `section` 不完整时失败
- `target_user_ids` 为空时失败

- [ ] **Step 2: 运行 DTO 定向测试确认失败**

Run: `go test ./internal/dto -run TestSignForUserRequestValidate -v`
Expected: FAIL，提示缺少 `Validate` / 兼容字段未定义。

- [ ] **Step 3: 最小化扩展 `SignForUserRequest`**

实现：
- 新增 `Date string` 和 `Section int`
- 保留 `RecordID` 和 `TargetUserIDs`
- 提供 `Validate() error`，规则固定为“`record_id` 或 `date + section` 至少满足一种”

- [ ] **Step 4: 重跑 DTO 定向测试确认通过**

Run: `go test ./internal/dto -run TestSignForUserRequestValidate -v`
Expected: PASS

### Task 2: 先写 service 级失败回归测试

**Files:**
- Modify: `internal/service/attendance_record_service_test.go`

- [ ] **Step 1: 扩展 sqlite 夹具，预留人工覆盖模型和仓储注入位**

在 `newAttendanceRealtimeFixture` 的 `AutoMigrate` 和 `AttendanceRecordService` 构造里预留：
- `model.AttendanceManualOverride`
- `repository.NewAttendanceManualOverrideRepository(db)`
- `AttendanceRecordService` 上的人工覆盖仓储字段

- [ ] **Step 2: 写“实时节次无 record_id 也能代签并立即反映到 detail”失败测试**

建议命名：`TestSignForUsersSupportsRealtimeDateSectionAndDetailShowsOverride`

断言：
- `SignForUsers` 只传 `date + section` 也能成功
- 再次调用 `GetAttendanceDetail` 时，该用户已经从 `late`/`not_arrived` 转入 `on_time`

- [ ] **Step 3: 写“请假 / 休息日 / 有课用户不能被实时强制签到”失败测试**

建议命名：`TestSignForUsersRejectsRealtimeOverridesForNonAttendTargets`

断言：
- 用户不在当前可签到候选集时返回业务错误
- 不产生人工覆盖记录

- [ ] **Step 4: 写“最终结算仍以人工代签优先于真实迟到打卡”失败测试**

建议命名：`TestFinalizeAttendanceRecordKeepsManualOverrideOverLatePunch`

断言：
- 先写人工覆盖，再让钉钉返回迟到打卡
- `FinalizeAttendanceRecord` 落库后，该用户在快照里仍位于 `on_time`，不在 `late`

- [ ] **Step 5: 写“已 finalize 节次走旧 record_id 路径后 detail 和 snapshot 一致”失败测试**

建议命名：`TestSignForUsersWithRecordIDKeepsSnapshotAndDetailConsistent`

断言：
- 历史快照代签后，`GetAttendanceRecordFromDB` 和 `/detail` 最终视图口径一致
- 人工覆盖记录同步存在

- [ ] **Step 6: 运行 service 定向测试确认失败**

Run: `go test ./internal/service -run "Test(SignForUsersSupportsRealtimeDateSectionAndDetailShowsOverride|SignForUsersRejectsRealtimeOverridesForNonAttendTargets|FinalizeAttendanceRecordKeepsManualOverrideOverLatePunch|SignForUsersWithRecordIDKeepsSnapshotAndDetailConsistent)" -v`
Expected: FAIL，原因应集中在缺少人工覆盖模型/仓储/合并逻辑。

### Task 3: 补人工覆盖持久层和依赖注入

**Files:**
- Create: `internal/model/attendance_manual_override.go`
- Create: `internal/repository/attendance_manual_override_repository.go`
- Modify: `internal/repository/repository.go`
- Modify: `inits/database.go`
- Modify: `internal/service/attendance_record_service.go`
- Modify: `internal/service/service.go`
- Modify: `internal/app/app.go`
- Modify: `internal/service/attendance_record_service_test.go`

- [ ] **Step 1: 新增人工覆盖模型**

实现字段：
- `TenantID`
- `Date`
- `Week`
- `Section`
- `UserID`
- `OverrideType`
- `OperatorID`
- `AppliedAt`
- 标准时间戳 / 软删除字段

并把唯一键固定为 `tenant_id + date + section + user_id`。

- [ ] **Step 2: 新增人工覆盖仓储**

最小接口只保留：
- `UpsertForceOnTime(...)`
- `ListByDateSection(ctx, date, section)`

不要在首版加入撤销接口。

- [ ] **Step 3: 接入全局仓储和自动迁移**

修改：
- `internal/repository/repository.go`
- `inits/database.go`

确保应用启动和 sqlite 测试夹具都能拿到新表。

- [ ] **Step 4: 把新仓储注入 `AttendanceRecordService` 的所有构造路径**

修改：
- `internal/service/attendance_record_service.go`
- `internal/service/service.go`
- `internal/app/app.go`
- `internal/service/attendance_record_service_test.go`

确保主服务、agent wiring 用到的 service、scheduler 路径和测试夹具都不会遗漏注入。

- [ ] **Step 5: 重跑定向 service 测试，确认编译通过但业务断言仍是红的**

Run: `go test ./internal/service -run "Test(SignForUsersSupportsRealtimeDateSectionAndDetailShowsOverride|SignForUsersRejectsRealtimeOverridesForNonAttendTargets|FinalizeAttendanceRecordKeepsManualOverrideOverLatePunch|SignForUsersWithRecordIDKeepsSnapshotAndDetailConsistent)" -v`
Expected: 仍 FAIL，但不再是缺表/缺字段/缺构造参数这类编译问题。

### Task 4: 实现统一的代签解析与覆盖合并逻辑

**Files:**
- Modify: `internal/service/attendance_record_service.go`
- Modify: `internal/dto/sign_attendance.go`
- Modify: `internal/handler/attendance_record_handler.go`

- [ ] **Step 1: 在 handler 中接入请求校验 helper**

让 `AttendanceRecordHandler.SignForUser` 在 `ShouldBindJSON` 后调用 `req.Validate()`，避免把明显非法请求丢给 service。

- [ ] **Step 2: 在 service 中实现“槽位解析” helper**

统一支持两条入口：
- `record_id` 路径：按快照记录反查 `date / week / section`
- 实时路径：按 `date + section` 解析节次，`week` 由服务端根据日期推导或校验

- [ ] **Step 3: 实现“写人工覆盖” helper**

规则：
- 只支持 `force_on_time`
- 仅允许当前节次 `should_attend` 且不在 `leave / rest_day / has_course` 的用户写入
- 重复代签走 upsert，保持幂等
- 若请求本身是 `record_id` 路径，除了更新快照，也同步 upsert 覆盖记录

- [ ] **Step 4: 实现“把人工覆盖合并进详情结果” helper**

统一执行：
- 从 `late` 移除
- 从 `not_arrived` 移除
- 追加到 `on_time`
- 重算 `statistics.on_time / statistics.late / statistics.not_arrived`
- `on_time.check_time` 直接使用 `applied_at`

- [ ] **Step 5: 在 4 条读取/写入路径里复用同一 helper**

至少覆盖：
- `GetAttendanceDetail` 的实时分支
- `GetAttendanceRecordFromDB`
- `FinalizeAttendanceRecord`
- `SignForUsers` 的历史快照修正路径

- [ ] **Step 6: 重跑定向测试确认通过**

Run: `go test ./internal/service -run "Test(SignForUsersSupportsRealtimeDateSectionAndDetailShowsOverride|SignForUsersRejectsRealtimeOverridesForNonAttendTargets|FinalizeAttendanceRecordKeepsManualOverrideOverLatePunch|SignForUsersWithRecordIDKeepsSnapshotAndDetailConsistent)" -v`
Expected: PASS

### Task 5: 做范围验证并更新记录

**Files:**
- Modify: `tasks/todo.md`

- [ ] **Step 1: 运行 DTO / handler / service 范围验证**

Run:
- `go test ./internal/dto -v`
- `go test ./internal/handler ./internal/service -v`

- [ ] **Step 2: 做一次针对实时 / 最终两类链路的回归验证**

Run: `go test ./internal/service -run "Test(GetAttendanceDetailReturnsCurrentViewBeforeFinalize|GetAttendanceDetailReturnsFinalSnapshotAfterFinalize|FinalizeAttendanceRecordPersistsLateAndNotArrived|SignForUsersSupportsRealtimeDateSectionAndDetailShowsOverride|FinalizeAttendanceRecordKeepsManualOverrideOverLatePunch|SignForUsersWithRecordIDKeepsSnapshotAndDetailConsistent)" -v`

- [ ] **Step 3: 更新 `tasks/todo.md` 复盘**

记录：
- 新增人工覆盖表和仓储
- 代签请求契约变化
- 实时详情 / 最终结算 / 快照读取的合并行为
- 实际执行过的验证命令

- [ ] **Step 4: 检查最终差异**

Run:
- `git diff -- internal/model/attendance_manual_override.go internal/repository/attendance_manual_override_repository.go internal/repository/repository.go inits/database.go internal/dto/sign_attendance.go internal/dto/sign_attendance_test.go internal/handler/attendance_record_handler.go internal/service/attendance_record_service.go internal/service/attendance_record_service_test.go internal/service/service.go internal/app/app.go tasks/todo.md docs/superpowers/specs/2026-03-21-attendance-realtime-manual-sign-override-design.md docs/superpowers/plans/2026-03-21-attendance-realtime-manual-sign-override-plan.md`
- `git status --short`

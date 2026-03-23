# Attendance Carry-Forward Late Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让“连续节次顺延”在现有触发条件不变的前提下，同时支持上一节 `on_time` 和 `late` 用户顺延到当前节 `on_time`。

**Architecture:** 保持 `applyCarryForward` 作为唯一顺延入口，不改实时打卡、最终结算、快照结构和统计优先级。把“上一节哪些人可顺延”提取成一个小 helper，由它统一解析上一节快照里的 `OnTimeIDs` 和 `LateIDs` 并按用户去重，`applyCarryForward` 继续只负责触发条件和追加结果。

**Tech Stack:** Go, GORM, 现有 `AttendanceRecordService`, SQLite 测试夹具

---

## File Map

- Create: `docs/superpowers/plans/2026-03-23-attendance-carry-forward-late-plan.md`
  - 记录本次顺延改造的实施步骤。
- Modify: `internal/service/attendance_record_service.go`
  - 新增“上一节可顺延用户” helper，并让 `applyCarryForward` 同时消费上一节 `OnTimeIDs` 和 `LateIDs`。
- Modify: `internal/service/attendance_record_service_test.go`
  - 增加“上一节迟到也会顺延”的失败回归测试，并保护现有顺延边界。
- Modify: `tasks/todo.md`
  - 更新执行状态、验证命令与复盘。

### Task 1: 先锁定新顺延行为

**Files:**
- Modify: `internal/service/attendance_record_service_test.go`

- [ ] **Step 1: 写“上一节迟到也会顺延”的失败测试**

新增定向测试，建议命名：
`TestGetAttendanceDetailCarryForwardIncludesPreviousLateUsers`

场景约束：
- 当前节为第 2 节或更后节次
- `MaxCarryForwardGapMinutes` 大于两节间隔
- 上一节已存在快照
- 目标用户上一节只出现在 `LateIDs`
- 目标用户当前节属于 `shouldAttend`
- 目标用户当前节没有自己的实时 `on_time`

断言：
- 修复前，目标用户不会进入当前节 `on_time`
- 期望行为下，目标用户应进入当前节 `on_time`

- [ ] **Step 2: 增加一个保护性断言**

在同一测试或相邻测试中锁定：
- 当前节不在 `shouldAttend` 的用户不会因为上一节迟到而被顺延
- 已在当前节 `on_time` 的用户不会被重复追加

- [ ] **Step 3: 运行定向测试确认先红**

Run:
`go test ./internal/service -run TestGetAttendanceDetailCarryForwardIncludesPreviousLateUsers -v`

Expected:
FAIL，失败原因应集中在“当前实现未把上一节 `LateIDs` 纳入顺延来源”。

### Task 2: 以最小改动扩展顺延来源

**Files:**
- Modify: `internal/service/attendance_record_service.go`

- [ ] **Step 1: 提取上一节“可顺延来源” helper**

新增一个内部 helper，职责只包括：
- 读取上一节 `attendance_records`
- 解析 `OnTimeIDs`
- 解析 `LateIDs`
- 按用户 ID 去重后返回可顺延用户集合

不要把当前节 `shouldAttend`、去重追加或其它业务判断塞进 helper。

- [ ] **Step 2: 保持 `applyCarryForward` 其它条件不变**

保留现有逻辑：
- 仅第 2 节及以上才参与顺延
- `MaxCarryForwardGapMinutes > 0`
- 节次间隔超过阈值不顺延
- 上一节快照不存在时不顺延
- 仅当前节 `shouldAttend` 用户可顺延
- 当前节已在 `on_time` 的用户不重复追加

- [ ] **Step 3: 让 `applyCarryForward` 消费 helper 返回结果**

实现结果：
- 上一节 `OnTimeIDs` 用户继续顺延
- 上一节 `LateIDs` 用户新增顺延
- 顺延落点仍然是当前节 `on_time`
- 使用上一节存储的 `check_time`

- [ ] **Step 4: 重跑定向测试确认转绿**

Run:
`go test ./internal/service -run TestGetAttendanceDetailCarryForwardIncludesPreviousLateUsers -v`

Expected:
PASS

### Task 3: 做范围回归并更新记录

**Files:**
- Modify: `tasks/todo.md`

- [ ] **Step 1: 运行顺延与详情相关回归测试**

Run:
`go test ./internal/service -run "Test(GetAttendanceDetailCarryForwardIncludesPreviousLateUsers|GetAttendanceDetailReturnsCurrentViewBeforeFinalize|GetAttendanceDetailReturnsFinalSnapshotAfterFinalize|FinalizeAttendanceRecordPersistsLateAndNotArrived|AttendanceDetailPrioritizesRestDayAndLeaveOverHasCourse|GetAttendanceDetailDeduplicatesMultiplePunchesFromSameUser)" -v`

Expected:
PASS

- [ ] **Step 2: 运行打卡状态接口相关回归测试**

Run:
`go test ./internal/service -run "Test(SlotAttendanceStatusPrioritizesRestDayAndLeaveOverHasCourse|GetAttendanceDetailCarryForwardIncludesPreviousLateUsers)" -v`

Expected:
PASS

- [ ] **Step 3: 更新 `tasks/todo.md` 复盘**

记录：
- 顺延来源从上一节 `OnTimeIDs` 扩展到 `OnTimeIDs + LateIDs`
- 其它顺延前置条件和统计口径未改
- 实际执行过的验证命令

- [ ] **Step 4: 检查最终差异**

Run:
- `git diff -- internal/service/attendance_record_service.go internal/service/attendance_record_service_test.go tasks/todo.md docs/superpowers/specs/2026-03-23-attendance-carry-forward-late-design.md docs/superpowers/plans/2026-03-23-attendance-carry-forward-late-plan.md`
- `git status --short`

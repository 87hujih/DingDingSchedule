# 假期模式下的代码执行逻辑详解

## 概述

当系统处于**假期模式**（`schedule_settings.current_mode = 'holiday'`）时，考勤接口会自动使用假期作息配置来计算时间窗口和考勤状态。

## 完整执行流程

### 1. 接口调用

**请求示例**:
```http
GET /api/attendance/slots/status?date=2024-03-15&week=1&section=2&dept_ids=1,2
```

**参数说明**:
- `date`: 日期（2024-03-15）
- `week`: 周次（1）
- `section`: 节次（2，表示第2节课）
- `dept_ids`: 部门ID列表（可选）

---

### 2. Handler 层处理

**文件**: `internal/handler/attendance_handler.go:25-67`

```go
func (h *AttendanceHandler) SlotAttendanceStatus(ctx *gin.Context) {
    // 1. 获取查看者ID（从JWT中提取）
    viewerID, err := h.getViewerID(ctx)

    // 2. 解析请求参数
    params, err := ParseAttendanceQueryParams(ctx)  // date, week, section
    deptIDs, err := ParseDeptIDsQuery(ctx.Query("dept_ids"))

    // 3. 校验参数
    ValidateWeekDate(ctx, h.semesterSrv, params.Date, params.Week)

    // 4. 调用服务层
    result, err := h.attendanceSrv.GetSlotAttendanceStatus(
        ctx.Request.Context(),
        viewerID,
        params.Date,
        params.Week,
        params.Section,
        deptIDs,
    )

    // 5. 返回响应
    response.OK(ctx, result)
}
```

---

### 3. Service 层 - 获取作息配置

**文件**: `internal/service/attendance_service.go:48-99`

#### 3.1 调用 `resolveActivePeriods(ctx)`

```go
func (s *AttendanceService) GetSlotAttendanceStatus(...) {
    // 从数据库获取当前生效的作息时间配置
    periods := s.resolveActivePeriods(ctx)
    if len(periods) == 0 || section > len(periods) {
        return nil, response.ErrInvalidParamWithMsg("节次无效或作息配置缺失")
    }

    // 使用获取到的作息配置计算时间窗口
    sessionStart, sessionEnd, err := scheduleutil.CalculateSlotTime(date, section, periods)
    ...
}
```

#### 3.2 `resolveActivePeriods` 方法

**文件**: `internal/service/attendance_service.go:421-430`

```go
func (s *AttendanceService) resolveActivePeriods(ctx context.Context) []config.Period {
    if s.schedulePeriodSrv != nil {
        // 优先从数据库获取
        periods, err := s.schedulePeriodSrv.GetActivePeriods(ctx)
        if err == nil && len(periods) > 0 {
            return periods  // 返回数据库中的配置
        }
    }
    // 回退到配置文件
    return s.scheduleCfg.Periods
}
```

---

### 4. SchedulePeriodService - 获取当前模式配置

**文件**: `internal/service/schedule_period_service.go:32-56`

```go
func (s *SchedulePeriodService) GetActivePeriods(ctx context.Context) ([]config.Period, error) {
    // 从数据库获取作息配置
    dbPeriods, err := s.periodRepo.ListActive(ctx)
    if err != nil {
        return nil, err
    }

    if len(dbPeriods) > 0 {
        // 转换为 config.Period 格式
        result := make([]config.Period, len(dbPeriods))
        for i, p := range dbPeriods {
            result[i] = config.Period{
                Name:  p.Name,
                Start: trimTimeSeconds(p.StartTime),  // "09:00:00" -> "09:00"
                End:   trimTimeSeconds(p.EndTime),    // "10:30:00" -> "10:30"
            }
        }
        return result, nil
    }

    // 回退到配置文件
    if s.fallbackConfig != nil && len(s.fallbackConfig.Periods) > 0 {
        return s.fallbackConfig.Periods, nil
    }

    return []config.Period{}, nil
}
```

---

### 5. Repository 层 - 数据库查询

#### 5.1 `ListActive` - 获取当前模式的配置

**文件**: `internal/repository/schedule_period_repository.go:49-57`

```go
func (r *schedulePeriodRepository) ListActive(ctx context.Context) ([]*model.SchedulePeriod, error) {
    // 第一步：获取当前模式
    setting, err := r.settingRepo.GetByTenantID(ctx)
    if err != nil {
        // 如果获取失败，默认使用上学模式
        return r.ListActiveByMode(ctx, model.ScheduleModeSchool)
    }

    // 第二步：根据当前模式获取作息配置
    return r.ListActiveByMode(ctx, setting.CurrentMode)
}
```

#### 5.2 获取租户的作息设置

**文件**: `internal/repository/schedule_setting_repository.go:37-41`

```go
func (r *scheduleSettingRepository) GetByTenantID(ctx context.Context) (*model.ScheduleSetting, error) {
    var setting model.ScheduleSetting
    // 自动根据 context 中的 tenant_id 过滤
    err := r.db.WithContext(ctx).First(&setting).Error
    return &setting, err
}
```

**SQL 查询**（由 GORM 自动添加 tenant_id 过滤）:
```sql
SELECT * FROM schedule_settings
WHERE tenant_id = ?
LIMIT 1
```

**假期模式下的查询结果**:
```json
{
  "id": 1,
  "tenant_id": 1,
  "current_mode": "holiday",  // 关键字段
  "attendance_enabled": true,
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-03-01T00:00:00Z"
}
```

#### 5.3 根据模式获取作息配置

**文件**: `internal/repository/schedule_period_repository.go:40-47`

```go
func (r *schedulePeriodRepository) ListActiveByMode(ctx context.Context, mode string) ([]*model.SchedulePeriod, error) {
    var periods []*model.SchedulePeriod
    err := r.db.WithContext(ctx).
        Where("mode = ? AND is_active = ?", mode, true).
        Order("sort_order ASC").
        Find(&periods).Error
    return periods, err
}
```

**假期模式下的 SQL 查询**:
```sql
SELECT * FROM schedule_periods
WHERE tenant_id = ?           -- 自动添加
  AND mode = 'holiday'        -- 假期模式
  AND is_active = true        -- 启用状态
  AND deleted_at IS NULL      -- 未删除
ORDER BY sort_order ASC
```

**假期模式下的查询结果示例**:
```json
[
  {
    "id": 11,
    "tenant_id": 1,
    "mode": "holiday",
    "name": "第1节",
    "start_time": "09:00:00",
    "end_time": "10:30:00",
    "sort_order": 1,
    "is_active": true
  },
  {
    "id": 12,
    "tenant_id": 1,
    "mode": "holiday",
    "name": "第2节",
    "start_time": "14:00:00",
    "end_time": "15:30:00",
    "sort_order": 2,
    "is_active": true
  }
]
```

---

### 6. 时间窗口计算

**文件**: `pkg/scheduleutil/scheduleutil.go:19-52`

假设请求参数为 `section=2`（第2节），使用假期模式的配置：

```go
func CalculateSlotTime(date time.Time, section int, periods []config.Period) (time.Time, time.Time, error) {
    // section=2，取 periods[1]
    period := periods[section-1]  // periods[1] = {Name: "第2节", Start: "14:00", End: "15:30"}

    // 解析时间
    startClock, _ := time.Parse("15:04", period.Start)  // 14:00
    endClock, _ := time.Parse("15:04", period.End)      // 15:30

    // 组合日期和时间
    // date = 2024-03-15
    slotStart := time.Date(2024, 3, 15, 14, 0, 0, 0, loc)  // 2024-03-15 14:00:00
    slotEnd := time.Date(2024, 3, 15, 15, 30, 0, 0, loc)   // 2024-03-15 15:30:00

    return slotStart, slotEnd, nil
}
```

**计算结果**:
- `sessionStart`: `2024-03-15 14:00:00`
- `sessionEnd`: `2024-03-15 15:30:00`

---

### 7. 计算应到人员

**文件**: `internal/service/attendance_service.go:158-182`

```go
func (s *AttendanceService) computeShouldArriveUsersByDeptFilter(...) {
    // 1. 获取候选人员（参与考勤的用户）
    activeUsers, err := s.listActiveUsersByDeptIDs(ctx, deptIDs)
    // 结果：所有 status=1 的用户（可按部门过滤）

    // 2. 获取忙碌人员（本节有课的用户）
    busyUsers, err := s.busyUserSetForSlot(ctx, activeUsers, dayOfWeek, section, week)
    // 查询条件：day_of_week=5（周五）, section=2, week=1
    // 结果：在周五第2节有课，且 week_list 包含第1周的用户

    // 3. 应到 = 候选 - 忙碌
    shouldArriveUsers := filterUsersByExclude(activeUsers, busyUsers)

    return shouldArriveUsers, toAttendanceUserItems(shouldArriveUsers, nil), nil
}
```

**应到人员计算公式**:
```
应到人员 = (全体参与考勤用户 或 指定部门用户) - 本节有课用户
```

---

### 8. 计算请假人员

**文件**: `internal/service/attendance_service.go:232-288`

```go
func (s *AttendanceService) computeOnLeaveUserItems(
    ctx context.Context,
    users []model.User,
    sessionStart time.Time,  // 2024-03-15 14:00:00
    sessionEnd time.Time,    // 2024-03-15 15:30:00
) ([]dto.CourseAttendanceUserItem, error) {
    // 1. 收集用户ID
    userIDs := []uint{1, 2, 3, ...}

    // 2. 从数据库查询请假记录
    leaveRecords, err := s.leaveApprovalRepo.ListApprovedByUserIDs(
        ctx,
        userIDs,
        sessionStart,  // 2024-03-15 14:00:00
        sessionEnd,    // 2024-03-15 15:30:00
    )
    // SQL: SELECT * FROM leave_approvals
    //      WHERE user_id IN (1,2,3,...)
    //        AND approve_status = 'agree'
    //        AND start_at < '2024-03-15 15:30:00'
    //        AND end_at > '2024-03-15 14:00:00'

    // 3. 构建请假用户集合
    onLeaveSet := make(map[uint]struct{})
    for _, rec := range leaveRecords {
        // 二次确认时间重叠（半开区间）
        if timeOverlaps(rec.StartAt, rec.EndAt, sessionStart, sessionEnd) {
            onLeaveSet[rec.UserID] = struct{}{}
        }
    }

    // 4. 返回请假用户列表
    items := []dto.CourseAttendanceUserItem{}
    for _, u := range users {
        if _, ok := onLeaveSet[u.ID]; ok {
            items = append(items, dto.CourseAttendanceUserItem{
                ID:     u.ID,
                Name:   u.Name,
                Avatar: u.Avatar,
                Phone:  u.Phone,
            })
        }
    }
    return items, nil
}
```

**时间重叠判断**（半开区间）:
```go
func timeOverlaps(startA, endA, startB, endB time.Time) bool {
    // [startA, endA) 与 [startB, endB) 是否重叠
    return startA.Before(endB) && endA.After(startB)
}
```

**示例**:
- 请假时间：`2024-03-15 13:00:00` ~ `2024-03-15 16:00:00`
- 课节时间：`2024-03-15 14:00:00` ~ `2024-03-15 15:30:00`
- 判断：`13:00 < 15:30` && `16:00 > 14:00` = `true` → **有重叠，算请假**

---

### 9. 返回响应

**文件**: `internal/service/attendance_service.go:84-91`

```go
return &dto.SlotAttendanceStatusResponse{
    Date:         "2024-03-15",
    Week:         1,
    DayOfWeek:    5,  // 周五
    Section:      2,  // 第2节
    ShouldArrive: shouldArriveItems,  // 应到人员列表
    OnLeave:      onLeaveItems,       // 请假人员列表
}, nil
```

**响应示例**:
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "date": "2024-03-15",
    "week": 1,
    "day_of_week": 5,
    "section": 2,
    "should_arrive": [
      {
        "id": 1,
        "name": "张三",
        "avatar": "https://...",
        "phone": "13800138000"
      },
      {
        "id": 3,
        "name": "王五",
        "avatar": "https://...",
        "phone": "13900139000"
      }
    ],
    "on_leave": [
      {
        "id": 2,
        "name": "李四",
        "avatar": "https://...",
        "phone": "13800138001"
      }
    ]
  }
}
```

---

## 假期模式 vs 上学模式对比

### 数据库配置示例

#### schedule_settings 表
```sql
-- 假期模式
UPDATE schedule_settings SET current_mode = 'holiday' WHERE tenant_id = 1;

-- 上学模式
UPDATE schedule_settings SET current_mode = 'school' WHERE tenant_id = 1;
```

#### schedule_periods 表

| id | tenant_id | mode | name | start_time | end_time | sort_order |
|----|-----------|------|------|------------|----------|------------|
| 1  | 1         | school | 第1节 | 08:00:00 | 09:40:00 | 1 |
| 2  | 1         | school | 第2节 | 10:00:00 | 11:40:00 | 2 |
| 11 | 1         | holiday | 第1节 | 09:00:00 | 10:30:00 | 1 |
| 12 | 1         | holiday | 第2节 | 14:00:00 | 15:30:00 | 2 |

### 执行结果对比

**请求**: `GET /api/attendance/slots/status?date=2024-03-15&section=2`

| 模式 | 使用的配置 | 时间窗口 | 说明 |
|------|-----------|---------|------|
| **上学模式** | `mode='school'` 的记录 | 10:00-11:40 | 使用上学作息时间 |
| **假期模式** | `mode='holiday'` 的记录 | 14:00-15:30 | 使用假期作息时间 |

---

## 关键特性

### 1. 自动模式识别
- 系统自动从 `schedule_settings.current_mode` 读取当前模式
- 无需在接口参数中指定模式

### 2. 多租户隔离
- 所有查询自动按 `tenant_id` 过滤
- 不同租户可以有不同的作息模式

### 3. 配置回退机制
- 优先使用数据库配置
- 如果数据库查询失败或无配置，回退到 YAML 配置文件
- 保证系统稳定性

### 4. 动态切换
- 管理员可以随时切换作息模式
- 切换后立即生效，无需重启服务
- 考勤调度器每10秒自动重载配置

---

## 假期模式的特殊处理

### 考勤调度器中的假期模式逻辑

**文件**: `internal/scheduler/attendance_scheduler.go:352-367`

```go
// 获取当前周数
week, err := s.semesterSrv.GetCurrentWeek(ctx)
if err != nil {
    // 假期模式下，不受学期配置限制，使用周数 0 继续执行
    if currentMode == "holiday" {
        s.logger.Infow("假期模式：忽略学期配置，继续执行考勤",
            "tenantId", tenantID,
            "tenantName", tenant.Name,
        )
        week = 0  // 假期模式使用周数 0
    } else {
        // 上学模式下，如果没有学期配置则返回错误
        s.logger.Warnw("获取当前周数失败", ...)
        return
    }
}
```

**说明**:
- 假期模式下，即使没有配置学期，考勤调度器仍会继续执行
- 使用 `week = 0` 作为占位符
- 上学模式下，必须有有效的学期配置才能执行考勤

---

## 总结

假期模式下的执行逻辑：

1. ✅ **自动识别模式**：从 `schedule_settings.current_mode` 读取
2. ✅ **动态加载配置**：查询 `schedule_periods` 表中 `mode='holiday'` 的记录
3. ✅ **正确计算时间窗口**：使用假期作息时间
4. ✅ **准确判定请假**：基于正确的时间窗口进行重叠判断
5. ✅ **多租户隔离**：每个租户独立配置和切换
6. ✅ **配置回退**：数据库失败时使用 YAML 配置
7. ✅ **特殊处理**：假期模式下忽略学期配置限制

整个流程完全自动化，管理员只需在数据库中切换 `current_mode` 字段，系统即可自动使用对应的作息配置。

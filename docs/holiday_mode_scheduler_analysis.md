# 假期模式下自动考勤调度器分析

## 1. 系统架构概述

系统支持两种作息模式，通过 `schedule_settings` 表跟踪当前模式：

```go
// internal/model/schedule_period.go (lines 5-8)
const (
    ScheduleModeSchool  = "school"  // 上学模式
    ScheduleModeHoliday = "holiday" // 假期模式
)
```

### 核心数据模型

- **`schedule_settings`**: 存储每个租户的当前模式 (`CurrentMode` 字段)
- **`schedule_periods`**: 存储各模式的时间段配置，通过 `mode` 字段区分

---

## 2. 调度器核心实现

**主文件**: `internal/scheduler/attendance_scheduler.go`

### 2.1 调度器初始化 (`Start` 方法, lines 64-85)

```go
func (s *AttendanceScheduler) Start() {
    // 1. 创建 cron 实例，使用本地时区
    s.cron = cron.New(cron.WithLocation(time.Local))

    // 2. 加载所有租户的调度配置
    s.reloadAllTenantSchedules()

    // 3. 注册定期重载任务（每5分钟检测配置变化）
    s.cron.AddFunc("@every 5m", s.reloadAllTenantSchedules)

    s.cron.Start()
}
```

### 2.2 多租户重载机制 (`reloadAllTenantSchedules`, lines 103-122)

```go
func (s *AttendanceScheduler) reloadAllTenantSchedules() {
    // 获取所有活跃租户
    tenants, err := s.tenantRepo.GetAllActive(context.Background())
    if err != nil {
        s.logger.Errorw("Failed to get active tenants", "error", err)
        return
    }

    // 遍历每个租户，重载其调度配置
    for _, tenant := range tenants {
        s.reloadTenantSchedule(tenant.ID)
    }
}
```

### 2.3 租户级调度重载 (`reloadTenantSchedule`, lines 125-202)

这是假期模式生效的关键方法：

```go
func (s *AttendanceScheduler) reloadTenantSchedule(tenantID uint) {
    // 1. 创建租户上下文
    ctx := tenantctx.WithTenantID(context.Background(), tenantID)

    // 2. 获取当前模式的活跃时间段
    // ListActive() 内部会查询 schedule_settings 获取当前模式
    // 然后返回该模式下 is_active=true 的时间段
    periods, err := s.schedulePeriodRepo.ListActive(ctx)

    // 3. 如果数据库查询失败，回退到 YAML 配置
    if err != nil {
        periods = s.loadPeriodsFromConfig()
    }

    // 4. 移除该租户的旧任务
    s.removeTenantJobs(tenantID)

    // 5. 为每个时间段注册新的 cron 任务
    for _, period := range periods {
        cronExpr := s.buildCronExpressionFromTime(period.StartTime, s.cfg.Schedule.AttendanceDelay)
        entryID, _ := s.cron.AddFunc(cronExpr, func() {
            s.triggerAttendanceForTenant(tenantID, period.ID, period.Name)
        })
        s.tenantJobs[tenantID] = append(s.tenantJobs[tenantID], entryID)
    }
}
```

---

## 3. 模式感知的查询机制

### 3.1 时间段仓库 (`schedule_period_repository.go`, lines 49-57)

```go
func (r *schedulePeriodRepository) ListActive(ctx context.Context) ([]model.SchedulePeriod, error) {
    // 1. 获取当前模式
    setting, err := r.settingRepo.GetByTenantID(ctx)
    if err != nil {
        return nil, err
    }

    var periods []model.SchedulePeriod
    // 2. 查询当前模式的活跃时间段
    err = r.db.WithContext(ctx).
        Where("mode = ? AND is_active = ?", setting.CurrentMode, true).
        Order("sort_order").
        Find(&periods).Error

    return periods, err
}
```

### 3.2 模式切换 (`schedule_setting_repository.go`)

```go
func (r *scheduleSettingRepository) SwitchMode(ctx context.Context, mode string) error {
    return r.db.WithContext(ctx).
        Model(&model.ScheduleSetting{}).
        Update("current_mode", mode).Error
}
```

---

## 4. 假期模式下的执行流程

```
┌─────────────────────────────────────────────────────────────────┐
│                     假期模式激活流程                              │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│ 管理员调用 POST /api/schedule/switch-mode {mode: "holiday"}     │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│ ScheduleSettingRepository.SwitchMode() 更新 schedule_settings   │
│ 数据库: UPDATE schedule_settings SET current_mode = 'holiday'   │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│ 调度器每5分钟执行一次 reloadAllTenantSchedules()                 │
│ 检测到配置变化                                                   │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│ reloadTenantSchedule(tenantID) 被调用                           │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│ ListActive() 返回假期模式的时间段                                │
│ 查询: WHERE mode = 'holiday' AND is_active = true               │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│ 移除旧的上学模式 cron 任务 (removeTenantJobs)                    │
│ 注册新的假期模式 cron 任务                                       │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│ triggerAttendanceForTenant() 在新的时间点触发                    │
│ 使用假期作息时间执行考勤                                         │
└─────────────────────────────────────────────────────────────────┘
```

---

## 5. Cron 表达式生成 (`buildCronExpressionFromTime`, lines 279-303)

```go
func (s *AttendanceScheduler) buildCronExpressionFromTime(startTime string, delay int) string {
    // 解析时间段开始时间 (支持 "HH:MM:SS" 或 "HH:MM" 格式)
    t, _ := time.Parse("15:04:05", startTime)

    // 加上延迟时间（默认3分钟后触发考勤）
    t = t.Add(time.Duration(delay) * time.Minute)

    // 生成 cron 表达式: "分钟 小时 * * *"
    // 例如: "3 8 * * *" 表示每天 08:03 触发
    return fmt.Sprintf("%d %d * * *", t.Minute(), t.Hour())
}
```

---

## 6. 考勤触发执行 (`triggerAttendanceForTenant`, lines 321-386)

```go
func (s *AttendanceScheduler) triggerAttendanceForTenant(tenantID, periodID uint, periodName string) {
    ctx := tenantctx.WithTenantID(context.Background(), tenantID)

    // 1. 获取当前学期和周次
    semester, _ := s.semesterRepo.GetActive(ctx)
    currentWeek := weekutil.GetCurrentWeek(semester.StartDate)

    // 2. 获取当天是星期几
    weekday := int(time.Now().Weekday())
    if weekday == 0 {
        weekday = 7 // 周日转为7
    }

    // 3. 计算考勤详情
    detail, _ := s.attendanceRecordSrv.GetAttendanceDetail(ctx, currentWeek, weekday, periodID)

    // 4. 保存考勤记录
    s.attendanceRecordSrv.SaveAttendanceRecord(ctx, &dto.SaveAttendanceRecordRequest{
        Week:     currentWeek,
        Weekday:  weekday,
        PeriodID: periodID,
        // ... 其他字段
    })
}
```

---

## 7. 关键文件清单

| 文件路径 | 功能描述 |
|---------|----------|
| `internal/scheduler/attendance_scheduler.go` | 调度器主逻辑 (387行) |
| `internal/model/schedule_period.go` | 时间段模型，包含模式常量 |
| `internal/model/schedule_setting.go` | 当前模式跟踪 |
| `internal/service/schedule_period_service.go` | 模式/时间段业务逻辑 |
| `internal/repository/schedule_period_repository.go` | 模式感知的时间段查询 |
| `internal/repository/schedule_setting_repository.go` | 模式切换 |
| `internal/service/attendance_record_service.go` | 考勤计算 (554行) |
| `internal/app/app.go` | 服务器启动时初始化调度器 |
| `scripts/migrate_holiday_periods.go` | 为现有租户设置假期时间段的迁移脚本 |
| `docs/作息模式使用说明.md` | 模式切换用户指南 |

---

## 8. 配置示例

### YAML 配置回退 (`configs/*.yaml`)

```yaml
schedule:
  attendance_delay: 3  # 考勤延迟分钟数
  periods:
    - name: "第一节"
      start_time: "08:00:00"
      end_time: "09:40:00"
    - name: "第二节"
      start_time: "10:00:00"
      end_time: "11:40:00"
```

### 数据库模式示例

```sql
-- 上学模式时间段
INSERT INTO schedule_periods (tenant_id, mode, name, start_time, end_time, is_active, sort_order)
VALUES (1, 'school', '第一节', '08:00:00', '09:40:00', 1, 1);

-- 假期模式时间段
INSERT INTO schedule_periods (tenant_id, mode, name, start_time, end_time, is_active, sort_order)
VALUES (1, 'holiday', '上午', '09:00:00', '12:00:00', 1, 1);
```

---

## 9. 设计亮点

1. **自动响应模式变化**: 调度器每5分钟检测配置变化，无需重启服务
2. **多租户隔离**: 每个租户独立的模式和时间段配置
3. **优雅降级**: 数据库不可用时回退到 YAML 配置
4. **租户级错误隔离**: 单个租户配置错误不影响其他租户

---

## 10. 待实现功能

**考勤排除设计** (`docs/attendance_exclusion_design.md`)：

- `attendance_exclusions` 表：特定日期排除
- `attendance_weekly_rules` 表：周期性规则（如假期模式下周日不考勤）
- 在 `triggerAttendanceForTenant()` 中检查排除规则

当前状态：仅设计文档，代码尚未实现。
2026-01-28T10:49:00.000+0800	info	v3@v3.0.1/cron.go:136	触发考勤统计	{"service": "schedule-server", "env": "dev", "tenantId": 1, "section": 1, "periodName": "上午", "cronExpr": "49 10 * * *"}

2026/01/28 10:49:00 G:/gofile/schedule_server/internal/repository/tenant_repository.go:64
[122.136ms] [rows:1] SELECT * FROM `tenants` WHERE id = 1 AND status = 1 ORDER BY `tenants`.`id` LIMIT 1

2026/01/28 10:49:00 G:/gofile/schedule_server/internal/repository/semester_repository.go:32
[97.388ms] [rows:1] SELECT * FROM `semesters` WHERE is_active = true AND `semesters`.`tenant_id` = 1 ORDER BY `semesters`.`id` LIMIT 1
2026-01-28T10:49:00.221+0800	warn	scheduler/attendance_scheduler.go:176	获取当前周数失败	{"service": "schedule-server", "env": "dev", "tenantId": 1, "tenantName": "乐知院", "err": "日期在学期开始之前"}
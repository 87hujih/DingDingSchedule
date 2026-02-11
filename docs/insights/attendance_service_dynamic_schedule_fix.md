# AttendanceService 动态作息配置修复

## 问题描述

在假期模式下，`AttendanceService` 的 `SlotAttendanceStatus` 接口存在逻辑错误：
- 使用 YAML 配置文件中的**静态作息时间**（`scheduleCfg.Periods`）
- 无法根据当前作息模式（school/holiday）动态切换作息配置
- 导致时间窗口计算错误，请假判定不准确

## 修复方案

### 1. 修改 `AttendanceService` 结构体

**文件**: `internal/service/attendance_service.go`

添加 `schedulePeriodSrv` 字段，用于从数据库动态获取作息配置：

```go
type AttendanceService struct {
    repo              repository.AttendanceRepository
    leaveApprovalRepo repository.LeaveApprovalRepository
    dingMgr           *DingTalkClientManager
    schedulePeriodSrv *SchedulePeriodService // 从数据库读取作息时间
    scheduleCfg       config.Schedule        // 配置文件作为回退
    logger            *zap.SugaredLogger
}
```

### 2. 更新构造函数

添加 `schedulePeriodSrv` 参数：

```go
func NewAttendanceService(
    repo repository.AttendanceRepository,
    leaveApprovalRepo repository.LeaveApprovalRepository,
    dingMgr *DingTalkClientManager,
    schedulePeriodSrv *SchedulePeriodService,
    scheduleCfg config.Schedule,
    logger *zap.SugaredLogger,
) *AttendanceService
```

### 3. 添加 `resolveActivePeriods` 方法

实现与 `AttendanceRecordService` 一致的动态配置获取逻辑：

```go
// resolveActivePeriods 获取当前生效的作息时间配置（优先从数据库，回退到配置文件）
func (s *AttendanceService) resolveActivePeriods(ctx context.Context) []config.Period {
    if s.schedulePeriodSrv != nil {
        periods, err := s.schedulePeriodSrv.GetActivePeriods(ctx)
        if err == nil && len(periods) > 0 {
            return periods
        }
    }
    return s.scheduleCfg.Periods
}
```

### 4. 修改 `GetSlotAttendanceStatus` 方法

**修改前**:
```go
sessionStart, sessionEnd, err := scheduleutil.CalculateSlotTime(date, section, s.scheduleCfg.Periods)
```

**修改后**:
```go
// 从数据库获取当前生效的作息时间配置
periods := s.resolveActivePeriods(ctx)
if len(periods) == 0 || section > len(periods) {
    return nil, response.ErrInvalidParamWithMsg("节次无效或作息配置缺失")
}

sessionStart, sessionEnd, err := scheduleutil.CalculateSlotTime(date, section, periods)
```

### 5. 修改 `GetSlotUserLeaveDetail` 方法

同样替换为动态配置获取：

```go
// 从数据库获取当前生效的作息时间配置
periods := s.resolveActivePeriods(ctx)
if len(periods) == 0 || section > len(periods) {
    return nil, response.ErrInvalidParamWithMsg("节次无效或作息配置缺失")
}

sessionStart, sessionEnd, err := scheduleutil.CalculateSlotTime(date, section, periods)
```

### 6. 更新依赖注入

**文件**: `internal/service/service.go`

在 `NewService` 函数中，更新 `AttendanceService` 的创建：

```go
AttendanceSrv: NewAttendanceService(
    attendanceRepo,
    repo.LeaveRepo,
    dingMgr,
    schedulePeriodSrv,  // 新增参数
    scheduleCfg,
    logger,
),
```

## 修复效果

### 修复前
- ❌ 假期模式下使用上学模式的作息时间
- ❌ 时间窗口计算错误
- ❌ 请假时间重叠判断不准确

### 修复后
- ✅ 自动根据 `schedule_settings.current_mode` 获取对应的作息配置
- ✅ 支持上学模式（school）和假期模式（holiday）动态切换
- ✅ 时间窗口计算准确
- ✅ 请假判定正确
- ✅ 配置文件作为回退方案，保证系统稳定性

## 数据流

```
请求 → AttendanceService.GetSlotAttendanceStatus
    ↓
resolveActivePeriods(ctx)
    ↓
SchedulePeriodService.GetActivePeriods(ctx)
    ↓
SchedulePeriodRepository.ListActive(ctx)
    ↓
1. 查询 schedule_settings 获取 current_mode
2. 查询 schedule_periods 获取对应模式的配置
    ↓
返回当前生效的作息时间配置
```

## 一致性保证

现在系统中所有需要作息配置的服务都使用统一的动态获取方式：

| 服务 | 方法 | 状态 |
|------|------|------|
| AttendanceScheduler | reloadTenantSchedule | ✅ 使用 `schedulePeriodRepo.ListActive(ctx)` |
| AttendanceRecordService | resolveActivePeriods | ✅ 使用 `schedulePeriodSrv.GetActivePeriods(ctx)` |
| AttendanceService | resolveActivePeriods | ✅ 使用 `schedulePeriodSrv.GetActivePeriods(ctx)` |

## 测试建议

1. **切换作息模式测试**
   - 在上学模式下调用接口，验证使用上学作息时间
   - 切换到假期模式，验证使用假期作息时间

2. **时间窗口验证**
   - 验证不同节次的时间窗口计算是否正确
   - 验证请假时间重叠判断是否准确

3. **回退机制测试**
   - 删除数据库中的作息配置，验证是否回退到配置文件
   - 验证回退后系统仍能正常运行

## 相关文件

- `internal/service/attendance_service.go` - 主要修改文件
- `internal/service/service.go` - 依赖注入更新
- `internal/service/schedule_period_service.go` - 作息配置服务
- `internal/repository/schedule_period_repository.go` - 作息配置数据访问

## 修复日期

2026-02-09

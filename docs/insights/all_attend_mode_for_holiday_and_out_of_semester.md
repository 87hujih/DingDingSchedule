# 假期模式和超出学期时间的全体应到逻辑

## 需求说明

在以下两种情况下，考勤计算应使用"全体应到"模式，不再排除有课人员：
1. **假期模式**：当 `schedule_settings.current_mode = 'holiday'` 时
2. **超出学期时间**：当查询日期不在当前学期范围内时

## 业务逻辑

### 原有逻辑（上学模式且在学期内）
```
应到人员 = (全体参与考勤用户 或 指定部门用户) - 本节有课用户
```

### 新增逻辑（假期模式或超出学期）
```
应到人员 = 全体参与考勤用户 或 指定部门用户
```

**原因**：
- 假期期间通常没有正常的课程安排，课表数据不适用
- 超出学期时间时，课表数据可能不准确或不相关
- 这两种情况下，所有参与考勤的用户都应该被视为"应到"

## 代码实现

### 1. 修改 `AttendanceService` 结构体

**文件**: `internal/service/attendance_service.go`

添加 `semesterSrv` 字段：

```go
type AttendanceService struct {
    repo              repository.AttendanceRepository
    leaveApprovalRepo repository.LeaveApprovalRepository
    dingMgr           *DingTalkClientManager
    schedulePeriodSrv *SchedulePeriodService // 从数据库读取作息时间
    semesterSrv       *SemesterService       // 学期服务（新增）
    scheduleCfg       config.Schedule        // 配置文件作为回退
    logger            *zap.SugaredLogger
}
```

### 2. 更新构造函数

添加 `semesterSrv` 参数：

```go
func NewAttendanceService(
    repo repository.AttendanceRepository,
    leaveApprovalRepo repository.LeaveApprovalRepository,
    dingMgr *DingTalkClientManager,
    schedulePeriodSrv *SchedulePeriodService,
    semesterSrv *SemesterService,  // 新增参数
    scheduleCfg config.Schedule,
    logger *zap.SugaredLogger,
) *AttendanceService
```

### 3. 添加 `shouldUseAllAttendMode` 方法

判断是否应该使用"全体应到"模式：

```go
// shouldUseAllAttendMode 判断是否应该使用"全体应到"模式
// 返回 true 的情况：
// 1. 当前为假期模式
// 2. 日期超出学期范围
func (s *AttendanceService) shouldUseAllAttendMode(ctx context.Context, date time.Time) bool {
    // 1. 检查是否为假期模式
    if s.schedulePeriodSrv != nil {
        currentMode, err := s.schedulePeriodSrv.GetCurrentMode(ctx)
        if err == nil && currentMode == model.ScheduleModeHoliday {
            s.logger.Infow("假期模式：使用全体应到模式", "date", date.Format("2006-01-02"))
            return true
        }
    }

    // 2. 检查日期是否超出学期范围
    if s.semesterSrv != nil {
        semester, err := s.semesterSrv.GetActiveSemester(ctx)
        if err != nil {
            // 没有学期配置，使用全体应到模式
            s.logger.Infow("无学期配置：使用全体应到模式", "date", date.Format("2006-01-02"))
            return true
        }

        _, err = s.semesterSrv.CalculateWeekFromDate(semester, date)
        if err != nil {
            // 日期超出学期范围
            s.logger.Infow("日期超出学期范围：使用全体应到模式",
                "date", date.Format("2006-01-02"),
                "error", err.Error(),
            )
            return true
        }
    }

    // 默认使用正常模式（应到 = 候选 - 有课）
    return false
}
```

### 4. 修改 `GetSlotAttendanceStatus` 方法

根据判断结果选择不同的计算逻辑：

```go
func (s *AttendanceService) GetSlotAttendanceStatus(...) {
    // ... 前置校验和时间窗口计算 ...

    // 判断是否使用"全体应到"模式（假期模式或超出学期时间）
    var shouldArriveUsers []model.User
    var shouldArriveItems []dto.CourseAttendanceUserItem

    if s.shouldUseAllAttendMode(ctx, date) {
        // 全体应到模式：不排除有课人员
        shouldArriveUsers, err = s.listActiveUsersByDeptIDs(ctx, deptIDs)
        if err != nil {
            return nil, err
        }
        shouldArriveItems = toAttendanceUserItems(shouldArriveUsers, nil)
    } else {
        // 正常模式：应到 = 候选 - 有课
        shouldArriveUsers, shouldArriveItems, err = s.computeShouldArriveUsersByDeptFilter(
            ctx,
            dayOfWeek,
            section,
            week,
            deptIDs,
        )
        if err != nil {
            return nil, err
        }
    }

    // ... 计算请假人员和返回响应 ...
}
```

### 5. 更新依赖注入

**文件**: `internal/service/service.go`

```go
func NewService(...) *Service {
    // 创建 SemesterService
    semesterSrv := NewSemesterService(repo.SemesterRepo)

    return &Service{
        // ...
        AttendanceSrv: NewAttendanceService(
            attendanceRepo,
            repo.LeaveRepo,
            dingMgr,
            schedulePeriodSrv,
            semesterSrv,  // 新增参数
            scheduleCfg,
            logger,
        ),
        SemesterSrv: semesterSrv,
        // ...
    }
}
```

## 执行流程

### 场景1：假期模式

**数据库状态**:
```sql
-- schedule_settings 表
current_mode = 'holiday'
```

**执行流程**:
```
1. 调用 shouldUseAllAttendMode(ctx, date)
2. 调用 schedulePeriodSrv.GetCurrentMode(ctx)
3. 返回 "holiday"
4. 判断为假期模式，返回 true
5. 执行全体应到逻辑：
   - 调用 listActiveUsersByDeptIDs(ctx, deptIDs)
   - 返回所有 status=1 的用户（可按部门过滤）
   - 不排除有课人员
```

**日志输出**:
```
假期模式：使用全体应到模式 date=2024-03-15
```

### 场景2：超出学期时间

**数据库状态**:
```sql
-- semesters 表
start_date = '2024-02-26'
total_weeks = 18
-- 学期结束日期 = 2024-02-26 + 18周 = 2024-06-23
```

**查询日期**: `2024-07-15`（超出学期）

**执行流程**:
```
1. 调用 shouldUseAllAttendMode(ctx, date)
2. 检查假期模式：current_mode = 'school'（不是假期）
3. 调用 semesterSrv.GetActiveSemester(ctx)
4. 调用 semesterSrv.CalculateWeekFromDate(semester, date)
5. 计算结果：date 超出学期范围，返回错误
6. 判断为超出学期，返回 true
7. 执行全体应到逻辑
```

**日志输出**:
```
日期超出学期范围：使用全体应到模式 date=2024-07-15 error=日期超出学期范围
```

### 场景3：上学模式且在学期内（正常模式）

**数据库状态**:
```sql
-- schedule_settings 表
current_mode = 'school'

-- semesters 表
start_date = '2024-02-26'
total_weeks = 18
```

**查询日期**: `2024-03-15`（在学期内）

**执行流程**:
```
1. 调用 shouldUseAllAttendMode(ctx, date)
2. 检查假期模式：current_mode = 'school'（不是假期）
3. 调用 semesterSrv.GetActiveSemester(ctx)
4. 调用 semesterSrv.CalculateWeekFromDate(semester, date)
5. 计算结果：week = 3（在学期范围内）
6. 返回 false（使用正常模式）
7. 执行正常逻辑：
   - 调用 computeShouldArriveUsersByDeptFilter(...)
   - 应到 = 候选用户 - 有课用户
```

### 场景4：无学期配置

**数据库状态**:
```sql
-- semesters 表为空或没有激活的学期
```

**执行流程**:
```
1. 调用 shouldUseAllAttendMode(ctx, date)
2. 检查假期模式：current_mode = 'school'（不是假期）
3. 调用 semesterSrv.GetActiveSemester(ctx)
4. 返回错误：没有学期配置
5. 判断为无学期配置，返回 true
6. 执行全体应到逻辑
```

**日志输出**:
```
无学期配置：使用全体应到模式 date=2024-03-15
```

## 判断逻辑总结

| 条件 | 结果 | 说明 |
|------|------|------|
| `current_mode = 'holiday'` | ✅ 全体应到 | 假期模式 |
| 无学期配置 | ✅ 全体应到 | 没有激活的学期 |
| 日期 < 学期开始日期 | ✅ 全体应到 | 学期开始前 |
| 日期 > 学期结束日期 | ✅ 全体应到 | 学期结束后 |
| `current_mode = 'school'` 且在学期内 | ❌ 正常模式 | 应到 = 候选 - 有课 |

## 对比示例

### 假设数据
- 全体参与考勤用户：张三、李四、王五、赵六（4人）
- 周五第2节有课的用户：李四、王五（2人）

### 上学模式且在学期内（正常模式）
```
应到人员 = {张三, 李四, 王五, 赵六} - {李四, 王五}
         = {张三, 赵六}  （2人）
```

### 假期模式或超出学期（全体应到模式）
```
应到人员 = {张三, 李四, 王五, 赵六}  （4人）
```

## API 响应对比

### 正常模式响应
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "date": "2024-03-15",
    "week": 3,
    "day_of_week": 5,
    "section": 2,
    "should_arrive": [
      {"id": 1, "name": "张三", ...},
      {"id": 4, "name": "赵六", ...}
    ],
    "on_leave": []
  }
}
```

### 全体应到模式响应
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "date": "2024-07-15",
    "week": 20,
    "day_of_week": 1,
    "section": 2,
    "should_arrive": [
      {"id": 1, "name": "张三", ...},
      {"id": 2, "name": "李四", ...},
      {"id": 3, "name": "王五", ...},
      {"id": 4, "name": "赵六", ...}
    ],
    "on_leave": []
  }
}
```

## 日志监控

系统会在日志中记录使用"全体应到"模式的原因：

```
# 假期模式
假期模式：使用全体应到模式 date=2024-03-15

# 超出学期
日期超出学期范围：使用全体应到模式 date=2024-07-15 error=日期超出学期范围

# 无学期配置
无学期配置：使用全体应到模式 date=2024-03-15
```

可以通过这些日志来监控和审计考勤计算逻辑的执行情况。

## 相关文件

- `internal/service/attendance_service.go` - 主要修改文件
- `internal/service/service.go` - 依赖注入更新
- `internal/service/semester_service.go` - 学期服务
- `internal/service/schedule_period_service.go` - 作息配置服务

## 修改日期

2026-02-09

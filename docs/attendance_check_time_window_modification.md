# 考勤打卡时间窗口修改方案

## 一、需求变更说明

### 当前逻辑（旧）
- **打卡方式**：一天打一次卡，全天有效
- **查询范围**：当天 00:00:00 到当前节下课时间
- **判断标准**：打卡时间 ≤ 上课时间 → 正常打卡

**示例**（假期模式）：
```
用户在 08:00 打卡
- 查询第1节（08:00-11:50）→ 08:00 ≤ 08:00 → 正常 ✅
- 查询第2节（14:00-17:30）→ 08:00 ≤ 14:00 → 正常 ✅（问题：早上的打卡不应该用于下午）
- 查询第3节（19:00-21:30）→ 08:00 ≤ 19:00 → 正常 ✅（问题：早上的打卡不应该用于晚上）
```

### 新需求（新）
- **打卡方式**：每节课单独打卡
- **查询范围**：上一节下课时间（或当天00:00如果是第一节）到当前节下课时间
- **判断标准**：打卡时间在有效时间窗口内 → 正常打卡

**示例**（假期模式）：
```
第1节（08:00-11:50）：
  - 有效打卡窗口：00:00:00 - 08:00:00
  - 用户在 08:00 打卡 → 正常 ✅

第2节（14:00-17:30）：
  - 有效打卡窗口：11:50:00 - 14:00:00（第1节下课到第2节上课）
  - 用户在 08:00 打卡 → 无效 ❌（不在窗口内）
  - 用户在 13:00 打卡 → 正常 ✅（在窗口内）

第3节（19:00-21:30）：
  - 有效打卡窗口：17:30:00 - 19:00:00（第2节下课到第3节上课）
  - 用户在 18:00 打卡 → 正常 ✅（在窗口内）
```

---

## 二、核心逻辑变更

### 2.1 时间窗口计算

#### **当前逻辑**
```go
// 查询打卡记录：从当天00:00到下课时间
queryStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
records, err := dingClient.GetAttendanceRecords(ctx, dingUserIDs, queryStart, slotEnd)
```

#### **新逻辑**
```go
// 计算有效打卡窗口
windowStart := calculateWindowStart(ctx, date, section, periods)
windowEnd := deadline  // 上课时间作为打卡截止时间

// 查询打卡记录：从窗口开始到下课时间（需要包含迟到的打卡）
queryStart := windowStart
queryEnd := slotEnd

records, err := dingClient.GetAttendanceRecords(ctx, dingUserIDs, queryStart, queryEnd)
```

### 2.2 有效打卡窗口计算规则

```
第1节：
  windowStart = 当天 00:00:00
  windowEnd = 第1节上课时间

第2节及以后：
  windowStart = 上一节下课时间
  windowEnd = 当前节上课时间

示例（假期模式）：
  第1节（08:00-11:50）：窗口 = 00:00:00 - 08:00:00
  第2节（14:00-17:30）：窗口 = 11:50:00 - 14:00:00
  第3节（19:00-21:30）：窗口 = 17:30:00 - 19:00:00

示例（上课模式）：
  第1节（08:00-09:40）：窗口 = 00:00:00 - 08:00:00
  第2节（10:10-11:50）：窗口 = 09:40:00 - 10:10:00
  第3节（14:30-16:10）：窗口 = 11:50:00 - 14:30:00（跨越午休）
  第4节（16:40-18:20）：窗口 = 16:10:00 - 16:40:00
  第5节（19:30-21:10）：窗口 = 18:20:00 - 19:30:00
```

### 2.3 打卡有效性判断

#### **当前逻辑**
```go
if checkTime.Before(deadline) || checkTime.Equal(deadline) {
    // 正常打卡
    onTime = append(onTime, ...)
} else {
    // 迟到
    lateCount++
}
```

#### **新逻辑**
```go
// 判断打卡是否在有效窗口内
if checkTime.Before(windowStart) {
    // 打卡时间太早，不在有效窗口内
    s.logger.Infow("打卡时间早于有效窗口",
        "用户", user.Name,
        "打卡时间", checkTime.Format("2006-01-02 15:04:05"),
        "窗口开始", windowStart.Format("2006-01-02 15:04:05"),
    )
    continue  // 跳过这条打卡记录
}

if checkTime.Before(deadline) || checkTime.Equal(deadline) {
    // 在窗口内且不迟到
    onTime = append(onTime, ...)
} else {
    // 在窗口内但迟到了
    lateCount++
}
```

---

## 三、需要修改的文件

### 3.1 主要修改文件

#### **文件1：`internal/service/attendance_record_service.go`**

**修改点1：添加窗口计算方法**
```go
// calculateCheckWindowStart 计算打卡窗口开始时间
// 第1节：当天00:00
// 第2节及以后：上一节的下课时间
func (s *AttendanceRecordService) calculateCheckWindowStart(
    ctx context.Context,
    date time.Time,
    section int,
) (time.Time, error) {
    if section <= 1 {
        // 第1节：从当天00:00开始
        return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location()), nil
    }

    // 第2节及以后：从上一节的下课时间开始
    prevSection := section - 1
    if prevSection > len(s.scheduleCfg.Periods) {
        return time.Time{}, fmt.Errorf("无效的节次: %d", section)
    }

    // 获取上一节的下课时间
    prevPeriod := s.scheduleCfg.Periods[prevSection-1]
    prevEndTime, err := time.Parse("15:04", prevPeriod.End)
    if err != nil {
        return time.Time{}, fmt.Errorf("解析上一节下课时间失败: %w", err)
    }

    // 构造完整的日期时间
    windowStart := time.Date(
        date.Year(), date.Month(), date.Day(),
        prevEndTime.Hour(), prevEndTime.Minute(), 0, 0,
        date.Location(),
    )

    return windowStart, nil
}
```

**修改点2：修改 `getOnTimeUsers` 方法**
```go
func (s *AttendanceRecordService) getOnTimeUsers(
    ctx context.Context,
    users []model.User,
    date time.Time,
    section int,        // 新增参数：节次
    deadline time.Time, // 上课时间（打卡截止时间）
    slotEnd time.Time,
) ([]dto.AttendanceUserCheck, error) {
    if len(users) == 0 {
        return []dto.AttendanceUserCheck{}, nil
    }

    // 提取钉钉用户ID
    dingUserIDs := make([]string, 0, len(users))
    userByDingID := make(map[string]*model.User)
    for i := range users {
        if users[i].DingUserID != "" {
            dingUserIDs = append(dingUserIDs, users[i].DingUserID)
            userByDingID[users[i].DingUserID] = &users[i]
        }
    }

    if len(dingUserIDs) == 0 {
        return []dto.AttendanceUserCheck{}, nil
    }

    // 获取钉钉客户端
    if s.dingMgr == nil {
        return nil, response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
    }
    _, dingClient, err := s.dingMgr.FromContext(ctx)
    if err != nil {
        return nil, response.NewBizError(response.CodeUnauthorized, "缺少租户信息")
    }

    // 【核心修改】计算有效打卡窗口
    windowStart, err := s.calculateCheckWindowStart(ctx, date, section)
    if err != nil {
        return nil, errs.WrapMsgErr("计算打卡窗口失败", err)
    }

    // 查询打卡记录：从窗口开始到下课时间
    // 注意：仍然查询到下课时间，以便统计迟到人数
    queryStart := windowStart
    queryEnd := slotEnd

    records, err := dingClient.GetAttendanceRecords(ctx, dingUserIDs, queryStart, queryEnd)
    if err != nil {
        s.logger.Errorw("获取钉钉打卡记录失败",
            "userCount", len(dingUserIDs),
            "queryStart", queryStart,
            "queryEnd", queryEnd,
            "error", err,
        )
        return nil, errs.WrapMsgErr("获取钉钉打卡记录失败", err)
    }

    // 按用户ID去重，取最早的打卡记录
    earliestCheck := make(map[string]time.Time)
    for _, r := range records {
        // 只统计上班打卡
        if r.CheckType != "OnDuty" {
            continue
        }
        if existing, ok := earliestCheck[r.DingUserID]; !ok || r.CheckTime.Before(existing) {
            earliestCheck[r.DingUserID] = r.CheckTime
        }
    }

    s.logger.Infow("打卡记录统计",
        "应到人数", len(users),
        "查询到的打卡记录数", len(records),
        "有效打卡人数", len(earliestCheck),
        "窗口开始", windowStart.Format("2006-01-02 15:04:05"),
        "截止时间", deadline.Format("2006-01-02 15:04:05"),
    )

    // 【核心修改】只返回在有效窗口内打卡的人
    onTime := make([]dto.AttendanceUserCheck, 0)
    lateCount := 0
    tooEarlyCount := 0

    for dingUserID, checkTime := range earliestCheck {
        user := userByDingID[dingUserID]
        if user == nil {
            s.logger.Warnw("找不到对应的用户", "dingUserID", dingUserID)
            continue
        }

        // 【新增】检查打卡时间是否在有效窗口内
        if checkTime.Before(windowStart) {
            tooEarlyCount++
            s.logger.Infow("打卡时间早于有效窗口",
                "用户", user.Name,
                "打卡时间", checkTime.Format("2006-01-02 15:04:05"),
                "窗口开始", windowStart.Format("2006-01-02 15:04:05"),
                "提前了", windowStart.Sub(checkTime).String(),
            )
            continue  // 跳过这条打卡记录
        }

        // 判断是否迟到
        if checkTime.Before(deadline) || checkTime.Equal(deadline) {
            onTime = append(onTime, dto.AttendanceUserCheck{
                ID:        user.ID,
                Name:      user.Name,
                CheckTime: checkTime,
            })
            s.logger.Debugw("正常打卡",
                "用户", user.Name,
                "打卡时间", checkTime.Format("2006-01-02 15:04:05"),
                "窗口开始", windowStart.Format("2006-01-02 15:04:05"),
                "截止时间", deadline.Format("2006-01-02 15:04:05"),
            )
        } else {
            lateCount++
            s.logger.Infow("打卡晚于截止时间",
                "用户", user.Name,
                "打卡时间", checkTime.Format("2006-01-02 15:04:05"),
                "截止时间", deadline.Format("2006-01-02 15:04:05"),
                "晚了", checkTime.Sub(deadline).String(),
            )
        }
    }

    s.logger.Infow("打卡统计结果",
        "正常打卡", len(onTime),
        "晚于截止时间", lateCount,
        "早于窗口", tooEarlyCount,
        "未打卡", len(users)-len(onTime)-lateCount,
    )

    return onTime, nil
}
```

**修改点3：更新调用 `getOnTimeUsers` 的地方**
```go
// 在 GetAttendanceDetail 方法中
func (s *AttendanceRecordService) GetAttendanceDetail(
    ctx context.Context,
    req *dto.AttendanceDetailRequest,
) (*dto.AttendanceDetailResponse, error) {
    // ... 前面的代码不变 ...

    // 5. 获取打卡记录（只返回正常打卡的人）
    // 【修改】传入 section 参数
    onTime, err := s.getOnTimeUsers(ctx, shouldAttend, date, req.Section, slotStart, slotEnd)
    if err != nil {
        return nil, err
    }

    // ... 后面的代码不变 ...
}
```

---

## 四、数据库配置依赖

### 4.1 需要从数据库读取作息时间配置

当前代码中，`calculateCheckWindowStart` 方法使用的是配置文件中的 `s.scheduleCfg.Periods`。

但实际上，系统是从数据库的 `schedule_periods` 表读取配置的，需要修改为从数据库读取。

#### **修改方案**

**方案A：在 Service 初始化时注入 SchedulePeriodRepository**

```go
type AttendanceRecordService struct {
    userRepo             repository.UserRepository
    courseRepo           repository.CourseRepository
    leaveRepo            repository.LeaveApprovalRepository
    attendanceRecordRepo repository.AttendanceRecordRepository
    schedulePeriodRepo   repository.SchedulePeriodRepository  // 新增
    dingMgr              *DingTalkClientManager
    scheduleCfg          config.Schedule
    schedulePeriodSrv    *SchedulePeriodService
    logger               *zap.SugaredLogger
}
```

**方案B：使用 SchedulePeriodService 获取配置**

```go
// 在 calculateCheckWindowStart 方法中
func (s *AttendanceRecordService) calculateCheckWindowStart(
    ctx context.Context,
    date time.Time,
    section int,
) (time.Time, error) {
    if section <= 1 {
        return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location()), nil
    }

    // 从数据库获取当前激活的作息时间配置
    periods, err := s.schedulePeriodSrv.GetActivePeriods(ctx)
    if err != nil {
        // 回退到配置文件
        s.logger.Warnw("获取作息配置失败，使用配置文件回退", "err", err)
        return s.calculateCheckWindowStartFromConfig(date, section)
    }

    if len(periods) == 0 {
        return s.calculateCheckWindowStartFromConfig(date, section)
    }

    // 获取上一节的下课时间
    prevSection := section - 1
    if prevSection > len(periods) {
        return time.Time{}, fmt.Errorf("无效的节次: %d", section)
    }

    prevPeriod := periods[prevSection-1]
    prevEndTime, err := time.Parse("15:04:05", prevPeriod.EndTime)
    if err != nil {
        // 尝试 "15:04" 格式
        prevEndTime, err = time.Parse("15:04", prevPeriod.EndTime)
        if err != nil {
            return time.Time{}, fmt.Errorf("解析上一节下课时间失败: %w", err)
        }
    }

    windowStart := time.Date(
        date.Year(), date.Month(), date.Day(),
        prevEndTime.Hour(), prevEndTime.Minute(), 0, 0,
        date.Location(),
    )

    return windowStart, nil
}

// 从配置文件计算（回退方案）
func (s *AttendanceRecordService) calculateCheckWindowStartFromConfig(
    date time.Time,
    section int,
) (time.Time, error) {
    if section <= 1 {
        return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location()), nil
    }

    prevSection := section - 1
    if prevSection > len(s.scheduleCfg.Periods) {
        return time.Time{}, fmt.Errorf("无效的节次: %d", section)
    }

    prevPeriod := s.scheduleCfg.Periods[prevSection-1]
    prevEndTime, err := time.Parse("15:04", prevPeriod.End)
    if err != nil {
        return time.Time{}, fmt.Errorf("解析上一节下课时间失败: %w", err)
    }

    windowStart := time.Date(
        date.Year(), date.Month(), date.Day(),
        prevEndTime.Hour(), prevEndTime.Minute(), 0, 0,
        date.Location(),
    )

    return windowStart, nil
}
```

---

## 五、测试场景

### 5.1 假期模式测试

**配置**：
```
第1节：08:00-11:50
第2节：14:00-17:30
第3节：19:00-21:30
```

**测试用例1：第1节考勤**
```
打卡时间：07:50
查询第1节：
  - 窗口：00:00 - 08:00
  - 判断：07:50 在窗口内 且 07:50 < 08:00 → 正常 ✅
```

**测试用例2：第2节考勤（早上打卡）**
```
打卡时间：08:00
查询第2节：
  - 窗口：11:50 - 14:00
  - 判断：08:00 < 11:50（早于窗口）→ 无效 ❌
  - 结果：未打卡
```

**测试用例3：第2节考勤（中午打卡）**
```
打卡时间：13:00
查询第2节：
  - 窗口：11:50 - 14:00
  - 判断：13:00 在窗口内 且 13:00 < 14:00 → 正常 ✅
```

**测试用例4：第2节考勤（迟到）**
```
打卡时间：14:10
查询第2节：
  - 窗口：11:50 - 14:00
  - 判断：14:10 在窗口内 但 14:10 > 14:00 → 迟到 ⏰
```

### 5.2 上课模式测试

**配置**：
```
第1节：08:00-09:40
第2节：10:10-11:50
第3节：14:30-16:10
```

**测试用例5：第2节考勤（课间打卡）**
```
打卡时间：10:00
查询第2节：
  - 窗口：09:40 - 10:10（课间休息时间）
  - 判断：10:00 在窗口内 且 10:00 < 10:10 → 正常 ✅
```

**测试用例6：第3节考勤（午休打卡）**
```
打卡时间：13:00
查询第3节：
  - 窗口：11:50 - 14:30（午休时间）
  - 判断：13:00 在窗口内 且 13:00 < 14:30 → 正常 ✅
```

---

## 六、影响范围评估

### 6.1 代码修改范围
- ✅ **核心文件**：`internal/service/attendance_record_service.go`
- ⚠️ **可能影响**：已保存的历史考勤记录（如果需要重新统计）

### 6.2 业务影响
- ✅ **用户行为变化**：需要每节课单独打卡
- ✅ **考勤规则变化**：早上的打卡不再对下午/晚上有效
- ⚠️ **历史数据**：已保存的考勤记录可能需要重新统计

### 6.3 兼容性
- ✅ **向后兼容**：新逻辑不影响已有的API接口
- ✅ **数据库兼容**：不需要修改数据库表结构
- ⚠️ **定时任务**：定时任务逻辑不需要修改，但统计结果会不同

---

## 七、实施步骤

### 步骤1：备份当前代码
```bash
git checkout -b feature/attendance-time-window
```

### 步骤2：修改代码
按照上述方案修改 `attendance_record_service.go`

### 步骤3：测试
1. 单元测试：测试窗口计算逻辑
2. 集成测试：测试完整的考勤统计流程
3. 手动测试：使用实际数据测试各种场景

### 步骤4：部署
1. 在测试环境验证
2. 通知用户新的打卡规则
3. 部署到生产环境

---

## 八、注意事项

### 8.1 用户通知
需要提前通知用户新的打卡规则：
- 每节课需要单独打卡
- 打卡时间窗口：上一节下课到本节上课
- 早上的打卡不再对下午/晚上有效

### 8.2 历史数据处理
如果需要重新统计历史考勤记录：
- 可以使用手动触发接口重新统计
- 或者编写脚本批量重新统计

### 8.3 边界情况
- 第1节的窗口从00:00开始，可能太早
- 跨天的情况（如果有晚上的课延续到第二天）
- 节假日的处理

---

## 九、总结

### 核心变更
1. **时间窗口计算**：从"当天00:00"改为"上一节下课时间"
2. **打卡有效性判断**：增加"早于窗口"的检查
3. **日志输出**：增加窗口信息和"早于窗口"的统计

### 优点
- ✅ 更符合实际考勤需求
- ✅ 防止一次打卡全天有效的漏洞
- ✅ 更精确的考勤统计

### 缺点
- ⚠️ 用户需要每节课都打卡，增加操作负担
- ⚠️ 如果忘记打卡，无法补救（除非管理员手动修改）

### 建议
- 考虑增加"补打卡"功能
- 考虑增加打卡提醒功能
- 考虑增加"宽限时间"（如提前5分钟可以打卡）

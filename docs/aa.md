一、方案概述

1.1 核心思想

定时重载调度器通过周期性检查数据库配置变更，实现考勤调度器的动态适配。该
方案无需复杂的事件通知机制，通过简单的定时任务即可实现配置的自动更新。

1.2 关键特性

- ✅ 多租户隔离：每个租户独立的调度任务和配置
- ✅ 动态更新：每5分钟自动检测并应用配置变更
- ✅ 配置回退：数据库无配置时自动使用YAML文件
- ✅ 错误隔离：单个租户失败不影响其他租户
- ✅ 零停机：配置变更无需重启服务

1.3 技术架构

┌─────────────────────────────────────────────────────────────┐         
│                    AttendanceScheduler                       │        
├─────────────────────────────────────────────────────────────┤         
│  ┌──────────────┐      ┌──────────────┐                    │          
│  │ Cron Engine  │◄─────│ Reload Task  │ (Every 5 min)      │          
│  └──────┬───────┘      └──────────────┘                    │          
│         │                                                    │        
│         ├─► Tenant A Jobs: [Job1, Job2, Job3, ...]         │          
│         ├─► Tenant B Jobs: [Job1, Job2, ...]               │          
│         └─► Tenant C Jobs: [Job1, Job2, Job3, Job4, ...]   │          
└─────────────────────────────────────────────────────────────┘         
│                                             
▼                                             
┌─────────────────────────────────────┐                         
│         Database Layer               │                        
├─────────────────────────────────────┤                         
│  • schedule_settings (current_mode)  │                        
│  • schedule_periods (time config)    │                        
│  • tenants (active tenants)          │                        
└─────────────────────────────────────┘
                                                                          
---                                                                     
二、完整实现流程

2.1 系统启动流程

[系统启动]                                                              
│                                                                   
├─► 1. 初始化调度器                                                 
│      - 创建 Cron 实例                                             
│      - 初始化 tenantJobs map                                      
│                                                                   
├─► 2. 加载所有租户配置                                             
│      │                                                            
│      ├─► 2.1 查询活跃租户列表                                     
│      │      SELECT * FROM tenants WHERE status = 1                
│      │                                                            
│      └─► 2.2 为每个租户加载配置                                   
│             │                                                     
│             ├─► 获取当前模式                                      
│             │   (通过 SchedulePeriodRepo.ListActive 自动处理)     
│             │                                                     
│             ├─► 获取作息时段配置                                  
│             │   SELECT * FROM schedule_periods                    
│             │   WHERE tenant_id = ? AND mode = current_mode       
│             │                                                     
│             └─► 创建 Cron 任务                                    
│                    - 为每个时段生成 cron 表达式                   
│                    - 注册到 Cron 引擎                             
│                    - 保存 EntryID 到 tenantJobs                   
│                                                                   
├─► 3. 注册定时重载任务                                             
│      Cron Expression: "*/5 * * * *"                               
│      Function: reloadAllTenantSchedules()                         
│                                                                   
└─► 4. 启动 Cron 引擎                                               
s.cron.Start()

2.2 配置重载流程

[定时触发 - 每5分钟]                                                    
│                                                                   
├─► 1. 查询所有活跃租户                                             
│      tenants, err := s.tenantRepo.ListActive(ctx)                 
│                                                                   
├─► 2. 遍历每个租户                                                 
│      for _, tenant := range tenants {                             
│                                                                   
│      ├─► 2.1 构造租户上下文                                       
│      │      ctx := tenantctx.WithTenantID(context.Background(),   
tenantID)                                                               
│      │                                                            
│      ├─► 2.2 查询当前配置                                         
│      │      periods, err := s.schedulePeriodRepo.ListActive(ctx)  
│      │                                                            
│      │      [Repository 内部逻辑]                                 
│      │      ├─► 查询 schedule_settings 获取 current_mode          
│      │      └─► 查询 schedule_periods WHERE mode = current_mode   
│      │                                                            
│      ├─► 2.3 配置对比与更新                                       
│      │      │                                                     
│      │      ├─► 移除旧任务                                        
│      │      │   if entryIDs, exists := s.tenantJobs[tenantID];    
exists {                                                                
│      │      │       for _, entryID := range entryIDs {            
│      │      │           s.cron.Remove(entryID)                    
│      │      │       }                                             
│      │      │   }                                                 
│      │      │                                                     
│      │      └─► 创建新任务                                        
│      │          for i, period := range periods {                  
│      │              cronExpr :=                                   
buildCronExpression(period.StartTime)                                   
│      │              entryID := s.cron.AddFunc(cronExpr, handler)  
│      │              newEntryIDs = append(newEntryIDs, entryID)    
│      │          }                                                 
│      │                                                            
│      └─► 2.4 更新任务映射                                         
│             s.tenantJobs[tenantID] = newEntryIDs                  
│                                                                   
└─► 3. 记录重载结果                                                 
logger.Info("配置重载完成")

2.3 考勤触发流程

[Cron 任务触发 - 例如每天 08:03]                                        
│                                                                   
├─► 1. 执行闭包函数                                                 
│      func() {                                                     
│          s.triggerAttendanceForTenant(tenantID, section,          
time.Now())                                                             
│      }                                                            
│                                                                   
├─► 2. 构造租户上下文                                               
│      ctx := tenantctx.WithTenantID(context.Background(), tenantID)
│                                                                   
├─► 3. 获取当前周数                                                 
│      week, err := s.semesterSrv.GetCurrentWeek(ctx)               
│                                                                   
│      [内部逻辑]                                                   
│      ├─► 查询活跃学期                                             
│      │   SELECT * FROM semesters                                  
│      │   WHERE tenant_id = ? AND is_active = 1                    
│      │                                                            
│      └─► 计算当前周                                               
│          week = weekutil.GetCurrentWeek(semester.StartDate,       
time.Now())                                                             
│                                                                   
├─► 4. 构造考勤请求                                                 
│      req := &dto.AttendanceDetailRequest{                         
│          Date:    "2026-01-22",                                   
│          Week:    week,                                           
│          Section: section,                                        
│      }                                                            
│                                                                   
├─► 5. 计算考勤详情                                                 
│      result, err := s.attendanceRecordSrv.GetAttendanceDetail(ctx,
req)                                                                   
│                                                                   
│      [内部逻辑 - 详见第三节]                                      
│      ├─► 查询候选用户（status=1）                                 
│      ├─► 查询有课用户                                             
│      ├─► 查询请假用户                                             
│      └─► 计算统计数据                                             
│                                                                   
└─► 6. 保存考勤记录                                                 
err := s.attendanceRecordSrv.SaveAttendanceRecord(ctx,       
result)
                                                                          
---                                                                     
三、核心逻辑详解

3.1 Cron 表达式生成逻辑

// 输入：数据库时间字符串 "08:00:00" 或 "08:00"                         
// 输出：Cron 表达式 "3 8 * * *"（每天 8:03 触发）

func buildCronExpressionFromTime(timeStr string) (string, error) {      
// 步骤1：解析时间字符串                                            
var startClock time.Time                                            
if len(timeStr) == 8 {  // "08:00:00"                               
startClock, _ = time.Parse("15:04:05", timeStr)                 
} else if len(timeStr) == 5 {  // "08:00"                           
startClock, _ = time.Parse("15:04", timeStr)                    
}

      // 步骤2：添加延迟（默认3分钟）                                     
      // 原因：给学生上课打卡留出时间                                     
      triggerTime := startClock.Add(3 * time.Minute)                      
                                                                          
      // 步骤3：生成 Cron 表达式                                          
      // 格式：分 时 日 月 周                                             
      // * * * * * 表示每天都触发                                         
      cronExpr := fmt.Sprintf("%d %d * * *",                              
          triggerTime.Minute(),  // 分钟：3                               
          triggerTime.Hour())     // 小时：8                              
                                                                          
      return cronExpr  // "3 8 * * *"                                     
}

示例转换：                                                              
┌────────────┬──────────┬────────┬─────────────┬────────────┐           
│ 数据库时间 │ 解析结果 │ 延迟后 │ Cron 表达式 │  触发时间  │           
├────────────┼──────────┼────────┼─────────────┼────────────┤           
│ 08:00:00   │ 08:00    │ 08:03  │ 3 8 * * *   │ 每天 08:03 │           
├────────────┼──────────┼────────┼─────────────┼────────────┤           
│ 10:15:00   │ 10:15    │ 10:18  │ 18 10 * * * │ 每天 10:18 │           
├────────────┼──────────┼────────┼─────────────┼────────────┤           
│ 14:30:00   │ 14:30    │ 14:33  │ 33 14 * * * │ 每天 14:33 │           
└────────────┴──────────┴────────┴─────────────┴────────────┘           
3.2 任务管理逻辑

// tenantJobs 数据结构                                                  
type AttendanceScheduler struct {                                       
tenantJobs map[uint][]cron.EntryID                                  
// key: 租户ID                                                      
// value: 该租户的所有 Cron 任务 ID 列表                            
}

// 示例数据：                                                           
// tenantJobs = {                                                       
//     1: [EntryID(101), EntryID(102), EntryID(103)],  // 租户1有3个任务
//     2: [EntryID(201), EntryID(202)],                 //              
租户2有2个任务                                                          
//     3: [EntryID(301), EntryID(302), EntryID(303), EntryID(304)],  //
租户3有4个任务                                                          
// }

// 移除租户任务的逻辑                                                   
func removeTenantJobs(tenantID uint) {                                  
// 1. 检查租户是否有任务                                            
if entryIDs, exists := s.tenantJobs[tenantID]; exists {             
// 2. 遍历所有任务ID                                            
for _, entryID := range entryIDs {                              
// 3. 从 Cron 引擎中移除                                    
s.cron.Remove(entryID)                                      
}                                                               
// 4. 从映射中删除                                              
delete(s.tenantJobs, tenantID)                                  
}                                                                   
}

// 添加租户任务的逻辑                                                   
func addTenantJobs(tenantID uint, periods []SchedulePeriod) {           
var newEntryIDs []cron.EntryID

      for i, period := range periods {                                    
          // 1. 生成 Cron 表达式                                          
          cronExpr := buildCronExpression(period.StartTime)               
                                                                          
          // 2. 注册到 Cron 引擎                                          
          entryID, _ := s.cron.AddFunc(cronExpr, func() {                 
              s.triggerAttendance(tenantID, i+1, time.Now())              
          })                                                              
                                                                          
          // 3. 收集任务ID                                                
          newEntryIDs = append(newEntryIDs, entryID)                      
      }                                                                   
                                                                          
      // 4. 保存到映射                                                    
      s.tenantJobs[tenantID] = newEntryIDs                                
}

3.3 配置回退逻辑

// 决策树                                                               
func reloadTenantSchedule(tenantID uint) {                              
ctx := tenantctx.WithTenantID(context.Background(), tenantID)

      // 尝试从数据库加载                                                 
      periods, err := s.schedulePeriodRepo.ListActive(ctx)                
                                                                          
      if err != nil {                                                     
          // 情况1：数据库查询失败                                        
          logger.Warn("数据库查询失败，使用配置文件回退")                 
          loadFromConfigFile(tenantID)                                    
          return                                                          
      }                                                                   
                                                                          
      if len(periods) == 0 {                                              
          // 情况2：数据库无配置                                          
          logger.Warn("数据库无配置，使用配置文件回退")                   
          loadFromConfigFile(tenantID)                                    
          return                                                          
      }                                                                   
                                                                          
      // 情况3：数据库有配置，正常加载                                    
      loadFromDatabase(tenantID, periods)                                 
}

// 配置文件回退                                                         
func loadFromConfigFile(tenantID uint) {                                
// 从 YAML 配置读取                                                 
periods := s.scheduleCfg.Periods

      // 创建任务（逻辑与数据库加载相同）                                 
      for i, period := range periods {                                    
          cronExpr := buildCronExpression(period.Start)  //               
注意：配置文件用 Start 字段                                             
s.cron.AddFunc(cronExpr, handler)                               
}                                                                   
}

3.4 考勤计算逻辑

// 核心公式：应到人数 = 候选用户 - 有课用户

func GetAttendanceDetail(ctx context.Context, req                       
*AttendanceDetailRequest) (*AttendanceDetail, error) {                  
// 步骤1：查询候选用户                                              
// 条件：status = 1（参与考勤）                                     
candidates, _ := userRepo.ListByStatus(ctx, 1)                      
candidateIDs := extractIDs(candidates)  // [1, 2, 3, 4, 5, 6, 7, 8]

      // 步骤2：查询有课用户                                              
      // 条件：在指定周、星期、节次有课程安排                             
      busyUsers, _ := courseRepo.FindBusyUsers(ctx, req.Week, req.Weekday,
req.Section)                                                           
busyUserIDs := extractIDs(busyUsers)  // [3, 5, 7]

      // 步骤3：计算应到用户                                              
      // 应到 = 候选 - 有课                                               
      shouldAttendIDs := difference(candidateIDs, busyUserIDs)  // [1, 2, 
4, 6, 8]

      // 步骤4：查询请假用户                                              
      // 条件：请假时间与考勤时间有重叠                                   
      leaveUsers, _ := leaveRepo.FindOverlapping(ctx, req.Date, startTime,
endTime)                                                               
leaveUserIDs := extractIDs(leaveUsers)  // [2, 6]

      // 步骤5：计算统计数据                                              
      statistics := &AttendanceStatistics{                                
          ShouldAttend: len(shouldAttendIDs),           // 5              
          OnTime:       len(shouldAttendIDs) - len(leaveUserIDs),  // 3   
          Leave:        len(leaveUserIDs),              // 2              
          NotArrived:   0,  // 调度器触发时默认为0，后续打卡后更新        
      }                                                                   
                                                                          
      return &AttendanceDetail{                                           
          Statistics: statistics,                                         
          Users:      buildUserDetails(shouldAttendIDs, leaveUserIDs),    
      }                                                                   
}
                                                                          
---                                                                     
四、场景模拟

场景1：系统首次启动

前置条件

数据库状态：

-- tenants 表                                                           
| id | name      | corp_id | status |                                   
|----|-----------|---------|--------|                                   
| 1  | 阳光小学   | corp001 | 1      |                                  
| 2  | 明德中学   | corp002 | 1      |

-- schedule_settings 表                                                 
| id | tenant_id | current_mode |                                       
|----|-----------|--------------|                                       
| 1  | 1         | school       |                                       
| 2  | 2         | holiday      |

-- schedule_periods 表（租户1 - school模式）                            
| id | tenant_id | mode   | name    | start_time | end_time |           
|----|-----------|--------|---------|------------|----------|           
| 1  | 1         | school | 第1节   | 08:00:00   | 08:45:00 |           
| 2  | 1         | school | 第2节   | 10:00:00   | 10:45:00 |           
| 3  | 1         | school | 第3节   | 14:00:00   | 14:45:00 |

-- schedule_periods 表（租户2 - holiday模式）                           
| id | tenant_id | mode    | name    | start_time | end_time |          
|----|-----------|---------|---------|------------|----------|          
| 4  | 2         | holiday | 上午班   | 09:00:00   | 11:00:00 |         
| 5  | 2         | holiday | 下午班   | 15:00:00   | 17:00:00 |

执行流程

[2026-01-22 07:00:00] 系统启动                                          
│                                                                       
├─► [07:00:00.100] 初始化调度器                                         
│   INFO: 考勤调度器启动                                                
│                                                                       
├─► [07:00:00.150] 查询活跃租户                                         
│   SQL: SELECT * FROM tenants WHERE status = 1                         
│   结果: [租户1, 租户2]                                                
│                                                                       
├─► [07:00:00.200] 加载租户1配置                                        
│   ├─► 构造上下文: ctx = WithTenantID(ctx, 1)                          
│   ├─► 查询配置:                                                       
│   │   SQL1: SELECT current_mode FROM schedule_settings WHERE tenant_id
= 1                                                                    
│   │   结果: mode = "school"                                           
│   │   SQL2: SELECT * FROM schedule_periods WHERE tenant_id = 1 AND    
mode = 'school'                                                         
│   │   结果: 3条记录                                                   
│   │                                                                   
│   ├─► 创建任务1: section=1, startTime=08:00:00                        
│   │   cronExpr = "3 8 * * *"                                          
│   │   entryID = 101                                                   
│   │   INFO: 添加考勤定时任务, tenantId=1, section=1, periodName=第1节,
cronExpr=3 8 * * *
│   │                                                                   
│   ├─► 创建任务2: section=2, startTime=10:00:00                        
│   │   cronExpr = "3 10 * * *"                                         
│   │   entryID = 102                                                   
│   │   INFO: 添加考勤定时任务, tenantId=1, section=2, periodName=第2节,
cronExpr=3 10 * * *
│   │                                                                   
│   ├─► 创建任务3: section=3, startTime=14:00:00                        
│   │   cronExpr = "3 14 * * *"                                         
│   │   entryID = 103                                                   
│   │   INFO: 添加考勤定时任务, tenantId=1, section=3, periodName=第3节,
cronExpr=3 14 * * *
│   │                                                                   
│   └─► 保存任务映射: tenantJobs[1] = [101, 102, 103]                   
│                                                                       
├─► [07:00:00.300] 加载租户2配置                                        
│   ├─► 构造上下文: ctx = WithTenantID(ctx, 2)                          
│   ├─► 查询配置:                                                       
│   │   SQL1: SELECT current_mode FROM schedule_settings WHERE tenant_id
= 2                                                                    
│   │   结果: mode = "holiday"                                          
│   │   SQL2: SELECT * FROM schedule_periods WHERE tenant_id = 2 AND    
mode = 'holiday'                                                        
│   │   结果: 2条记录                                                   
│   │                                                                   
│   ├─► 创建任务1: section=1, startTime=09:00:00                        
│   │   cronExpr = "3 9 * * *"                                          
│   │   entryID = 201                                                   
│   │   INFO: 添加考勤定时任务, tenantId=2, section=1,                  
periodName=上午班, cronExpr=3 9 * * *
│   │                                                                   
│   ├─► 创建任务2: section=2, startTime=15:00:00                        
│   │   cronExpr = "3 15 * * *"                                         
│   │   entryID = 202                                                   
│   │   INFO: 添加考勤定时任务, tenantId=2, section=2,                  
periodName=下午班, cronExpr=3 15 * * *
│   │                                                                   
│   └─► 保存任务映射: tenantJobs[2] = [201, 202]                        
│                                                                       
├─► [07:00:00.350] 注册定时重载任务                                     
│   cronExpr = "*/5 * * * *"                                            
│   entryID = 999                                                       
│   INFO: 添加配置重载任务                                              
│                                                                       
└─► [07:00:00.400] 启动 Cron 引擎                                       
s.cron.Start()                                                      
INFO: 考勤调度器已启动，共 6 个定时任务

      任务列表:                                                           
      - EntryID 101: 租户1-第1节 (3 8 * * *)                              
      - EntryID 102: 租户1-第2节 (3 10 * * *)                             
      - EntryID 103: 租户1-第3节 (3 14 * * *)                             
      - EntryID 201: 租户2-上午班 (3 9 * * *)                             
      - EntryID 202: 租户2-下午班 (3 15 * * *)                            
      - EntryID 999: 配置重载 (*/5 * * * *)                               

内存状态

tenantJobs = {                                                          
1: [101, 102, 103],  // 租户1: 3个任务                              
2: [201, 202],       // 租户2: 2个任务                              
}

cron.Entries() = [                                                      
{ID: 101, Schedule: "3 8 * * *",  Next: 2026-01-22 08:03:00},       
{ID: 102, Schedule: "3 10 * * *", Next: 2026-01-22 10:03:00},       
{ID: 103, Schedule: "3 14 * * *", Next: 2026-01-22 14:03:00},       
{ID: 201, Schedule: "3 9 * * *",  Next: 2026-01-22 09:03:00},       
{ID: 202, Schedule: "3 15 * * *", Next: 2026-01-22 15:03:00},       
{ID: 999, Schedule: "*/5 * * * *", Next: 2026-01-22 07:05:00},      
]
                                                                          
---                                                                     
场景2：管理员切换作息模式

前置条件

- 系统已运行（场景1完成）
- 当前时间：2026-01-22 09:30:00
- 租户1当前模式：school（3个时段）

操作步骤

步骤1：管理员切换模式

[09:30:00] 管理员调用 API                                               
POST /api/schedule/switch-mode                                          
{                                                                       
"mode": "holiday"                                                   
}

[09:30:00.050] Service 层处理                                           
func SwitchMode(ctx context.Context, mode string) error {               
tenantID := tenantctx.GetTenantID(ctx)  // 1

      // 更新数据库                                                       
      UPDATE schedule_settings                                            
      SET current_mode = 'holiday'                                        
      WHERE tenant_id = 1                                                 
                                                                          
      return nil                                                          
}

[09:30:00.100] 返回成功响应                                             
{                                                                       
"code": 0,                                                          
"message": "success"                                                
}

数据库变更：

-- schedule_settings 表（变更后）                                       
| id | tenant_id | current_mode |                                       
|----|-----------|--------------|                                       
| 1  | 1         | holiday      |  ← 从 school 改为 holiday             
| 2  | 2         | holiday      |

步骤2：等待定时重载

[09:30:00 - 09:35:00] 等待中...                                         
调度器尚未感知配置变更                                                  
租户1的任务仍然是 school 模式的3个任务

当前任务状态:
- EntryID 101: 租户1-第1节 (3 8 * * *)  ← 仍然存在
- EntryID 102: 租户1-第2节 (3 10 * * *)  ← 仍然存在
- EntryID 103: 租户1-第3节 (3 14 * * *)  ← 仍然存在

步骤3：定时重载触发

[09:35:00] Cron 任务触发（EntryID 999）                                 
│                                                                       
├─► [09:35:00.010] 开始重载                                             
│   INFO: 定时重载租户作息配置                                          
│                                                                       
├─► [09:35:00.020] 查询活跃租户                                         
│   SQL: SELECT * FROM tenants WHERE status = 1                         
│   结果: [租户1, 租户2]                                                
│                                                                       
├─► [09:35:00.030] 重载租户1                                            
│   ├─► 构造上下文: ctx = WithTenantID(ctx, 1)                          
│   │                                                                   
│   ├─► 查询配置:                                                       
│   │   SQL1: SELECT current_mode FROM schedule_settings WHERE tenant_id
= 1                                                                    
│   │   结果: mode = "holiday"  ← 检测到变更！                          
│   │   SQL2: SELECT * FROM schedule_periods WHERE tenant_id = 1 AND    
mode = 'holiday'                                                        
│   │   结果: 2条记录                                                   
│   │                                                                   
│   ├─► 移除旧任务                                                      
│   │   oldEntryIDs = tenantJobs[1]  // [101, 102, 103]                 
│   │   for _, entryID := range oldEntryIDs {                           
│   │       s.cron.Remove(entryID)                                      
│   │   }                                                               
│   │   delete(tenantJobs, 1)                                           
│   │   INFO: 移除租户旧任务, tenantId=1, count=3                       
│   │                                                                   
│   ├─► 创建新任务1: section=1, startTime=09:00:00                      
│   │   cronExpr = "3 9 * * *"                                          
│   │   entryID = 301  ← 新的 EntryID                                   
│   │   INFO: 添加考勤定时任务, tenantId=1, section=1,                  
periodName=上午班, cronExpr=3 9 * * *
│   │                                                                   
│   ├─► 创建新任务2: section=2, startTime=15:00:00                      
│   │   cronExpr = "3 15 * * *"                                         
│   │   entryID = 302  ← 新的 EntryID                                   
│   │   INFO: 添加考勤定时任务, tenantId=1, section=2,                  
periodName=下午班, cronExpr=3 15 * * *
│   │                                                                   
│   └─► 保存任务映射: tenantJobs[1] = [301, 302]                        
│                                                                       
├─► [09:35:00.080] 重载租户2                                            
│   ├─► 查询配置: mode = "holiday"（无变更）                            
│   ├─► 移除旧任务: [201, 202]                                          
│   ├─► 创建新任务: [401, 402]（EntryID 可能变化）                      
│   └─► INFO: 租户2配置无变更，任务已更新                               
│                                                                       
└─► [09:35:00.100] 重载完成                                             
INFO: 配置重载完成

结果对比

变更前（09:30:00）：

租户1任务:
- EntryID 101: 第1节 (3 8 * * *)   - school模式
- EntryID 102: 第2节 (3 10 * * *)  - school模式
- EntryID 103: 第3节 (3 14 * * *)  - school模式

tenantJobs[1] = [101, 102, 103]

变更后（09:35:00）：

租户1任务:
- EntryID 301: 上午班 (3 9 * * *)   - holiday模式
- EntryID 302: 下午班 (3 15 * * *)  - holiday模式

tenantJobs[1] = [301, 302]

关键观察：
- ✅ 旧任务（101, 102, 103）已从 Cron 引擎中移除
- ✅ 新任务（301, 302）已注册到 Cron 引擎
- ✅ 配置变更延迟：5分钟
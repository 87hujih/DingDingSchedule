# 学期配置与周数校验实施计划

## Context

当前系统存在以下问题：

1. **week 与 date 不一致**：`/api/attendance/slots/status` 接口可以传 `date=2025-03-10`（假设是第3周）但 `week=10`，接口不会报错
2. **缺少学期配置**：系统没有学期开始日期的配置，无法计算某个日期属于第几周
3. **课程无学期归属**：当前 `courses` 表没有 `semester_id` 字段，无法区分不同学期的课程

相关代码位置：
- `internal/handler/attendance_handler.go`：`parseCommonQueryParams()` 解析参数，`validateDayOfWeek()` 校验星期
- `internal/service/attendance_service.go`：`GetSlotAttendanceStatus()` 业务逻辑
- `internal/model/course.go`：课程模型（缺少学期关联）

## Goal & Acceptance

目标：引入学期配置表，实现"周数与日期一致性校验"，并将课程与学期绑定。

验收标准：
- 管理员可配置学期开始日期和总周数
- 调用考勤接口时，若 `week` 与 `date` 计算结果不一致，返回明确错误
- 课程与学期绑定，支持按学期查询课程
- 支持多租户隔离（每个租户独立配置学期）
- 兼容性：学期未配置时，跳过周数校验（降级为当前行为）

## Design Summary

### 1. 数据模型

#### 1.1 新增 `semesters` 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint | 主键 |
| tenant_id | uint | 租户ID（多租户隔离） |
| name | varchar(50) | 学期名称（如"2024-2025学年第二学期"） |
| start_date | date | 学期开始日期（第1周周一） |
| total_weeks | int | 总周数（如20） |
| is_active | bool | 是否当前生效学期 |
| created_at | datetime | 创建时间 |
| updated_at | datetime | 更新时间 |

约束：
- 每个租户同一时间只能有一个 `is_active=true` 的学期
- `start_date` 必须是周一

#### 1.2 修改 `courses` 表

新增字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| semester_id | uint | 所属学期ID（外键关联 semesters.id） |

索引：
- `idx_courses_semester_id`：加速按学期查询
- `idx_courses_tenant_semester_user`：复合索引 (tenant_id, semester_id, user_id)

#### 1.3 课程与学期的关系

```
Semester 1:N Course
- 一个学期包含多门课程
- 一门课程只属于一个学期
- 查询课程时默认按当前激活学期过滤
```

### 2. 周数计算逻辑

```
给定 date 和 semester.start_date：
1. 计算 date 与 start_date 的天数差 days
2. 若 days < 0，说明 date 在学期开始之前，返回错误
3. derived_week = days / 7 + 1
4. 若 derived_week > total_weeks，说明 date 超出学期范围，返回错误
5. 若 derived_week != 传入的 week，返回"周数与日期不一致"错误
```

### 3. 校验时机

在 `attendance_handler.go` 的 `SlotAttendanceStatus()` 中，`parseCommonQueryParams()` 之后增加 `validateWeekDate()` 调用。

### 4. 管理方式

#### 4.1 GoAdmin 后台管理

学期管理通过 GoAdmin 后台进行，不提供独立 API。

在 `internal/adminui/tables/` 下新增 `semesters.go`，配置：
- 列表页：显示 ID、租户、名称、开始日期、总周数、是否激活、创建时间
- 表单页：支持创建/编辑学期
- 校验：`start_date` 必须是周一
- 激活逻辑：激活某学期时自动将同租户其他学期设为非激活

#### 4.2 公共接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/semesters/current | 获取当前生效学期（供前端获取学期信息） |

## Implementation Checklist

### Phase 1：学期数据层

- [ ] 新增 `internal/model/semester.go`：定义 `Semester` 结构体
- [ ] 新增 `internal/repository/semester_repository.go`：实现 CRUD 和 `GetActiveSemester()`
- [ ] 更新 `inits/database.go`：AutoMigrate 添加 `&model.Semester{}`

### Phase 2：课程表改造

- [ ] 修改 `internal/model/course.go`：
  - [ ] 新增 `SemesterID` 字段（可为空，兼容历史数据）
  - [ ] 添加 `Semester` 关联（GORM BelongsTo）
- [ ] 修改 `internal/repository/course_repository.go`：
  - [ ] 查询方法增加 `semester_id` 过滤条件
  - [ ] 新增 `GetCoursesBySemester()` 方法
- [ ] 修改课程相关 Service/Handler：
  - [ ] 创建课程时自动关联当前激活学期
  - [ ] 查询课程时默认按当前学期过滤（可选参数覆盖）

### Phase 3：学期服务层

- [ ] 新增 `internal/service/semester_service.go`：
  - [ ] `CreateSemester()` - 创建学期（校验 start_date 是否为周一）
  - [ ] `ActivateSemester()` - 激活学期（同时将其他学期设为非激活）
  - [ ] `GetActiveSemester()` - 获取当前生效学期
  - [ ] `CalculateWeekFromDate()` - 根据日期计算周数

### Phase 4：周数校验逻辑

- [ ] 修改 `internal/handler/attendance_handler.go`：
  - [ ] 新增 `validateWeekDate(ctx, date, week)` 方法
  - [ ] 在 `SlotAttendanceStatus()` 中调用校验
  - [ ] 在 `SlotUserLeaveDetail()` 中调用校验
- [ ] 错误码定义：
  - [ ] `CodeSemesterNotConfigured` - 学期未配置（可选：跳过校验或返回警告）
  - [ ] `CodeDateOutOfSemester` - 日期不在学期范围内
  - [ ] `CodeWeekDateMismatch` - 周数与日期不一致

### Phase 5：GoAdmin 后台管理

- [ ] 新增 `internal/adminui/tables/semesters.go`：
  - [ ] 列表页配置（ID、租户、名称、开始日期、总周数、是否激活）
  - [ ] 表单页配置（创建/编辑学期）
  - [ ] 租户下拉选择（关联 tenants 表）
  - [ ] 激活状态单选（是/否）
  - [ ] `start_date` 周一校验（PostHook）
  - [ ] 激活时自动将同租户其他学期设为非激活（PostHook）
- [ ] 更新 `internal/adminui/tables/tenants.go`：在 `Generators` 中注册 `"semesters": GetSemesterTable`

### Phase 6：公共接口

- [ ] 新增 `internal/handler/semester_handler.go`：实现 `GetCurrentSemester()` 接口
- [ ] 新增 `internal/app/routers_semester.go`：注册 `/api/semesters/current` 路由

## Verification

### 单元测试

- [ ] `CalculateWeekFromDate()` 边界测试：
  - 学期第1天 → week=1
  - 学期第7天 → week=1
  - 学期第8天 → week=2
  - 学期开始前1天 → 错误
  - 超出总周数 → 错误

### 集成测试

- [ ] 创建学期 → 激活 → 调用考勤接口验证校验生效
- [ ] 传入错误的 week → 返回 `CodeWeekDateMismatch`
- [ ] 未配置学期 → 跳过校验（或返回警告）

### 多租户验证

- [ ] 租户A配置学期，租户B未配置 → 各自独立
- [ ] 租户A的学期配置不影响租户B

## Guardrails

- `start_date` 必须是周一，否则拒绝创建
- 每个租户最多一个激活学期
- 学期配置变更不影响历史考勤数据
- 未配置学期时降级处理，不阻断业务（可配置是否强制校验）
- 删除学期前需检查是否有关联课程（有则拒绝或提示迁移）

## Migration Notes

### 数据库迁移

1. **新增 semesters 表**：通过 AutoMigrate 自动创建
2. **courses 表新增 semester_id 字段**：
   - 字段设为可空（`*uint`），兼容历史数据
   - 历史课程的 `semester_id` 为 NULL

### 首次部署的历史数据处理

| 策略 | 说明 | 适用场景 |
|------|------|---------|
| A. 保持 NULL | 历史课程不关联学期，查询时特殊处理 | 历史数据量大，不想迁移 |
| B. 创建历史学期 | 创建一个"历史学期"，将所有旧课程关联到该学期 | 需要统一管理 |
| C. 软删除旧数据 | 新学期开始时，旧课程自动软删除，用户重新导入 | 每学期课表变化大 |

推荐 **策略A**，实现简单，查询时：
```
WHERE semester_id = :current_semester_id OR semester_id IS NULL
```

### 部署步骤

1. 部署新版本（自动迁移表结构）
2. 管理员创建当前学期并激活
3. 用户新导入的课程自动关联当前学期
4. 历史课程保持 `semester_id = NULL`，逐步淘汰

---

## 学期切换策略（重要）

当新学期开始、管理员激活新学期时，上学期的课程数据如何处理？

### 采用方案：保留历史 + 重新导入

1. 管理员创建新学期并激活
2. 旧学期课程保持不变（`semester_id` 指向旧学期）
3. 用户查询课程时，默认只返回当前激活学期的课程
4. 用户重新导入新学期课表

### 学期切换流程

```
┌─────────────────────────────────────────────────────────────┐
│                    学期切换流程                              │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. 管理员在 GoAdmin 后台创建新学期                          │
│     /admin/info/semesters/new                              │
│     填写：名称、开始日期、总周数、激活状态                    │
│                                                             │
│  2. 激活新学期（勾选"是否激活"）                             │
│     - 自动将同租户其他学期设为非激活                         │
│     - 旧课程数据不变                                         │
│                                                             │
│  3. 用户重新导入课表                                         │
│     POST /api/courses (批量导入新课表)                       │
│     - 新课程自动关联当前激活学期                              │
│                                                             │
│  4. 查询课程（自动按当前学期过滤）                            │
│     GET /api/courses                                        │
│     - 默认返回当前激活学期的课程                              │
│     - 可选参数 semester_id 查看历史学期                      │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 数据查询逻辑

```sql
-- 获取用户当前学期课程
SELECT * FROM courses
WHERE user_id = ?
  AND tenant_id = ?
  AND deleted_at IS NULL
  AND (semester_id = :current_semester_id OR semester_id IS NULL)

-- 获取用户指定学期课程（历史查询）
SELECT * FROM courses
WHERE user_id = ?
  AND tenant_id = ?
  AND semester_id = :specified_semester_id
  AND deleted_at IS NULL
```

### 配置项

```yaml
semester:
  validation:
    enabled: true           # 是否启用周数校验
    strict: false           # 严格模式：true=学期未配置时报错，false=跳过校验
  course:
    require_semester: false # 创建课程是否必须指定学期（false=自动使用当前激活学期）
```
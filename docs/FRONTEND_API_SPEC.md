# Frontend Integration Guide (FRONTEND_API_SPEC)

本文件为前端联调使用的接口规范与集成说明，基于代码实际扫描汇总，包含模块说明、接口清单、参数校验与枚举值定义。

注意：所有接口响应均遵循统一格式：

```json
// 成功
{
  "code": 0,
  "message": "success",
  "data": { ... }
}

// 失败
{
  "code": <错误码>,
  "message": "错误信息",
  "data": null
}
```

通用错误码定义见 internal/response/code.go:11。

---

## 1. 模块总览（按路由分组）

- 健康检查（Public）
  - GET /health（internal/app/router.go:49）
- 认证（Public）
  - POST /api/auth/login（internal/app/routers_auth.go:13 → internal/handler/auth_handler.go:24）
- 用户（Protected，需携带 Authorization: Bearer <token>）
  - GET /api/users/me（internal/app/routers_user.go:14 → internal/handler/user_handler.go:49）
  - POST /api/users/refresh（internal/app/routers_user.go:15 → internal/handler/user_handler.go:128）
  - GET /api/users/:id（internal/app/routers_user.go:16 → internal/handler/user_handler.go:70）
  - GET /api/search（internal/app/routers_user.go:21 → internal/handler/user_handler.go:92）
- 管理员（Protected + RequireAdmin）
  - 部门：GET/POST/PATCH/DELETE /api/admin/departments...（internal/app/routers_admin.go:17-20 → internal/handler/department_handler.go）
  - 用户：POST/PATCH/DELETE /api/admin/users...（internal/app/routers_admin.go:26-28 → internal/handler/user_handler.go）
- 课表（Protected）
  - /api/schedules/...（internal/app/routers_schedule.go:13-18 → internal/handler/schedule_handler.go）
- 考勤（Protected，部分管理员）
  - /api/attendance/...（internal/app/routers_attendance.go:14-24 → internal/handler/attendance_handler.go, attendance_record_handler.go）
- 学期（Protected）
  - GET /api/semesters/current（internal/app/router.go:70 → internal/handler/semester_handler.go:21）
- 作息设置（Protected）
  - /api/schedule/info|current-mode|switch-mode（internal/app/router.go:77-79 → internal/handler/schedule_setting_handler.go）

中间件：
- JWTAuth（所有 Protected 路由组，internal/app/router.go:58；internal/middleware/jwt_auth.go:51）
- RequireAdmin（管理员路由组，internal/app/routers_admin.go:12；internal/middleware/jwt_auth.go:131）

---

## 2. 接口详情

说明：
- Auth: 是否需要携带 Authorization 头（Bearer Token）
- Admin: 是否需要管理员权限（RequireAdmin）
- Handler: 对应处理函数（文件:行号）

### 2.1 健康检查

| Method | URL | Auth | Admin | Handler | 200 响应 |
|---|---|---|---|---|---|
| GET | /health | 否 | 否 | internal/app/router.go:49 | `{"status":"ok"}` |

---

### 2.2 认证（Auth）

| Method | URL | Auth | Admin | Handler | 请求体验证 | 200 响应 | 4xx/5xx |
|---|---|---|---|---|---|---|---|
| POST | /api/auth/login | 否 | 否 | internal/handler/auth_handler.go:24 | `dto.LoginRequest`：auth_code(required), corp_id(required)（internal/dto/auth.go:4-7） | `dto.LoginResponse`（token, expires_in, user） | 20001 参数无效；10003 授权码无效；50001/50003 钉钉服务错误 |

请求体示例：
```json
{
  "auth_code": "dingtalk_temp_code",
  "corp_id": "dingxxxx"
}
```

成功响应示例：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "token": "<JWT>",
    "expires_in": 259200,
    "user": {
      "id": 1,
      "ding_user_id": "abc",
      "name": "张三",
      "avatar": "...",
      "phone": "13800138000",
      "role": 0,
      "role_name": "普通用户",
      "dept_ids": [1,2]
    }
  }
}
```

---

### 2.3 用户（Users，需 Auth）

| Method | URL | Auth | Admin | Handler | 请求参数 | 200 响应 | 4xx/5xx |
|---|---|---|---|---|---|---|---|
| GET | /api/users/me | 是 | 否 | internal/handler/user_handler.go:49 | - | `dto.GetUserResponse`（internal/dto/user.go:16-27） | 31001 用户不存在 |
| POST | /api/users/refresh | 是 | 否 | internal/handler/user_handler.go:128 | - | `dto.LoginUser`（同步后的基础信息） | 10000 未登录；50003 拉取钉钉用户失败 |
| GET | /api/users/:id | 是 | 否 | internal/handler/user_handler.go:70 | path: id | `dto.GetUserResponse` | 20001 参数无效；10004 无权限；31001 不存在 |
| GET | /api/search | 是 | 否 | internal/handler/user_handler.go:92 | query: keyword, page(默认1), page_size(默认10) | `dto.UserListResponse` | - |

---

### 2.4 管理员（Admin，需 Auth + Admin）

部门管理：

| Method | URL | Auth | Admin | Handler | 请求体验证 | 200 响应 | 4xx/5xx |
|---|---|---|---|---|---|---|---|
| GET | /api/admin/departments | 是 | 是 | internal/handler/department_handler.go:23-32 | - | `dto.DepartmentListResponse` | - |
| POST | /api/admin/departments/sync | 是 | 是 | internal/handler/department_handler.go:34-43 | - | message: "部门数据同步成功" | 500xx 外部或内部错误 |
| PATCH | /api/admin/departments/:id/status | 是 | 是 | internal/handler/department_handler.go:46-66 | `dto.UpdateDeptStatusRequest.status` oneof=0 1（internal/dto/department.go:31-33） | message: "更新成功" | 20001 参数无效 |
| DELETE | /api/admin/departments/:id | 是 | 是 | internal/handler/department_handler.go:69-84 | path: id | message: "删除成功" | 20001 参数无效 |

用户管理：

| Method | URL | Auth | Admin | Handler | 请求体验证 | 200 响应 | 4xx/5xx |
|---|---|---|---|---|---|---|---|
| POST | /api/admin/users/sync_all | 是 | 是 | internal/handler/user_handler.go:160-175 | `dto.SyncAllUsersRequest`（limit, offset，可空） | `dto.SyncAllUsersResponse` | - |
| PATCH | /api/admin/users/:id/status | 是 | 是 | internal/handler/user_handler.go:177-198 | `dto.UpdateUserStatusRequest.status` required, oneof=0 1（internal/dto/user.go:138-140） | message: "更新成功" | 20001 参数无效 |
| DELETE | /api/admin/users/:id | 是 | 是 | internal/handler/user_handler.go:200-215 | path: id | message: "删除成功" | 20001 参数无效 |

---

### 2.5 课表（Schedules，需 Auth）

| Method | URL | Auth | Admin | Handler | 请求参数/体验证 | 200 响应 | 4xx/5xx |
|---|---|---|---|---|---|---|---|
| POST | /api/schedules/import | 是 | 否 | internal/handler/schedule_handler.go:22-48 | form-data: file(required) | `{ inserted: number }` | 20002 缺少参数；500xx 解析失败 |
| GET | /api/schedules/week | 是 | 否 | internal/handler/schedule_handler.go:50-96 | query: week(required, int>0), user_id(optional) | `dto.ScheduleListResponse` | 20001/20002 参数错误 |
| GET | /api/schedules/all | 是 | 否 | internal/handler/schedule_handler.go:98-123 | query: page, page_size | `dto.AllCoursesListResponse` | - |
| POST | /api/schedules/create | 是 | 否 | internal/handler/schedule_handler.go:125-143 | `dto.CreateCourseRequest`（见下） | `{ id: number }` | 20001 参数无效 |
| PUT | /api/schedules/update/:id | 是 | 否 | internal/handler/schedule_handler.go:145-167 | `dto.UpdateCourseRequest` | `null` | 20001 参数无效 |
| DELETE | /api/schedules/delete/:id | 是 | 否 | internal/handler/schedule_handler.go:170-186 | path: id | `null` | 20001 参数无效 |

CreateCourseRequest（internal/dto/schedule.go:59-67）：
- course_name required
- day_of_week required, min=1, max=7
- section required, min=1
- week_list required（如 "1,2,3" 或包含区间）

UpdateCourseRequest（internal/dto/schedule.go:70-77）：
- day_of_week: omitempty,min=1,max=7
- section: omitempty,min=1

---

### 2.6 考勤（Attendance，部分接口需 Admin）

| Method | URL | Auth | Admin | Handler | Query | 200 响应 |
|---|---|---|---|---|---|---|
| GET | /api/attendance/slots/status | 是 | 否 | internal/handler/attendance_handler.go:24-67 | date(YYYY-MM-DD, required), week(required, >0), section(required, >0), dept_ids(optional, 逗号分隔), day_of_week(optional) | `dto.SlotAttendanceStatusResponse`（internal/dto/schedule.go:140-148） |
| GET | /api/attendance/slots/users/:user_id/leave | 是 | 是 | internal/handler/attendance_handler.go:69-100 | path: user_id + 与上相同的 date/week/section | `dto.SlotUserLeaveDetailResponse`（internal/dto/schedule.go:160-170） |

考勤统计记录（Admin）：

| Method | URL | Auth | Admin | Handler | 请求/查询 | 200 响应 |
|---|---|---|---|---|---|---|
| GET | /api/attendance/record/detail | 是 | 是 | internal/handler/attendance_record_handler.go:31-70 | query: date(required), week(required), section(required), dept_ids(optional) | `dto.AttendanceDetailResponse`（internal/dto/attendance_record.go:29-37） |
| GET | /api/attendance/record/snapshot | 是 | 是 | internal/handler/attendance_record_handler.go:119-157 | 同上 | `dto.AttendanceDetailResponse`（由数据库快照构造） |
| POST | /api/attendance/record/trigger | 是 | 是 | internal/handler/attendance_record_handler.go:72-117 | body: `dto.AttendanceTriggerRequest`（date(required), week>=1, section>=1, dept_ids[]） | `dto.AttendanceDetailResponse`（并持久化） |
| GET | /api/attendance/record/list | 是 | 是 | internal/handler/attendance_record_handler.go:160-189 | query: date(required), dept_ids(optional) | 列表（服务返回结构） |

公共查询参数解析与校验：internal/handler/common.go:22-60、62-71、73-100。

---

### 2.7 学期（Semesters，需 Auth）

| Method | URL | Auth | Admin | Handler | 200 响应 | 4xx |
|---|---|---|---|---|---|---|
| GET | /api/semesters/current | 是 | 否 | internal/handler/semester_handler.go:21-28 | `model.Semester` | 30001 当前无激活学期 |

---

### 2.8 作息设置（Schedule Settings，需 Auth）

| Method | URL | Auth | Admin | Handler | 请求体验证 | 200 响应 |
|---|---|---|---|---|---|---|
| GET | /api/schedule/info | 是 | 否 | internal/handler/schedule_setting_handler.go:20-29 | - | 作息配置对象（periods, current_mode 等，由 service 返回） |
| GET | /api/schedule/current-mode | 是 | 否 | internal/handler/schedule_setting_handler.go:48-56 | - | `{ current_mode: string }` |
| POST | /api/schedule/switch-mode | 是 | 否 | internal/handler/schedule_setting_handler.go:31-46 | `dto.SwitchModeRequest.mode` required, oneof=school holiday（internal/dto/schedule_setting.go:3-6） | `{ message: "切换成功", current_mode: string }` |

---

## 3. 前端校验规则（根据 binding 标签提取）

- Auth
  - LoginRequest（internal/dto/auth.go:4-7）
    - auth_code: required
    - corp_id: required
- Users (Admin)
  - UpdateUserStatusRequest（internal/dto/user.go:138-140）：status required, oneof=0 1
  - SyncAllUsersRequest：无 binding，limit/offset 可空
- Departments (Admin)
  - UpdateDeptStatusRequest（internal/dto/department.go:31-33）：status oneof=0 1
- Schedules
  - CreateCourseRequest（internal/dto/schedule.go:59-67）
    - course_name: required
    - day_of_week: required, min=1, max=7
    - section: required, min=1
    - week_list: required
  - UpdateCourseRequest（internal/dto/schedule.go:70-77）
    - day_of_week: omitempty,min=1,max=7
    - section: omitempty,min=1
- Attendance Records (Admin 查询/触发)
  - AttendanceDetailRequest（internal/dto/attendance_record.go:11-17）
    - date: required, format YYYY-MM-DD（form）
    - week: required, min=1（form）
    - section: required, min=1（form）
  - AttendanceTriggerRequest（internal/dto/attendance_record.go:20-25）
    - date: required, format YYYY-MM-DD（json）
    - week: required, min=1
    - section: required, min=1
- Schedule Settings
  - SwitchModeRequest（internal/dto/schedule_setting.go:3-6）
    - mode: required, oneof=school holiday

补充校验：部分 Handler 对 query 参数进行了额外校验，如：
- week/date/section 格式与范围（internal/handler/common.go）
- day_of_week 与 date 一致性可选校验（internal/handler/attendance_handler.go:138-156）

---

## 4. 枚举值与业务含义

- 角色（internal/consts/role.go:4-15）
  - RoleUser = 0 → 普通用户
  - RoleAdmin = 1 → 管理员
  - RoleSuperAdmin = 2 → 超级管理员

- 用户/部门状态（参与考勤标志）
  - 0：不参与考勤（internal/dto/user.go:139 注释；internal/dto/department.go:32 注释）
  - 1：参与考勤

- 作息模式（Schedule Settings）
  - mode：school / holiday（internal/dto/schedule_setting.go:5）

- 错误码（internal/response/code.go:11-108）
  - 0：success
  - 10xxx：认证/身份（10000 未登录，10004 无权限等）
  - 20xxx：参数（20001 参数无效，20002 缺少参数等）
  - 30xxx：业务（30001 资源不存在 等）
  - 31xxx：用户（31001 用户不存在 等）
  - 32xxx：考勤/排班（32001 排班不存在 等）
  - 40xxx：系统（40001 服务器内部错误 等）
  - 50xxx：钉钉服务（50001 钉钉服务异常；50003 获取钉钉用户信息失败）

- 其它字段含义（从 DTO 可推断）
  - day_of_week：1-7（周一至周日）
  - section：节次（>=1，结合作息配置 periods 计算时间窗）

---

## 5. 认证与上下文

- 认证头：`Authorization: Bearer <token>`（internal/middleware/jwt_auth.go:51-71）
- JWT 解析后写入 Gin Context：user_id, ding_user_id, user_name, user_role, tenant_id, corp_id（internal/middleware/jwt_auth.go:84-96）
- 多租户：tenant_id 注入到 request context，DAO 自动按租户隔离（internal/middleware/jwt_auth.go:98-101）
- 管理员鉴权：RequireAdmin = RequireRole(1)（internal/middleware/jwt_auth.go:130-133）

---

## 6. 示例请求头

```http
Authorization: Bearer <JWT>
Content-Type: application/json
```

---

## 7. 变更与来源

本规范由代码扫描生成，关键引用：
- 路由注册：internal/app/router.go:47-65 与 routes_* 文件
- 处理器：internal/handler/*.go
- DTO：internal/dto/*.go
- 中间件：internal/middleware/jwt_auth.go
- 错误码：internal/response/code.go

若后端代码调整，请重新生成或手动同步本文件。

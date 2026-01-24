# 前端开发者项目指南

本文档面向首次接触 Schedule Server 项目的前端开发人员，帮助你快速理解项目全貌、核心概念和开发要点。

---

## 一、项目是什么？

### 1.1 一句话概述

Schedule Server 是一套**多租户排课考勤管理系统**，专为教育/培训机构设计，深度集成钉钉平台。

### 1.2 解决什么问题？

| 用户痛点 | 系统方案 |
|----------|----------|
| 手工统计考勤效率低 | 自动计算「应到人员」，实时生成统计报表 |
| 课表分散在 Excel/纸质文件 | 支持 Excel 导入，统一存储、按周查询 |
| 请假审批与考勤脱节 | 钉钉审批自动同步，请假自动扣减考勤 |
| 多校区数据混杂 | 多租户架构，数据完全隔离 |

### 1.3 系统定位

```
┌─────────────────────────────────────────────────────────────┐
│                       用户视角                               │
├─────────────────────────────────────────────────────────────┤
│  教师/学员          教务管理员          平台运维              │
│  ├── 查看自己课表   ├── 管理所有用户    ├── 跨租户管理       │
│  ├── 查看考勤状态   ├── 同步钉钉数据    └── (预留)          │
│  └── 请假           ├── 统计考勤                            │
│                     └── 配置学期/作息                       │
└─────────────────────────────────────────────────────────────┘
```

---

## 二、核心概念速查

作为前端开发者，你需要理解以下核心概念：

### 2.1 多租户（Tenant）

**什么是租户？**
- 一个租户 = 一个企业/机构
- 每个租户拥有独立的用户、部门、课程、考勤数据
- 数据完全隔离，A 企业看不到 B 企业的任何数据

**前端影响：**
- 登录后，后端会在 JWT 中携带 `tenant_id`
- 所有 API 请求自动按租户过滤，无需前端传递租户参数
- 不同租户可能有不同的作息配置

### 2.2 角色权限

| 角色值 | 名称 | 权限范围 | 典型场景 |
|--------|------|----------|----------|
| 0 | 普通用户 | 仅可访问自己的数据 | 教师/学员 |
| 1 | 管理员 | 可访问租户内所有数据 | 教务管理员 |
| 2 | 超级管理员 | 跨租户管理（预留） | 平台运维 |

**前端影响：**
- 登录响应包含 `role` 字段
- 根据角色显示/隐藏功能模块（如管理员才能看到「同步」「统计」按钮）
- 调用管理员接口时，后端会自动校验权限

### 2.3 学期与周次

**学期（Semester）：**
- 每个租户同一时间只有一个「激活学期」
- 学期定义了开学日期、总周数
- 课程导入时自动关联当前激活学期

**周次计算：**
```
当前周 = (当前日期 - 开学日期) / 7 + 1

示例：
开学日期 = 2026-02-09（周一）
当前日期 = 2026-02-20（周五）
当前周 = (11天 / 7) + 1 = 2（第2周）
```

**前端影响：**
- 查询课表需要传 `week` 参数
- 可调用 `/api/semesters/current` 获取当前学期信息
- 周次列表格式如 `"1,2,3,5,8-10"`，表示第1、2、3、5、8到10周有课

### 2.4 节次与作息

**节次（Section）：**
- 一天分为多个教学时段，每个时段称为一个「节次」
- 节次从 1 开始编号

**作息模式：**
| 模式 | 标识 | 说明 |
|------|------|------|
| 上学模式 | `school` | 正常教学期间，节次时间固定 |
| 假期模式 | `holiday` | 寒暑假/节假日，节次时间可能不同 |

**作息时段示例：**
```
上学模式:
  节次1: 08:00 - 09:40
  节次2: 10:00 - 11:40
  节次3: 14:00 - 15:40
  节次4: 16:00 - 17:40

假期模式:
  节次1: 09:00 - 11:30
  节次2: 14:00 - 16:30
```

**前端影响：**
- 调用 `/api/schedule/info` 获取作息配置
- 考勤查询需要传 `section` 参数
- 管理员可切换作息模式

### 2.5 考勤计算逻辑

**核心公式：**
```
应到人员 = 参与考勤的候选人 - 当前时段有课人员
```

**考勤状态：**
| 状态 | 说明 |
|------|------|
| 应到 | 参与考勤 + 该时段无课 |
| 请假 | 有请假，且时间重叠 |
| 未到 | 应到但未打卡、未请假 |

**前端影响：**
- 考勤查询需要 `date`、`week`、`section` 三个参数
- 可选按 `dept_ids` 过滤部门
- 返回的用户列表已按考勤状态分类

---

## 三、认证与鉴权

### 3.1 登录流程（钉钉 SSO）

```
┌──────────┐    ①获取免登码     ┌──────────────┐
│ 钉钉客户端 │ ───────────────> │ 前端页面      │
└──────────┘                   └──────┬───────┘
                                      │
                               ②调用登录接口
                               POST /api/auth/login
                               { auth_code, corp_id }
                                      │
                                      ▼
                               ┌──────────────┐
                               │ Schedule     │
                               │ Server       │
                               └──────┬───────┘
                                      │
                               ③调用钉钉API
                               换取用户身份
                                      │
                                      ▼
                               ┌──────────────┐
                               │ 钉钉开放平台  │
                               └──────┬───────┘
                                      │
                               ④返回用户信息
                                      │
                                      ▼
                               ┌──────────────┐
┌──────────┐    ⑥JWT + 用户信息  │ Schedule     │
│ 前端页面  │ <──────────────── │ Server       │
└──────────┘                   └──────────────┘
```

### 3.2 请求认证

**请求头格式：**
```http
Authorization: Bearer <JWT Token>
Content-Type: application/json
```

**JWT 有效期：**
- 默认 72 小时（259200 秒）
- 可调用 `/api/users/refresh` 刷新用户信息（不更新 Token）

### 3.3 权限校验

| 路由类型 | 中间件 | 说明 |
|----------|--------|------|
| Public | 无 | 如 `/health`、`/api/auth/login` |
| Protected | JWTAuth | 需要登录 |
| Admin | JWTAuth + RequireAdmin | 需要管理员权限 |

**前端处理：**
```javascript
// 登录成功后存储 Token
localStorage.setItem('token', response.data.token)
localStorage.setItem('user', JSON.stringify(response.data.user))

// 请求拦截器添加 Token
axios.interceptors.request.use(config => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// 响应拦截器处理 401
axios.interceptors.response.use(
  response => response,
  error => {
    if (error.response?.status === 401) {
      // Token 过期，跳转登录
      localStorage.clear()
      window.location.href = '/login'
    }
    return Promise.reject(error)
  }
)
```

---

## 四、API 使用指南

### 4.1 响应格式

**所有接口统一格式：**
```json
// 成功
{
  "code": 0,
  "message": "success",
  "data": { ... }
}

// 失败
{
  "code": 40001,
  "message": "用户不存在",
  "data": null
}
```

### 4.2 错误码速查

| 范围 | 类型 | 常见错误码 |
|------|------|------------|
| 0 | 成功 | 0 = success |
| 10xxx | 认证 | 10000 未登录, 10004 无权限 |
| 20xxx | 参数 | 20001 参数无效, 20002 缺少参数 |
| 30xxx | 业务 | 30001 资源不存在 |
| 31xxx | 用户 | 31001 用户不存在 |
| 32xxx | 排班 | 32001 排班不存在 |
| 40xxx | 系统 | 40001 服务器内部错误 |
| 50xxx | 钉钉 | 50001 钉钉服务异常 |

### 4.3 常用 API 速查

**认证：**
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/auth/login | 钉钉登录 |

**用户：**
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/users/me | 获取当前用户信息 |
| GET | /api/users/:id | 获取指定用户信息 |
| GET | /api/search | 搜索用户（支持拼音） |
| POST | /api/users/refresh | 刷新用户信息 |

**课表：**
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/schedules/week | 按周查询课表 |
| GET | /api/schedules/all | 查询所有课程（分页） |
| POST | /api/schedules/import | 导入课表（Excel） |
| POST | /api/schedules/create | 新增课程 |
| PUT | /api/schedules/update/:id | 更新课程 |
| DELETE | /api/schedules/delete/:id | 删除课程 |

**考勤：**
| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| GET | /api/attendance/slots/status | 登录 | 查询时段考勤状态 |
| GET | /api/attendance/slots/users/:user_id/leave | 管理员 | 查询用户请假详情 |
| GET | /api/attendance/record/detail | 管理员 | 实时计算考勤明细 |
| GET | /api/attendance/record/snapshot | 管理员 | 查询已保存的考勤快照 |
| POST | /api/attendance/record/trigger | 管理员 | 手动触发考勤统计 |

**学期：**
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/semesters/current | 获取当前激活学期 |

**作息：**
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/schedule/info | 获取作息配置 |
| GET | /api/schedule/current-mode | 获取当前模式 |
| POST | /api/schedule/switch-mode | 切换作息模式 |

**管理员（需 Admin 权限）：**
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/admin/departments | 获取部门列表 |
| POST | /api/admin/departments/sync | 同步钉钉部门 |
| POST | /api/admin/users/sync_all | 同步钉钉用户 |
| PATCH | /api/admin/users/:id/status | 更新用户考勤状态 |
| PATCH | /api/admin/departments/:id/status | 更新部门考勤状态 |

---

## 五、典型开发场景

### 场景 1：登录并获取用户信息

```javascript
// 1. 从钉钉获取免登码（钉钉 JSAPI）
const authCode = await dd.getAuthCode()

// 2. 调用后端登录接口
const res = await axios.post('/api/auth/login', {
  auth_code: authCode,
  corp_id: 'dingxxxxxx'
})

// 3. 存储 Token 和用户信息
const { token, expires_in, user } = res.data.data
localStorage.setItem('token', token)
localStorage.setItem('user', JSON.stringify(user))

// 4. 根据角色跳转
if (user.role >= 1) {
  router.push('/admin/dashboard')
} else {
  router.push('/my/schedule')
}
```

### 场景 2：显示周课表

```javascript
// 1. 获取当前学期
const semesterRes = await axios.get('/api/semesters/current')
const semester = semesterRes.data.data

// 2. 计算当前周（也可以让用户选择）
const startDate = new Date(semester.start_date)
const today = new Date()
const currentWeek = Math.floor((today - startDate) / (7 * 24 * 60 * 60 * 1000)) + 1

// 3. 查询课表
const scheduleRes = await axios.get('/api/schedules/week', {
  params: { week: currentWeek }
})

// 4. 渲染课表
const courses = scheduleRes.data.data.courses
// courses 结构: [{ course_name, day_of_week, section, location, teacher }]
```

### 场景 3：查看考勤状态

```javascript
// 1. 获取作息配置
const infoRes = await axios.get('/api/schedule/info')
const { periods, current_mode } = infoRes.data.data

// 2. 查询指定时段的考勤
const date = '2026-01-22'
const week = 20
const section = 1  // 第一节

const attendanceRes = await axios.get('/api/attendance/slots/status', {
  params: { date, week, section }
})

const { should_arrive, on_leave, busy_users } = attendanceRes.data.data
// should_arrive: 应到用户列表
// on_leave: 请假用户列表
// busy_users: 有课用户列表
```

### 场景 4：管理员同步数据

```javascript
// 1. 同步部门（先同步部门，再同步用户）
await axios.post('/api/admin/departments/sync')

// 2. 同步用户
const syncRes = await axios.post('/api/admin/users/sync_all', {
  limit: 100,  // 可选，每批数量
  offset: 0    // 可选，偏移量
})

// 3. 查看同步结果
const { created, updated, total } = syncRes.data.data
console.log(`新增 ${created} 人，更新 ${updated} 人，共 ${total} 人`)
```

### 场景 5：导入课表

```javascript
// 1. 准备文件（支持 .xls / .xlsx）
const fileInput = document.querySelector('input[type="file"]')
const file = fileInput.files[0]

// 2. 构建 FormData
const formData = new FormData()
formData.append('file', file)

// 3. 调用导入接口
const importRes = await axios.post('/api/schedules/import', formData, {
  headers: { 'Content-Type': 'multipart/form-data' }
})

// 4. 查看导入结果
const { inserted } = importRes.data.data
alert(`成功导入 ${inserted} 条课程`)
```

---

## 六、前端项目建议

### 6.1 推荐技术栈

| 场景 | 推荐方案 |
|------|----------|
| 钉钉小程序 | 钉钉原生小程序 / Taro |
| 钉钉 H5 | Vue 3 + Vite + Vant |
| 独立 Web | Vue 3 + Element Plus / React + Ant Design |
| 状态管理 | Pinia (Vue) / Zustand (React) |
| HTTP 请求 | Axios + 请求/响应拦截器 |

### 6.2 目录结构建议

```
frontend/
├── src/
│   ├── api/              # API 请求封装
│   │   ├── auth.js       # 认证相关
│   │   ├── user.js       # 用户相关
│   │   ├── schedule.js   # 课表相关
│   │   └── attendance.js # 考勤相关
│   ├── stores/           # 状态管理
│   │   ├── user.js       # 用户状态
│   │   └── semester.js   # 学期状态
│   ├── views/            # 页面组件
│   │   ├── Login.vue
│   │   ├── MySchedule.vue
│   │   ├── Attendance.vue
│   │   └── admin/
│   │       ├── Dashboard.vue
│   │       └── Sync.vue
│   ├── components/       # 通用组件
│   ├── utils/            # 工具函数
│   │   ├── request.js    # Axios 封装
│   │   └── week.js       # 周次计算
│   └── router/           # 路由配置
```

### 6.3 Axios 封装示例

```javascript
// src/utils/request.js
import axios from 'axios'

const request = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL,
  timeout: 10000
})

// 请求拦截器
request.interceptors.request.use(
  config => {
    const token = localStorage.getItem('token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  error => Promise.reject(error)
)

// 响应拦截器
request.interceptors.response.use(
  response => {
    const { code, message, data } = response.data
    if (code === 0) {
      return data
    }
    // 业务错误
    return Promise.reject(new Error(message))
  },
  error => {
    if (error.response?.status === 401) {
      localStorage.clear()
      window.location.href = '/login'
    }
    return Promise.reject(error)
  }
)

export default request
```

### 6.4 权限控制示例

```javascript
// src/router/index.js
import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('../views/Login.vue') },
    { path: '/my/schedule', component: () => import('../views/MySchedule.vue'), meta: { requiresAuth: true } },
    { path: '/admin/dashboard', component: () => import('../views/admin/Dashboard.vue'), meta: { requiresAuth: true, requiresAdmin: true } }
  ]
})

router.beforeEach((to, from, next) => {
  const token = localStorage.getItem('token')
  const user = JSON.parse(localStorage.getItem('user') || '{}')

  if (to.meta.requiresAuth && !token) {
    return next('/login')
  }

  if (to.meta.requiresAdmin && user.role < 1) {
    return next('/403')
  }

  next()
})

export default router
```

---

## 七、常见问题

### Q1: 如何判断用户是否有某个时段的课？

后端在考勤查询时会自动计算。前端只需调用 `/api/attendance/slots/status`，返回结果中的 `busy_users` 就是有课的用户列表。

### Q2: 周次列表 `"1-5,8,10-12"` 如何解析？

```javascript
function parseWeekList(weekListStr) {
  const weeks = new Set()
  const parts = weekListStr.split(',')
  for (const part of parts) {
    if (part.includes('-')) {
      const [start, end] = part.split('-').map(Number)
      for (let i = start; i <= end; i++) {
        weeks.add(i)
      }
    } else {
      weeks.add(Number(part))
    }
  }
  return Array.from(weeks).sort((a, b) => a - b)
}

parseWeekList('1-5,8,10-12')  // [1, 2, 3, 4, 5, 8, 10, 11, 12]
```

### Q3: 如何处理 Token 过期？

在 Axios 响应拦截器中检测 401 状态码，清除本地存储并跳转登录页。建议在 Token 即将过期时（如剩余 1 小时）提示用户重新登录。

### Q4: 管理员和普通用户看到的数据有什么区别？

- **普通用户**：只能看到自己的课表和考勤状态
- **管理员**：可以看到租户内所有用户的数据，可以执行同步、统计等操作

后端会根据 JWT 中的角色自动过滤数据，前端无需传递额外参数。

### Q5: 请假数据从哪里来？

请假数据来自钉钉审批流程。当员工在钉钉提交请假审批，审批通过后，钉钉会推送回调到后端，后端自动同步到本地数据库。前端无需处理请假的创建，只需展示请假结果。

---

## 八、相关文档

| 文档 | 说明 |
|------|------|
| [FRONTEND_API_SPEC.md](FRONTEND_API_SPEC.md) | 详细 API 接口规范 |
| [PROJECT_README.md](项目介绍.md) | 项目整体介绍 |
| [CORE_FEATURES_DETAIL.md](./CORE_FEATURES_DETAIL.md) | 核心功能详解 |

---

## 九、联系方式

如有疑问，请联系后端开发人员或提交 Issue。

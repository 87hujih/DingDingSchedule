# 考勤模块 API 文档

## 基础信息

- **Base URL**: `/api/attendance`
- **认证方式**: JWT Token (Header: `Authorization: Bearer <token>`)
- **响应格式**:
```json
{
  "code": 0,
  "message": "success",
  "data": { ... }
}
```

---

## 1. 时段考勤状态

获取指定日期+周次+节次的应到/请假人员列表。

### 请求

```
GET /api/attendance/slots/status
```

### Query 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| date | string | 是 | 日期，格式 YYYY-MM-DD |
| week | int | 是 | 周次，从1开始 |
| section | int | 是 | 节次，从1开始 |
| dept_ids | string | 否 | 部门ID列表，逗号分隔，如 `1,2,3` |
| day_of_week | int | 否 | 星期几(1-7)，用于校验与date是否一致 |

### 请求示例

```
GET /api/attendance/slots/status?date=2026-01-20&week=1&section=2&dept_ids=1,2
```

### 响应示例

**成功响应**:
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "date": "2026-01-20",
    "week": 1,
    "day_of_week": 1,
    "section": 2,
    "should_arrive": [
      {
        "id": 1,
        "name": "张三",
        "avatar": "https://example.com/avatar1.jpg",
        "phone": "13800138001"
      },
      {
        "id": 2,
        "name": "李四",
        "avatar": "https://example.com/avatar2.jpg",
        "phone": "13800138002"
      }
    ],
    "on_leave": [
      {
        "id": 3,
        "name": "王五",
        "avatar": "https://example.com/avatar3.jpg",
        "phone": "13800138003"
      }
    ]
  }
}
```

**错误响应 - 缺少参数**:
```json
{
  "code": 40002,
  "message": "缺少 date 参数"
}
```

**错误响应 - 参数无效**:
```json
{
  "code": 40001,
  "message": "date 格式错误，应为 YYYY-MM-DD"
}
```

---

## 2. 时段用户请假明细

查看某用户在指定时段内的请假明细（仅管理员可访问）。

### 请求

```
GET /api/attendance/slots/users/:user_id/leave
```

### Path 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| user_id | int | 是 | 用户ID |

### Query 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| date | string | 是 | 日期，格式 YYYY-MM-DD |
| week | int | 是 | 周次，从1开始 |
| section | int | 是 | 节次，从1开始 |

### 请求示例

```
GET /api/attendance/slots/users/3/leave?date=2026-01-20&week=1&section=2
```

### 响应示例

**成功响应**:
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "user_id": 3,
    "week": 1,
    "date": "2026-01-20",
    "day_of_week": 1,
    "section": 2,
    "session_start": "2026-01-20T10:00:00+08:00",
    "session_end": "2026-01-20T11:40:00+08:00",
    "items": [
      {
        "leave_type": "事假",
        "start_at": "2026-01-20T09:00:00+08:00",
        "end_at": "2026-01-20T12:00:00+08:00",
        "duration_seconds": 10800,
        "status": "COMPLETED",
        "remark": "家中有事"
      }
    ]
  }
}
```-

**错误响应 - 无权限**:
```json
{
  "code": 40003,
  "message": "无权访问"
}
```

---

## 3. 获取考勤详情（管理员）

获取指定日期+周次+节次的详细考勤统计数据。

### 请求

```
GET /api/attendance/record/detail
```

### Query 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| date | string | 是 | 日期，格式 YYYY-MM-DD |
| week | int | 是 | 周次，从1开始 |
| section | int | 是 | 节次，从1开始 |
| dept_ids | string | 否 | 部门ID列表，逗号分隔 |

### 请求示例

```
GET /api/attendance/record/detail?date=2026-01-18&week=1&section=1&dept_ids=1,2,3
```

### 响应示例

**成功响应**:
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "date": "2026-01-18",
    "week": 1,
    "section": 1,
    "slot_time": {
      "start": "08:00",
      "end": "09:40"
    },
    "statistics": {
      "should_attend": 30,
      "on_time": 25,
      "leave": 2,
      "not_arrived": 3
    },
    "users": {
      "should_attend": [
        {
          "id": 1,
          "name": "张三"
        },
        {
          "id": 2,
          "name": "李四"
        }
      ],
      "on_time": [
        {
          "id": 1,
          "name": "张三",
          "check_time": "2026-01-18T07:55:00+08:00"
        }
      ],
      "leave": [
        {
          "id": 5,
          "name": "孙七",
          "leave_type": "病假",
          "reason": "身体不适"
        }
      ],
      "not_arrived": [
        {
          "id": 6,
          "name": "周八"
        }
      ]
    }
  }
}
```

---

## 4. 手动触发考勤统计（管理员）

手动触发指定日期+周次+节次的考勤统计，并保存到数据库。

### 请求

```
POST /api/attendance/record/trigger
```

### 请求体

```json
{
  "date": "2026-01-18",
  "week": 1,
  "section": 1,
  "dept_ids": [1, 2, 3]
}
```

### 请求体参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| date | string | 是 | 日期，格式 YYYY-MM-DD |
| week | int | 是 | 周次，从1开始 |
| section | int | 是 | 节次，从1开始 |
| dept_ids | int[] | 否 | 部门ID列表 |

### 请求示例

```bash
curl -X POST "http://localhost:8080/api/attendance/record/trigger" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "date": "2026-01-18",
    "week": 1,
    "section": 1,
    "dept_ids": [1, 2]
  }'
```

### 响应示例

**成功响应**:
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "date": "2026-01-18",
    "week": 1,
    "section": 1,
    "slot_time": {
      "start": "08:00",
      "end": "09:40"
    },
    "statistics": {
      "should_attend": 30,
      "on_time": 25,
      "leave": 2,
      "not_arrived": 3
    },
    "users": {
      "should_attend": [...],
      "on_time": [...],
      "leave": [...],
      "not_arrived": [...]
    }
  }
}
```

**错误响应 - 参数校验失败**:
```json
{
  "code": 40001,
  "message": "week 与 date 不在同一学期周内"
}
```

---

## 5. 获取某天所有考勤记录（管理员）

获取指定日期的所有考勤统计记录。

### 请求

```
GET /api/attendance/record/list
```

### Query 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| date | string | 是 | 日期，格式 YYYY-MM-DD |
| dept_ids | string | 否 | 部门ID列表，逗号分隔 |

### 请求示例

```
GET /api/attendance/record/list?date=2026-01-18&dept_ids=1,2,3
```

### 响应示例

**成功响应**:
```json
{
  "code": 0,
  "message": "success",
  "data": [
    {
      "date": "2026-01-18",
      "week": 1,
      "section": 1,
      "slot_time": {
        "start": "08:00",
        "end": "09:40"
      },
      "statistics": {
        "should_attend": 30,
        "on_time": 25,
        "leave": 2,
        "not_arrived": 3
      },
      "users": {...}
    },
    {
      "date": "2026-01-18",
      "week": 1,
      "section": 2,
      "slot_time": {
        "start": "10:00",
        "end": "11:40"
      },
      "statistics": {
        "should_attend": 28,
        "on_time": 26,
        "leave": 1,
        "not_arrived": 1
      },
      "users": {...}
    }
  ]
}
```

**错误响应 - 缺少参数**:
```json
{
  "code": 40002,
  "message": "缺少 date 参数"
}
```

---

## 错误码说明

| 错误码 | 说明 |
|--------|------|
| 0 | 成功 |
| 40001 | 参数无效 |
| 40002 | 缺少必填参数 |
| 40003 | 无权访问 |
| 40004 | 资源不存在 |
| 40101 | 未登录或Token无效 |
| 50000 | 服务器内部错误 |
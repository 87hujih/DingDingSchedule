# 考勤记录接口文档

本文档描述考勤记录模块的三个核心接口。

## 1. 获取考勤详情

**路由**: `GET /api/admin/attendance/record/detail`

### 功能说明
实时查询指定日期、周次、节次的考勤情况。该接口会：
1. 根据日期和节次计算考勤时间窗口
2. 从用户表筛选「应到人员」（参与考勤且该时段无课的用户）
3. 调用钉钉接口获取打卡记录，分类为「正常打卡」和「迟到」
4. 查询本地请假审批表获取「请假人员」
5. 计算「缺勤人员」= 应到 - 正常 - 迟到 - 请假

### 请求参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| date | string | 是 | 日期，格式 YYYY-MM-DD |
| week | int | 是 | 周次（第几周） |
| section | int | 是 | 节次（第几节课） |
| dept_ids | string | 否 | 部门ID列表，逗号分隔，如 `1,2,3`。不传则查全部 |

### 响应示例
```json
{
  "code": 0,
  "data": {
    "date": "2026-01-18",
    "week": 1,
    "section": 1,
    "slot_time": {
      "start": "08:00",
      "end": "09:40"
    },
    "statistics": {
      "should_attend": 50,
      "on_time": 40,
      "late": 5,
      "leave": 3,
      "absent": 2
    },
    "users": {
      "should_attend": [{"id": 1, "name": "张三"}, ...],
      "on_time": [{"id": 1, "name": "张三", "check_time": "2026-01-18T07:55:00+08:00"}, ...],
      "late": [{"id": 5, "name": "李四", "check_time": "2026-01-18T08:15:00+08:00", "late_minutes": 15}, ...],
      "leave": [{"id": 8, "name": "王五", "leave_type": "事假", "reason": "家中有事"}, ...],
      "absent": [{"id": 10, "name": "赵六"}, ...]
    }
  }
}
```

---

## 2. 手动触发考勤统计

**路由**: `POST /api/admin/attendance/record/trigger`

### 功能说明
手动触发考勤统计并持久化到数据库。该接口会：
1. 调用 `GetAttendanceDetail` 获取实时考勤数据
2. 将结果序列化后存入 `attendance_records` 表（按 date + section 唯一键 Upsert）

适用场景：
- 管理员手动补录历史考勤数据
- 定时任务调用，定期保存考勤快照
- 调试/测试时手动触发

### 请求参数（JSON Body）

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| date | string | 是 | 日期，格式 YYYY-MM-DD |
| week | int | 是 | 周次 |
| section | int | 是 | 节次 |
| dept_ids | []int64 | 否 | 部门ID列表，如 `[1, 2, 3]` |

### 请求示例
```json
{
  "date": "2026-01-18",
  "week": 1,
  "section": 1,
  "dept_ids": [1, 2]
}
```

### 响应
与 `GetAttendanceDetail` 响应结构相同，返回本次统计的考勤数据。

---

## 3. 获取某天所有考勤记录

**路由**: `GET /api/admin/attendance/record/list`

### 功能说明
查询某一天所有节次的考勤记录汇总。

- 输入一个日期，返回该日所有节次的考勤统计
- 支持按部门过滤（仅返回属于指定部门的用户数据）
- 数据来源：已持久化的 `attendance_records` 表

### 请求参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| date | string | 是 | 日期，格式 YYYY-MM-DD |
| dept_ids | string | 否 | 部门ID列表，逗号分隔 |

### 响应结构
```json
{
  "code": 0,
  "data": [
    {
      "section": 1,
      "slot_time": {"start": "08:00", "end": "09:40"},
      "statistics": {...}
    },
    {
      "section": 2,
      "slot_time": {"start": "10:00", "end": "11:40"},
      "statistics": {...}
    }
  ]
}
```

---

## 接口对比

| 接口 | 方法 | 数据来源 | 是否持久化 | 状态 |
|------|------|----------|------------|------|
| `/detail` | GET | 实时查询钉钉+本地数据库 | 否 | ✅ 已实现 |
| `/trigger` | POST | 实时查询后保存 | 是 | ✅ 已实现 |
| `/list` | GET | 读取已保存的记录 | - | ✅ 已实现 |

## 核心业务逻辑

### 应到人员计算
```
应到人员 = 参与考勤的用户(status=1) - 该时段有课的用户
```

### 考勤状态判定
- **正常打卡**: 打卡时间 ≤ 节次开始时间
- **迟到**: 打卡时间 > 节次开始时间 且 ≤ 节次结束时间
- **请假**: 在本地 `leave_approvals` 表中有已通过的请假记录
- **缺勤**: 应到但既没打卡也没请假的人员
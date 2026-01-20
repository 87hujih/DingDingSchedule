# 钉钉请假同步流程详解

## 概述

本文档详细描述从用户在钉钉发起请假申请，到系统接收事件并同步入库的完整流程。

## 整体架构

```
┌─────────────┐     ┌─────────────┐     ┌─────────────────┐     ┌──────────────┐
│  钉钉 APP   │ ──→ │  钉钉服务器  │ ──→ │  Stream 客户端   │ ──→ │   数据库      │
│  发起请假   │     │  推送事件    │     │  处理事件        │     │  保存记录     │
└─────────────┘     └─────────────┘     └─────────────────┘     └──────────────┘
```

## 详细流程

### 第一阶段：用户发起请假

1. 用户在钉钉 APP 中打开「请假」审批
2. 填写请假表单（开始时间、结束时间、请假类型、请假事由）
3. 点击提交

### 第二阶段：钉钉推送事件

钉钉服务器通过 WebSocket 长连接向我们的服务推送事件。

**推送的原始数据格式：**
```json
{
  "approveType": "LEAVE",
  "processInstanceId": "EgZc4LxmQ9GxUwUtzl4qWQ02161767678492",
  "eventId": "2513f757a8bc4e309238151619de043e",
  "processCode": "PROC-68BFCD19-5ACC-4655-ADE6-DA13C9D43866",
  "status": "start"
}
```

**字段说明：**
| 字段 | 说明 |
|------|------|
| approveType | 审批类型，LEAVE 表示请假 |
| processInstanceId | 审批实例 ID（唯一标识） |
| eventId | 事件 ID |
| processCode | 流程编码 |
| status | 状态：start=发起, finish=完成, terminate=终止 |

### 第三阶段：Stream 客户端接收事件

**代码位置：** `pkg/dingtalk/stream.go:53-84`

```go
func (s *StreamClient) handleEvent(ctx context.Context, df *payload.DataFrame) (*payload.DataFrameResponse, error) {
    // 1. 打印原始数据（调试用）
    s.logger.Infow("收到钉钉原始事件", "data", df.Data)

    // 2. 解析事件数据
    var evt bpmsEvent
    json.Unmarshal([]byte(df.Data), &evt)

    // 3. 记录日志
    s.logger.Infow("收到钉钉事件",
        "approveType", evt.ApproveType,
        "status", evt.Status,
        "processInstanceId", evt.ProcessInstanceID,
    )

    // 4. 判断是否为请假事件
    if evt.ApproveType == "LEAVE" && evt.ProcessInstanceID != "" {
        // 5. 异步处理（避免阻塞事件接收）
        go func() {
            s.onBpmsEvent(ctx, s.corpID, evt.ProcessInstanceID, evt.Status)
        }()
    }

    // 6. 返回成功响应
    return payload.NewSuccessDataFrameResponse(), nil
}
```

**事件结构体：**
```go
type bpmsEvent struct {
    ApproveType       string `json:"approveType"`       // LEAVE=请假
    ProcessInstanceID string `json:"processInstanceId"` // 审批实例ID
    ProcessCode       string `json:"processCode"`       // 流程编码
    Status            string `json:"status"`            // start/finish/terminate
    EventID           string `json:"eventId"`           // 事件ID
}
```

### 第四阶段：触发业务处理

**代码位置：** `internal/app/app.go:101-108`

```go
streamClient.SetBpmsEventHandler(func(ctx context.Context, corpID, processInstanceID, eventType string) error {
    global.Log.Infow("处理审批事件",
        "corpId", corpID,
        "processInstanceId", processInstanceID,
        "eventType", eventType,
    )
    return leaveSyncSrv.SyncProcessInstance(ctx, corpID, processInstanceID)
})
```

### 第五阶段：同步审批实例

**代码位置：** `internal/service/leave_sync_service.go:53-141`

#### 5.1 获取租户和钉钉客户端

```go
tenant, client, err := s.dingMgr.GetByCorpID(ctx, corpID)
```

根据 `corpID` 从数据库查询对应的租户信息，并获取该租户的钉钉 API 客户端。

**SQL：**
```sql
SELECT * FROM tenants WHERE corp_id = 'dinge292658c9243df4235c2f4657eb6378f' AND status = 1
```

#### 5.2 调用钉钉 API 获取审批实例详情

**代码位置：** `pkg/dingtalk/approval.go:99-151`

```go
pi, err := client.GetProcessInstance(ctx, processInstanceID)
```

**API 请求：**
```
POST https://oapi.dingtalk.com/topapi/processinstance/get?access_token=xxx
Content-Type: application/json

{
  "process_instance_id": "EgZc4LxmQ9GxUwUtzl4qWQ02161767678492"
}
```

**API 响应（简化）：**
```json
{
  "errcode": 0,
  "errmsg": "ok",
  "process_instance": {
    "title": "马华恩提交的请假",
    "status": "RUNNING",
    "result": "",
    "originator_userid": "01375837500038676039",
    "form_component_values": [
      {
        "component_type": "DDHolidayField",
        "name": "[\"开始时间\",\"结束时间\"]",
        "value": "[\"2026-01-06 13:51\",\"2026-01-06 13:52\",0.02,\"hour\",\"事假\",\"请假类型\"]"
      },
      {
        "name": "请假事由",
        "value": "1"
      }
    ]
  }
}
```

#### 5.3 解析表单字段

**代码位置：** `internal/service/leave_sync_service.go:144-235`

钉钉请假组件（DDHolidayField）使用特殊的 JSON 数组格式：

```go
// name: "[\"开始时间\",\"结束时间\"]"
// value: "[\"2026-01-06 13:51\",\"2026-01-06 13:52\",0.02,\"hour\",\"事假\",\"请假类型\"]"

if strings.HasPrefix(name, "[") && strings.Contains(name, "开始时间") {
    var valueArr []interface{}
    json.Unmarshal([]byte(value), &valueArr)

    // valueArr[0] = 开始时间 "2026-01-06 13:51"
    // valueArr[1] = 结束时间 "2026-01-06 13:52"
    // valueArr[2] = 时长 0.02
    // valueArr[3] = 单位 "hour"
    // valueArr[4] = 请假类型 "事假"
    // valueArr[5] = 标签 "请假类型"
}
```

**解析结果：**
| 字段 | 值 |
|------|------|
| startAt | 2026-01-06 13:51:00 |
| endAt | 2026-01-06 13:52:00 |
| leaveType | 事假 |
| reason | 1 |

#### 5.4 查找本地用户

```go
user, err := s.userRepo.FindByDingUserID(ctx, dingUserID)
```

根据钉钉用户 ID 查找本地用户，关联 `user_id`。

#### 5.5 构建记录并入库

```go
rec := &model.LeaveApproval{
    TenantID:          tenant.ID,
    ProcessInstanceID: pi.ProcessInstanceID,
    ProcessCode:       pi.ProcessCode,
    DingUserID:        dingUserID,
    UserID:            userID,
    StartAt:           startAt,
    EndAt:             endAt,
    LeaveType:         leaveType,
    Reason:            reason,
    ApproveStatus:     normalizeStatus(pi.Status),  // RUNNING
    Result:            normalizeResult(pi.Result),  // ""
    RawInstanceJSON:   rawJSON,
    RawFormJSON:       formJSON,
}

s.leaveRepo.UpsertByProcessInstanceID(ctx, rec)
```

**SQL（Upsert）：**
```sql
INSERT INTO leave_approvals (
    tenant_id, process_instance_id, process_code, ding_user_id, user_id,
    start_at, end_at, leave_type, reason, approve_status, result,
    raw_instance_json, raw_form_json, created_at, updated_at
) VALUES (...)
ON DUPLICATE KEY UPDATE
    process_code = VALUES(process_code),
    approve_status = VALUES(approve_status),
    result = VALUES(result),
    ...
```

### 第六阶段：后续事件

当审批人同意/拒绝请假时，钉钉会再次推送事件：

```json
{
  "approveType": "LEAVE",
  "processInstanceId": "EgZc4LxmQ9GxUwUtzl4qWQ02161767678492",
  "status": "finish"
}
```

系统会再次执行上述流程，更新数据库中的 `approve_status` 和 `result` 字段。

## 数据流图

```
用户提交请假
    │
    ▼
钉钉服务器生成审批实例
    │
    ▼
推送 WebSocket 事件 ─────────────────────────────────────┐
    │                                                    │
    ▼                                                    │
StreamClient.handleEvent()                               │
    │ 解析 approveType="LEAVE"                           │
    │ 解析 processInstanceId                             │
    ▼                                                    │
LeaveSyncService.SyncProcessInstance()                   │
    │                                                    │
    ├─► 1. GetByCorpID() ─► 查询 tenants 表              │
    │                                                    │
    ├─► 2. GetProcessInstance() ─► 调用钉钉 API          │
    │       POST /topapi/processinstance/get             │
    │                                                    │
    ├─► 3. parseLeaveFormFields() ─► 解析表单            │
    │       处理 DDHolidayField 特殊格式                  │
    │                                                    │
    ├─► 4. FindByDingUserID() ─► 查询 users 表           │
    │                                                    │
    └─► 5. UpsertByProcessInstanceID() ─► 写入数据库     │
            INSERT ... ON DUPLICATE KEY UPDATE           │
                                                         │
    ◄────────────────────────────────────────────────────┘
返回 SUCCESS 响应给钉钉
```

## 日志示例

完整的一次请假同步日志：

```json
// 1. 收到原始事件
{"msg":"收到钉钉原始事件","data":"{\"approveType\":\"LEAVE\",\"processInstanceId\":\"xxx\",\"status\":\"start\"}"}

// 2. 解析事件
{"msg":"收到钉钉事件","approveType":"LEAVE","status":"start","processInstanceId":"xxx"}

// 3. 触发处理
{"msg":"处理审批事件","corpId":"dingxxx","processInstanceId":"xxx","eventType":"start"}

// 4. 查询租户
[rows:1] SELECT * FROM tenants WHERE corp_id = 'dingxxx' AND status = 1

// 5. 调用钉钉 API（内部）

// 6. 写入数据库
[rows:1] INSERT INTO leave_approvals (...) ON DUPLICATE KEY UPDATE ...

// 7. 同步成功
{"msg":"leave_sync: 同步成功","tenantId":1,"processInstanceId":"xxx","startAt":"2026-01-06T13:51:00","endAt":"2026-01-06T13:52:00"}
```

## 相关文件

| 文件 | 说明 |
|------|------|
| `pkg/dingtalk/stream.go` | Stream 客户端，接收钉钉事件 |
| `pkg/dingtalk/approval.go` | 钉钉审批 API 封装 |
| `internal/app/app.go` | 服务启动，注册事件处理器 |
| `internal/service/leave_sync_service.go` | 请假同步业务逻辑 |
| `internal/service/dingtalk_client_manager.go` | 钉钉客户端管理（多租户） |
| `internal/repository/leave_approval_repository.go` | 请假记录数据访问层 |
| `internal/model/leave_approval.go` | 请假记录数据模型 |

## 错误处理

| 错误 | 原因 | 解决方案 |
|------|------|---------|
| 获取租户/客户端失败 | corpID 不存在或租户未启用 | 检查 tenants 表数据 |
| 获取审批实例详情失败 (60011) | 缺少 qyapi_aflow 权限 | 在开放平台申请权限 |
| 获取审批实例详情失败 (60020) | IP 不在白名单 | 添加服务器 IP 到白名单 |
| 落库失败 (datetime) | 表单解析失败，时间为零值 | 检查 DDHolidayField 解析逻辑 |




● 问题分析

根本原因： DingTalk Stream SDK 内部的并发 bug

从堆栈信息可以看到：
client.(*StreamClient).processLoop.func6()
client/client.go:161

SDK 内部的 processLoop 函数使用了 goroutine 和 channel 来处理消息。当 WebSocket 连接断开时（网络波动 、服务端主动断开等），SDK 会关闭 channel，但某个 goroutine 仍在尝试向已关闭的 channel 发送数据，导致 panic。

这是 SDK 自身的问题，不是你的代码问题。

解决思路

由于无法修改第三方 SDK 代码，采用防御性编程：

1. Panic Recovery - 用 defer/recover 捕获 SDK 内部的 panic，将其转换为普通 error 返回：
   defer func() {
   if r := recover(); r != nil {
   err = fmt.Errorf("stream panic: %v", r)
   }
   }()
2. 自动重连 - 既然连接断开是不可避免的，就设计成断开后自动重连：
   for {
   err := s.startOnce(ctx)  // 启动一次
   if err != nil {
   time.Sleep(5 * time.Second)  // 失败后等5秒
   continue  // 重新连接
   }
   }
3. 优雅退出 - 通过 context 控制，允许外部主动停止重连循环

这样即使 SDK 内部 panic，你的服务也能自动恢复，不会整个进程崩溃。

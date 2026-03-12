# 钉钉 AI 助手集成方案

## 背景与目标

在现有 `schedule_server`（Go + Gin + GORM + MySQL + 钉钉 Stream SDK）中新增 AI Agent 模块，让用户通过**钉钉机器人单聊**用自然语言查询课表和考勤信息。

**核心决策**：
- 集成到现有项目（而非新建），复用 Service/Repository 层
- 原生实现（而非 Eino 框架），零新依赖，符合现有项目风格
- 第一阶段只读工具（查课表、查考勤），写操作放第二阶段
- 内存 Session 管理（30 分钟 TTL），不引入 Redis/MySQL 新依赖

---

## 整体架构

```
用户钉钉单聊
      │
      ▼ (Stream SDK - ROBOT topic)
pkg/dingtalk/stream.go
  handleChatMessage()
      │ corpID + dingUserID + 消息内容
      ▼
internal/agent/agent.go
  Agent.Chat()
      │
      ├─ 1. 查租户 → DingTalkClientManager.GetByCorpID()
      ├─ 2. 查用户 → UserRepository.FindByDingUserID()
      ├─ 3. 注入租户 → tenantctx.WithTenantID(ctx, tenantID)
      ├─ 4. 加载/创建 Session（内存）
      ├─ 5. ReAct Loop
      │       ├─ LLMClient.Chat(messages + tools)
      │       ├─ finish_reason == "tool_calls" → 执行工具 → 追加结果 → 继续
      │       └─ finish_reason == "stop" → 返回最终回复
      └─ 6. 更新 Session，返回回复文本
            │
            ▼
pkg/dingtalk/stream.go
  DataFrameResponse（直接在 Stream 连接内回复）
```

---

## 文件变更清单

### 新建文件（3 个）

| 文件 | 用途 |
|------|------|
| `internal/agent/client.go` | LLM HTTP 客户端（调 SiliconFlow/DeepSeek-V3） |
| `internal/agent/tools.go` | 工具注册表 + 第一批工具实现 |
| `internal/agent/agent.go` | ReAct Loop + Session 管理 + Agent 入口 |

### 修改文件（5 个）

| 文件 | 改动内容 |
|------|----------|
| `config/config.go` | 新增 `LLM` 配置结构体（BaseURL、APIKey、Model） |
| `configs/dev.yaml` | 新增 `llm` 配置节 |
| `pkg/dingtalk/stream.go` | 新增 ROBOT 订阅 + `SetChatMessageHandler` + `handleChatMessage` |
| `internal/service/stream_client_manager.go` | 新增 `SetChatMessageHandler` 方法，向下传递给 StreamClient |
| `internal/app/app.go` | 在 `startDingTalkStream` 中构建 Agent 并注册 chatHandler |

---

## 各文件详细设计

### 1. `config/config.go`

在 `Config` struct 增加一个字段：

```go
type Config struct {
    // ... 已有字段 ...
    LLM LLM // LLM 接口配置
}

// LLM OpenAI-compatible 接口配置
type LLM struct {
    BaseURL string // 接口地址，如 https://api.siliconflow.cn/v1/chat/completions
    APIKey  string // 鉴权 Key
    Model   string // 模型名称，如 deepseek-ai/DeepSeek-V3
}
```

### 2. `configs/dev.yaml`

```yaml
llm:
  base_url: "https://api.siliconflow.cn/v1/chat/completions"
  api_key: "sk-rzprdwdetcgzlvmkvddgaraqzisjaavrytcootogxybofsba"
  model: "deepseek-ai/DeepSeek-V3"
```

---

### 3. `internal/agent/client.go`

LLM HTTP 客户端，复用学习项目 `01-llm-client` 的设计模式。

**核心类型**：

```go
// Message 对话消息
type Message struct {
    Role       string     // "system" / "user" / "assistant" / "tool"
    Content    string
    ToolCallID string     // role == "tool" 时填写
    ToolCalls  []ToolCall // role == "assistant" 且有工具调用时填写
}

// ToolCall LLM 返回的工具调用请求
type ToolCall struct {
    ID       string
    Function struct {
        Name      string
        Arguments string // JSON 字符串
    }
}

// ToolDef 工具定义（发给 LLM 的 JSON Schema）
type ToolDef struct {
    Type     string       // 固定 "function"
    Function FunctionDef
}

type FunctionDef struct {
    Name        string
    Description string
    Parameters  json.RawMessage // JSON Schema
}

// LLMClient HTTP 客户端
type LLMClient struct {
    baseURL    string
    apiKey     string
    model      string
    httpClient *http.Client
}
```

**核心方法**：

```go
// Chat 发送对话请求（阻塞），内置 3 次重试（429/5xx）
func (c *LLMClient) Chat(ctx context.Context, messages []Message, tools []ToolDef) (Message, error)
```

---

### 4. `internal/agent/tools.go`

**用户上下文**（工具执行时的身份信息）：

```go
// UserContext 工具执行时携带的调用者身份
type UserContext struct {
    TenantID   uint   // 租户 ID（用于 tenantctx 注入）
    UserID     uint   // 本地用户 ID
    UserRole   int    // 0=普通用户, 1=管理员, 2=超级管理员
    DingUserID string // 钉钉用户 ID
    Name       string // 姓名（用于日志）
}
```

**工具注册表**：

```go
type ToolRegistry struct {
    defs     []ToolDef                                                           // 发给 LLM 的工具定义列表
    handlers map[string]func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error)
}

func (r *ToolRegistry) Dispatch(ctx, uctx, name, params) (string, error)
func (r *ToolRegistry) ToToolDefs() []ToolDef
```

**第一批工具（只读）**：

#### `get_current_time`
- **描述**：获取当前日期、星期、第几周（相对于学期开始）
- **参数**：无
- **实现**：调 `SemesterRepository` 查当前学期 + `weekutil` 计算周次
- **返回示例**：`"2026-03-12（周四，第6周）"`

#### `query_my_schedule`
- **描述**：查询当前用户指定周的课表
- **参数**：`{ "week": 6 }` week 可选，默认当前周
- **实现**：`ScheduleService.ListByWeek(ctx, userID, userRole, userID, week)`
- **返回示例**：
  ```
  第6周课表：
  周一 第1-2节 08:00-09:40 《数据结构》 B204 张老师
  周三 第3-4节 10:10-11:50 《操作系统》 A101
  周五 第7-8节 16:40-18:20 《英语》 语音室
  ```

#### `query_attendance_status`
- **描述**：查询指定日期指定节次的考勤状态（正常/缺勤/请假人员列表）
- **参数**：`{ "date": "2026-03-12", "section": 3 }` 均可选，默认今天/当前节次
- **实现**：`AttendanceRecordService.GetAttendanceDetail(ctx, req)` 解析 response
- **返回示例**：
  ```
  2026-03-12 第5-6节（14:30-16:10）考勤：
  正常到课：15人
  缺勤：张三、李四（共2人）
  请假：王五（共1人）
  休息日：0人
  ```

#### `query_weekly_ranking`
- **描述**：查询本周考勤出勤率排行（前10名）
- **参数**：无
- **实现**：`AttendanceRecordService.GetWeeklyRanking(ctx, req)`
- **返回示例**：
  ```
  本周考勤排行（第6周）：
  1. 张三  出勤率 100%
  2. 李四  出勤率 95%
  ...
  ```

---

### 5. `internal/agent/agent.go`

**Session 管理**：

```go
type session struct {
    messages  []Message  // 对话历史（含 system prompt）
    updatedAt time.Time
}

// key = "corpID:dingUserID"，防止跨租户碰撞
sessions map[string]*session
mu        sync.RWMutex

const (
    maxHistory = 20              // 最多保留 20 条消息
    sessionTTL = 30 * time.Minute
)
```

**Session 过期清理**：启动时开一个 goroutine，每 10 分钟扫描删除过期 session。

**System Prompt（三段式）**：

```
你是「课表助手」，服务于学校课表与考勤管理系统。

当前用户：{name}（{role_text}）
今日日期由工具 get_current_time 获取，查询时先调用它。

约束：
- 只能使用提供的工具获取数据，不要编造或猜测
- 回复用中文，简洁明了，避免冗余解释
- 如果工具返回错误，如实告知用户
```

**ReAct Loop**：

```
输入：corpID, dingUserID, senderNick, userMessage
  │
  ├─ 查租户、查用户、构建 UserContext
  ├─ 注入 tenantctx.WithTenantID(ctx, tenantID)
  ├─ 加载或创建 session（追加用户消息）
  │
  └─ Loop（最多 5 轮，防无限循环）:
       ├─ LLMClient.Chat(systemPrompt + history, tools)
       ├─ finish_reason == "tool_calls":
       │     for each tool_call:
       │       result = registry.Dispatch(name, params)
       │       追加 assistant 消息（含 tool_calls）
       │       追加 tool 消息（含 tool call id + result）
       │     continue loop
       └─ finish_reason == "stop":
             保存 session（裁剪超长历史）
             返回 content
```

---

### 6. `pkg/dingtalk/stream.go`

**新增内容**：

```go
// RobotMessage 机器人收到的聊天消息结构
type RobotMessage struct {
    MsgID            string `json:"msgId"`
    MsgType          string `json:"msgtype"`
    Text             struct {
        Content string `json:"content"`
    } `json:"text"`
    SenderID         string `json:"senderId"`     // dingUserId
    SenderCorpID     string `json:"senderCorpId"`
    SenderNick       string `json:"senderNick"`
    ConversationType string `json:"conversationType"` // "1"=单聊, "2"=群聊
}

// SetChatMessageHandler 设置机器人聊天消息处理器（与 SetBpmsEventHandler 对称）
func (s *StreamClient) SetChatMessageHandler(
    handler func(ctx context.Context, corpID, staffID, nick, content string) (string, error),
)

// handleChatMessage 处理 ROBOT topic 消息
// 解析 payload → 只处理单聊（conversationType == "1"）→ 调 handler → 封装回复
func (s *StreamClient) handleChatMessage(ctx context.Context, df *payload.DataFrame) (*payload.DataFrameResponse, error)
```

**`Start()` 修改**：新增 `client.WithSubscription("ROBOT", "*", s.handleChatMessage)`。

**回复格式**（DingTalk Stream ROBOT 回复）：

```json
{
  "msgtype": "text",
  "text": { "content": "agent 的回复内容" }
}
```

---

### 7. `internal/service/stream_client_manager.go`

镜像 `onBpmsEvent` 的模式，在 `StreamClientManager` 上新增：

```go
// SetChatMessageHandler 设置聊天消息处理器，将在 startClient 时注入到每个 StreamClient
func (m *StreamClientManager) SetChatMessageHandler(
    handler func(ctx context.Context, corpID, staffID, nick, content string) (string, error),
)
```

在内部 `startClient(tenant)` 时调用 `streamClient.SetChatMessageHandler(m.chatHandler)`。

---

### 8. `internal/app/app.go`

`startDingTalkStream` 函数末尾新增 Agent 构建与注册：

```go
// 构建 Agent 所需服务
scheduleSvc  := service.NewScheduleService(...)
attendanceSvc := service.NewAttendanceRecordService(...)
semesterSvc  := service.NewSemesterService(repo.SemesterRepo)

// 构建 Agent
agentInst := agent.NewAgent(
    global.AppConfig.LLM,
    repo,
    dingMgr,
    scheduleSvc,
    attendanceSvc,
    semesterSvc,
    global.Log,
)

// 注册聊天消息处理器
streamMgr.SetChatMessageHandler(func(ctx context.Context, corpID, staffID, nick, content string) (string, error) {
    return agentInst.Chat(ctx, corpID, staffID, nick, content)
})
```

---

## 对话示例

**场景 1：查自己课表**
```
用户：我今天有什么课？
助手：[调用 get_current_time → 2026-03-12 周四第6周]
      [调用 query_my_schedule → week=6]
回复：今天周四，你有 2 门课：
      · 第1-2节（08:00-09:40）《数据库原理》B204
      · 第7-8节（16:40-18:20）《软件工程》A302
```

**场景 2：查考勤（管理员视角）**
```
用户：刚刚第5节谁没来？
助手：[调用 get_current_time → 今天 14:30-16:10 是第5-6节]
      [调用 query_attendance_status → date=今天, section=3]
回复：第5-6节（14:30-16:10）考勤：
      缺勤 2 人：张三、李四
      请假 1 人：王五
      正常 15 人
```

**场景 3：多轮对话**
```
用户：本周考勤谁最好？
助手：[调用 query_weekly_ranking]
回复：本周考勤第一是张三，出勤率 100%。

用户：那最差的呢？
助手：[复用上一轮结果，不重复调工具]
回复：本周出勤率最低的是李四，出勤率 60%（缺勤 4 节）。
```

---

## 注意事项

1. **租户隔离**：所有工具调用前必须调用 `tenantctx.WithTenantID(ctx, tenantID)`，否则 GORM 租户插件过滤会报错或查不到数据。

2. **用户未注册情况**：若 dingUserID 在本地 users 表找不到对应记录，Agent 回复「您尚未绑定账户，请先通过小程序登录」。

3. **只处理单聊**：`conversationType != "1"` 的群消息直接返回成功，不处理（防止群里 @机器人 触发）。

4. **最大 Loop 轮数**：ReAct Loop 设 5 轮上限，防止 LLM 无限调工具。超出限制回复「处理超时，请重试」。

5. **无新 Go 依赖**：整个 agent 模块只用标准库 + 现有项目依赖，不修改 go.mod。

---

## 实现顺序

1. `config/config.go` + `configs/dev.yaml`（配置，最简单）
2. `internal/agent/client.go`（LLM 客户端）
3. `internal/agent/tools.go`（工具定义）
4. `internal/agent/agent.go`（ReAct Loop）
5. `pkg/dingtalk/stream.go`（ROBOT 订阅）
6. `internal/service/stream_client_manager.go`（传递 chatHandler）
7. `internal/app/app.go`（最终接线）
8. `go build ./...` 验证编译

---

## 本次不涉及（第二阶段）

- 写操作工具（修改课表、切换作息模式、触发考勤统计）
- Session 持久化
- 流式输出（DingTalk 不支持流式气泡）
- 管理员专属工具的权限校验强化

# 钉钉 AI 助手集成方案（v2）

## 背景与目标

在现有 `schedule_server`（Go + Gin + GORM + MySQL + 钉钉 Stream SDK）中新增 AI Agent 模块，让用户通过**钉钉机器人单聊或群聊 @机器人**用自然语言查询课表和考勤信息。

**核心决策**：
- 集成到现有项目（而非新建），复用 Service/Repository 层
- 原生实现（而非 Eino 框架），零新依赖，符合现有项目风格
- 第一阶段只读工具（查课表、查考勤），写操作放第二阶段
- 内存 Session 管理（30 分钟 TTL），不引入 Redis/MySQL 新依赖

---

## 整体架构

```
用户钉钉单聊 / 群聊 @机器人
      │
      ▼ (Stream SDK - ROBOT topic)
pkg/dingtalk/stream.go
  handleChatMessage()
      │ 解析为 ChatMessage 结构体
      ▼
internal/agent/agent.go
  Agent.Chat()
      │
      ├─ 1. 查租户 → DingTalkClientManager.GetByCorpID()
      ├─ 2. 查用户 → UserPort.FindByDingUserID()
      ├─ 3. 注入租户 → tenantctx.WithTenantID(ctx, tenantID)
      ├─ 4. 限流检查（per-user 令牌桶）
      ├─ 5. 加载/创建 Session（内存）
      │       单聊 key：corpID:dingUserID
      │       群聊 key：corpID:conversationID:dingUserID
      ├─ 6. 构建 System Prompt（注入当前日期、周次、用户身份）
      ├─ 7. ReAct Loop
      │       ├─ LLMClient.Chat(messages + tools)
      │       ├─ finish_reason == "tool_calls" → 执行工具 → 追加结果 → 继续
      │       └─ finish_reason == "stop" → 返回最终回复
      └─ 8. 更新 Session，返回回复文本
            │
            ▼
pkg/dingtalk/stream.go
  DataFrameResponse（直接在 Stream 连接内回复）
  群聊时在回复内容前加 @发送者
```

---

## 架构设计原则

### 核心思路：端口适配器（Port & Adapter）

Agent 只依赖自己在 `port.go` 中定义的接口和数据类型，不 import `internal/service` 或 `internal/model` 包。现有 Service 的方法签名与 Port 接口**不直接匹配**（参数不同、返回类型不同），因此需要在组装层（`internal/app/`）编写 Adapter 完成适配。

```
现有代码：Handler → Service → Repository   ← 零改动
Agent层：  Agent  → [Port接口]              ← 只依赖自定义接口
组装层：   app/agent_wiring.go → Adapter    ← 桥接 Service 和 Port
```

**包依赖方向**：
- `internal/agent/` → 只依赖标准库 + `go.uber.org/zap`
- `internal/app/agent_wiring.go` → import `agent` + `service`，做适配和注入
- `internal/agent/` **不** import `internal/service`、`internal/model`、`internal/dto`

这保证了 Agent 模块是**可插拔的**：删掉 `internal/agent/` + `agent_wiring.go` 即完整移除，不留任何痕迹。

### Agent 目录结构

```
internal/agent/
├── port.go          # Agent 所需能力的接口定义 + Agent 自有数据类型
├── agent.go         # ReAct Loop + 生命周期管理（Stop 方法）
├── client.go        # LLM HTTP 客户端
├── session.go       # Session 管理（独立文件，不堆在 agent.go）
├── ratelimit.go     # Per-user 速率限制
└── tools/
    ├── registry.go     # 工具注册表（含角色过滤）
    ├── schedule.go     # 课表相关工具
    ├── attendance.go   # 考勤相关工具
    ├── leave.go        # 请假工具
    └── admin.go        # 管理员工具（补签、订阅推送）

internal/app/
├── agent_wiring.go  # Adapter 适配层 + buildAgent 函数
```

工具按领域拆分文件，避免单个 `tools.go` 膨胀到千行。

### 为什么需要 Adapter 而非"天然满足"

以 `ScheduleService.ListByWeek` 为例：

```go
// 实际 Service 签名（5 个参数，返回 *WeekScheduleResult）
func (s *ScheduleService) ListByWeek(
    ctx context.Context,
    viewerID uint,      // Agent 不关心
    viewerRole int,     // Agent 不关心
    targetUserID uint,
    week int,
) (*WeekScheduleResult, error)

// Port 接口签名（3 个参数，返回 []CourseItem）
type SchedulePort interface {
    ListMyScheduleByWeek(ctx context.Context, userID uint, week int) ([]CourseItem, error)
}
```

两者参数数量、返回类型均不同。类似情况存在于 `AttendanceRecordService.GetAttendanceDetail`（接收 `*dto.AttendanceDetailRequest`，返回 `*dto.AttendanceDetailResponse`）等多个方法。

Adapter 的职责就是做这层桥接，放在 `internal/app/agent_wiring.go` 中，使 `internal/agent/` 保持纯净。

---

### `port.go`：接口隔离 + Agent 自有数据类型

```go
// internal/agent/port.go
// Agent 只依赖这里的接口和类型，不 import internal/service 或 internal/model

package agent

import "context"

// ────────────── Port 接口 ──────────────

// SchedulePort 课表能力
type SchedulePort interface {
    ListMyScheduleByWeek(ctx context.Context, userID uint, week int) ([]CourseItem, error)
    GetFreeUsersBySlot(ctx context.Context, week, dayStart, dayEnd int) ([]FreeSlotResult, error)
}

// AttendancePort 考勤能力
type AttendancePort interface {
    GetAttendanceDetail(ctx context.Context, req AttendanceQuery) (*AttendanceResult, error)
    GetAttendanceText(ctx context.Context, req AttendanceQuery) (string, error)
    GetWeeklyAbsenceRanking(ctx context.Context) ([]RankItem, error)
    GetWeeklyAttendanceRateRanking(ctx context.Context) ([]RankItem, error)
    FindRecordByDateSection(ctx context.Context, date string, section int) (uint, error)
    SignForUsers(ctx context.Context, recordID uint, userIDs []uint) error
}

// LeavePort 请假能力
type LeavePort interface {
    GetRecentLeaves(ctx context.Context, userID uint, days int) ([]LeaveItem, error)
}

// UserPort 用户能力
type UserPort interface {
    FindByDingUserID(ctx context.Context, dingUserID string) (*UserInfo, error)
    SearchByName(ctx context.Context, name string) ([]UserInfo, error)
}

// SemesterPort 学期能力
type SemesterPort interface {
    GetCurrentWeek(ctx context.Context) (week int, totalWeeks int, err error)
}

// SchedulePeriodPort 作息时间能力
type SchedulePeriodPort interface {
    GetScheduleInfo(ctx context.Context) ([]PeriodInfo, string, error) // periods, mode, err
}

// RestDayPort 休息日能力
type RestDayPort interface {
    GetMyRestDay(ctx context.Context, userID uint) (dayOfWeek int, dayName string, err error)
}

// GroupSubPort 群订阅能力（直接操作，无需适配现有 Service）
type GroupSubPort interface {
    Subscribe(ctx context.Context, tenantID uint, conversationID, groupName string, enabledByUID uint) error
    Unsubscribe(ctx context.Context, tenantID uint, conversationID string) error
}

// ────────────── Agent 自有数据类型 ──────────────

// CourseItem 课程条目（Agent 视角）
type CourseItem struct {
    CourseName string `json:"course_name"`
    DayOfWeek  int    `json:"day_of_week"`
    Section    int    `json:"section"`
    Location   string `json:"location"`
    Teacher    string `json:"teacher"`
    WeekList   string `json:"week_list"`
}

// AttendanceQuery 考勤查询参数
type AttendanceQuery struct {
    Date    string // "2006-01-02"
    Week    int
    Section int
}

// AttendanceResult 考勤查询结果
type AttendanceResult struct {
    Date         string           `json:"date"`
    Week         int              `json:"week"`
    Section      int              `json:"section"`
    SlotStart    string           `json:"slot_start"`
    SlotEnd      string           `json:"slot_end"`
    ShouldAttend int              `json:"should_attend"`
    OnTimeCount  int              `json:"on_time_count"`
    LeaveCount   int              `json:"leave_count"`
    AbsentCount  int              `json:"absent_count"`
    RestDayCount int              `json:"rest_day_count"`
    OnTimeUsers  []string         `json:"on_time_users"`
    LeaveUsers   []AttendLeave    `json:"leave_users"`
    AbsentUsers  []string         `json:"absent_users"`
    RestDayUsers []string         `json:"rest_day_users"`
}

type AttendLeave struct {
    Name      string `json:"name"`
    LeaveType string `json:"leave_type"`
}

// FreeSlotResult 无课人员查询结果
type FreeSlotResult struct {
    DayOfWeek int      `json:"day_of_week"`
    Section   int      `json:"section"`
    SlotStart string   `json:"slot_start"`
    SlotEnd   string   `json:"slot_end"`
    FreeUsers []string `json:"free_users"`
    FreeCount int      `json:"free_count"`
}

// RankItem 排行条目
type RankItem struct {
    Name  string `json:"name"`
    Count int    `json:"count"`
    Rate  string `json:"rate,omitempty"` // 出勤率，如 "75%"
}

// LeaveItem 请假条目
type LeaveItem struct {
    Date      string `json:"date"`
    LeaveType string `json:"leave_type"`
    Duration  string `json:"duration"` // 如 "2天"
    Status    string `json:"status"`   // 如 "已批准"
}

// UserInfo 用户基本信息
type UserInfo struct {
    ID         uint   `json:"id"`
    Name       string `json:"name"`
    DingUserID string `json:"ding_user_id"`
    Role       int    `json:"role"`
    TenantID   uint   `json:"tenant_id"`
}

// PeriodInfo 作息时段信息
type PeriodInfo struct {
    Name  string `json:"name"`  // "第1-2节"
    Start string `json:"start"` // "08:00"
    End   string `json:"end"`   // "09:40"
}
```

### `agent.go`：只接收接口，不依赖具体类型

```go
// internal/agent/agent.go

type Deps struct {
    LLMBaseURL string
    LLMAPIKey  string
    LLMModel   string

    Schedule       SchedulePort
    Attendance     AttendancePort
    Leave          LeavePort
    User           UserPort
    Semester       SemesterPort
    SchedulePeriod SchedulePeriodPort
    RestDay        RestDayPort
    GroupSub       GroupSubPort

    Logger *zap.SugaredLogger
}

func NewAgent(deps Deps) *Agent { ... }
```

### `agent_wiring.go`：适配层（放在 internal/app/）

```go
// internal/app/agent_wiring.go
// 职责：桥接 internal/service → internal/agent Port 接口

package app

import (
    "schedule_server/internal/agent"
    "schedule_server/internal/consts"
    "schedule_server/internal/service"
    // ...
)

// ────────── Schedule Adapter ──────────

type scheduleAdapter struct {
    svc *service.ScheduleService
}

func (a *scheduleAdapter) ListMyScheduleByWeek(ctx context.Context, userID uint, week int) ([]agent.CourseItem, error) {
    // 用 RoleUser + targetUserID=userID 调用现有 Service
    result, err := a.svc.ListByWeek(ctx, userID, consts.RoleUser, userID, week)
    if err != nil {
        return nil, err
    }
    items := make([]agent.CourseItem, 0, len(result.Courses))
    for _, c := range result.Courses {
        items = append(items, agent.CourseItem{
            CourseName: c.CourseName,
            DayOfWeek:  c.DayOfWeek,
            Section:    c.Section,
            Location:   c.Location,
            Teacher:    c.Teacher,
            WeekList:   c.WeekList,
        })
    }
    return items, nil
}

func (a *scheduleAdapter) GetFreeUsersBySlot(ctx context.Context, week, dayStart, dayEnd int) ([]agent.FreeSlotResult, error) {
    // 调用 ScheduleService.GetFreeUsersBySlot（新增方法）
    // ... 转换返回类型
}

// ────────── Attendance Adapter ──────────

type attendanceAdapter struct {
    svc *service.AttendanceRecordService
}

func (a *attendanceAdapter) GetAttendanceDetail(ctx context.Context, req agent.AttendanceQuery) (*agent.AttendanceResult, error) {
    // agent.AttendanceQuery → dto.AttendanceDetailRequest
    dtoReq := &dto.AttendanceDetailRequest{
        Date:    req.Date,
        Week:    req.Week,
        Section: req.Section,
    }
    resp, err := a.svc.GetAttendanceRecordFromDB(ctx, dtoReq)
    if err != nil {
        return nil, err
    }
    // dto.AttendanceDetailResponse → agent.AttendanceResult
    return convertAttendanceResult(resp), nil
}

// ... 其他 Adapter 同理

// ────────── 构建 Agent ──────────

func buildAgent(
    scheduleSvc *service.ScheduleService,
    attendanceSvc *service.AttendanceRecordService,
    semesterSvc *service.SemesterService,
    schedulePeriodSvc *service.SchedulePeriodService,
    restDaySvc *service.RestDayService,
    leaveSyncSvc *service.LeaveSyncService,
    userRepo repository.UserRepository,
    groupSubRepo repository.GroupAttendanceSubscriptionRepository,
    logger *zap.SugaredLogger,
) *agent.Agent {
    return agent.NewAgent(agent.Deps{
        LLMBaseURL:     global.AppConfig.LLM.BaseURL,
        LLMAPIKey:      global.AppConfig.LLM.APIKey,
        LLMModel:       global.AppConfig.LLM.Model,
        Schedule:       &scheduleAdapter{svc: scheduleSvc},
        Attendance:     &attendanceAdapter{svc: attendanceSvc},
        Semester:       &semesterAdapter{svc: semesterSvc},
        SchedulePeriod: &schedulePeriodAdapter{svc: schedulePeriodSvc},
        RestDay:        &restDayAdapter{svc: restDaySvc},
        Leave:          &leaveAdapter{svc: leaveSyncSvc},
        User:           &userAdapter{repo: userRepo},
        GroupSub:       &groupSubAdapter{repo: groupSubRepo},
        Logger:         logger,
    })
}
```

### 与原方案对比

| | 原方案 | v2 方案 |
|--|--------|---------|
| Agent 依赖 | 声称只依赖 Port 接口，实际签名不匹配 | 真正只依赖 Port 接口，Adapter 在 app 层桥接 |
| 数据类型 | 隐式复用 model/dto 类型（未定义） | Agent 自定义数据类型，与 model/dto 完全解耦 |
| 工具文件 | 1 个大 `tools.go` | 按领域拆分为 5 个文件 |
| 现有 Service 改动 | 零改动（但签名不匹配的问题被忽略） | 零改动 + 显式 Adapter 解决签名差异 |
| 工具权限 | 执行时校验 → 浪费 LLM 调用 | 注册时标记 MinRole，发送前过滤工具列表 |
| 当前时间 | 每轮调 get_current_time 工具 | System Prompt 直接注入，工具作备用 |
| 工具返回格式 | 格式化 string | JSON 结构化数据，LLM 灵活组织回复 |
| chatHandler 签名 | 7 个 string 参数 | `ChatMessage` 结构体 |
| 速率限制 | 无 | per-user 令牌桶 |
| 生命周期 | 只有启动 | `Stop()` 方法 + 优雅关闭集成 |
| 可测试性 | 难以单独测试 Agent | Agent 可独立 mock 所有 Port |
| 可移除性 | 与现有代码深度耦合 | 删掉 `internal/agent/` + `agent_wiring.go` 即完整移除 |

---

## 文件变更清单

### 新建文件（11 个）

| 文件 | 用途 |
|------|------|
| `internal/agent/port.go` | Agent 所需能力的接口定义 + 自有数据类型 |
| `internal/agent/client.go` | LLM HTTP 客户端（调 SiliconFlow/DeepSeek-V3） |
| `internal/agent/session.go` | Session 管理（内存存储 + TTL 清理 + 生命周期） |
| `internal/agent/ratelimit.go` | Per-user 速率限制 |
| `internal/agent/agent.go` | ReAct Loop + 依赖注入入口 + `Stop()` |
| `internal/agent/tools/registry.go` | 工具注册表（含角色过滤） |
| `internal/agent/tools/schedule.go` | 课表相关工具实现 |
| `internal/agent/tools/attendance.go` | 考勤相关工具实现 |
| `internal/agent/tools/admin.go` | 管理员工具（补签、订阅推送） |
| `internal/app/agent_wiring.go` | Adapter 适配层 + buildAgent 函数 |
| `internal/model/group_attendance_subscription.go` | 群考勤推送订阅模型 |

### 修改文件（7 个）

| 文件 | 改动内容 | 改动量 |
|------|----------|--------|
| `config/config.go` | 新增 `LLM` 配置结构体（BaseURL、APIKey、Model） | +5 行 |
| `configs/dev.yaml` | 新增 `llm` 配置节 | +4 行 |
| `internal/repository/repository.go` | 新增 `GroupSubRepo` 字段和初始化 | +2 行 |
| `internal/service/stream_client_manager.go` | 新增 `chatHandler` 字段 + `SetChatMessageHandler` 方法 + `StartForTenant` 内 2 行调用 | +10 行 |
| `internal/app/app.go` | `startDingTalkStream` 内调用 `buildAgent` + 优雅关闭时调 `agent.Stop()` | +5 行 |
| `pkg/dingtalk/stream.go` | 新增 `ChatMessage` 结构体 + ROBOT 订阅 + `handleChatMessage` | +60 行 |
| `inits/database.go` | AutoMigrate 加入 `GroupAttendanceSubscription` 新表 | +1 行 |

> **现有 Handler / Service / Repository 代码：零改动。**

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
  api_key: "sk-your-api-key-here"
  model: "deepseek-ai/DeepSeek-V3"
```

---

### 3. `pkg/dingtalk/stream.go` — ChatMessage 结构体

**关键改动**：用结构体替代 7 个 string 参数，消除传参顺序错误的风险。

```go
// ChatMessage 机器人收到的聊天消息（解析后的结构）
type ChatMessage struct {
    CorpID            string // 企业 corpID
    SenderID          string // 发送者 dingUserId
    SenderNick        string // 发送者昵称
    Content           string // 消息内容（群聊已剥离 @mention）
    ConversationID    string // 群聊会话ID（单聊为空）
    ConversationType  string // "1"=单聊, "2"=群聊
    ConversationTitle string // 群名称（单聊为空）
}
```

**SetChatMessageHandler**：

```go
func (s *StreamClient) SetChatMessageHandler(
    handler func(ctx context.Context, msg *ChatMessage) (string, error),
)
```

**handleChatMessage**：处理 ROBOT topic 消息

```go
func (s *StreamClient) handleChatMessage(ctx context.Context, df *payload.DataFrame) (*payload.DataFrameResponse, error) {
    // 1. 解析 payload 为 RobotMessage
    // 2. 群聊时剥离 @mention（正则 @\S+\s*）
    // 3. 构建 ChatMessage 结构体
    // 4. 25 秒超时保护
    ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
    defer cancel()

    reply, err := s.chatHandler(ctx, &ChatMessage{
        CorpID:            raw.SenderCorpID,
        SenderID:          raw.SenderID,
        SenderNick:        raw.SenderNick,
        Content:           content, // 已剥离 @mention
        ConversationID:    raw.ConversationID,
        ConversationType:  raw.ConversationType,
        ConversationTitle: raw.ConversationTitle,
    })
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            reply = "处理超时，请重试"
        } else {
            reply = "处理出错，请重试"
        }
    }

    // 5. 封装回复（群聊 @发送者）
    return buildReply(reply, raw), nil
}
```

**回复格式**：

```json
// 单聊
{ "msgtype": "text", "text": { "content": "agent 的回复内容" } }

// 群聊（@发送者）
{
  "msgtype": "text",
  "text": { "content": "@发送者昵称 agent 的回复内容" },
  "at": { "atDingtalkIds": ["senderStaffID"] }
}
```

**`Start()` 修改**：新增 `client.WithSubscription("ROBOT", "*", s.handleChatMessage)`。

---

### 4. `internal/agent/client.go`

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

### 5. `internal/agent/tools/registry.go` — 含角色过滤

```go
package tools

import (
    "context"
    "encoding/json"
    "fmt"
)

// UserContext 工具执行时携带的调用者身份
type UserContext struct {
    TenantID          uint
    UserID            uint
    UserRole          int    // 0=普通用户, 1=管理员, 2=超级管理员
    DingUserID        string
    Name              string
    ConversationType  string // "1"=单聊, "2"=群聊
    ConversationID    string // 群聊会话 ID
    ConversationTitle string // 群名称
}

// ToolHandler 工具处理函数
type ToolHandler func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error)

// ToolEntry 注册的工具条目
type ToolEntry struct {
    Def     agent.ToolDef
    MinRole int        // 最低角色要求：0=所有用户, 1=管理员
    Handler ToolHandler
}

// Registry 工具注册表
type Registry struct {
    entries []ToolEntry
    byName  map[string]*ToolEntry
}

func NewRegistry() *Registry {
    return &Registry{
        byName: make(map[string]*ToolEntry),
    }
}

// Register 注册工具
func (r *Registry) Register(def agent.ToolDef, minRole int, handler ToolHandler) {
    entry := ToolEntry{Def: def, MinRole: minRole, Handler: handler}
    r.entries = append(r.entries, entry)
    r.byName[def.Function.Name] = &entry
}

// ToToolDefs 根据用户角色过滤，返回该用户可用的工具定义列表
// 只有 userRole >= entry.MinRole 的工具才会发送给 LLM
func (r *Registry) ToToolDefs(userRole int) []agent.ToolDef {
    var defs []agent.ToolDef
    for _, entry := range r.entries {
        if userRole >= entry.MinRole {
            defs = append(defs, entry.Def)
        }
    }
    return defs
}

// Dispatch 分发工具调用
// 即使工具未发送给 LLM，此处仍做二次权限校验，防止 LLM 幻觉调用
func (r *Registry) Dispatch(ctx context.Context, uctx *UserContext, name string, params json.RawMessage) (string, error) {
    entry, ok := r.byName[name]
    if !ok {
        return "", fmt.Errorf("unknown tool: %s", name)
    }
    if uctx.UserRole < entry.MinRole {
        return `{"error": "权限不足，该功能仅管理员可用"}`, nil
    }
    return entry.Handler(ctx, uctx, params)
}
```

**设计要点**：
- `ToToolDefs(userRole)` 在每次 LLM 调用前过滤，普通用户看不到管理员工具 → 减少无效工具调用
- `Dispatch` 做二次权限校验，双重保险

---

### 6. 工具实现 — 返回 JSON 结构化数据

工具返回 JSON 而非格式化文本。LLM 收到结构化数据后自行组织中文回复，可根据上下文灵活调整措辞。

#### `tools/schedule.go`

**`get_current_time`**
- **描述**：获取当前日期、星期、第几周
- **参数**：无
- **MinRole**：0（所有用户）
- **实现**：调 `SemesterPort.GetCurrentWeek(ctx)`
- **返回**：
  ```json
  {"date": "2026-03-13", "weekday": "周五", "weekday_num": 5, "week": 6, "total_weeks": 20}
  ```
  若无活跃学期：`{"date": "2026-03-13", "weekday": "周五", "weekday_num": 5, "week": 0, "error": "当前无活跃学期"}`

**`query_my_schedule`**
- **描述**：查询当前用户指定周的课表
- **参数**：`{ "week": 6 }` week 可选，默认当前周
- **MinRole**：0
- **实现**：`SchedulePort.ListMyScheduleByWeek(ctx, userID, week)`
- **返回**：
  ```json
  {
    "week": 6,
    "count": 2,
    "courses": [
      {"course_name": "数据结构", "day_of_week": 1, "section": 1, "location": "B204", "teacher": "张老师"},
      {"course_name": "操作系统", "day_of_week": 3, "section": 2, "location": "A101", "teacher": ""}
    ]
  }
  ```

**`query_free_users_by_slot`**
- **描述**：汇总指定周次、指定星期范围各节次的无课人员名单
- **参数**：`{ "week": 6, "day_start": 1, "day_end": 3 }` week/day_start/day_end 均可选
- **MinRole**：0
- **week 兜底逻辑**：若 `week` 未传或为 0，工具内部调 `SemesterPort.GetCurrentWeek(ctx)` 自动计算
- **实现**：`SchedulePort.GetFreeUsersBySlot(ctx, week, dayStart, dayEnd)`
- **返回**：
  ```json
  {
    "week": 1,
    "day_start": 1,
    "day_end": 3,
    "slots": [
      {"day_of_week": 1, "section": 1, "slot_start": "08:00", "slot_end": "09:40", "free_count": 10, "free_users": ["伍荣云", "俞希闻", ...]},
      ...
    ]
  }
  ```

**`query_schedule_info`**
- **描述**：查询当前作息模式及各节次时间安排
- **参数**：无
- **MinRole**：0
- **实现**：`SchedulePeriodPort.GetScheduleInfo(ctx)`
- **返回**：
  ```json
  {
    "mode": "上学",
    "periods": [
      {"name": "第1-2节", "start": "08:00", "end": "09:40"},
      {"name": "第3-4节", "start": "10:10", "end": "11:50"},
      ...
    ]
  }
  ```

#### `tools/attendance.go`

**`query_attendance_status`**
- **描述**：查询指定日期指定节次的考勤状态
- **参数**：
  ```json
  {
    "date":    "2026-03-12",  // 可选，默认今天
    "week":    6,              // 可选，默认自动计算
    "section": 3               // 必填
  }
  ```
- **MinRole**：0
- **week 兜底逻辑**：若 `week` 未传或为 0，工具内部调 `SemesterPort.GetCurrentWeek(ctx)` 自动计算
- **实现**：`AttendancePort.GetAttendanceDetail(ctx, req)`
- **返回**：
  ```json
  {
    "date": "2026-03-12", "week": 6, "section": 3,
    "slot_start": "14:30", "slot_end": "16:10",
    "should_attend": 18, "on_time_count": 15, "leave_count": 1, "absent_count": 2,
    "on_time_users": ["王芳", "赵伟", ...],
    "leave_users": [{"name": "王五", "leave_type": "年假"}],
    "absent_users": ["张三", "李四"]
  }
  ```

**`generate_attendance_text`**
- **描述**：生成可直接群发的考勤通报文本
- **参数**：同 `query_attendance_status`
- **MinRole**：0
- **实现**：`AttendancePort.GetAttendanceText(ctx, req)`
- **返回**：直接返回格式化文本字符串（此工具例外，因为其输出本身就是最终产物）

**`query_weekly_absence_ranking`**
- **描述**：查询本周缺勤次数排行（前10名）
- **参数**：无
- **MinRole**：0
- **实现**：`AttendancePort.GetWeeklyAbsenceRanking(ctx)`
- **返回**：
  ```json
  {"week": 6, "items": [{"name": "张三", "count": 3}, {"name": "李四", "count": 2}]}
  ```

**`query_weekly_attendance_ranking`**
- **描述**：查询本周出勤率排行（前10名）
- **参数**：无
- **MinRole**：0
- **实现**：`AttendancePort.GetWeeklyAttendanceRateRanking(ctx)`
- **返回**：
  ```json
  {"week": 6, "items": [{"name": "王芳", "count": 4, "rate": "100%"}, {"name": "赵伟", "count": 3, "rate": "75%"}]}
  ```

**`query_rest_days`**
- **描述**：查询当前用户的休息日配置
- **参数**：无
- **MinRole**：0
- **实现**：`RestDayPort.GetMyRestDay(ctx, userID)`
- **返回**：
  ```json
  {"day_of_week": 6, "day_name": "周六"}
  ```
  若未设置：`{"day_of_week": 0, "day_name": ""}`

**`query_my_leave`**
- **描述**：查询当前用户近期请假记录
- **参数**：`{ "days": 30 }` 可选，默认 30
- **MinRole**：0
- **实现**：`LeavePort.GetRecentLeaves(ctx, userID, days)`
- **返回**：
  ```json
  {
    "days": 30,
    "count": 2,
    "items": [
      {"date": "2026-03-08", "leave_type": "年假", "duration": "2天", "status": "已批准"},
      {"date": "2026-02-20", "leave_type": "病假", "duration": "1天", "status": "已批准"}
    ]
  }
  ```

#### `tools/admin.go`（MinRole = 1）

**`sign_for_user`**
- **描述**：为指定用户补签某节次考勤
- **MinRole**：1（管理员）
- **参数**：
  ```json
  {
    "user_name": "张三",
    "date":      "2026-03-12",
    "week":      6,
    "section":   1
  }
  ```
- **实现（多步骤）**：
  1. `UserPort.SearchByName(ctx, userName)` → 查找匹配用户
  2. 若结果为空 → 返回 `{"error": "找不到用户「张三」，请确认姓名"}`
  3. 若结果多于 1 人 → 取第一位继续执行
  4. `AttendancePort.FindRecordByDateSection(ctx, date, section)` → 找到 recordID
  5. 若 recordID 不存在 → 返回 `{"error": "该节次尚未统计，请等待系统自动统计后再操作"}`
  6. `AttendancePort.SignForUsers(ctx, recordID, []uint{userID})`
- **返回**：`{"success": true, "message": "已为张三补签 2026-03-12 第1-2节考勤"}`

**`subscribe_attendance_push`**
- **描述**：将当前群聊订阅为考勤自动推送目标
- **MinRole**：1（管理员）
- **前置校验**：必须在群聊（`conversationType == "2"`）中调用
- **参数**：无（`conversationID` 和 `conversationTitle` 从 `UserContext` 取）
- **实现**：`GroupSubPort.Subscribe(ctx, tenantID, conversationID, groupName, userID)`
- **返回**：`{"success": true, "message": "已为此群开启考勤推送"}`

**`unsubscribe_attendance_push`**
- **描述**：取消当前群聊的考勤自动推送
- **MinRole**：1（管理员）
- **前置校验**：必须在群聊中调用
- **参数**：无
- **实现**：`GroupSubPort.Unsubscribe(ctx, tenantID, conversationID)`
- **返回**：`{"success": true, "message": "已取消此群的考勤自动推送"}`

---

### 7. `internal/agent/session.go`

```go
type session struct {
    messages  []Message
    updatedAt time.Time
}

// 单聊 key = "corpID:dingUserID"
// 群聊 key = "corpID:conversationID:dingUserID"（每人在群内独立上下文）
sessions map[string]*session
mu       sync.RWMutex

const (
    maxHistory = 20              // 最多保留 20 条消息（system prompt 不计入）
    sessionTTL = 30 * time.Minute
)
```

---

### 8. `internal/agent/ratelimit.go` — Per-User 速率限制

防止单个用户频繁发消息导致 LLM API 费用暴增。

```go
package agent

import (
    "sync"
    "time"
)

// rateLimiter 简单的固定窗口计数器（不引入新依赖）
type rateLimiter struct {
    mu       sync.Mutex
    counters map[string]*counter
}

type counter struct {
    count    int
    windowAt time.Time
}

const (
    rateLimitWindow = 1 * time.Minute // 窗口大小
    rateLimitMax    = 10              // 每窗口最大请求数
)

func newRateLimiter() *rateLimiter {
    return &rateLimiter{counters: make(map[string]*counter)}
}

// Allow 检查是否允许请求。key 格式：corpID:dingUserID
func (r *rateLimiter) Allow(key string) bool {
    r.mu.Lock()
    defer r.mu.Unlock()

    now := time.Now()
    c, ok := r.counters[key]
    if !ok || now.Sub(c.windowAt) > rateLimitWindow {
        r.counters[key] = &counter{count: 1, windowAt: now}
        return true
    }
    if c.count >= rateLimitMax {
        return false
    }
    c.count++
    return true
}
```

在 `Agent.Chat()` 入口处检查：

```go
if !a.limiter.Allow(sessionKey) {
    return "你发消息太快了，请稍后再试", nil
}
```

---

### 9. `internal/agent/agent.go` — ReAct Loop + 生命周期管理

**System Prompt 策略**：直接注入当前时间信息，减少工具调用轮次。

```go
func (a *Agent) buildSystemPrompt(uctx *tools.UserContext) string {
    roleText := "普通用户"
    if uctx.UserRole >= consts.RoleAdmin {
        roleText = "管理员"
    }

    // 尝试获取当前周次（静默失败）
    weekInfo := ""
    if week, total, err := a.deps.Semester.GetCurrentWeek(context.Background()); err == nil {
        weekInfo = fmt.Sprintf("\n- 学期周次：第%d周（共%d周）", week, total)
    } else {
        weekInfo = "\n- 学期周次：当前无活跃学期"
    }

    now := time.Now()
    weekdays := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

    return fmt.Sprintf(`你是「课表助手」，服务于学校课表与考勤管理系统。

当前信息：
- 用户：%s（%s）
- 日期：%s（%s）%s

约束：
- 只能使用提供的工具获取数据，不要编造或猜测
- 回复用中文，简洁明了，避免冗余解释
- 如果工具返回错误，如实告知用户
- 工具返回的是 JSON 数据，请用自然语言组织回复`,
        uctx.Name, roleText,
        now.Format("2006-01-02"), weekdays[now.Weekday()],
        weekInfo,
    )
}
```

**生命周期管理**：

```go
type Agent struct {
    deps        Deps
    llmClient   *LLMClient
    registry    *tools.Registry
    sessions    map[string]*session
    mu          sync.RWMutex
    limiter     *rateLimiter
    stopCleanup chan struct{} // 用于停止清理 goroutine
}

func NewAgent(deps Deps) *Agent {
    a := &Agent{
        deps:        deps,
        llmClient:   NewLLMClient(deps.LLMBaseURL, deps.LLMAPIKey, deps.LLMModel),
        sessions:    make(map[string]*session),
        limiter:     newRateLimiter(),
        stopCleanup: make(chan struct{}),
    }

    // 注册工具
    a.registry = tools.NewRegistry()
    tools.RegisterScheduleTools(a.registry, deps.Schedule, deps.Semester, deps.SchedulePeriod)
    tools.RegisterAttendanceTools(a.registry, deps.Attendance, deps.Semester, deps.RestDay, deps.Leave)
    tools.RegisterAdminTools(a.registry, deps.Attendance, deps.User, deps.GroupSub)

    // 启动 Session 过期清理
    go a.cleanupLoop()

    return a
}

// Stop 停止 Agent（清理 goroutine），在优雅关闭时调用
func (a *Agent) Stop() {
    close(a.stopCleanup)
}

func (a *Agent) cleanupLoop() {
    ticker := time.NewTicker(10 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            a.purgeExpiredSessions()
        case <-a.stopCleanup:
            return
        }
    }
}
```

**ReAct Loop**：

```
输入：ctx context.Context, msg *dingtalk.ChatMessage
  │
  ├─ 查用户 → UserPort.FindByDingUserID(ctx, msg.SenderID)
  │    若未找到 → 返回 "您尚未绑定账户，请先通过小程序登录"
  ├─ 注入租户 → tenantctx.WithTenantID(ctx, user.TenantID)
  ├─ 构建 UserContext
  ├─ 构建 sessionKey：
  │     单聊(type=="1")：corpID:dingUserID
  │     群聊(type=="2")：corpID:conversationID:dingUserID
  ├─ 限流检查 → limiter.Allow(sessionKey)
  │    若被限流 → 返回 "你发消息太快了，请稍后再试"
  ├─ 加载或创建 session（追加用户消息）
  ├─ 构建 System Prompt（注入日期/周次/身份）
  ├─ 获取工具列表 → registry.ToToolDefs(uctx.UserRole)
  │
  └─ Loop（最多 5 轮，防无限循环）:
       ├─ LLMClient.Chat(systemPrompt + history, filteredTools)
       ├─ finish_reason == "tool_calls":
       │     for each tool_call:
       │       result = registry.Dispatch(ctx, uctx, name, params)
       │       追加 assistant 消息（含 tool_calls）
       │       追加 tool 消息（含 tool call id + result）
       │     continue loop
       └─ finish_reason == "stop":
             保存 session（裁剪超长历史）
             返回 content
```

---

### 10. `internal/service/stream_client_manager.go`

镜像 `onBpmsEvent` 的模式，新增聊天消息处理器：

```go
type StreamClientManager struct {
    // 已有字段不动
    chatHandler func(ctx context.Context, msg *dingtalk.ChatMessage) (string, error) // 新增
}

func (m *StreamClientManager) SetChatMessageHandler(
    h func(ctx context.Context, msg *dingtalk.ChatMessage) (string, error),
) {
    m.chatHandler = h // 在 StartAll 之前调用
}

// StartForTenant 内只加两行
streamClient.SetBpmsEventHandler(eventHandler)
if m.chatHandler != nil {
    streamClient.SetChatMessageHandler(m.chatHandler) // 新增
}
```

---

### 11. `internal/app/app.go`

`startDingTalkStream` 函数内已有 `repo` 和 `dingMgr`，Agent 直接复用：

```go
// 包级变量，用于优雅关闭
var agentInstance *agent.Agent

func startDingTalkStream(ctx context.Context) {
    defer func() { /* 已有的 recover */ }()

    // 已有代码
    repo := repository.NewRepository(global.DB)
    dingMgr := service.NewDingTalkClientManager(repo.TenantRepo)
    leaveSyncSrv := service.NewLeaveSyncService(repo.LeaveRepo, repo.UserRepo, dingMgr, global.Log)

    // 创建多租户 Stream 客户端管理器
    streamMgr := service.NewStreamClientManager(repo.TenantRepo, global.Log)

    // 定义事件处理器
    eventHandler := func(ctx context.Context, corpID, processInstanceID, eventType string) error {
        return leaveSyncSrv.SyncProcessInstance(ctx, corpID, processInstanceID)
    }

    // ── 新增：构建 Agent ──
    // 构建 Agent 所需 Service（复用上方已有的 repo 和 dingMgr）
    semesterSrv := service.NewSemesterService(repo.SemesterRepo)
    schedulePeriodSrv := service.NewSchedulePeriodService(
        repo.SchedulePeriodRepo, repo.ScheduleSettingRepo, &global.AppConfig.Schedule,
    )
    attendanceSvc := service.NewAttendanceRecordService(
        repo.UserRepo, repo.CourseRepo, repo.LeaveRepo,
        repo.AttendanceRecordRepo, repo.ScheduleSettingRepo, repo.UserRestDayRepo,
        dingMgr, schedulePeriodSrv, semesterSrv,
        global.AppConfig.Schedule, global.Log,
    )
    scheduleSvc := service.NewScheduleService(
        repo.CourseRepo, repo.UserRepo, repo.SemesterRepo,
        repo.ScheduleSettingRepo, dingMgr, global.Log,
    )
    restDaySvc := service.NewRestDayService(
        repo.UserRestDayRepo, repo.ScheduleSettingRepo, repo.UserRepo, global.Log,
    )

    agentInstance = buildAgent(
        scheduleSvc, attendanceSvc, semesterSrv, schedulePeriodSrv,
        restDaySvc, leaveSyncSrv, repo.UserRepo, repo.GroupSubRepo, global.Log,
    )

    // 注册聊天消息处理器
    streamMgr.SetChatMessageHandler(agentInstance.Chat)
    // ── 新增结束 ──

    // 启动所有活跃租户的 Stream 客户端
    if err := streamMgr.StartAll(ctx, eventHandler); err != nil {
        global.Log.Errorw("启动 Stream 客户端管理器失败", "err", err)
        return
    }

    <-ctx.Done()
    streamMgr.StopAll()
}
```

`RunServer` 优雅关闭时停止 Agent：

```go
// 停止钉钉 Stream 客户端
streamCancel()
time.Sleep(2 * time.Second)

// 停止 Agent（清理 session 清理 goroutine）
if agentInstance != nil {
    agentInstance.Stop()
}

// 停止考勤调度器
if attendanceScheduler != nil {
    attendanceScheduler.Stop()
}
```

---

### 12. `internal/model/group_attendance_subscription.go`

```go
// GroupAttendanceSubscription 群考勤推送订阅
type GroupAttendanceSubscription struct {
    ID             uint           `gorm:"primaryKey"`
    TenantID       uint           `gorm:"not null;uniqueIndex:uniq_tenant_conv"`
    ConversationID string         `gorm:"not null;uniqueIndex:uniq_tenant_conv"` // 群 openConversationId
    GroupName      string         // 群名称（展示用）
    EnabledByUID   uint           // 开启订阅的管理员本地用户 ID
    CreatedAt      time.Time
    DeletedAt      gorm.DeletedAt `gorm:"index"` // 软删除用于取消订阅
}

func (*GroupAttendanceSubscription) TableName() string {
    return "group_attendance_subscriptions"
}
```

---

### 13. `pkg/dingtalk/client.go` 新增方法

机器人主动发消息到群聊，使用钉钉 `POST /v1.0/robot/groupMessages/send` 接口：

```go
// SendGroupRobotMessage 机器人主动发文本消息到群聊
// robotCode = 应用的 AppKey，conversationID = 群的 openConversationId
func (c *Client) SendGroupRobotMessage(
    ctx context.Context,
    robotCode string,
    conversationID string,
    content string,
) error
```

---

### 14. `internal/scheduler/attendance_scheduler.go` 修改

**构造函数新增两个依赖**：

```go
func NewAttendanceScheduler(
    // ...已有参数...
    groupSubRepo  repository.GroupAttendanceSubscriptionRepository, // 新增
    dingClientMgr *dingtalk.DingTalkClientManager,                  // 新增
    // ...
) *AttendanceScheduler
```

在 `triggerAttendanceForTenant` 末尾，考勤快照保存成功后，新增推送步骤：

```
考勤快照保存完成
  └─ 查询 group_attendance_subscriptions WHERE tenant_id = X（过滤软删除）
  └─ 若无订阅群 → 跳过
  └─ GetAttendanceText(ctx, req) → 生成考勤文本
  └─ for each subscription:
       dingClient.SendGroupRobotMessage(ctx, tenant.AppKey, sub.ConversationID, text)
       若返回错误 → 记录日志，不影响其他群推送
```

**错误处理**：推送失败只记日志，不影响考勤统计主流程；不自动删除失败的订阅。

---

## 对话示例

**场景 1：查自己课表**
```
用户：我今天有什么课？

System Prompt 已包含：日期 2026-03-13（周五），第6周

助手：[调用 query_my_schedule → week=6]
      工具返回 JSON：{"week":6,"count":2,"courses":[...]}

回复：今天周五，你有 2 门课：
      · 第1-2节（08:00-09:40）《数据库原理》B204
      · 第7-8节（16:40-18:20）《软件工程》A302
```

注意：因为 System Prompt 已注入日期和周次，LLM 不需要先调 `get_current_time`，**直接调 `query_my_schedule`，一轮工具调用即完成**。

**场景 2：查考勤（管理员视角）**
```
用户：刚刚第3节谁没来？

助手：[调用 query_attendance_status → date="2026-03-13", week=6, section=3]
      工具返回 JSON：{"should_attend":18,"on_time_count":15,"leave_count":1,"absent_count":2,...}

回复：第5-6节（14:30-16:10）考勤：
      缺勤 2 人：张三、李四
      请假 1 人：王五
      正常 15 人
```

**场景 3：多轮对话（单聊）**
```
用户：本周缺勤最多的是谁？
助手：[调用 query_weekly_absence_ranking]
回复：本周缺勤最多的是张三，缺勤 3 次。

用户：那出勤最好的呢？
助手：[调用 query_weekly_attendance_ranking]
回复：本周出勤率最高的是王芳，出勤率 100%（4/4节）。
```

**场景 4：群聊 @机器人**
```
用户（群里）：@课表助手 刚刚第3节谁没来？
助手（群里）：[消息预处理：剥离 @mention → "刚刚第3节谁没来？"]
              [调用 query_attendance_status → date="2026-03-13", week=6, section=3]
回复（群里）：@王老师 第5-6节考勤：缺勤 2 人：张三、李四 ...
```

**场景 5：订阅 / 取消考勤自动推送**
```
【订阅】
管理员（群里）：@课表助手 以后每次考勤统计完，把结果发到这个群
机器人（群里）：[调用 subscribe_attendance_push]
回复（群里）：@王老师 好的，已为此群开启考勤推送。
              每次考勤统计完成后将自动在本群发送结果。
              如需取消，请告诉我「取消考勤推送」。

【Scheduler 自动触发，无需用户操作】
机器人（群里）：【考勤通报】2026-03-13 第5-6节（14:30-16:10）
               应到：18人  正常：15人  缺勤：2人  请假：1人
               ————————
               缺勤：张三、李四
               请假：王五（年假）

【取消订阅】
管理员（群里）：@课表助手 取消考勤推送
机器人（群里）：[调用 unsubscribe_attendance_push]
回复（群里）：@王老师 已取消此群的考勤自动推送。
```

**场景 6：普通用户尝试使用管理员功能**
```
用户（普通用户）：帮张三补签第3节
助手：（工具列表中没有 sign_for_user，LLM 无法调用）
回复：抱歉，补签操作仅管理员可用。请联系管理员帮忙操作。
```

**场景 7：被限流**
```
用户：（1 分钟内第 11 次发消息）
回复：你发消息太快了，请稍后再试
```

---

## 设计要点总结

1. **租户隔离**：所有工具调用前必须调用 `tenantctx.WithTenantID(ctx, tenantID)`。

2. **用户未注册**：若 dingUserID 在本地 users 表找不到对应记录，Agent 回复「您尚未绑定账户，请先通过小程序登录」。

3. **群聊 @mention 处理**：`conversationType == "2"` 时，用正则剥离消息内容中的 `@机器人名称` 前缀后再送入 Agent；回复时在内容前拼接 `@发送者昵称` 并填写 `at.atDingtalkIds`。

4. **最大 Loop 轮数**：ReAct Loop 设 5 轮上限，防止 LLM 无限调工具。超出限制回复「处理超时，请重试」。

5. **Stream 回调超时**：`handleChatMessage` 内设 25 秒 context 超时，保证回调必然在超时前返回。若触发超时，回复「处理超时，请重试」。若后续实际遇到超时问题，可升级为「立即 ACK + 异步处理 + HTTP 主动推送」方案。

6. **工具角色过滤**：`Registry.ToToolDefs(userRole)` 根据用户角色过滤工具定义，普通用户看不到管理员工具 → 减少无效 LLM 调用。`Dispatch` 做二次权限校验，双重保险。

7. **System Prompt 注入时间**：日期、星期、周次直接注入 System Prompt，减少 `get_current_time` 工具调用轮次。保留该工具作为备用（用户问"下周二是几号"等场景）。

8. **工具返回 JSON**：工具返回结构化 JSON 数据，由 LLM 自行组织自然语言回复，措辞更灵活。

9. **Per-user 速率限制**：简单固定窗口计数器，每用户每分钟最多 10 次请求，防止 LLM API 费用暴增。

10. **生命周期管理**：Agent 提供 `Stop()` 方法，在优雅关闭时停止 Session 清理 goroutine。

11. **无新 Go 依赖**：整个 agent 模块只用标准库 + 现有项目依赖，不修改 go.mod。

---

## 实现顺序

1. `config/config.go` + `configs/dev.yaml`（配置）
2. `internal/agent/port.go`（接口 + 数据类型定义，先确定契约）
3. `internal/agent/client.go`（LLM 客户端）
4. `internal/agent/session.go`（Session 管理）
5. `internal/agent/ratelimit.go`（速率限制）
6. `internal/agent/tools/registry.go`（工具注册表 + 角色过滤）
7. `internal/agent/tools/schedule.go` + `attendance.go` + `admin.go`（工具实现）
8. `internal/agent/agent.go`（ReAct Loop，组装上述模块）
9. `internal/app/agent_wiring.go`（Adapter 适配层 + buildAgent）
10. `internal/model/group_attendance_subscription.go`（订阅模型）
11. `internal/repository/repository.go`（加 `GroupSubRepo` 字段）
12. `inits/database.go`（AutoMigrate 加新表）
13. `pkg/dingtalk/stream.go`（`ChatMessage` 结构体 + ROBOT 订阅 + handleChatMessage）
14. `internal/service/stream_client_manager.go`（加 chatHandler 字段和方法）
15. `internal/app/app.go`（`startDingTalkStream` 内接线 + 优雅关闭）
16. `go build ./...` 验证编译

---

## 本次不涉及（第二阶段）

- 写操作工具（修改课表、切换作息模式、补签以外的考勤写操作）
  > 注：考勤统计由 `internal/scheduler/attendance_scheduler.go` 自动定时触发，HTTP 触发接口已废弃，不作为工具提供
- Session 持久化
- 流式输出（DingTalk 不支持流式气泡）
- 统一 DI 容器（当前 HTTP/Stream/Scheduler/Agent 各自创建 Service 实例，Service 无状态故不影响正确性，但有维护负担，可后续统一）

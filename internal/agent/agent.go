package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"schedule_server/internal/agent/tools"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/dingtalk"

	"go.uber.org/zap"
)

const maxReactRounds = 5

// Deps Agent 依赖注入
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
	CallLog        CallLogPort

	Logger *zap.SugaredLogger
}

// Agent AI 助手
type Agent struct {
	deps        Deps
	llmClient   *LLMClient
	registry    *tools.Registry
	sessions    *sessionManager
	limiter     *rateLimiter
	stopCleanup chan struct{}
	once        sync.Once
}

// NewAgent 创建 Agent
func NewAgent(deps Deps) *Agent {
	a := &Agent{
		deps:        deps,
		llmClient:   NewLLMClient(deps.LLMBaseURL, deps.LLMAPIKey, deps.LLMModel),
		sessions:    newSessionManager(),
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
	a.once.Do(func() {
		close(a.stopCleanup)
	})
}

func (a *Agent) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.sessions.purgeExpired()
			a.limiter.purgeExpired()
		case <-a.stopCleanup:
			return
		}
	}
}

// Chat 处理聊天消息（DingTalk Stream 回调入口）
func (a *Agent) Chat(ctx context.Context, msg *dingtalk.ChatMessage) (string, error) {
	if msg == nil || msg.Content == "" {
		return "请输入您的问题", nil
	}

	// 1. 查用户
	user, err := a.deps.User.FindByDingUserID(ctx, msg.SenderID)
	if err != nil {
		a.deps.Logger.Errorw("查找用户失败", "senderID", msg.SenderID, "err", err)
		return "系统错误，请稍后重试", nil
	}
	if user == nil {
		return "您尚未绑定账户，请先通过小程序登录", nil
	}

	// 2. 注入租户上下文
	ctx = tenantctx.WithTenantID(ctx, user.TenantID)

	// 3. 构建 UserContext
	uctx := &tools.UserContext{
		TenantID:          user.TenantID,
		UserID:            user.ID,
		UserRole:          user.Role,
		DingUserID:        user.DingUserID,
		Name:              user.Name,
		ConversationType:  msg.ConversationType,
		ConversationID:    msg.ConversationID,
		ConversationTitle: msg.ConversationTitle,
	}

	// 4. 构建 session key（使用 tenantID 确保租户隔离）
	var sessionKey string
	if msg.ConversationType == "2" {
		sessionKey = fmt.Sprintf("%d:%s:%s", user.TenantID, msg.ConversationID, msg.SenderID)
	} else {
		sessionKey = fmt.Sprintf("%d:%s", user.TenantID, msg.SenderID)
	}

	// 5. 限流检查
	if !a.limiter.Allow(sessionKey) {
		a.deps.Logger.Infow("限流拦截", "user", user.Name, "tenantID", user.TenantID)
		return "你发消息太快了，请稍后再试", nil
	}

	a.deps.Logger.Infow("收到消息",
		"user", user.Name,
		"tenantID", user.TenantID,
		"convType", msg.ConversationType,
		"content", msg.Content,
	)

	startTime := time.Now()
	var toolsCalled []string

	// 6. 加载历史消息
	history := a.sessions.getMessages(sessionKey)
	userMsg := tools.Message{Role: "user", Content: msg.Content}

	// 7. 构建完整消息列表（system + history + 当前用户消息）
	systemMsg := tools.Message{Role: "system", Content: a.buildSystemPrompt(ctx, uctx)}
	messages := make([]tools.Message, 0, 1+len(history)+1)
	messages = append(messages, systemMsg)
	messages = append(messages, history...)
	messages = append(messages, userMsg)

	// 8. 获取该用户可用的工具列表
	toolDefs := a.registry.ToToolDefs(uctx.UserRole)

	// 9. ReAct Loop
	for round := 0; round < maxReactRounds; round++ {
		resp, err := a.llmClient.Chat(ctx, messages, toolDefs)
		if err != nil {
			a.deps.Logger.Errorw("LLM 调用失败", "round", round, "err", err)
			a.writeCallLog(ctx, uctx, msg.Content, "", toolsCalled, round, startTime, "failed", err.Error())
			return "AI 服务暂时不可用，请稍后重试", nil
		}

		// 无工具调用 → 返回最终回复
		if len(resp.ToolCalls) == 0 {
			reply := resp.Content
			if reply == "" {
				reply = "抱歉，我无法理解您的问题，请换个方式描述"
			}
			a.deps.Logger.Infow("回复完成", "user", uctx.Name, "rounds", round+1, "reply", reply)
			a.writeCallLog(ctx, uctx, msg.Content, reply, toolsCalled, round+1, startTime, "success", "")
			a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
			return reply, nil
		}

		// 有工具调用 → 执行工具
		messages = append(messages, resp) // assistant message with tool_calls

		for _, tc := range resp.ToolCalls {
			a.deps.Logger.Infow("调用工具", "tool", tc.Function.Name, "user", uctx.Name, "args", tc.Function.Arguments)
			toolsCalled = append(toolsCalled, tc.Function.Name)
			toolResult, err := a.registry.Dispatch(ctx, uctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			if err != nil {
				a.deps.Logger.Warnw("工具执行失败",
					"tool", tc.Function.Name,
					"err", err,
				)
				toolResult = fmt.Sprintf(`{"error": "工具执行失败: %s"}`, err.Error())
			}

			messages = append(messages, tools.Message{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
			})
		}
	}

	// 超出最大轮数
	a.deps.Logger.Warnw("ReAct Loop 超出最大轮数", "sessionKey", sessionKey)
	a.writeCallLog(ctx, uctx, msg.Content, "", toolsCalled, maxReactRounds, startTime, "failed", "超出最大轮数")
	return "处理轮次过多，请简化您的问题后重试", nil
}

// writeCallLog 异步写入调用记录，不阻塞对话响应
func (a *Agent) writeCallLog(ctx context.Context, uctx *tools.UserContext, question, reply string, toolsCalled []string, rounds int, startTime time.Time, status, errMsg string) {
	if a.deps.CallLog == nil {
		return
	}
	log := tools.CallLog{
		TenantID:    uctx.TenantID,
		UserID:      uctx.UserID,
		UserName:    uctx.Name,
		ConvType:    uctx.ConversationType,
		Question:    question,
		ToolsCalled: toolsCalled,
		Reply:       reply,
		Rounds:      rounds,
		DurationMs:  time.Since(startTime).Milliseconds(),
		Status:      status,
		ErrorMsg:    errMsg,
	}
	go a.deps.CallLog.Write(context.Background(), log)
}

// buildSystemPrompt 构建系统提示词
func (a *Agent) buildSystemPrompt(ctx context.Context, uctx *tools.UserContext) string {
	roleText := "普通用户"
	if uctx.UserRole >= 1 {
		roleText = "管理员"
	}

	weekInfo := ""
	if week, total, err := a.deps.Semester.GetCurrentWeek(ctx); err == nil {
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

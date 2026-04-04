package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"schedule_server/internal/agent/tools"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/dingtalk"

	"go.uber.org/zap"
)

// 最大循环次数
const maxReactRounds = 8

const (
	outOfDomainReply = "抱歉，我只能回答课程人员、考勤及请假相关的问题，无法回答其他内容。"
	noKnowledgeReply = "抱歉，我没有检索到相关规则说明，请联系管理员补充知识库"
)

// Deps Agent 依赖注入
type Deps struct {
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string

	Schedule        SchedulePort
	Attendance      AttendancePort
	Leave           LeavePort
	User            UserPort
	Semester        SemesterPort
	SchedulePeriod  SchedulePeriodPort
	RestDay         RestDayPort
	GroupSub        GroupSubPort
	Dept            DeptPort
	Knowledge       KnowledgePort
	CallLog         CallLogPort
	AttendanceStats AttendanceStatsPort
	UserCross       UserCrossPort
	Tenant          TenantPort

	Logger *zap.SugaredLogger
}

// Agent AI 助手
type Agent struct {
	deps        Deps
	llmClient   *LLMClient
	registry    *tools.Registry
	domainGate  *domainGate
	router      *queryRouter
	sessions    *sessionManager
	limiter     *rateLimiter
	stopCleanup chan struct{}
	once        sync.Once
}

type callMetrics struct {
	QueryType               queryKind
	ConversationEvent       conversationEvent
	ActiveTaskType          string
	TaskStatusBefore        string
	TaskStatusAfter         string
	DomainResult            domainResult
	AnswerMode              answerMode
	LLMDurationMs           int64
	RetrievalDurationMs     int64
	RetrievalHitCount       int
	RetrievalCandidateCount int
	SourceRefs              []string
	RetrievalTopRefs        []string
	RetrievalScores         []int
	FollowUpMatchedSlots    []string
	RetrievalFilteredReason string
	KnowledgeDocTypes       []string
}

// NewAgent 创建 Agent
func NewAgent(deps Deps) *Agent {
	a := &Agent{
		deps:        deps,
		llmClient:   NewLLMClient(deps.LLMBaseURL, deps.LLMAPIKey, deps.LLMModel),
		sessions:    newSessionManager(),
		domainGate:  newDomainGate(),
		router:      newQueryRouter(),
		limiter:     newRateLimiter(),
		stopCleanup: make(chan struct{}),
	}

	// 注册工具
	a.registry = tools.NewRegistry()
	tools.RegisterScheduleTools(a.registry, deps.Schedule, deps.Semester, deps.SchedulePeriod, deps.Dept)
	tools.RegisterAttendanceTools(a.registry, deps.Attendance, deps.Semester, deps.RestDay, deps.Leave, deps.Dept)
	tools.RegisterAdminTools(a.registry, deps.Attendance, deps.User, deps.GroupSub, deps.Dept)
	tools.RegisterAnalyticsTools(a.registry, deps.AttendanceStats, deps.UserCross, deps.Dept)

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
	reply, err := a.chat(ctx, msg)
	if err != nil || msg == nil || msg.ConversationType != "2" {
		return reply, err
	}
	// 群聊中在回复前 @发送者，群成员可以清楚这条回复针对谁
	return fmt.Sprintf("@%s\n%s", msg.SenderNick, reply), nil
}

// chat 内部处理逻辑
func (a *Agent) chat(ctx context.Context, msg *dingtalk.ChatMessage) (string, error) {
	if msg == nil || msg.Content == "" {
		return "请输入您的问题", nil
	}

	// 1. 通过 CorpID 确定租户并注入上下文（必须先于用户查询，保证租户隔离正确）
	tenantID, err := a.deps.Tenant.FindTenantIDByCorpID(ctx, msg.CorpID)
	if err != nil {
		a.deps.Logger.Errorw("查找租户失败", "corpID", msg.CorpID, "err", err)
		return "系统错误，请稍后重试", nil
	}
	if tenantID == 0 {
		a.deps.Logger.Infow("未找到对应企业，拒绝服务", "corpID", msg.CorpID)
		return "未找到对应的企业，请联系管理员", nil
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)

	// 2. 查用户（已有租户上下文，隔离正确）
	user, err := a.deps.User.FindByDingUserID(ctx, msg.SenderID)
	if err != nil {
		a.deps.Logger.Errorw("查找用户失败", "senderID", msg.SenderID, "err", err)
		return "系统错误，请稍后重试", nil
	}
	if user == nil {
		a.deps.Logger.Infow("用户未绑定账户，拒绝服务", "senderID", msg.SenderID)
		return "您尚未绑定账户，请先通过小程序登录", nil
	}

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

	// 6. 加载历史消息和活动任务
	history, activeTask := a.sessions.getSessionState(sessionKey)
	userMsg := tools.Message{Role: "user", Content: msg.Content}
	metrics := callMetrics{}
	beforeTask := cloneActiveTask(activeTask)

	conversationDecision := interpretConversation(msg.Content, activeTask)
	metrics.ConversationEvent = conversationDecision.Event
	switch conversationDecision.Event {
	case eventGreeting:
		reply := buildGreetingReply(uctx)
		applyConversationMetrics(&metrics, beforeTask, beforeTask, nil)
		metrics.DomainResult = domainIn
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	case eventCancel:
		a.sessions.clearActiveTask(sessionKey)
		reply := "已取消当前任务。如需继续，请重新告诉我。"
		applyConversationMetrics(&metrics, beforeTask, taskWithStatus(beforeTask, taskStatusCanceled), nil)
		metrics.DomainResult = domainIn
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	case eventTaskFollowUp:
		fill := fillTaskSlots(activeTask, msg.Content)
		nextTask := applySlotFillToTask(activeTask, fill)
		reply, followUpTools, err := a.respondForTaskState(ctx, uctx, sessionKey, nextTask)
		if err != nil {
			applyConversationMetrics(&metrics, beforeTask, nextTask, matchedSlotNames(fill))
			a.deps.Logger.Errorw("task follow-up 执行失败", "user", uctx.Name, "question", msg.Content, "err", err)
			a.writeCallLog(ctx, uctx, msg.Content, "", followUpTools, 0, startTime, "failed", err.Error(), metrics)
			return "系统错误，请稍后重试", nil
		}
		afterTask := nextTask
		if nextTask != nil && nextTask.Status == taskStatusReady {
			afterTask = taskWithStatus(nextTask, taskStatusCompleted)
		}
		applyConversationMetrics(&metrics, beforeTask, afterTask, matchedSlotNames(fill))
		metrics.DomainResult = domainIn
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		toolsCalled = append(toolsCalled, followUpTools...)
		a.writeCallLog(ctx, uctx, msg.Content, reply, toolsCalled, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	case eventUnknown:
		reply := buildUnknownFollowUpReply(activeTask)
		applyConversationMetrics(&metrics, beforeTask, beforeTask, nil)
		metrics.DomainResult = domainIn
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}
	if activeTask != nil {
		a.sessions.clearActiveTask(sessionKey)
	}
	applyConversationMetrics(&metrics, beforeTask, nil, nil)

	normalized := normalizeQuery(msg.Content)
	if hasHelpIntent(normalized) {
		reply := buildHelpReply(uctx)
		metrics.DomainResult = domainIn
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	domainHint := domainHintUnknown
	if a.domainGate != nil {
		domainHint = a.domainGate.Hint(msg.Content)
	}
	if domainHint == domainHintObviousOut {
		metrics.DomainResult = domainOut
		a.deps.Logger.Infow("明显站外问题，直接拒答", "user", uctx.Name, "question", msg.Content)
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeReject
		reply := outOfDomainReply
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		return reply, nil
	}
	metrics.DomainResult = domainIn

	taskCandidate := buildTaskFromRequest(msg.Content, uctx)
	retrievalResult := RetrievalResult{}
	if a.deps.Knowledge != nil && taskCandidate == nil {
		retrievalStart := time.Now()
		retrievalResult, err = a.retrieveKnowledge(ctx, uctx.TenantID, msg.Content)
		metrics.RetrievalDurationMs = elapsedMs(retrievalStart)
		if err != nil {
			a.deps.Logger.Errorw("知识预检失败", "user", uctx.Name, "question", msg.Content, "err", err)
			retrievalResult = RetrievalResult{}
		}
	}
	metrics.RetrievalHitCount = len(retrievalResult.Hits)
	metrics.RetrievalCandidateCount = retrievalResult.CandidateCount
	metrics.SourceRefs = append([]string(nil), retrievalResult.TopRefs...)
	metrics.RetrievalTopRefs = append([]string(nil), retrievalResult.TopRefs...)
	metrics.RetrievalScores = append([]int(nil), retrievalResult.TopScores...)
	metrics.RetrievalFilteredReason = retrievalResult.FilteredReason
	metrics.KnowledgeDocTypes = append([]string(nil), retrievalResult.KnowledgeDocTypes...)

	planDecision := plan(PlanInput{
		Question:          msg.Content,
		UserContext:       uctx,
		History:           history,
		ActiveTask:        nil,
		ConversationEvent: conversationDecision,
		DomainHint:        domainHint,
		Retrieval:         retrievalResult,
		TaskCandidate:     taskCandidate,
		HasLiveSignal:     hasLiveSignal(normalized),
		HasRuleSignal:     hasRuleSignal(normalized),
		HasActionIntent:   hasActionIntent(normalized),
		HasClarifyIntent:  hasClarifyIntent(normalized),
		HasHelpIntent:     hasHelpIntent(normalized),
	})

	switch planDecision.Kind {
	case planKindClarify:
		if planDecision.ActiveTask != nil {
			reply, taskTools, err := a.respondForTaskState(ctx, uctx, sessionKey, planDecision.ActiveTask)
			if err != nil {
				applyConversationMetrics(&metrics, beforeTask, planDecision.ActiveTask, nil)
				a.deps.Logger.Errorw("planner clarify 执行失败", "user", uctx.Name, "question", msg.Content, "err", err)
				a.writeCallLog(ctx, uctx, msg.Content, "", taskTools, 0, startTime, "failed", err.Error(), metrics)
				return "系统错误，请稍后重试", nil
			}
			applyConversationMetrics(&metrics, beforeTask, planDecision.ActiveTask, nil)
			metrics.QueryType = queryKindTool
			metrics.AnswerMode = answerModeToolFirst
			toolsCalled = append(toolsCalled, taskTools...)
			a.writeCallLog(ctx, uctx, msg.Content, reply, toolsCalled, 0, startTime, "success", "", metrics)
			a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
			return reply, nil
		}
		reply := buildPlannerClarifyReply(planDecision.ClarifyReason, nil)
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	case planKindTool:
		if planDecision.ActiveTask != nil {
			reply, taskTools, err := a.respondForTaskState(ctx, uctx, sessionKey, planDecision.ActiveTask)
			if err != nil {
				applyConversationMetrics(&metrics, beforeTask, planDecision.ActiveTask, nil)
				a.deps.Logger.Errorw("planner task 执行失败", "user", uctx.Name, "question", msg.Content, "err", err)
				a.writeCallLog(ctx, uctx, msg.Content, "", taskTools, 0, startTime, "failed", err.Error(), metrics)
				return "系统错误，请稍后重试", nil
			}
			afterTask := planDecision.ActiveTask
			if afterTask != nil && afterTask.Status == taskStatusReady {
				afterTask = taskWithStatus(afterTask, taskStatusCompleted)
			}
			applyConversationMetrics(&metrics, beforeTask, afterTask, nil)
			metrics.QueryType = queryKindTool
			metrics.AnswerMode = answerModeToolFirst
			toolsCalled = append(toolsCalled, taskTools...)
			a.writeCallLog(ctx, uctx, msg.Content, reply, toolsCalled, 0, startTime, "success", "", metrics)
			a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
			return reply, nil
		}
	}

	answerMode := answerModeToolFirst
	switch planDecision.Kind {
	case planKindRAG:
		answerMode = answerModeKnowledgeOnly
	case planKindMixed:
		answerMode = answerModeMixed
	case planKindTool:
		answerMode = answerModeToolFirst
	}
	metrics.QueryType = modeToQueryKind(answerMode)
	metrics.AnswerMode = answerMode

	if answerMode == answerModeKnowledgeOnly && classifyKnowledgeStrength(retrievalResult) != knowledgeStrengthStrong {
		reply := buildPlannerClarifyReply("weak_knowledge_match", nil)
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	// 8. 构建完整消息列表（system + retrieval prompt + history + 当前用户消息）
	systemMsg := tools.Message{Role: "system", Content: a.buildSystemPrompt(ctx, uctx)}
	messages := make([]tools.Message, 0, 3+len(history)+1)
	messages = append(messages, systemMsg)
	switch answerMode {
	case answerModeKnowledgeOnly:
		if prompt := buildKnowledgeOnlyPrompt(retrievalResult); prompt != "" {
			messages = append(messages, tools.Message{Role: "system", Content: prompt})
		}
	case answerModeMixed:
		if prompt := buildMixedAnswerPrompt(retrievalResult); prompt != "" {
			messages = append(messages, tools.Message{Role: "system", Content: prompt})
		}
	}
	messages = append(messages, history...)
	messages = append(messages, userMsg)

	// 9. 获取该用户可用的工具列表
	toolDefs := a.registry.ToToolDefs(uctx.UserRole)
	if answerMode == answerModeKnowledgeOnly {
		toolDefs = nil
	}

	// 10. ReAct Loop
	for round := 0; round < maxReactRounds; round++ {
		// 总结阶段（末尾为 tool 消息）LLM 需处理完整工具结果，输入 token 较多，给予更长超时时间
		// 工具调用阶段使用 50s，总结阶段使用 90s
		llmTimeout := 50 * time.Second
		if len(messages) > 0 && messages[len(messages)-1].Role == "tool" {
			llmTimeout = 90 * time.Second
		}
		llmCtx, llmCancel := context.WithTimeout(context.Background(), llmTimeout)
		llmStart := time.Now()
		resp, err := a.llmClient.Chat(llmCtx, messages, toolDefs)
		metrics.LLMDurationMs += elapsedMs(llmStart)
		llmCancel()
		if err != nil {
			a.deps.Logger.Errorw("LLM 调用失败", "round", round, "err", err)
			a.writeCallLog(ctx, uctx, msg.Content, "", toolsCalled, round, startTime, "failed", err.Error(), metrics)
			return "AI 服务暂时不可用，请稍后重试", nil
		}

		// 无工具调用 → 返回最终回复
		if len(resp.ToolCalls) == 0 {
			reply := resp.Content
			if reply == "" {
				reply = "抱歉，我无法理解您的问题，请换个方式描述"
			}
			a.deps.Logger.Infow("回复完成", "user", uctx.Name, "rounds", round+1, "reply", reply)
			a.writeCallLog(ctx, uctx, msg.Content, reply, toolsCalled, round+1, startTime, "success", "", metrics)
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
	a.writeCallLog(ctx, uctx, msg.Content, "", toolsCalled, maxReactRounds, startTime, "failed", "超出最大轮数", metrics)
	return "处理轮次过多，请简化您的问题后重试", nil
}

func buildGreetingReply(uctx *tools.UserContext) string {
	if uctx != nil && uctx.UserRole >= 1 {
		return "你好，我是课表助手。你可以直接让我查课表、考勤、请假；如果需要，也可以继续处理补签、统计和群订阅。"
	}
	return "你好，我是课表助手。你可以直接让我查课表、考勤或请假相关信息。"
}

func (a *Agent) respondForTaskState(ctx context.Context, uctx *tools.UserContext, sessionKey string, task *ActiveTask) (string, []string, error) {
	if task == nil {
		a.sessions.clearActiveTask(sessionKey)
		return "请再具体说明你要查询或操作的内容。", nil, nil
	}

	if task.Status == taskStatusReady {
		reply, toolsCalled, err := a.executeReadyTask(ctx, uctx, task)
		if err != nil {
			return "", toolsCalled, err
		}
		a.sessions.clearActiveTask(sessionKey)
		return reply, toolsCalled, nil
	}

	a.sessions.setActiveTask(sessionKey, task)

	if task.Type == "subscribe_attendance_push" && containsAnySlot(task.MissingSlots(), "dept_names") {
		toolResult, err := a.registry.Dispatch(ctx, uctx, "list_departments", json.RawMessage(`{}`))
		if err != nil {
			return "", []string{"list_departments"}, err
		}
		reply, err := buildClarifyReply(clarifyPlan{
			ToolName:       "list_departments",
			ToolArguments:  `{}`,
			FollowUpPrompt: "我先列出当前可选部门。请告诉我需要订阅哪些部门。",
		}, toolResult)
		return reply, []string{"list_departments"}, err
	}

	return buildTaskClarifyReply(task), nil, nil
}

func (a *Agent) executeReadyTask(ctx context.Context, uctx *tools.UserContext, task *ActiveTask) (string, []string, error) {
	if task == nil {
		return "", nil, nil
	}

	switch task.Type {
	case "query_subscription_status":
		toolResult, err := a.registry.Dispatch(ctx, uctx, "query_subscription_status", json.RawMessage(`{}`))
		if err != nil {
			return "", []string{"query_subscription_status"}, err
		}
		reply, err := buildClarifyReply(clarifyPlan{ToolName: "query_subscription_status", ToolArguments: `{}`}, toolResult)
		return reply, []string{"query_subscription_status"}, err
	case "subscribe_attendance_push":
		payload := map[string]any{}
		if task.FilledSlots["scope"] == "department" {
			payload["dept_names"] = splitTaskValues(task.FilledSlots["dept_names"])
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", []string{"subscribe_attendance_push"}, err
		}
		toolResult, err := a.registry.Dispatch(ctx, uctx, "subscribe_attendance_push", raw)
		if err != nil {
			return "", []string{"subscribe_attendance_push"}, err
		}
		reply, err := renderToolMessage(toolResult)
		return reply, []string{"subscribe_attendance_push"}, err
	case "sign_for_user":
		section, err := strconv.Atoi(task.FilledSlots["section"])
		if err != nil {
			return "", nil, err
		}
		payload := map[string]any{
			"user_name": task.FilledSlots["user_name"],
			"date":      materializeTaskDate(task.FilledSlots["date"]),
			"section":   section,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", []string{"sign_for_user"}, err
		}
		toolResult, err := a.registry.Dispatch(ctx, uctx, "sign_for_user", raw)
		if err != nil {
			return "", []string{"sign_for_user"}, err
		}
		reply, err := renderToolMessage(toolResult)
		return reply, []string{"sign_for_user"}, err
	default:
		return "", nil, nil
	}
}

func renderToolMessage(toolResult string) (string, error) {
	if toolErr := extractToolError(toolResult); toolErr != "" {
		return toolErr, nil
	}

	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(toolResult), &payload); err != nil {
		return toolResult, nil
	}
	if strings.TrimSpace(payload.Message) != "" {
		return strings.TrimSpace(payload.Message), nil
	}
	if strings.TrimSpace(toolResult) == "" {
		return "操作已完成。", nil
	}
	return toolResult, nil
}

func materializeTaskDate(value string) string {
	switch value {
	case "today":
		return time.Now().Format("2006-01-02")
	case "yesterday":
		return time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	case "tomorrow":
		return time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	default:
		return value
	}
}

func splitTaskValues(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == '、' || r == ',' || r == '，'
	})
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		values = append(values, name)
	}
	if len(values) == 0 {
		return []string{trimmed}
	}
	return values
}

// writeCallLog 异步写入调用记录，不阻塞对话响应
func (a *Agent) writeCallLog(ctx context.Context, uctx *tools.UserContext, question, reply string, toolsCalled []string, rounds int, startTime time.Time, status, errMsg string, metrics callMetrics) {
	if a.deps.CallLog == nil {
		return
	}
	log := tools.CallLog{
		TenantID:                uctx.TenantID,
		UserID:                  uctx.UserID,
		UserName:                uctx.Name,
		ConvType:                uctx.ConversationType,
		QueryType:               string(metrics.QueryType),
		ConversationEvent:       string(metrics.ConversationEvent),
		ActiveTaskType:          metrics.ActiveTaskType,
		TaskStatusBefore:        metrics.TaskStatusBefore,
		TaskStatusAfter:         metrics.TaskStatusAfter,
		DomainResult:            string(metrics.DomainResult),
		AnswerMode:              string(metrics.AnswerMode),
		Question:                question,
		ToolsCalled:             toolsCalled,
		ToolCallCount:           len(toolsCalled),
		Reply:                   reply,
		SourceRefs:              append([]string(nil), metrics.SourceRefs...),
		RetrievalHitCount:       metrics.RetrievalHitCount,
		RetrievalCandidateCount: metrics.RetrievalCandidateCount,
		RetrievalTopRefs:        append([]string(nil), metrics.RetrievalTopRefs...),
		RetrievalScores:         append([]int(nil), metrics.RetrievalScores...),
		FollowUpMatchedSlots:    append([]string(nil), metrics.FollowUpMatchedSlots...),
		RetrievalFilteredReason: metrics.RetrievalFilteredReason,
		KnowledgeDocTypes:       append([]string(nil), metrics.KnowledgeDocTypes...),
		RetrievalDurationMs:     metrics.RetrievalDurationMs,
		LLMDurationMs:           metrics.LLMDurationMs,
		Rounds:                  rounds,
		DurationMs:              elapsedMs(startTime),
		Status:                  status,
		ErrorMsg:                errMsg,
	}
	go a.deps.CallLog.Write(context.Background(), log)
}

func applyConversationMetrics(metrics *callMetrics, before, after *ActiveTask, matchedSlots []string) {
	if metrics == nil {
		return
	}
	metrics.ActiveTaskType = taskTypeForLog(after)
	if metrics.ActiveTaskType == "" {
		metrics.ActiveTaskType = taskTypeForLog(before)
	}
	metrics.TaskStatusBefore = taskStatusForLog(before)
	metrics.TaskStatusAfter = taskStatusForLog(after)
	metrics.FollowUpMatchedSlots = append([]string(nil), matchedSlots...)
}

func taskTypeForLog(task *ActiveTask) string {
	if task == nil {
		return ""
	}
	return task.Type
}

func taskStatusForLog(task *ActiveTask) string {
	if task == nil {
		return ""
	}
	return string(task.Status)
}

func taskWithStatus(task *ActiveTask, status taskStatus) *ActiveTask {
	cloned := cloneActiveTask(task)
	if cloned == nil {
		return nil
	}
	cloned.Status = status
	return cloned
}

func matchedSlotNames(fill slotFillResult) []string {
	if len(fill.Filled) == 0 {
		return nil
	}
	names := make([]string, 0, len(fill.Filled))
	for name := range fill.Filled {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

	periodsInfo := ""
	if periods, _, err := a.deps.SchedulePeriod.GetScheduleInfo(ctx); err == nil && len(periods) > 0 {
		periodsInfo = "\n- 节次时间（直接使用，无需调用 query_schedule_info）："
		for i, p := range periods {
			periodsInfo += fmt.Sprintf("\n  第%d节（%s）：%s-%s", i+1, p.Name, p.Start, p.End)
		}
	}

	convInfo := "\n- 当前对话：单聊（钉钉）"
	if uctx.ConversationType == "2" {
		convInfo = fmt.Sprintf("\n- 当前对话：群聊（%s）", uctx.ConversationTitle)
	}

	now := time.Now()
	weekdays := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

	return fmt.Sprintf(`你是「课表助手」，服务于学校课表与考勤管理系统。

当前信息：
- 用户：%s（%s）
- 日期：%s（%s）%s%s%s

职责范围（仅限以下内容）：
- 查询课程安排及相关人员信息（如：某人的课表、某时间段谁有课/无课）
- 查询考勤记录与出勤情况
- 查询请假信息
- 管理员：考勤统计、签到操作、群订阅管理

约束：
- 实时业务数据只能使用提供的工具获取；规则说明只能基于已检索知识片段，严禁编造、推测或补充不存在的信息
- 如果当前工具或知识都无法完成用户请求，直接回复"抱歉，我没有相应的功能来完成这个操作"，不要尝试用其他方式绕过或模拟
- 如果工具返回空结果或无数据，如实告知"未查询到相关数据"，不要猜测或编造内容
- 如果工具返回错误，如实告知用户，不要尝试用假设数据代替
- 回复用中文，简洁明了，避免冗余解释
- 工具返回的是 JSON 数据，请用自然语言组织回复
- 列出人员名单时，必须完整逐一列出工具返回的所有姓名，禁止使用"等"字省略
- 如果用户的问题与课程人员信息、考勤、请假无关，请礼貌拒绝并说明："抱歉，我只能回答课程人员、考勤及请假相关的问题，无法回答其他内容。"`,
		uctx.Name, roleText,
		now.Format("2006-01-02"), weekdays[now.Weekday()],
		weekInfo, periodsInfo, convInfo,
	)
}

// elapsedMs 返回至少为 1 的毫秒耗时，避免极短请求被记录为 0。
func elapsedMs(start time.Time) int64 {
	durationMs := time.Since(start).Milliseconds()
	if durationMs <= 0 {
		return 1
	}
	return durationMs
}

// modeToQueryKind 在 answer mode 过渡期内维持旧 query_type 口径。
func modeToQueryKind(mode answerMode) queryKind {
	switch mode {
	case answerModeKnowledgeOnly:
		return queryKindRAG
	case answerModeMixed:
		return queryKindMixed
	default:
		return queryKindTool
	}
}

func (a *Agent) handleClarifyIntent(ctx context.Context, uctx *tools.UserContext, question string) (string, []string, error) {
	plan := buildClarifyPlan(question, uctx)
	if !plan.NeedsToolLookup {
		return plan.FollowUpPrompt, nil, nil
	}

	toolArgs := plan.ToolArguments
	if strings.TrimSpace(toolArgs) == "" {
		toolArgs = "{}"
	}

	result, err := a.registry.Dispatch(ctx, uctx, plan.ToolName, json.RawMessage(toolArgs))
	if err != nil {
		return "", []string{plan.ToolName}, err
	}

	reply, err := buildClarifyReply(plan, result)
	return reply, []string{plan.ToolName}, err
}

func buildClarifyReply(plan clarifyPlan, toolResult string) (string, error) {
	if toolErr := extractToolError(toolResult); toolErr != "" {
		return toolErr, nil
	}

	switch plan.ToolName {
	case "list_departments":
		var payload struct {
			Depts []struct {
				Name string `json:"name"`
			} `json:"depts"`
		}
		if err := json.Unmarshal([]byte(toolResult), &payload); err != nil {
			return "", err
		}

		names := make([]string, 0, len(payload.Depts))
		for _, dept := range payload.Depts {
			name := strings.TrimSpace(dept.Name)
			if name == "" {
				continue
			}
			names = append(names, name)
		}
		if len(names) == 0 {
			return "当前暂无可选部门。", nil
		}

		reply := fmt.Sprintf("当前可选部门有：%s。", strings.Join(names, "、"))
		if plan.FollowUpPrompt != "" {
			reply += plan.FollowUpPrompt
		}
		return reply, nil
	case "query_subscription_status":
		var info *tools.GroupSubInfo
		if err := json.Unmarshal([]byte(toolResult), &info); err != nil {
			return "", err
		}
		if info == nil || !info.Subscribed {
			return "当前群还没有订阅考勤推送。", nil
		}

		reply := "当前群已订阅考勤推送。"
		if len(info.DeptIDs) > 0 {
			reply += "目前是按指定部门范围推送。"
		}
		return reply, nil
	default:
		return plan.FollowUpPrompt, nil
	}
}

func extractToolError(toolResult string) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(toolResult), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Error)
}

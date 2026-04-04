package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
	DomainResult            domainResult
	AnswerMode              answerMode
	LLMDurationMs           int64
	RetrievalDurationMs     int64
	RetrievalHitCount       int
	RetrievalCandidateCount int
	SourceRefs              []string
	RetrievalTopRefs        []string
	RetrievalScores         []int
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

	// 6. 加载历史消息
	history := a.sessions.getMessages(sessionKey)
	userMsg := tools.Message{Role: "user", Content: msg.Content}
	metrics := callMetrics{}

	if hasGreetingIntent(msg.Content) {
		reply := buildGreetingReply(uctx)
		metrics.DomainResult = domainIn
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	// 7. 先做领域门禁，站外问题直接拒答，不进入检索与 LLM。
	domainResult := domainOut
	if a.domainGate != nil {
		domainResult = a.domainGate.Check(msg.Content)
	}
	continueClarify := domainResult != domainIn && isClarificationFollowUp(msg.Content, history)
	if continueClarify {
		domainResult = domainIn
	}
	metrics.DomainResult = domainResult
	if domainResult != domainIn {
		a.deps.Logger.Infow("站外问题，直接拒答", "user", uctx.Name, "question", msg.Content)
		metrics.QueryType = modeToQueryKind(answerModeReject)
		metrics.AnswerMode = answerModeReject
		reply := outOfDomainReply
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		return reply, nil
	}

	initialDecision := intentDecision{}
	if continueClarify {
		initialDecision = intentDecision{Intent: intentAction, AnswerMode: answerModeToolFirst}
	} else {
		initialDecision = a.router.DecideIntent(msg.Content, domainResult, RetrievalResult{})
	}
	switch initialDecision.Intent {
	case intentHelp:
		reply := buildHelpReply(uctx)
		metrics.QueryType = modeToQueryKind(initialDecision.AnswerMode)
		metrics.AnswerMode = initialDecision.AnswerMode
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	case intentClarify:
		reply, clarifyTools, err := a.handleClarifyIntent(ctx, uctx, msg.Content)
		if err != nil {
			a.deps.Logger.Errorw("clarify 执行失败", "user", uctx.Name, "question", msg.Content, "err", err)
			a.writeCallLog(ctx, uctx, msg.Content, "", clarifyTools, 0, startTime, "failed", err.Error(), metrics)
			return "系统错误，请稍后重试", nil
		}
		if reply == "" {
			reply = "请再具体说明你要查询或操作的内容。"
		}
		toolsCalled = append(toolsCalled, clarifyTools...)
		metrics.QueryType = modeToQueryKind(initialDecision.AnswerMode)
		metrics.AnswerMode = initialDecision.AnswerMode
		a.writeCallLog(ctx, uctx, msg.Content, reply, toolsCalled, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	retrievalResult := RetrievalResult{}
	shouldRetrieveKnowledge := initialDecision.Intent == intentRule || initialDecision.Intent == intentMixed
	if shouldRetrieveKnowledge && a.deps.Knowledge != nil {
		retrievalStart := time.Now()
		retrievalResult, err = a.retrieveKnowledge(ctx, uctx.TenantID, msg.Content)
		metrics.RetrievalDurationMs = elapsedMs(retrievalStart)
		if err != nil {
			a.deps.Logger.Errorw("知识检索失败", "user", uctx.Name, "question", msg.Content, "err", err)
			if initialDecision.Intent != intentMixed {
				return "规则知识检索暂时不可用，请稍后重试", nil
			}
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
	decision := initialDecision
	if shouldRetrieveKnowledge {
		decision = a.router.DecideIntent(msg.Content, domainResult, retrievalResult)
	}
	answerMode := decision.AnswerMode
	metrics.QueryType = modeToQueryKind(answerMode)
	metrics.AnswerMode = answerMode

	if answerMode == answerModeReject {
		a.deps.Logger.Infow("领域内问题无有效知识命中，直接拒答", "user", uctx.Name, "question", msg.Content)
		a.writeCallLog(ctx, uctx, msg.Content, noKnowledgeReply, nil, 0, startTime, "success", "", metrics)
		return noKnowledgeReply, nil
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

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"schedule_server/internal/agent/tools"
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
	LLMBaseURL            string
	LLMAPIKey             string
	LLMModel              string
	RouterLLMBaseURL      string
	RouterLLMAPIKey       string
	RouterLLMModel        string
	RouteMode             string
	ProtocolMode          string
	IntentCompiler        IntentCompiler
	IntentCompilerTimeout time.Duration
	WorkflowStore         WorkflowStore

	Schedule                SchedulePort
	Attendance              AttendancePort
	AttendanceUserDayStatus AttendanceUserDayStatusPort
	Leave                   LeavePort
	User                    UserPort
	Semester                SemesterPort
	SchedulePeriod          SchedulePeriodPort
	RestDay                 RestDayPort
	GroupSub                GroupSubPort
	Dept                    DeptPort
	Knowledge               KnowledgePort
	CallLog                 CallLogPort
	AttendanceStats         AttendanceStatsPort
	UserCross               UserCrossPort
	Tenant                  TenantPort

	Logger *zap.SugaredLogger
}

// Agent AI 助手
type Agent struct {
	deps           Deps
	llmClient      *LLMClient
	routerClient   *LLMClient
	routeMode      string
	protocolMode   ProtocolMode
	intentCompiler IntentCompiler
	registry       *tools.Registry
	runtime        *taskRuntime
	taskCatalog    *taskCatalog
	sessions       *sessionManager
	workflowStore  WorkflowStore
	limiter        *rateLimiter
	logWriter      *callLogWriter
	stopCleanup    chan struct{}
	once           sync.Once
}

// NewAgent 创建 Agent
func NewAgent(deps Deps) *Agent {
	routeMode := strings.TrimSpace(deps.RouteMode)
	if routeMode == "" {
		routeMode = string(RouteModeLive)
	}
	protocolMode := normalizeProtocolMode(strings.TrimSpace(deps.ProtocolMode))

	mainClient := NewLLMClient(deps.LLMBaseURL, deps.LLMAPIKey, deps.LLMModel)
	routerClient := mainClient
	if strings.TrimSpace(deps.RouterLLMBaseURL) != "" || strings.TrimSpace(deps.RouterLLMModel) != "" {
		routerBaseURL := strings.TrimSpace(deps.RouterLLMBaseURL)
		if routerBaseURL == "" {
			routerBaseURL = deps.LLMBaseURL
		}
		routerAPIKey := strings.TrimSpace(deps.RouterLLMAPIKey)
		if routerAPIKey == "" {
			routerAPIKey = deps.LLMAPIKey
		}
		routerModel := strings.TrimSpace(deps.RouterLLMModel)
		if routerModel == "" {
			routerModel = deps.LLMModel
		}
		routerClient = NewLLMClient(routerBaseURL, routerAPIKey, routerModel)
	}
	intentCompiler := deps.IntentCompiler
	if intentCompiler == nil && protocolMode == ProtocolModeLive && protocolLLMCompilerAvailable(mainClient) {
		intentCompiler = newLLMIntentCompiler(mainClient, intentCompilerOptions{Timeout: deps.IntentCompilerTimeout})
	}

	workflowStore := deps.WorkflowStore
	if workflowStore == nil {
		workflowStore = newMemoryWorkflowStore(nil)
	}

	a := &Agent{
		deps:           deps,
		llmClient:      mainClient,
		routerClient:   routerClient,
		routeMode:      routeMode,
		protocolMode:   protocolMode,
		intentCompiler: intentCompiler,
		workflowStore:  workflowStore,
		sessions:       newSessionManager(workflowStore),
		limiter:        newRateLimiter(),
		stopCleanup:    make(chan struct{}),
	}

	handlers := []TaskHandler{
		newSubscribeTaskHandler(),
		newUnsubscribeTaskHandler(),
		newSubscriptionStatusTaskHandler(),
		newManualSignTaskHandler(),
	}
	a.runtime = newTaskRuntime(handlers)
	a.taskCatalog = newTaskCatalog(a.runtime)

	// 注册工具
	a.registry = tools.NewRegistry()
	tools.RegisterScheduleTools(a.registry, deps.Schedule, deps.User, deps.Semester, deps.SchedulePeriod, deps.Dept)
	tools.RegisterAttendanceTools(a.registry, deps.Attendance, deps.Semester, deps.RestDay, deps.Leave, deps.Dept)
	tools.RegisterAdminTools(a.registry, deps.Attendance, deps.User, deps.GroupSub, deps.Dept)
	tools.RegisterAnalyticsTools(a.registry, deps.AttendanceStats, deps.UserCross, deps.Dept)

	// 启动 Session 过期清理
	go a.cleanupLoop()

	// 启动异步日志写入 worker
	if deps.CallLog != nil {
		a.logWriter = newCallLogWriter(deps.CallLog, deps.Logger)
	}

	return a
}

// Stop 停止 Agent（清理 goroutine），在优雅关闭时调用
func (a *Agent) Stop() {
	a.once.Do(func() {
		close(a.stopCleanup)
		if a.logWriter != nil {
			a.logWriter.Stop()
		}
	})
}

// cleanupLoop periodically purges expired session and rate-limit state.
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

	// 1. 认证：租户解析 + 用户查找 + 限流
	auth, rejectReply := a.resolveChatAuth(ctx, msg)
	if rejectReply != "" {
		return rejectReply, nil
	}
	ctx, uctx, sessionKey := auth.ctx, auth.uctx, auth.sessionKey

	a.deps.Logger.Infow("收到消息",
		"user", uctx.Name,
		"tenantID", uctx.TenantID,
		"convType", msg.ConversationType,
		"content", msg.Content,
	)

	startTime := time.Now()
	userMsg := tools.Message{Role: "user", Content: msg.Content}
	metrics := callMetrics{Proto: protocolMetrics{Mode: string(a.protocolMode)}}

	primaryChain := a.primaryDecisionChain()

	// 2. protocol_live 作为独占主链。shadow 模式只记录协议草稿，不抢占 route / legacy 主流程。
	if primaryChain == decisionChainProtocol {
		workflowKey := workflowKeyFromUserContext(uctx)
		var workflowBefore *WorkflowSnapshot
		var outcome protocolLiveOutcome

		activeWorkflow, workflowErr := a.workflowStore.Load(ctx, workflowKey)
		if workflowErr != nil {
			a.deps.Logger.Warnw("读取 workflow 失败", "tenantID", workflowKey.TenantID, "conversationID", workflowKey.ConversationID, "actorUserID", workflowKey.ActorUserID, "err", workflowErr)
			outcome = workflowStoreFailureOutcome()
		} else {
			workflowBefore = cloneWorkflowSnapshot(activeWorkflow)
			if activeWorkflow != nil {
				metrics.Wf.IDBefore = activeWorkflow.ID
			}
			outcome = a.protocolLivePipeline().Handle(ctx, protocolLiveInput{
				Message:        msg.Content,
				User:           uctx,
				ActiveWorkflow: activeWorkflow,
			})
			if persistErr := a.persistProtocolLiveWorkflowOutcome(ctx, workflowKey, outcome); persistErr != nil {
				a.deps.Logger.Warnw("更新 workflow 失败", "tenantID", workflowKey.TenantID, "conversationID", workflowKey.ConversationID, "actorUserID", workflowKey.ActorUserID, "err", persistErr)
				outcome = workflowStoreFailureOutcome()
			}
		}
		if a.sessions != nil {
			a.sessions.bindWorkflowKey(sessionKey, workflowKey)
		}
		a.applyProtocolLiveOutcomeMetrics(&metrics, outcome, workflowBefore)
		reply := renderProtocolResponse(outcome.Response)
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	if a.protocolMode == ProtocolModeShadow {
		_, activeWorkflow := a.sessions.getWorkflowState(sessionKey)
		protocolWorkflow := protocolWorkflowContextFromWorkflowSnapshot(activeWorkflow)
		protocolDraft := compileProtocol(protocolInput{
			Message:        msg.Content,
			ActiveWorkflow: protocolWorkflow,
		})
		protocolValidation := validateProtocol(protocolDraft, protocolWorkflow)
		applyProtocolMetrics(&metrics, protocolDraft, protocolValidation)
		if activeWorkflow != nil {
			metrics.Wf.IDBefore = activeWorkflow.ID
		}
	}

	// 3. 语义路由器为主决策入口。
	if primaryChain == decisionChainRoute {
		metrics.Proto.LegacyCalled = true
		return a.chatWithSemanticRouter(ctx, uctx, sessionKey, msg, userMsg, startTime, &metrics)
	}

	// 4. legacy 路径（routeMode=shadow 或 off）
	metrics.Proto.LegacyCalled = true
	return a.chatLegacy(ctx, uctx, sessionKey, msg, userMsg, startTime, &metrics)
}

type decisionChain string

const (
	decisionChainProtocol decisionChain = "protocol"
	decisionChainRoute    decisionChain = "route"
	decisionChainLegacy   decisionChain = "legacy"
)

// primaryDecisionChain 为当前轮次选择唯一的顶层决策主链。
func (a *Agent) primaryDecisionChain() decisionChain {
	if a.protocolMode == ProtocolModeLive {
		return decisionChainProtocol
	}
	if a.routeMode == string(RouteModeLive) {
		return decisionChainRoute
	}
	return decisionChainLegacy
}

func protocolLLMCompilerAvailable(client *LLMClient) bool {
	if client == nil {
		return false
	}
	baseURL := strings.TrimSpace(client.baseURL)
	if baseURL == "" || strings.TrimSpace(client.model) == "" {
		return false
	}
	trimmed := strings.TrimRight(baseURL, "/")
	parsed, err := url.Parse(trimmed)
	if err == nil {
		if parsed.Port() == "0" {
			return false
		}
		if parsed.Host != "" {
			return !strings.HasSuffix(parsed.Host, ":0")
		}
	}
	return !strings.HasSuffix(trimmed, ":0")
}

// legacy-only: chatWithSemanticRouter is the old semantic-router main path used outside protocol_live.
// chatWithSemanticRouter 使用语义路由器作为唯一决策入口的处理路径。
func (a *Agent) chatWithSemanticRouter(
	ctx context.Context,
	uctx *tools.UserContext,
	sessionKey string,
	msg *dingtalk.ChatMessage,
	userMsg tools.Message,
	startTime time.Time,
	metrics *callMetrics,
) (string, error) {
	history, _ := a.sessions.getSessionState(sessionKey)
	_, routeTask := a.sessions.getTaskState(sessionKey)

	var routeDecision RouteDecision
	if shortCircuit, ok := detectShortCircuitRoute(msg.Content, uctx, routeTask); ok {
		routeDecision = shortCircuit
	} else {
		routeContext := buildRouteContext(msg.Content, uctx, history, routeTask)
		routeStart := time.Now()
		routeDecision = newSemanticRouter(a.routerClient).Route(ctx, routeContext)
		metrics.Route.RouterLatencyMs = elapsedMs(routeStart)
	}

	metrics.Route.Kind = string(routeDecision.Kind)
	metrics.Route.Confidence = routeDecision.Confidence
	metrics.Route.ReasonCode = routeDecision.ReasonCode
	metrics.Route.Source = string(routeDecision.RouteSource)
	metrics.Route.ClarifyCode = routeDecision.ClarifyCode
	metrics.Route.SoftNoticeCode = routeDecision.SoftNoticeCode

	beforeTask := cloneActiveTask(activeTaskFromTaskInstance(routeTask))
	if handled, reply, err := a.tryHandleRoutePrimary(ctx, uctx, sessionKey, msg.Content, history, userMsg, startTime, beforeTask, metrics, routeDecision); handled {
		return reply, err
	}

	// 兜底：语义路由器返回了 tryHandleRoutePrimary 无法处理的 kind
	// 先尝试规则匹配降级，再回退到 clarify
	if kind := fallbackQueryKind(msg.Content); kind != "" {
		fallback := RouteDecision{
			Kind:        kind,
			ReasonCode:  "router_unhandled_kind",
			RouteSource: RouteSourceFallback,
		}
		metrics.Route.Kind = string(fallback.Kind)
		metrics.Route.ReasonCode = fallback.ReasonCode
		metrics.Route.Source = string(fallback.RouteSource)
		if handled, reply, err := a.tryHandleRoutePrimary(ctx, uctx, sessionKey, msg.Content, history, userMsg, startTime, beforeTask, metrics, fallback); handled {
			return reply, err
		}
	}

	clarifyFallback := RouteDecision{
		Kind:        RouteClarify,
		ReasonCode:  "router_unhandled_kind",
		ClarifyCode: "ambiguous_intent",
		RouteSource: RouteSourceFallback,
	}
	metrics.Route.Kind = string(clarifyFallback.Kind)
	metrics.Route.ReasonCode = clarifyFallback.ReasonCode
	metrics.Route.Source = string(clarifyFallback.RouteSource)
	metrics.Route.ClarifyCode = clarifyFallback.ClarifyCode
	if handled, reply, err := a.tryHandleRoutePrimary(ctx, uctx, sessionKey, msg.Content, history, userMsg, startTime, beforeTask, metrics, clarifyFallback); handled {
		return reply, err
	}

	return "请再具体说明你要查询或操作的内容。", nil
}

// legacy-only: chatLegacy uses planner + ReAct interpreter and must not run under protocol_live.
// chatLegacy 使用 planner + interpreter 的遗留处理路径。
func (a *Agent) chatLegacy(
	ctx context.Context,
	uctx *tools.UserContext,
	sessionKey string,
	msg *dingtalk.ChatMessage,
	userMsg tools.Message,
	startTime time.Time,
	metrics *callMetrics,
) (string, error) {
	history, activeTask := a.sessions.getSessionState(sessionKey)
	normalized := normalizeQuery(msg.Content)
	beforeTask := cloneActiveTask(activeTask)
	var toolsCalled []string

	var routeDecision RouteDecision
	if a.routeMode != string(RouteModeOff) {
		_, routeTask := a.sessions.getTaskState(sessionKey)
		if shortCircuit, ok := detectShortCircuitRoute(msg.Content, uctx, routeTask); ok {
			routeDecision = shortCircuit
		} else {
			routeContext := buildRouteContext(msg.Content, uctx, history, routeTask)
			shadowRouteStart := time.Now()
			routeDecision = newSemanticRouter(a.routerClient).Route(ctx, routeContext)
			metrics.Route.RouterLatencyMs = elapsedMs(shadowRouteStart)
		}
		metrics.Shadow.RouteKind = string(routeDecision.Kind)
	}

	shadowTask := plannerTaskFromLegacyTask(sessionKey, activeTask)
	shadowDecision := planConversation(PlannerInput{
		Message:     msg.Content,
		ActiveTask:  shadowTask,
		UserContext: uctx,
	})
	applyShadowPlannerMetrics(metrics, shadowDecision, shadowTask)

	if hasGreetingIntent(normalized) {
		recordLegacyPlannerAction(metrics, plannerActionSocialRefuse)
		reply := buildGreetingReply(uctx)
		applyConversationMetrics(metrics, beforeTask, beforeTask, nil)
		metrics.ConversationEvent = eventGreeting
		metrics.DomainResult = domainIn
		metrics.PlanKind = planKindTool
		metrics.Planner.Reason = "greeting"
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	if handled, reply, err := a.tryHandlePlannerPrimary(ctx, uctx, sessionKey, msg.Content, userMsg, startTime, beforeTask, metrics, shadowDecision); handled {
		return reply, err
	}

	conversationDecision := interpretConversation(msg.Content, activeTask)
	metrics.ConversationEvent = conversationDecision.Event
	if handled, reply, err := a.handleConversationEvent(ctx, uctx, sessionKey, msg, userMsg, startTime, metrics, activeTask, beforeTask, conversationDecision, toolsCalled); handled {
		return reply, err
	}
	if activeTask != nil {
		a.sessions.clearActiveTask(sessionKey)
	}
	applyConversationMetrics(metrics, beforeTask, nil, nil)

	if hasHelpIntent(normalized) {
		recordLegacyPlannerAction(metrics, plannerActionSocialRefuse)
		reply := buildHelpReply(uctx)
		metrics.DomainResult = domainIn
		metrics.PlanKind = planKindTool
		metrics.Planner.Reason = "help_intent"
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}
	if shadowDecision.Action == plannerActionSocialRefuse {
		recordLegacyPlannerAction(metrics, plannerActionSocialRefuse)
		reply := composePlannerReply(shadowDecision, nil, nil)
		metrics.DomainResult = domainIn
		metrics.PlanKind = planKindClarify
		metrics.KnowledgeStrength = knowledgeStrengthNone
		metrics.Planner.Reason = shadowDecision.Reason
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeReject
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	metrics.DomainResult = domainIn

	return a.handleWithKnowledgeAndPlan(ctx, uctx, sessionKey, msg, userMsg, startTime, metrics, history, normalized, conversationDecision, beforeTask)
}

// buildGreetingReply builds greeting reply.
func buildGreetingReply(uctx *tools.UserContext) string {
	role := 0
	convType := "1"
	if uctx != nil {
		role = uctx.UserRole
		convType = uctx.ConversationType
	}

	snapshot := capabilitySnapshot(capabilityContext{
		UserRole:         role,
		ConversationType: convType,
	})
	operations := make(map[string]bool, len(snapshot))
	for _, entry := range snapshot {
		operations[entry.Operation] = true
	}

	var capabilities []string
	if operations["schedule.describe_capability"] {
		capabilities = append(capabilities, "课表")
	}
	if operations["attendance.describe_capability"] {
		capabilities = append(capabilities, "考勤状态")
	}
	if operations["system.describe_capability"] {
		capabilities = append(capabilities, "规则")
	}

	var b strings.Builder
	b.WriteString("你好，我是课表与考勤助手。")
	if len(capabilities) > 0 {
		b.WriteString("你可以让我查询")
		b.WriteString(strings.Join(capabilities, "、"))
		b.WriteString("。")
	}
	if operations["subscription.describe_capability"] {
		b.WriteString("在群聊中还可以查询考勤订阅。")
	}
	if operations["subscription.start"] && operations["subscription.cancel"] {
		b.WriteString("管理员可以开启或取消订阅。")
	}
	if operations["manual_sign.describe_capability"] {
		b.WriteString("补签目前只提供能力说明，不在聊天中直接执行。")
	}
	return b.String()
}

// respondForTaskState handles replies while a legacy active task is still open.
func (a *Agent) respondForTaskState(ctx context.Context, uctx *tools.UserContext, sessionKey string, task *ActiveTask) (string, []string, *ActiveTask, error) {
	if task == nil {
		a.sessions.clearActiveTask(sessionKey)
		return "请再具体说明你要查询或操作的内容。", nil, nil, nil
	}

	if reply, toolsCalled, nextTask, handled, err := a.respondForRuntimeTaskState(ctx, uctx, sessionKey, task); handled {
		return reply, toolsCalled, nextTask, err
	}

	if task.Status == taskStatusReady {
		reply, toolsCalled, nextTask, err := a.executeReadyTask(ctx, uctx, task)
		if err != nil {
			return "", toolsCalled, nextTask, err
		}
		if nextTask != nil {
			a.sessions.setActiveTask(sessionKey, nextTask)
			return reply, toolsCalled, cloneActiveTask(nextTask), nil
		}
		a.sessions.clearActiveTask(sessionKey)
		return reply, toolsCalled, nil, nil
	}

	a.sessions.setActiveTask(sessionKey, task)

	if task.Type == "subscribe_attendance_push" && containsAnySlot(task.MissingSlots(), "dept_names") {
		toolResult, err := a.registry.Dispatch(ctx, uctx, "list_departments", json.RawMessage(`{}`))
		if err != nil {
			return "", []string{"list_departments"}, cloneActiveTask(task), err
		}
		reply, err := buildClarifyReply(clarifyPlan{
			ToolName:       "list_departments",
			ToolArguments:  `{}`,
			FollowUpPrompt: "我先列出当前可选部门。请告诉我需要订阅哪些部门。",
		}, toolResult)
		return reply, []string{"list_departments"}, cloneActiveTask(task), err
	}

	return buildTaskClarifyReply(task), nil, cloneActiveTask(task), nil
}

// respondForRuntimeTaskState handles replies while a runtime task instance is still open.
func (a *Agent) respondForRuntimeTaskState(ctx context.Context, uctx *tools.UserContext, sessionKey string, task *ActiveTask) (string, []string, *ActiveTask, bool, error) {
	if a.runtime == nil || task == nil {
		return "", nil, nil, false, nil
	}

	taskMemory := a.runtimeTaskMemory(sessionKey, task)
	handler, dispatch := a.runtime.resolveRuntimeHandler(taskMemory)
	if dispatch.FallbackReason != "" {
		return "", nil, nil, false, nil
	}

	if taskMemory.Status == string(taskStatusReady) {
		result, toolsCalled, err := handler.Execute(ctx, taskMemory, uctx, a.registry)
		if err != nil {
			return "", toolsCalled, activeTaskFromTaskInstance(taskMemory), true, err
		}
		if result.KeepTaskOpen {
			a.sessions.setTaskInstance(sessionKey, taskMemory)
			return result.Reply, toolsCalled, activeTaskFromTaskInstance(taskMemory), true, nil
		}
		a.sessions.clearTaskInstance(sessionKey)
		return result.Reply, toolsCalled, nil, true, nil
	}

	toolsCalled, err := handler.Prepare(ctx, taskMemory, a.deps)
	if err != nil {
		return "", toolsCalled, activeTaskFromTaskInstance(taskMemory), true, err
	}
	a.sessions.setTaskInstance(sessionKey, taskMemory)
	reply := handler.BuildClarifyReply(taskMemory)
	if strings.TrimSpace(reply) == "" {
		reply = buildTaskClarifyReply(activeTaskFromTaskInstance(taskMemory))
	}
	return reply, toolsCalled, activeTaskFromTaskInstance(taskMemory), true, nil
}

// runtimeTaskMemory rebuilds a task instance from the active task stored in session.
func (a *Agent) runtimeTaskMemory(sessionKey string, task *ActiveTask) *TaskInstance {
	if task == nil {
		return nil
	}

	_, current := a.sessions.getTaskState(sessionKey)
	var next *TaskInstance
	if current != nil && current.Type == task.Type {
		next = cloneTaskInstance(current)
	} else {
		next = plannerTaskFromLegacyTask(sessionKey, task)
	}
	if next == nil {
		next = plannerTaskFromLegacyTask(sessionKey, task)
	}
	if next == nil {
		return nil
	}

	next.Type = task.Type
	next.Status = string(task.Status)
	next.Slots = cloneTaskSlots(task.FilledSlots)
	next.MissingSlots = append([]string(nil), task.MissingSlots()...)
	next.ExpiresAt = task.ExpiresAt
	if next.ID == "" {
		next.ID = fmt.Sprintf("%s:%s", sessionKey, task.Type)
	}
	return next
}

// tryHandlePlannerPrimary attempts to answer through the planner-primary path.
func (a *Agent) tryHandlePlannerPrimary(ctx context.Context, uctx *tools.UserContext, sessionKey, question string, userMsg tools.Message, startTime time.Time, beforeTask *ActiveTask, metrics *callMetrics, decision PlannerDecision) (bool, string, error) {
	if !shouldHandlePlannerPrimary(decision, beforeTask) {
		return false, "", nil
	}

	recordLegacyPlannerAction(metrics, a.legacyPlannerPrimaryAction(question, beforeTask, uctx))
	metrics.ConversationEvent = plannerConversationEvent(decision, beforeTask)

	switch decision.Action {
	case plannerActionOffTopicReject:
		reply := composePlannerReply(decision, nil, nil)
		applyConversationMetrics(metrics, beforeTask, nil, nil)
		metrics.DomainResult = domainOut
		metrics.PlanKind = planKindObviousOut
		metrics.KnowledgeStrength = knowledgeStrengthNone
		metrics.Planner.Reason = decision.Reason
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeReject
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		return true, reply, nil
	case plannerActionSocialRefuse:
		reply := composePlannerReply(decision, nil, nil)
		applyConversationMetrics(metrics, beforeTask, beforeTask, nil)
		metrics.DomainResult = domainIn
		metrics.PlanKind = planKindClarify
		metrics.KnowledgeStrength = knowledgeStrengthNone
		metrics.Planner.Reason = decision.Reason
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeReject
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case plannerActionCancelTask:
		a.sessions.clearTaskInstance(sessionKey)
		reply := composePlannerReply(decision, nil, nil)
		applyConversationMetrics(metrics, beforeTask, taskWithStatus(beforeTask, taskStatusCanceled), nil)
		metrics.DomainResult = domainIn
		metrics.PlanKind = planKindTool
		metrics.KnowledgeStrength = knowledgeStrengthNone
		metrics.Planner.Reason = decision.Reason
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case plannerActionTaskMeta:
		taskMemory := a.runtimeTaskMemory(sessionKey, beforeTask)
		if taskMemory == nil {
			return false, "", nil
		}
		var toolsCalled []string
		if a.runtime != nil {
			if handler, dispatch := a.runtime.resolveRuntimeHandler(taskMemory); dispatch.FallbackReason == "" {
				var err error
				toolsCalled, err = handler.Prepare(ctx, taskMemory, a.deps)
				if err != nil {
					applyConversationMetrics(metrics, beforeTask, activeTaskFromTaskInstance(taskMemory), nil)
					metrics.DomainResult = domainIn
					metrics.PlanKind = planKindClarify
					metrics.KnowledgeStrength = knowledgeStrengthNone
					metrics.Planner.Reason = decision.Reason
					metrics.QueryType = queryKindTool
					metrics.AnswerMode = answerModeToolFirst
					a.writeCallLog(ctx, uctx, question, "", toolsCalled, 0, startTime, "failed", err.Error(), *metrics)
					return true, "系统错误，请稍后重试", nil
				}
			}
		}
		reply := composePlannerReply(decision, taskMemory, nil)
		a.sessions.setTaskInstance(sessionKey, taskMemory)
		afterTask := activeTaskFromTaskInstance(taskMemory)
		applyConversationMetrics(metrics, beforeTask, afterTask, nil)
		metrics.DomainResult = domainIn
		metrics.PlanKind = planKindClarify
		metrics.KnowledgeStrength = knowledgeStrengthNone
		metrics.Planner.Reason = decision.Reason
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, question, reply, toolsCalled, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case plannerActionStartTask, plannerActionContinueTask:
		nextTask, matchedSlots := a.plannerTaskFromDecision(sessionKey, beforeTask, question, uctx, decision)
		if nextTask == nil {
			return false, "", nil
		}

		reply, taskTools, resultingTask, err := a.respondForTaskState(ctx, uctx, sessionKey, nextTask)
		if err != nil {
			applyConversationMetrics(metrics, beforeTask, resultingTaskOrFallback(resultingTask, nextTask), matchedSlots)
			a.writeCallLog(ctx, uctx, question, "", taskTools, 0, startTime, "failed", err.Error(), *metrics)
			return true, "系统错误，请稍后重试", nil
		}

		afterTask := resultingTask
		if afterTask == nil && nextTask.Status == taskStatusReady {
			afterTask = taskWithStatus(nextTask, taskStatusCompleted)
		}
		applyConversationMetrics(metrics, beforeTask, afterTask, matchedSlots)
		metrics.DomainResult = domainIn
		metrics.PlanKind = plannerPrimaryPlanKind(decision, nextTask)
		metrics.KnowledgeStrength = knowledgeStrengthNone
		metrics.Planner.Reason = plannerPrimaryReason(decision, nextTask)
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, question, reply, taskTools, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	}

	return false, "", nil
}

// tryHandleRoutePrimary attempts to answer through the route-primary path.
func (a *Agent) tryHandleRoutePrimary(ctx context.Context, uctx *tools.UserContext, sessionKey, question string, history []tools.Message, userMsg tools.Message, startTime time.Time, beforeTask *ActiveTask, metrics *callMetrics, decision RouteDecision) (bool, string, error) {
	if a.routeMode != string(RouteModeLive) {
		return false, "", nil
	}
	if guard, ok := guardLowConfidenceRouteDecision(decision); ok {
		metrics.Route.Kind = string(guard.Kind)
		metrics.Route.ReasonCode = guard.ReasonCode
		metrics.Route.ClarifyCode = guard.ClarifyCode
		decision = guard
	}

	switch decision.Kind {
	case RouteOffTopicReject:
		result := (rejectExecutor{}).Execute()
		reply := result.Reply
		applyConversationMetrics(metrics, beforeTask, nil, nil)
		metrics.DomainResult = domainOut
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case RouteSocialRefuse:
		result := (socialExecutor{}).Execute()
		reply := result.Reply
		applyConversationMetrics(metrics, beforeTask, beforeTask, nil)
		metrics.DomainResult = domainIn
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case RouteClarify:
		_, routeTask := a.sessions.getTaskState(sessionKey)
		result := (clarifyExecutor{}).Execute(decision, summarizeTaskRouteState(routeTask))
		reply := result.Reply
		applyConversationMetrics(metrics, beforeTask, beforeTask, nil)
		metrics.DomainResult = domainIn
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case RouteRAGQuery:
		result, err := (ragExecutor{agent: a}).Execute(ctx, uctx, history, question)
		if err != nil {
			a.writeCallLog(ctx, uctx, question, "", nil, 0, startTime, "failed", err.Error(), *metrics)
			return true, "AI 服务暂时不可用，请稍后重试", nil
		}
		reply := result.Reply
		applyConversationMetrics(metrics, beforeTask, beforeTask, nil)
		metrics.DomainResult = domainIn
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		metrics.LLMDurationMs += result.LLMDuration
		metrics.Retrieval.HitCount = len(result.Retrieval.Hits)
		metrics.Retrieval.CandidateCount = result.Retrieval.CandidateCount
		metrics.SourceRefs = append([]string(nil), result.Retrieval.TopRefs...)
		metrics.Retrieval.TopRefs = append([]string(nil), result.Retrieval.TopRefs...)
		metrics.Retrieval.TopScores = append([]int(nil), result.Retrieval.TopScores...)
		metrics.Retrieval.FilteredReason = result.Retrieval.FilteredReason
		metrics.Retrieval.DocTypes = append([]string(nil), result.Retrieval.KnowledgeDocTypes...)
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case RouteToolQuery:
		executorStart := time.Now()
		result, toolsCalled, err := (toolQueryExecutor{agent: a}).Execute(ctx, uctx, history, question)
		metrics.Route.ExecutorLatencyMs = elapsedMs(executorStart)
		if err != nil {
			failReply := "AI 服务暂时不可用，请稍后重试"
			if err.Error() == "超出最大轮数" {
				failReply = "处理轮次过多，请简化您的问题后重试"
			}
			a.writeCallLog(ctx, uctx, question, "", toolsCalled, 0, startTime, "failed", err.Error(), *metrics)
			return true, failReply, nil
		}
		reply := result.Reply
		applyConversationMetrics(metrics, beforeTask, beforeTask, nil)
		metrics.DomainResult = domainIn
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		metrics.Route.ToolPool = result.ToolPool
		metrics.LLMDurationMs += result.LLMDuration
		a.writeCallLog(ctx, uctx, question, reply, toolsCalled, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case RouteMixedQuery:
		executorStart := time.Now()
		result, toolsCalled, err := (mixedQueryExecutor{agent: a}).Execute(ctx, uctx, history, question)
		metrics.Route.ExecutorLatencyMs = elapsedMs(executorStart)
		if err != nil {
			failReply := "AI 服务暂时不可用，请稍后重试"
			if err.Error() == "超出最大轮数" {
				failReply = "处理轮次过多，请简化您的问题后重试"
			}
			a.writeCallLog(ctx, uctx, question, "", toolsCalled, 0, startTime, "failed", err.Error(), *metrics)
			return true, failReply, nil
		}
		reply := result.Reply
		applyConversationMetrics(metrics, beforeTask, beforeTask, nil)
		metrics.DomainResult = domainIn
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		metrics.Route.ToolPool = result.ToolPool
		metrics.LLMDurationMs += result.LLMDuration
		metrics.Retrieval.HitCount = len(result.Retrieval.Hits)
		metrics.Retrieval.CandidateCount = result.Retrieval.CandidateCount
		metrics.SourceRefs = append([]string(nil), result.Retrieval.TopRefs...)
		metrics.Retrieval.TopRefs = append([]string(nil), result.Retrieval.TopRefs...)
		metrics.Retrieval.TopScores = append([]int(nil), result.Retrieval.TopScores...)
		metrics.Retrieval.FilteredReason = result.Retrieval.FilteredReason
		metrics.Retrieval.DocTypes = append([]string(nil), result.Retrieval.KnowledgeDocTypes...)
		a.writeCallLog(ctx, uctx, question, reply, toolsCalled, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case RouteTaskStart:
		executorStart := time.Now()
		result, err := (taskStartExecutor{agent: a}).Execute(ctx, decision, question, uctx)
		metrics.Route.ExecutorLatencyMs = elapsedMs(executorStart)
		if err != nil {
			a.writeCallLog(ctx, uctx, question, "", nil, 0, startTime, "failed", err.Error(), *metrics)
			return true, "系统错误，请稍后重试", nil
		}
		if result.KeepTaskOpen && result.Task != nil {
			a.sessions.setTaskInstance(sessionKey, result.Task)
		} else {
			a.sessions.clearTaskInstance(sessionKey)
		}
		afterTask := activeTaskFromTaskInstance(result.Task)
		applyConversationMetrics(metrics, beforeTask, afterTask, result.MatchedSlots)
		metrics.DomainResult = domainIn
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		a.writeCallLog(ctx, uctx, question, result.Reply, result.ToolsCalled, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: result.Reply})
		return true, result.Reply, nil
	case RouteTaskContinue:
		_, currentTask := a.sessions.getTaskState(sessionKey)
		if currentTask == nil {
			return false, "", nil
		}
		executorStart := time.Now()
		result, err := (taskContinueExecutor{agent: a}).Execute(ctx, currentTask, question, uctx, decision.ExtractedEntities)
		metrics.Route.ExecutorLatencyMs = elapsedMs(executorStart)
		if err != nil {
			a.writeCallLog(ctx, uctx, question, "", nil, 0, startTime, "failed", err.Error(), *metrics)
			return true, "系统错误，请稍后重试", nil
		}
		if result.KeepTaskOpen && result.Task != nil {
			a.sessions.setTaskInstance(sessionKey, result.Task)
		} else {
			a.sessions.clearTaskInstance(sessionKey)
		}
		afterTask := activeTaskFromTaskInstance(result.Task)
		applyConversationMetrics(metrics, beforeTask, afterTask, result.MatchedSlots)
		metrics.DomainResult = domainIn
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		a.writeCallLog(ctx, uctx, question, result.Reply, result.ToolsCalled, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: result.Reply})
		return true, result.Reply, nil
	case RouteTaskMeta:
		_, currentTask := a.sessions.getTaskState(sessionKey)
		if currentTask == nil {
			return false, "", nil
		}
		executorStart := time.Now()
		result, err := (taskMetaExecutor{agent: a}).Execute(ctx, currentTask)
		metrics.Route.ExecutorLatencyMs = elapsedMs(executorStart)
		if err != nil {
			a.writeCallLog(ctx, uctx, question, "", nil, 0, startTime, "failed", err.Error(), *metrics)
			return true, "系统错误，请稍后重试", nil
		}
		if result.KeepTaskOpen && result.Task != nil {
			a.sessions.setTaskInstance(sessionKey, result.Task)
		}
		afterTask := activeTaskFromTaskInstance(result.Task)
		applyConversationMetrics(metrics, beforeTask, afterTask, nil)
		metrics.DomainResult = domainIn
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		a.writeCallLog(ctx, uctx, question, result.Reply, result.ToolsCalled, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: result.Reply})
		return true, result.Reply, nil
	case RouteTaskCancel:
		_, currentTask := a.sessions.getTaskState(sessionKey)
		if currentTask == nil {
			return false, "", nil
		}
		result := (taskCancelExecutor{}).Execute(currentTask)
		a.sessions.clearTaskInstance(sessionKey)
		applyConversationMetrics(metrics, beforeTask, nil, nil)
		metrics.DomainResult = domainIn
		metrics.AnswerMode = result.AnswerMode
		metrics.QueryType = modeToQueryKind(result.AnswerMode)
		metrics.Route.ExecutorName = result.ExecutorName
		a.writeCallLog(ctx, uctx, question, result.Reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: result.Reply})
		return true, result.Reply, nil
	default:
		return false, "", nil
	}
}

// shouldHandlePlannerPrimary reports whether planner primary handling should run for the current decision.
func shouldHandlePlannerPrimary(decision PlannerDecision, activeTask *ActiveTask) bool {
	switch decision.Action {
	case plannerActionOffTopicReject, plannerActionSocialRefuse:
		return true
	case plannerActionTaskMeta, plannerActionCancelTask, plannerActionContinueTask:
		return activeTask != nil && isMigratedTaskType(activeTask.Type)
	case plannerActionStartTask:
		return isMigratedTaskType(decision.TaskType)
	default:
		return false
	}
}

// isMigratedTaskType reports whether the task type has been migrated to the runtime path.
func isMigratedTaskType(taskType string) bool {
	switch taskType {
	case "subscribe_attendance_push", "unsubscribe_attendance_push", "query_subscription_status", "sign_for_user":
		return true
	default:
		return false
	}
}

// plannerConversationEvent maps a planner decision to a conversation event.
func plannerConversationEvent(decision PlannerDecision, activeTask *ActiveTask) conversationEvent {
	switch decision.Action {
	case plannerActionContinueTask, plannerActionTaskMeta:
		if activeTask != nil {
			return eventTaskFollowUp
		}
	case plannerActionCancelTask:
		return eventCancel
	case plannerActionStartTask:
		return eventNewRequest
	}
	return eventNewRequest
}

// plannerPrimaryPlanKind derives the plan kind used by planner primary handling.
func plannerPrimaryPlanKind(decision PlannerDecision, nextTask *ActiveTask) PlanKind {
	switch decision.Action {
	case plannerActionContinueTask:
		return planKindContinueTask
	case plannerActionStartTask:
		if nextTask != nil && nextTask.Status == taskStatusReady {
			return planKindTool
		}
		return planKindClarify
	default:
		return planKindClarify
	}
}

// plannerPrimaryReason derives the reason code used by planner primary handling.
func plannerPrimaryReason(decision PlannerDecision, nextTask *ActiveTask) string {
	if nextTask != nil && nextTask.Status != taskStatusReady {
		return "missing_slots"
	}
	return decision.Reason
}

// plannerTaskFromDecision builds the next active task from a planner decision.
func (a *Agent) plannerTaskFromDecision(_ string, beforeTask *ActiveTask, question string, uctx *tools.UserContext, decision PlannerDecision) (*ActiveTask, []string) {
	switch decision.Action {
	case plannerActionStartTask:
		task := buildTaskFromRequest(question, uctx)
		return task, nil
	case plannerActionContinueTask:
		if beforeTask == nil {
			return nil, nil
		}
		nextTask := applySlotFillToTask(beforeTask, slotFillResult{Filled: cloneTaskSlots(decision.Slots)})
		return nextTask, plannerMatchedSlotNames(beforeTask.FilledSlots, decision.Slots)
	default:
		return nil, nil
	}
}

// plannerMatchedSlotNames returns the slot names matched by the planner turn.
func plannerMatchedSlotNames(before map[string]string, after map[string]string) []string {
	if len(after) == 0 {
		return nil
	}
	names := make([]string, 0, len(after))
	for key, value := range after {
		if before[key] == value {
			continue
		}
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}

// legacyPlannerPrimaryAction computes the legacy planner action for the current turn.
func (a *Agent) legacyPlannerPrimaryAction(question string, activeTask *ActiveTask, uctx *tools.UserContext) PlannerAction {
	decision := interpretConversation(question, activeTask)
	switch decision.Event {
	case eventGreeting:
		return plannerActionSocialRefuse
	case eventCancel:
		return plannerActionCancelTask
	case eventTaskFollowUp:
		return plannerActionContinueTask
	case eventUnknown:
		return plannerActionTaskMeta
	}

	normalized := normalizeQuery(question)
	if hasHelpIntent(normalized) {
		return plannerActionSocialRefuse
	}
	if candidate := buildTaskFromRequest(question, uctx); candidate != nil {
		return plannerActionStartTask
	}
	return ""
}

// executeReadyTask executes a ready legacy task and returns its reply.
func (a *Agent) executeReadyTask(ctx context.Context, uctx *tools.UserContext, task *ActiveTask) (string, []string, *ActiveTask, error) {
	if task == nil {
		return "", nil, nil, nil
	}

	switch task.Type {
	case "query_subscription_status":
		toolResult, err := a.registry.Dispatch(ctx, uctx, "query_subscription_status", json.RawMessage(`{}`))
		if err != nil {
			return "", []string{"query_subscription_status"}, nil, err
		}
		reply, err := buildClarifyReply(clarifyPlan{ToolName: "query_subscription_status", ToolArguments: `{}`}, toolResult)
		return reply, []string{"query_subscription_status"}, nil, err
	case "subscribe_attendance_push":
		payload := map[string]any{}
		if task.FilledSlots["scope"] == "department" {
			payload["dept_names"] = splitTaskValues(task.FilledSlots["dept_names"])
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", []string{"subscribe_attendance_push"}, nil, err
		}
		toolResult, err := a.registry.Dispatch(ctx, uctx, "subscribe_attendance_push", raw)
		if err != nil {
			return "", []string{"subscribe_attendance_push"}, nil, err
		}
		reply, err := renderToolMessage(toolResult)
		if err != nil {
			return "", []string{"subscribe_attendance_push"}, nil, err
		}
		if retryTask, retryHint := recoverableTaskFromToolResult(task, toolResult); retryTask != nil {
			if retryHint != "" {
				reply = strings.TrimSpace(reply + " " + retryHint)
			}
			return reply, []string{"subscribe_attendance_push"}, retryTask, nil
		}
		return reply, []string{"subscribe_attendance_push"}, nil, nil
	case "sign_for_user":
		section, err := strconv.Atoi(task.FilledSlots["section"])
		if err != nil {
			return "", nil, nil, err
		}
		payload := map[string]any{
			"user_name": task.FilledSlots["user_name"],
			"date":      materializeTaskDate(task.FilledSlots["date"]),
			"section":   section,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", []string{"sign_for_user"}, nil, err
		}
		toolResult, err := a.registry.Dispatch(ctx, uctx, "sign_for_user", raw)
		if err != nil {
			return "", []string{"sign_for_user"}, nil, err
		}
		reply, err := renderToolMessage(toolResult)
		return reply, []string{"sign_for_user"}, nil, err
	default:
		return "", nil, nil, nil
	}
}

type toolErrorPayload struct {
	Error              string   `json:"error"`
	ErrorCode          string   `json:"error_code"`
	InvalidDeptNames   []string `json:"invalid_dept_names"`
	AmbiguousDeptNames []string `json:"ambiguous_dept_names"`
	CandidateUsers     []string `json:"candidate_users"`
	Users              []string `json:"users"`
}

// recoverableTaskFromToolResult rebuilds a task from a retryable tool response.
func recoverableTaskFromToolResult(task *ActiveTask, toolResult string) (*ActiveTask, string) {
	if task == nil || task.Type != "subscribe_attendance_push" || task.FilledSlots["scope"] != "department" {
		return nil, ""
	}

	payload := parseToolErrorPayload(toolResult)
	switch payload.ErrorCode {
	case "department_name_not_found", "department_name_ambiguous":
		retryTask := &ActiveTask{
			Type:          "subscribe_attendance_push",
			Status:        taskStatusWaiting,
			RequiredSlots: []string{"dept_names"},
			FilledSlots:   map[string]string{"scope": "department"},
			ExpiresAt:     time.Now().Add(sessionTTL),
			LastPrompt:    "clarify_dept_names",
		}
		if payload.ErrorCode == "department_name_not_found" {
			return retryTask, "你也可以回复“现在都有哪些部门”，我会把可选部门列给你。"
		}
		return retryTask, ""
	default:
		return nil, ""
	}
}

// resultingTaskOrFallback chooses the resulting task while preserving the fallback task when needed.
func resultingTaskOrFallback(result *ActiveTask, fallback *ActiveTask) *ActiveTask {
	if result != nil {
		return result
	}
	return fallback
}

// renderToolMessage renders a tool payload into user-facing text.
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

// materializeTaskDate materializes a task date token into a concrete date string.
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

// splitTaskValues splits a comma-like task value list into normalized items.
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
func (a *Agent) writeCallLog(_ context.Context, uctx *tools.UserContext, question, reply string, toolsCalled []string, rounds int, startTime time.Time, status, errMsg string, metrics callMetrics) {
	if a.deps.CallLog == nil {
		return
	}
	log := tools.CallLog{
		TenantID:                   uctx.TenantID,
		UserID:                     uctx.UserID,
		UserRole:                   uctx.UserRole,
		UserName:                   uctx.Name,
		ConvType:                   uctx.ConversationType,
		QueryType:                  string(metrics.QueryType),
		ConversationEvent:          string(metrics.ConversationEvent),
		ActiveTaskType:             metrics.Task.ActiveTaskType,
		TaskStatusBefore:           metrics.Task.TaskStatusBefore,
		TaskStatusAfter:            metrics.Task.TaskStatusAfter,
		DomainResult:               string(metrics.DomainResult),
		DomainHint:                 string(metrics.DomainHint),
		PlanKind:                   string(metrics.PlanKind),
		KnowledgeStrength:          string(metrics.KnowledgeStrength),
		PlannerReason:              metrics.Planner.Reason,
		PlannerAction:              metrics.Planner.Action,
		PlannerConfidence:          metrics.Planner.Confidence,
		TaskID:                     metrics.Task.TaskID,
		TaskKeepOpen:               metrics.Task.TaskKeepOpen,
		TaskSwitch:                 metrics.Task.TaskSwitch,
		LastErrorCode:              metrics.Task.LastErrorCode,
		ShadowPlannerAction:        metrics.Shadow.PlannerAction,
		ShadowPlannerMatched:       metrics.Shadow.PlannerMatched,
		RouteKind:                  metrics.Route.Kind,
		RouteConfidence:            metrics.Route.Confidence,
		RouteReasonCode:            metrics.Route.ReasonCode,
		RouteSource:                metrics.Route.Source,
		ClarifyCode:                metrics.Route.ClarifyCode,
		SoftNoticeCode:             metrics.Route.SoftNoticeCode,
		ExecutorName:               metrics.Route.ExecutorName,
		ToolPool:                   metrics.Route.ToolPool,
		RouterLatencyMs:            metrics.Route.RouterLatencyMs,
		ExecutorLatencyMs:          metrics.Route.ExecutorLatencyMs,
		ShadowRouteKind:            metrics.Shadow.RouteKind,
		ShadowRouteMatched:         metrics.Shadow.RouteMatched,
		ProtocolMode:               metrics.Proto.Mode,
		ProtocolAct:                metrics.Proto.Act,
		ProtocolDomain:             metrics.Proto.Domain,
		ProtocolOperation:          metrics.Proto.Operation,
		ProtocolValidationCode:     metrics.Proto.ValidationCode,
		ProtocolBlockedReason:      metrics.Proto.BlockedReason,
		ProtocolResolvedSlots:      metrics.Proto.ResolvedSlots,
		ProtocolCandidateCount:     metrics.Proto.CandidateCount,
		RequestID:                  metrics.Proto.RequestID,
		ConversationID:             uctx.ConversationID,
		CompilerStatus:             metrics.Proto.CompilerStatus,
		CompilerLatencyMs:          metrics.Proto.CompilerLatencyMs,
		IntentDraftJSON:            metrics.Proto.IntentDraftJSON,
		CatalogValidationCode:      metrics.Proto.CatalogValidationCode,
		WorkflowDecision:           metrics.Proto.WorkflowDecision,
		WorkflowInterruptReason:    metrics.Proto.WorkflowInterruptReason,
		ResolvedSlotsJSON:          metrics.Proto.ResolvedSlotsJSON,
		EntityResolutionStatus:     metrics.Proto.EntityResolutionStatus,
		PrePolicyResult:            metrics.Proto.PrePolicyResult,
		ResourcePolicyResult:       metrics.Proto.ResourcePolicyResult,
		BlockedReason:              metrics.Proto.BlockedReason,
		WriteGuardResult:           metrics.Proto.WriteGuardResult,
		IdempotencyKey:             metrics.Proto.IdempotencyKey,
		ExecutorStatus:             metrics.Proto.ExecutorStatus,
		RendererName:               metrics.Proto.RendererName,
		FailureLayer:               metrics.Proto.FailureLayer,
		LegacyCalled:               metrics.Proto.LegacyCalled,
		ReplayCaseID:               metrics.Proto.RequestID,
		WorkflowIDBefore:           metrics.Wf.IDBefore,
		WorkflowIDAfter:            metrics.Wf.IDAfter,
		WorkflowTypeBefore:         metrics.Wf.TypeBefore,
		WorkflowTypeAfter:          metrics.Wf.TypeAfter,
		WorkflowStateBefore:        metrics.Wf.StateBefore,
		WorkflowStateAfter:         metrics.Wf.StateAfter,
		WorkflowSnapshotBeforeJSON: metrics.Wf.SnapshotBeforeJSON,
		WorkflowSnapshotAfterJSON:  metrics.Wf.SnapshotAfterJSON,
		ResponseKind:               metrics.ResponseKind,
		ExecutionAllowed:           metrics.Proto.ExecutionAllowed,
		AnswerMode:                 string(metrics.AnswerMode),
		Question:                   question,
		ToolsCalled:                toolsCalled,
		ToolCallCount:              len(toolsCalled),
		Reply:                      reply,
		SourceRefs:                 append([]string(nil), metrics.SourceRefs...),
		RetrievalHitCount:          metrics.Retrieval.HitCount,
		RetrievalCandidateCount:    metrics.Retrieval.CandidateCount,
		RetrievalTopRefs:           append([]string(nil), metrics.Retrieval.TopRefs...),
		RetrievalScores:            append([]int(nil), metrics.Retrieval.TopScores...),
		FollowUpMatchedSlots:       append([]string(nil), metrics.FollowUpMatchedSlots...),
		RetrievalFilteredReason:    metrics.Retrieval.FilteredReason,
		KnowledgeDocTypes:          append([]string(nil), metrics.Retrieval.DocTypes...),
		RetrievalDurationMs:        metrics.Retrieval.DurationMs,
		LLMDurationMs:              metrics.LLMDurationMs,
		Rounds:                     rounds,
		DurationMs:                 elapsedMs(startTime),
		Status:                     status,
		ErrorMsg:                   errMsg,
	}
	a.logWriter.Write(log)
}

// applyShadowPlannerMetrics writes shadow planner fields into call metrics.
func applyShadowPlannerMetrics(metrics *callMetrics, decision PlannerDecision, activeTask *TaskInstance) {
	if metrics == nil {
		return
	}
	metrics.Planner.Action = string(decision.Action)
	metrics.Planner.Confidence = decision.Confidence
	metrics.Task.TaskKeepOpen = decision.KeepTaskOpen
	metrics.Task.TaskSwitch = decision.SwitchTask
	if activeTask != nil {
		metrics.Task.TaskID = activeTask.ID
	}
}

// recordLegacyPlannerAction records the legacy planner action on the current metrics object.
func recordLegacyPlannerAction(metrics *callMetrics, action PlannerAction) {
	if metrics == nil {
		return
	}
	metrics.Shadow.PlannerAction = string(action)
	metrics.Shadow.PlannerMatched = metrics.Shadow.PlannerAction != "" && metrics.Shadow.PlannerAction == metrics.Planner.Action
}

// applyProtocolMetrics writes protocol draft and validation fields into call metrics.
func applyProtocolMetrics(metrics *callMetrics, draft ProtocolDraft, validation ProtocolValidationResult) {
	if metrics == nil {
		return
	}
	metrics.Proto.Act = string(draft.Act)
	metrics.Proto.Domain = string(draft.Domain)
	metrics.Proto.Operation = draft.Operation
	metrics.Proto.ValidationCode = validation.ValidationCode
	if metrics.Proto.CatalogValidationCode == "" {
		metrics.Proto.CatalogValidationCode = validation.ValidationCode
	}
	metrics.Proto.ExecutionAllowed = validation.AllowExecution
}

// protocolPrimaryDispatchAllowed reports whether the protocol primary path may dispatch to a handler.
func protocolPrimaryDispatchAllowed(_ ProtocolDraft, validation ProtocolValidationResult) bool {
	if validation.AllowExecution {
		return true
	}
	if validation.UseActiveWorkflow && validation.ResponseKind == ResponseResult {
		return true
	}
	return validation.ResponseKind == ResponseAnswer
}

func (a *Agent) operationExecutor() operationExecutor {
	return newOperationExecutor(operationExecutorDeps{
		Schedule:                a.deps.Schedule,
		Attendance:              a.deps.Attendance,
		AttendanceUserDayStatus: a.deps.AttendanceUserDayStatus,
		Semester:                a.deps.Semester,
		GroupSub:                a.deps.GroupSub,
		Dept:                    a.deps.Dept,
		Knowledge:               a.deps.Knowledge,
	})
}

func (a *Agent) protocolLivePipeline() protocolLivePipeline {
	return newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler:       a.intentCompiler,
		Executor:       a.operationExecutor(),
		User:           a.deps.User,
		Dept:           a.deps.Dept,
		Semester:       a.deps.Semester,
		SchedulePeriod: a.deps.SchedulePeriod,
	})
}

func (a *Agent) applyProtocolLiveOutcome(sessionKey string, metrics *callMetrics, outcome protocolLiveOutcome) {
	a.applyProtocolLiveOutcomeForKey(context.Background(), workflowKeyFromSessionKey(sessionKey, nil), sessionKey, metrics, outcome)
}

func (a *Agent) applyProtocolLiveOutcomeForKey(ctx context.Context, workflowKey WorkflowKey, sessionKey string, metrics *callMetrics, outcome protocolLiveOutcome) {
	var workflowBefore *WorkflowSnapshot
	if a != nil && a.workflowStore != nil {
		if loaded, err := a.workflowStore.Load(ctx, workflowKey); err == nil {
			workflowBefore = loaded
		} else if a.deps.Logger != nil {
			a.deps.Logger.Warnw("读取 workflow 失败", "tenantID", workflowKey.TenantID, "conversationID", workflowKey.ConversationID, "actorUserID", workflowKey.ActorUserID, "err", err)
		}
	}
	if workflowBefore == nil && a != nil && a.sessions != nil {
		_, workflowBefore = a.sessions.getWorkflowState(sessionKey)
	}

	if a != nil {
		if err := a.persistProtocolLiveWorkflowOutcome(ctx, workflowKey, outcome); err != nil && a.deps.Logger != nil {
			a.deps.Logger.Warnw("更新 workflow 失败", "tenantID", workflowKey.TenantID, "conversationID", workflowKey.ConversationID, "actorUserID", workflowKey.ActorUserID, "err", err)
		}
	}
	a.applyProtocolLiveOutcomeAfterStore(sessionKey, metrics, outcome, workflowBefore)
}

func (a *Agent) persistProtocolLiveWorkflowOutcome(ctx context.Context, workflowKey WorkflowKey, outcome protocolLiveOutcome) error {
	if a == nil || a.workflowStore == nil {
		return nil
	}
	if outcome.ClearWorkflow {
		return a.workflowStore.Clear(ctx, workflowKey, string(outcome.WorkflowDecision))
	}
	if outcome.WorkflowAfter == nil {
		return nil
	}
	next := cloneWorkflowSnapshot(outcome.WorkflowAfter)
	next.TenantID = workflowKey.TenantID
	next.ConversationID = workflowKey.ConversationID
	next.ActorUserID = workflowKey.ActorUserID
	return a.workflowStore.Save(ctx, next)
}

func (a *Agent) applyProtocolLiveOutcomeAfterStore(sessionKey string, metrics *callMetrics, outcome protocolLiveOutcome, workflowBefore *WorkflowSnapshot) {
	if a != nil && a.sessions != nil {
		if outcome.ClearWorkflow {
			a.sessions.clearWorkflowState(sessionKey)
		}
		if outcome.WorkflowAfter != nil {
			a.sessions.setWorkflowState(sessionKey, outcome.WorkflowAfter)
		}
	}

	a.applyProtocolLiveOutcomeMetrics(metrics, outcome, workflowBefore)
}

func (a *Agent) applyProtocolLiveOutcomeMetrics(metrics *callMetrics, outcome protocolLiveOutcome, workflowBefore *WorkflowSnapshot) {
	applyProtocolMetrics(metrics, outcome.Draft, outcome.Validation)
	if metrics == nil {
		return
	}
	if workflowBefore != nil {
		if metrics.Wf.IDBefore == "" {
			metrics.Wf.IDBefore = workflowBefore.ID
		}
		metrics.Wf.TypeBefore = string(workflowBefore.Type)
		metrics.Wf.StateBefore = string(workflowBefore.State)
		metrics.Wf.SnapshotBeforeJSON = compactWorkflowSnapshotForReplay(workflowBefore)
	}
	metrics.Proto.BlockedReason = outcome.BlockedReason
	metrics.Proto.ResolvedSlots = compactProtocolResolvedSlots(outcome.ResolvedSlots)
	metrics.Proto.ResolvedSlotsJSON = metrics.Proto.ResolvedSlots
	metrics.Proto.CandidateCount = outcome.CandidateCount
	metrics.Proto.RequestID = outcome.RequestID
	metrics.Proto.CompilerStatus = outcome.CompilerStatus
	metrics.Proto.CompilerLatencyMs = outcome.CompilerLatencyMs
	metrics.Proto.IntentDraftJSON = outcome.IntentDraftJSON
	metrics.Proto.CatalogValidationCode = firstNonEmpty(outcome.CatalogValidationCode, outcome.Validation.ValidationCode)
	metrics.Proto.WorkflowDecision = string(outcome.WorkflowDecision)
	metrics.Proto.WorkflowInterruptReason = outcome.WorkflowInterruptReason
	metrics.Proto.EntityResolutionStatus = outcome.EntityResolutionStatus
	metrics.Proto.PrePolicyResult = outcome.PrePolicyResult
	metrics.Proto.ResourcePolicyResult = outcome.ResourcePolicyResult
	metrics.Proto.WriteGuardResult = outcome.WriteGuardResult
	metrics.Proto.IdempotencyKey = outcome.IdempotencyKey
	metrics.Proto.ExecutorStatus = outcome.ExecutorStatus
	metrics.Proto.RendererName = outcome.RendererName
	metrics.Proto.FailureLayer = string(outcome.FailureLayer)
	metrics.Proto.LegacyCalled = outcome.LegacyCalled
	if outcome.ExecutionMetrics.ExecutorName != "" {
		applyOperationExecutionMetrics(metrics, OperationExecutionResult{
			Response: outcome.Response,
			Metrics:  outcome.ExecutionMetrics,
		})
	} else {
		metrics.ResponseKind = string(outcome.Response.Kind)
		metrics.AnswerMode = outcome.AnswerMode
		metrics.QueryType = modeToQueryKind(outcome.AnswerMode)
		if outcome.WorkflowDecision != "" && outcome.WorkflowDecision != WorkflowSingleTurn {
			metrics.Route.ExecutorName = "protocol_live_workflow"
		} else {
			metrics.Route.ExecutorName = "protocol_live_guardrail"
		}
	}
	if outcome.WorkflowAfter != nil {
		metrics.Wf.IDAfter = outcome.WorkflowAfter.ID
		metrics.Wf.TypeAfter = string(outcome.WorkflowAfter.Type)
		metrics.Wf.StateAfter = string(outcome.WorkflowAfter.State)
		metrics.Wf.SnapshotAfterJSON = compactWorkflowSnapshotForReplay(outcome.WorkflowAfter)
	} else if outcome.ClearWorkflow {
		metrics.Wf.IDAfter = ""
		metrics.Wf.TypeAfter = ""
		metrics.Wf.StateAfter = workflowStateAfterFromDecision(outcome.WorkflowDecision)
		metrics.Wf.SnapshotAfterJSON = ""
	}
}

func workflowStoreFailureOutcome() protocolLiveOutcome {
	draft := unknownIntentDraft("workflow_store_failed")
	validation := ProtocolValidationResult{
		ValidationCode: "workflow_store_failed",
		ResponseKind:   ResponseRefuse,
	}
	outcome := protocolLiveOutcome{
		Draft:           draft,
		Validation:      validation,
		Response:        ResponseModel{Kind: ResponseRefuse, RefusalReason: "系统暂时无法处理当前任务状态，请稍后重试。"},
		AnswerMode:      answerModeReject,
		BlockedReason:   "workflow_store_failed",
		RequestID:       newProtocolLiveRequestID(time.Now()),
		CompilerStatus:  "skipped",
		IntentDraftJSON: compactIntentDraft(draft),
		FailureLayer:    FailurePersistence,
		RendererName:    "response_renderer",
	}
	finalizeProtocolLiveOutcome(&outcome)
	return outcome
}

func workflowStateAfterFromDecision(decision WorkflowDecision) string {
	switch decision {
	case WorkflowCompletedDecision:
		return string(WorkflowCompleted)
	case WorkflowCanceled:
		return string(WorkflowCancelled)
	case WorkflowInterrupted:
		return string(WorkflowInterruptedState)
	default:
		return ""
	}
}

func compactProtocolResolvedSlots(slots map[string]any) string {
	if len(slots) == 0 {
		return ""
	}
	data, err := json.Marshal(slots)
	if err != nil {
		return ""
	}
	return string(data)
}

func applyOperationExecutionMetrics(metrics *callMetrics, result OperationExecutionResult) {
	if metrics == nil {
		return
	}
	metrics.ResponseKind = string(result.Response.Kind)
	metrics.AnswerMode = result.Metrics.AnswerMode
	metrics.QueryType = modeToQueryKind(result.Metrics.AnswerMode)
	metrics.Route.ExecutorName = result.Metrics.ExecutorName
	metrics.Route.ToolPool = result.Metrics.ToolPool
	metrics.Retrieval.HitCount = result.Metrics.RetrievalHitCount
	metrics.Retrieval.CandidateCount = result.Metrics.RetrievalCandidateCount
	metrics.Retrieval.TopRefs = append([]string(nil), result.Metrics.SourceRefs...)
	metrics.SourceRefs = append([]string(nil), result.Metrics.SourceRefs...)
}

// legacyTaskPlannerAction maps legacy task transitions to a planner action label.
func legacyTaskPlannerAction(beforeTask *ActiveTask, nextTask *ActiveTask) PlannerAction {
	if beforeTask == nil && nextTask != nil {
		return plannerActionStartTask
	}
	if beforeTask != nil && nextTask != nil {
		return plannerActionContinueTask
	}
	return plannerActionTaskMeta
}

// applyConversationMetrics writes task transition and slot metrics for the current conversation turn.
func applyConversationMetrics(metrics *callMetrics, before, after *ActiveTask, matchedSlots []string) {
	if metrics == nil {
		return
	}
	metrics.Task.ActiveTaskType = taskTypeForLog(after)
	if metrics.Task.ActiveTaskType == "" {
		metrics.Task.ActiveTaskType = taskTypeForLog(before)
	}
	metrics.Task.TaskStatusBefore = taskStatusForLog(before)
	metrics.Task.TaskStatusAfter = taskStatusForLog(after)
	metrics.FollowUpMatchedSlots = append([]string(nil), matchedSlots...)
}

// taskTypeForLog returns the task type string recorded in call logs.
func taskTypeForLog(task *ActiveTask) string {
	if task == nil {
		return ""
	}
	return task.Type
}

// taskStatusForLog returns the task status string recorded in call logs.
func taskStatusForLog(task *ActiveTask) string {
	if task == nil {
		return ""
	}
	return string(task.Status)
}

// taskWithStatus clones a task and overwrites its status for logging.
func taskWithStatus(task *ActiveTask, status taskStatus) *ActiveTask {
	cloned := cloneActiveTask(task)
	if cloned == nil {
		return nil
	}
	cloned.Status = status
	return cloned
}

// matchedSlotNames returns the matched slot names from a slot fill result.
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

// protocolWorkflowContextFromActiveTask derives protocol workflow context from a legacy active task.
func protocolWorkflowContextFromActiveTask(task *ActiveTask) *protocolWorkflowContext {
	if task == nil {
		return nil
	}

	workflowType := ""
	switch task.Type {
	case "subscribe_attendance_push":
		workflowType = "subscription.start"
	case "unsubscribe_attendance_push":
		workflowType = "subscription.cancel"
	case "sign_for_user":
		workflowType = "manual_sign.create"
	default:
		return nil
	}

	return &protocolWorkflowContext{
		Type:          workflowType,
		MissingFields: task.MissingSlots(),
	}
}

// protocolWorkflowContextFromWorkflowSnapshot derives protocol workflow context from a workflow snapshot.
func protocolWorkflowContextFromWorkflowSnapshot(workflow *WorkflowSnapshot) *protocolWorkflowContext {
	if workflow == nil {
		return nil
	}
	return &protocolWorkflowContext{
		Type:          string(workflow.Type),
		MissingFields: cloneStringSlice(workflowMissingFields(workflow)),
	}
}

// resolveAttendanceTrustedEntities extracts trusted attendance fields from a user message.
func resolveAttendanceTrustedEntities(message string) (trustedEntities, bool) {
	dateValue := resolveDateFromMessage(message)
	sectionValue := resolveSectionFromMessage(message)
	if dateValue == "" || sectionValue == 0 {
		return trustedEntities{}, false
	}
	return trustedEntities{
		Date:    dateValue,
		Section: sectionValue,
	}, true
}

// resolveDateFromMessage extracts a normalized date from a user message.
func resolveDateFromMessage(message string) string {
	dateValue := ""
	for _, candidate := range []string{"今天", "昨天", "明天"} {
		if !strings.Contains(message, candidate) {
			continue
		}
		if resolved, ok := resolveDate(candidate); ok {
			dateValue = resolved
			break
		}
	}
	if dateValue == "" {
		if explicit := extractDateToken(message); explicit != "" {
			if resolved, ok := resolveDate(explicit); ok {
				dateValue = resolved
			}
		}
	}
	return dateValue
}

// resolveSectionFromMessage extracts a section number from a user message.
func resolveSectionFromMessage(message string) int {
	sectionValue := 0
	for token, value := range map[string]int{
		"第一节": 1,
		"第二节": 2,
		"第三节": 3,
		"第四节": 4,
		"第五节": 5,
	} {
		if strings.Contains(message, token) {
			sectionValue = value
			break
		}
	}
	return sectionValue
}

// extractDateToken extracts the first supported date token from a user message.
func extractDateToken(message string) string {
	for i := 0; i+10 <= len(message); i++ {
		candidate := message[i : i+10]
		if _, err := time.Parse("2006-01-02", candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// buildManualSignCapabilityReply builds the capability-only reply for manual sign questions.
func buildManualSignCapabilityReply(uctx *tools.UserContext) string {
	if uctx != nil && uctx.UserRole >= 1 {
		return "代签（补签）属于管理员能力；当前聊天路径只说明权限、规则和所需信息，不直接执行代签。"
	}
	return "代签（补签）属于管理员能力；普通用户不能通过当前聊天路径执行代签。"
}

// buildProtocolCapabilityReply builds the capability reply for a protocol domain question.
func buildProtocolCapabilityReply(domain BusinessDomain, uctx *tools.UserContext) string {
	if entry, ok := lookupCapability(capabilityOperationForDomain(domain)); ok {
		if domain == DomainSubscription && (uctx == nil || uctx.ConversationType != "2") {
			return "群考勤订阅只能在群聊中使用；在群聊里管理员可以开启、取消或查询考勤推送订阅。"
		}
		if domain == DomainSubscription && uctx != nil && uctx.UserRole < 1 {
			return "你可以在群聊里查询当前群的考勤订阅状态；开启、取消或按部门管理订阅需要管理员权限。"
		}
		if domain == DomainManualSign {
			return buildManualSignCapabilityReply(uctx)
		}
		return entry.Description
	}
	return buildHelpReply(uctx)
}

func capabilityOperationForDomain(domain BusinessDomain) string {
	for _, manifest := range operationManifests() {
		if manifest.Domain == domain && manifest.Capability != nil {
			return manifest.Name
		}
	}
	return ""
}

func subscriptionDeptIDsFromTrusted(trusted trustedEntities) []int64 {
	if len(trusted.DeptIDs) > 0 {
		return append([]int64(nil), trusted.DeptIDs...)
	}
	if trusted.Scope == "department" && trusted.DepartmentID != 0 {
		return []int64{trusted.DepartmentID}
	}
	return nil
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
- 默认按钉钉纯文本聊天格式输出，不要使用 Markdown 语法，如 #、**、反引号、表格
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

// buildClarifyReply builds a reply from a clarify plan and optional tool output.
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
		return buildSubscriptionStatusReply(info), nil
	default:
		return plan.FollowUpPrompt, nil
	}
}

// extractToolError extracts a structured tool error payload from a tool result.
func extractToolError(toolResult string) string {
	return strings.TrimSpace(parseToolErrorPayload(toolResult).Error)
}

// parseToolErrorPayload parses a tool error payload from raw tool output.
func parseToolErrorPayload(toolResult string) toolErrorPayload {
	var payload toolErrorPayload
	if err := json.Unmarshal([]byte(toolResult), &payload); err != nil {
		return toolErrorPayload{}
	}
	return payload
}

// extractParamString safely extracts a string value from a params map.
func extractParamString(params map[string]TrustedParam, key string) (string, bool) {
	v, ok := trustedParamConcreteValue(params, key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// extractParamInt safely extracts an int value from a params map.
func extractParamInt(params map[string]TrustedParam, key string) (int, bool) {
	v, ok := trustedParamConcreteValue(params, key)
	if !ok {
		return 0, false
	}
	// JSON 数字反序列化为 float64，需要兼容
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// extractParamUint safely extracts a uint value from a params map.
func extractParamUint(params map[string]TrustedParam, key string) (uint, bool) {
	v, ok := trustedParamConcreteValue(params, key)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case uint:
		return n, true
	case int:
		if n < 0 {
			return 0, false
		}
		return uint(n), true
	case float64:
		if n < 0 {
			return 0, false
		}
		return uint(n), true
	default:
		return 0, false
	}
}

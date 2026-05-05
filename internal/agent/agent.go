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
	LLMBaseURL       string
	LLMAPIKey        string
	LLMModel         string
	RouterLLMBaseURL string
	RouterLLMAPIKey  string
	RouterLLMModel   string
	RouteMode        string
	ProtocolMode     string

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
	deps         Deps
	llmClient    *LLMClient
	routerClient *LLMClient
	routeMode    string
	protocolMode ProtocolMode
	registry     *tools.Registry
	runtime      *taskRuntime
	taskCatalog  *taskCatalog
	sessions     *sessionManager
	limiter      *rateLimiter
	logWriter    *callLogWriter
	stopCleanup  chan struct{}
	once         sync.Once
}


// NewAgent 创建 Agent
func NewAgent(deps Deps) *Agent {
	routeMode := strings.TrimSpace(deps.RouteMode)
	if routeMode == "" {
		routeMode = string(RouteModeOff)
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

	a := &Agent{
		deps:         deps,
		llmClient:    mainClient,
		routerClient: routerClient,
		routeMode:    routeMode,
		protocolMode: protocolMode,
		sessions:     newSessionManager(),
		limiter:      newRateLimiter(),
		stopCleanup:  make(chan struct{}),
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

	// 2. protocol 模式优先
	if a.protocolMode != ProtocolModeLegacy {
		_, activeWorkflow := a.sessions.getWorkflowState(sessionKey)
		protocolWorkflow := protocolWorkflowContextFromWorkflowSnapshot(activeWorkflow)
		if protocolWorkflow == nil && a.protocolMode == ProtocolModeShadow {
			_, activeTask := a.sessions.getSessionState(sessionKey)
			protocolWorkflow = protocolWorkflowContextFromActiveTask(activeTask)
		}
		var protocolDraft ProtocolDraft
		var protocolValidation ProtocolValidationResult
		protocolDraft = compileProtocol(protocolInput{
			Message:        msg.Content,
			ActiveWorkflow: protocolWorkflow,
		})
		protocolValidation = validateProtocol(protocolDraft, protocolWorkflow)
		applyProtocolMetrics(&metrics, protocolDraft, protocolValidation)
		if activeWorkflow != nil {
			metrics.Wf.IDBefore = activeWorkflow.ID
		}
		if handled, reply, err := a.tryHandleProtocolPrimary(ctx, uctx, sessionKey, msg.Content, userMsg, startTime, &metrics, activeWorkflow, protocolWorkflow); handled {
			return reply, err
		}
		if a.protocolMode == ProtocolModeLive {
			reply, err := a.handleProtocolFallback(ctx, uctx, sessionKey, msg.Content, userMsg, startTime, &metrics, protocolDraft, protocolValidation, activeWorkflow)
			return reply, err
		}
	}

	// 3. semantic router 为主决策入口（routeMode=live）
	if a.routeMode == string(RouteModeLive) {
		return a.chatWithSemanticRouter(ctx, uctx, sessionKey, msg, userMsg, startTime, &metrics)
	}

	// 4. legacy 路径（routeMode=shadow 或 off）
	return a.chatLegacy(ctx, uctx, sessionKey, msg, userMsg, startTime, &metrics)
}

// chatWithSemanticRouter 使用 semantic router 作为唯一决策入口的处理路径。
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

	// fallback：semantic router 返回了 tryHandleRoutePrimary 无法处理的 kind
	fallback := RouteDecision{
		Kind:        RouteClarify,
		ReasonCode:  "router_unhandled_kind",
		ClarifyCode: "ambiguous_intent",
		RouteSource: RouteSourceFallback,
	}
	metrics.Route.Kind = string(fallback.Kind)
	metrics.Route.ReasonCode = fallback.ReasonCode
	metrics.Route.Source = string(fallback.RouteSource)
	metrics.Route.ClarifyCode = fallback.ClarifyCode
	if handled, reply, err := a.tryHandleRoutePrimary(ctx, uctx, sessionKey, msg.Content, history, userMsg, startTime, beforeTask, metrics, fallback); handled {
		return reply, err
	}

	return "请再具体说明你要查询或操作的内容。", nil
}

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
	if uctx != nil && uctx.UserRole >= 1 {
		return "你好，我是课表助手。你可以直接让我查课表、考勤、请假；如果需要，也可以继续处理补签、统计和群订阅。"
	}
	return "你好，我是课表助手。你可以直接让我查课表、考勤或请假相关信息。"
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
		result, err := (taskContinueExecutor{agent: a}).Execute(ctx, currentTask, question, uctx)
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
		ActiveTaskType:          metrics.Task.ActiveTaskType,
		TaskStatusBefore:        metrics.Task.TaskStatusBefore,
		TaskStatusAfter:         metrics.Task.TaskStatusAfter,
		DomainResult:            string(metrics.DomainResult),
		DomainHint:              string(metrics.DomainHint),
		PlanKind:                string(metrics.PlanKind),
		KnowledgeStrength:       string(metrics.KnowledgeStrength),
		PlannerReason:           metrics.Planner.Reason,
		PlannerAction:           metrics.Planner.Action,
		PlannerConfidence:       metrics.Planner.Confidence,
		TaskID:                  metrics.Task.TaskID,
		TaskKeepOpen:            metrics.Task.TaskKeepOpen,
		TaskSwitch:              metrics.Task.TaskSwitch,
		LastErrorCode:           metrics.Task.LastErrorCode,
		ShadowPlannerAction:     metrics.Shadow.PlannerAction,
		ShadowPlannerMatched:    metrics.Shadow.PlannerMatched,
		RouteKind:               metrics.Route.Kind,
		RouteConfidence:         metrics.Route.Confidence,
		RouteReasonCode:         metrics.Route.ReasonCode,
		RouteSource:             metrics.Route.Source,
		ClarifyCode:             metrics.Route.ClarifyCode,
		SoftNoticeCode:          metrics.Route.SoftNoticeCode,
		ExecutorName:            metrics.Route.ExecutorName,
		ToolPool:                metrics.Route.ToolPool,
		RouterLatencyMs:         metrics.Route.RouterLatencyMs,
		ExecutorLatencyMs:       metrics.Route.ExecutorLatencyMs,
		ShadowRouteKind:         metrics.Shadow.RouteKind,
		ShadowRouteMatched:      metrics.Shadow.RouteMatched,
		ProtocolMode:            metrics.Proto.Mode,
		ProtocolAct:             metrics.Proto.Act,
		ProtocolDomain:          metrics.Proto.Domain,
		ProtocolOperation:       metrics.Proto.Operation,
		ProtocolValidationCode:  metrics.Proto.ValidationCode,
		WorkflowIDBefore:        metrics.Wf.IDBefore,
		WorkflowIDAfter:         metrics.Wf.IDAfter,
		ResponseKind:            metrics.ResponseKind,
		ExecutionAllowed:        metrics.Proto.ExecutionAllowed,
		AnswerMode:              string(metrics.AnswerMode),
		Question:                question,
		ToolsCalled:             toolsCalled,
		ToolCallCount:           len(toolsCalled),
		Reply:                   reply,
		SourceRefs:              append([]string(nil), metrics.SourceRefs...),
		RetrievalHitCount:       metrics.Retrieval.HitCount,
		RetrievalCandidateCount: metrics.Retrieval.CandidateCount,
		RetrievalTopRefs:        append([]string(nil), metrics.Retrieval.TopRefs...),
		RetrievalScores:         append([]int(nil), metrics.Retrieval.TopScores...),
		FollowUpMatchedSlots:    append([]string(nil), metrics.FollowUpMatchedSlots...),
		RetrievalFilteredReason: metrics.Retrieval.FilteredReason,
		KnowledgeDocTypes:       append([]string(nil), metrics.Retrieval.DocTypes...),
		RetrievalDurationMs:     metrics.Retrieval.DurationMs,
		LLMDurationMs:           metrics.LLMDurationMs,
		Rounds:                  rounds,
		DurationMs:              elapsedMs(startTime),
		Status:                  status,
		ErrorMsg:                errMsg,
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
	metrics.Proto.ExecutionAllowed = validation.AllowExecution
}

// tryHandleProtocolPrimary attempts to answer through the protocol-primary path.
func (a *Agent) tryHandleProtocolPrimary(ctx context.Context, uctx *tools.UserContext, sessionKey, question string, userMsg tools.Message, startTime time.Time, metrics *callMetrics, activeWorkflow *WorkflowSnapshot, workflowCtx *protocolWorkflowContext) (bool, string, error) {
	if a.protocolMode != ProtocolModeLive {
		return false, "", nil
	}

	draft := compileProtocol(protocolInput{
		Message:        question,
		ActiveWorkflow: workflowCtx,
	})
	validation := validateProtocol(draft, workflowCtx)
	applyProtocolMetrics(metrics, draft, validation)
	if activeWorkflow != nil {
		metrics.Wf.IDBefore = activeWorkflow.ID
	}

	switch draft.Operation {
	case "subscription.cancel":
		if !validation.AllowExecution || uctx == nil || uctx.ConversationType != "2" || a.deps.GroupSub == nil {
			return false, "", nil
		}
		if err := a.deps.GroupSub.Unsubscribe(ctx, uctx.TenantID, uctx.ConversationID); err != nil {
			return false, "", nil
		}
		reply := "已取消此群的考勤自动推送"
		metrics.ResponseKind = string(ResponseResult)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		metrics.Route.ExecutorName = "protocol_live_write"
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case "subscription.start", "subscription.list_departments":
		return a.handleProtocolSubscriptionPrimary(ctx, uctx, sessionKey, question, userMsg, startTime, metrics, draft, activeWorkflow)
	case "manual_sign.create":
		return a.handleProtocolManualSignPrimary(ctx, uctx, sessionKey, question, userMsg, startTime, metrics, draft, activeWorkflow)
	case "manual_sign.describe_capability":
		reply := buildManualSignCapabilityReply(uctx)
		metrics.ResponseKind = string(ResponseAnswer)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case "attendance.query_status":
		if !validation.AllowExecution || a.deps.Attendance == nil || a.deps.Semester == nil {
			return false, "", nil
		}

		trusted, ok := resolveAttendanceTrustedEntities(question)
		if !ok {
			reply := renderProtocolResponse(ResponseModel{
				Kind:          ResponseClarify,
				ClarifyReason: "missing_attendance_fields",
			})
			metrics.ResponseKind = string(ResponseClarify)
			metrics.AnswerMode = answerModeToolFirst
			metrics.QueryType = queryKindTool
			a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
			a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
			return true, reply, nil
		}

		req, blocked := buildOperationRequest(draft, trusted)
		if blocked {
			reply := renderProtocolResponse(ResponseModel{
				Kind:          ResponseClarify,
				ClarifyReason: "missing_attendance_fields",
			})
			metrics.ResponseKind = string(ResponseClarify)
			metrics.AnswerMode = answerModeToolFirst
			metrics.QueryType = queryKindTool
			a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
			a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
			return true, reply, nil
		}

		week, _, err := a.deps.Semester.GetCurrentWeek(ctx)
		if err != nil {
			return false, "", nil
		}
		dateStr, ok := extractParamString(req.TrustedParams, "date")
		if !ok {
			return false, "", nil
		}
		sectionInt, ok := extractParamInt(req.TrustedParams, "section")
		if !ok {
			return false, "", nil
		}
		result, err := a.deps.Attendance.GetAttendanceDetail(ctx, tools.AttendanceQuery{
			Date:    dateStr,
			Week:    week,
			Section: sectionInt,
		})
		if err != nil {
			return false, "", nil
		}

		reply := buildAttendanceStatusReply(result)
		metrics.ResponseKind = string(ResponseResult)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		metrics.Route.ExecutorName = "protocol_live_read"
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	default:
		return false, "", nil
	}
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
		MissingFields: append([]string(nil), workflow.MissingSlots...),
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

// buildAttendanceStatusReply builds the plain-text reply for an attendance query result.
func buildAttendanceStatusReply(result *tools.AttendanceResult) string {
	if result == nil {
		return "未查询到相关考勤数据。"
	}

	notArrivedLabel := "未到"
	if result.ViewMode == "current" {
		notArrivedLabel = "当前未到"
	}

	return fmt.Sprintf("%s第%d节考勤状态：应到%d人，正常%d人，迟到%d人，请假%d人，%s%d人。",
		result.Date,
		result.Section,
		result.ShouldAttend,
		result.OnTimeCount,
		result.LateCount,
		result.LeaveCount,
		notArrivedLabel,
		result.AbsentCount,
	)
}

// buildManualSignCapabilityReply builds the capability-only reply for manual sign questions.
func buildManualSignCapabilityReply(uctx *tools.UserContext) string {
	if uctx != nil && uctx.UserRole >= 1 {
		return "可以。你当前可以为指定用户代签某节次考勤，我需要明确的姓名、日期和节次。"
	}
	return "代签属于管理员能力，只有管理员可以为指定用户代签某节次考勤。"
}

// handleProtocolFallback returns a safe fallback reply when protocol primary cannot execute.
func (a *Agent) handleProtocolFallback(ctx context.Context, uctx *tools.UserContext, sessionKey, question string, userMsg tools.Message, startTime time.Time, metrics *callMetrics, draft ProtocolDraft, validation ProtocolValidationResult, activeWorkflow *WorkflowSnapshot) (string, error) {
	model := ResponseModel{
		Kind:          ResponseClarify,
		ClarifyReason: "unknown_intent",
	}
	answerMode := answerModeReject

	switch draft.Act {
	case ActCapabilityQuestion:
		model = ResponseModel{
			Kind:   ResponseAnswer,
			Answer: buildProtocolCapabilityReply(draft.Domain, uctx),
		}
		answerMode = answerModeToolFirst
	case ActHelp:
		model = ResponseModel{
			Kind:   ResponseAnswer,
			Answer: buildHelpReply(uctx),
		}
		answerMode = answerModeToolFirst
	case ActUnknown:
		if strings.TrimSpace(draft.ClarifyReason) != "" {
			model.ClarifyReason = draft.ClarifyReason
		}
	default:
		switch validation.ValidationCode {
		case "operation_not_allowed", "act_operation_mismatch", "read_query_cannot_write", "unsupported_act":
			model = ResponseModel{
				Kind:          ResponseRefuse,
				RefusalReason: "抱歉，我当前不能直接执行这个请求。",
			}
		}
	}

	reply := renderProtocolResponse(model)
	if activeWorkflow != nil {
		metrics.Wf.IDAfter = activeWorkflow.ID
	}
	metrics.ResponseKind = string(model.Kind)
	metrics.AnswerMode = answerMode
	metrics.QueryType = queryKindTool
	metrics.Route.ExecutorName = "protocol_live_guardrail"
	a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
	a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
	return reply, nil
}

// buildProtocolCapabilityReply builds the capability reply for a protocol domain question.
func buildProtocolCapabilityReply(domain BusinessDomain, uctx *tools.UserContext) string {
	switch domain {
	case DomainManualSign:
		return buildManualSignCapabilityReply(uctx)
	case DomainSubscription:
		return "我可以帮助开启、取消或查询当前群的考勤订阅。"
	case DomainAttendance:
		return "我可以帮助查询指定日期和节次的考勤状态。"
	case DomainSchedule:
		return "我可以帮助查询课表、空闲人员和节次时间。"
	case DomainLeave:
		return "我可以帮助查询请假信息。"
	case DomainAnalytics:
		return "我可以帮助查询考勤统计分析。"
	default:
		return buildHelpReply(uctx)
	}
}

type manualSignResolution struct {
	Trusted        trustedEntities
	UserInput      string
	UserResolution entityResolution
}

// handleProtocolManualSignPrimary runs the manual-sign protocol primary flow.
func (a *Agent) handleProtocolManualSignPrimary(ctx context.Context, uctx *tools.UserContext, sessionKey, question string, userMsg tools.Message, startTime time.Time, metrics *callMetrics, draft ProtocolDraft, activeWorkflow *WorkflowSnapshot) (bool, string, error) {
	if uctx == nil || a.deps.Attendance == nil || a.deps.User == nil {
		return false, "", nil
	}
	if uctx.UserRole < 1 {
		reply := buildManualSignCapabilityReply(uctx)
		metrics.ResponseKind = string(ResponseRefuse)
		metrics.AnswerMode = answerModeReject
		metrics.QueryType = queryKindTool
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	}

	workflow := cloneWorkflowSnapshot(activeWorkflow)
	if workflow == nil {
		if draft.Act != ActWriteRequest {
			return false, "", nil
		}
		started, ok := startWorkflow(draft)
		if !ok {
			return false, "", nil
		}
		workflow = &started
	}
	if workflow.Type != WorkflowManualSignCreate {
		return false, "", nil
	}

	resolution, err := a.resolveManualSignInput(ctx, question)
	if err != nil {
		return false, "", nil
	}

	result := continueWorkflow(*workflow, draft, resolution.Trusted)
	if result.Workflow == nil {
		result.Workflow = workflow
	}

	switch resolution.UserResolution.Status {
	case ResolveAmbiguous:
		a.sessions.setWorkflowState(sessionKey, result.Workflow)
		metrics.Wf.IDAfter = result.Workflow.ID
		metrics.ResponseKind = string(ResponseSelectOptions)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		reply := renderProtocolResponse(ResponseModel{
			Kind:    ResponseSelectOptions,
			Options: buildResponseOptions(resolution.UserResolution.Candidates),
		})
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case ResolveNotFound:
		if resolution.UserInput != "" {
			a.sessions.setWorkflowState(sessionKey, result.Workflow)
			metrics.Wf.IDAfter = result.Workflow.ID
			metrics.ResponseKind = string(ResponseClarify)
			metrics.AnswerMode = answerModeToolFirst
			metrics.QueryType = queryKindTool
			reply := fmt.Sprintf("找不到用户「%s」，请确认姓名。", resolution.UserInput)
			a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
			a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
			return true, reply, nil
		}
	}

	switch result.Decision {
	case WorkflowReadyToExecute:
		req, blocked := buildOperationRequest(draft, result.Workflow.Trusted)
		if blocked {
			reply := buildManualSignMissingFieldsReply(result.Workflow.MissingSlots)
			a.sessions.setWorkflowState(sessionKey, result.Workflow)
			metrics.Wf.IDAfter = result.Workflow.ID
			metrics.ResponseKind = string(ResponseClarify)
			metrics.AnswerMode = answerModeToolFirst
			metrics.QueryType = queryKindTool
			a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
			a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
			return true, reply, nil
		}
		dateStr, ok := extractParamString(req.TrustedParams, "date")
		if !ok {
			return false, "", nil
		}
		sectionInt, ok := extractParamInt(req.TrustedParams, "section")
		if !ok {
			return false, "", nil
		}
		userIDUint, ok := extractParamUint(req.TrustedParams, "user_id")
		if !ok {
			return false, "", nil
		}
		if err := a.deps.Attendance.SignForUsersBySlot(ctx, dateStr, sectionInt, []uint{userIDUint}); err != nil {
			return false, "", nil
		}
		a.sessions.clearWorkflowState(sessionKey)
		reply := fmt.Sprintf("已为%s补签 %s 第%d节考勤", result.Workflow.Trusted.UserName, dateStr, sectionInt)
		metrics.Wf.IDAfter = ""
		metrics.ResponseKind = string(ResponseResult)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		metrics.Route.ExecutorName = "protocol_live_write"
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case WorkflowContinueDecision, WorkflowRejectInvalidShape:
		a.sessions.setWorkflowState(sessionKey, result.Workflow)
		metrics.Wf.IDAfter = result.Workflow.ID
		metrics.ResponseKind = string(ResponseClarify)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		reply := buildManualSignMissingFieldsReply(result.Workflow.MissingSlots)
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	default:
		return false, "", nil
	}
}

// resolveManualSignInput resolves trusted manual-sign input fields from the current message.
func (a *Agent) resolveManualSignInput(ctx context.Context, message string) (manualSignResolution, error) {
	resolution := manualSignResolution{
		Trusted: trustedEntities{
			Date:    resolveDateFromMessage(message),
			Section: resolveSectionFromMessage(message),
		},
	}

	userName := extractManualSignUserName(message)
	if userName == "" {
		return resolution, nil
	}
	resolution.UserInput = userName

	users, err := a.deps.User.SearchByName(ctx, userName)
	if err != nil {
		return manualSignResolution{}, err
	}

	resolution.UserResolution = resolveUser(entityContext{
		Raw:   userName,
		Users: users,
	})
	if resolution.UserResolution.Status == ResolveResolved && resolution.UserResolution.User != nil {
		resolution.Trusted.UserID = resolution.UserResolution.User.ID
		resolution.Trusted.UserName = resolution.UserResolution.User.Name
	}
	return resolution, nil
}

// buildManualSignMissingFieldsReply builds the reply asking for missing manual-sign fields.
func buildManualSignMissingFieldsReply(missing []string) string {
	if len(missing) == 0 {
		return "请补充需要补签的姓名、日期和节次。"
	}
	names := make([]string, 0, len(missing))
	for _, field := range missing {
		switch field {
		case "user_id":
			names = append(names, "姓名")
		case "date":
			names = append(names, "日期")
		case "section":
			names = append(names, "节次")
		}
	}
	if len(names) == 0 {
		return "请补充需要补签的姓名、日期和节次。"
	}
	return fmt.Sprintf("我还缺少%s，请补充后我再帮你补签。", strings.Join(names, "和"))
}

// buildResponseOptions builds response options from a string candidate list.
func buildResponseOptions(candidates []string) []ResponseOption {
	options := make([]ResponseOption, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		options = append(options, ResponseOption{
			Label: candidate,
			Value: candidate,
		})
	}
	return options
}

// handleProtocolSubscriptionPrimary runs the subscription protocol primary flow.
func (a *Agent) handleProtocolSubscriptionPrimary(ctx context.Context, uctx *tools.UserContext, sessionKey, question string, userMsg tools.Message, startTime time.Time, metrics *callMetrics, draft ProtocolDraft, activeWorkflow *WorkflowSnapshot) (bool, string, error) {
	if uctx == nil || uctx.ConversationType != "2" || a.deps.GroupSub == nil {
		return false, "", nil
	}

	if activeWorkflow == nil {
		if draft.Act != ActWriteRequest || draft.Operation != "subscription.start" {
			return false, "", nil
		}
		workflow, ok := startWorkflow(draft)
		if !ok {
			return false, "", nil
		}
		a.sessions.setWorkflowState(sessionKey, &workflow)
		metrics.Wf.IDAfter = workflow.ID
		metrics.ResponseKind = string(ResponseClarify)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		reply := "需要先确认订阅范围。你可以回复“全部人员”，也可以回复“指定部门”。"
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	}

	if activeWorkflow.Type != WorkflowSubscriptionStart {
		return false, "", nil
	}

	if draft.Operation == "subscription.list_departments" {
		reply, err := a.buildProtocolDepartmentOptionsReply(ctx)
		if err != nil {
			return false, "", nil
		}
		metrics.Wf.IDAfter = activeWorkflow.ID
		metrics.ResponseKind = string(ResponseSelectOptions)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	}

	if draft.Act != ActWorkflowContinue {
		return false, "", nil
	}

	trusted, ok := a.resolveSubscriptionTrustedEntities(ctx, question, activeWorkflow)
	if !ok {
		reply := renderProtocolResponse(ResponseModel{
			Kind:          ResponseClarify,
			ClarifyReason: "subscription_missing_fields",
		})
		metrics.Wf.IDAfter = activeWorkflow.ID
		metrics.ResponseKind = string(ResponseClarify)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	}

	result := continueWorkflow(*activeWorkflow, draft, trusted)
	switch result.Decision {
	case WorkflowContinueDecision:
		if result.Workflow == nil {
			return false, "", nil
		}
		a.sessions.setWorkflowState(sessionKey, result.Workflow)
		metrics.Wf.IDAfter = result.Workflow.ID
		metrics.ResponseKind = string(ResponseClarify)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		reply, err := a.buildProtocolDepartmentOptionsReply(ctx)
		if err != nil {
			reply = "请告诉我需要订阅哪些部门。"
		}
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	case WorkflowReadyToExecute:
		if result.Workflow == nil {
			return false, "", nil
		}
		deptIDs := []int64(nil)
		if result.Workflow.Trusted.Scope == "department" && result.Workflow.Trusted.DepartmentID != 0 {
			deptIDs = []int64{result.Workflow.Trusted.DepartmentID}
		}
		if err := a.deps.GroupSub.Subscribe(ctx, uctx.TenantID, uctx.ConversationID, uctx.ConversationTitle, uctx.UserID, deptIDs); err != nil {
			return false, "", nil
		}
		a.sessions.clearWorkflowState(sessionKey)
		reply := "已为此群开启考勤推送"
		metrics.Wf.IDAfter = ""
		metrics.ResponseKind = string(ResponseResult)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	default:
		reply := renderProtocolResponse(ResponseModel{
			Kind:          ResponseClarify,
			ClarifyReason: "subscription_invalid_shape",
		})
		metrics.Wf.IDAfter = activeWorkflow.ID
		metrics.ResponseKind = string(ResponseClarify)
		metrics.AnswerMode = answerModeToolFirst
		metrics.QueryType = queryKindTool
		a.writeCallLog(ctx, uctx, question, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	}
}

// buildProtocolDepartmentOptionsReply builds the reply that lists selectable departments.
func (a *Agent) buildProtocolDepartmentOptionsReply(ctx context.Context) (string, error) {
	if a.deps.Dept == nil {
		return "请告诉我需要订阅哪些部门。", nil
	}
	depts, err := a.deps.Dept.ListDepts(ctx)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(depts))
	for _, dept := range depts {
		name := strings.TrimSpace(dept.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "当前暂无可选部门。", nil
	}
	return fmt.Sprintf("当前可选部门有：%s。请告诉我需要订阅哪些部门。", strings.Join(names, "、")), nil
}

// resolveSubscriptionTrustedEntities resolves trusted subscription fields from the current message.
func (a *Agent) resolveSubscriptionTrustedEntities(ctx context.Context, message string, workflow *WorkflowSnapshot) (trustedEntities, bool) {
	if workflow == nil {
		return trustedEntities{}, false
	}
	switch workflow.State {
	case WorkflowCollectScope:
		normalized := normalizeQuery(message)
		switch {
		case containsAny(normalized, []string{"全部人员", "全部"}):
			return trustedEntities{Scope: "all"}, true
		case containsAny(normalized, []string{"指定部门", "部分部门"}):
			return trustedEntities{Scope: "department"}, true
		default:
			return trustedEntities{}, false
		}
	case WorkflowCollectDepartments:
		if a.deps.Dept == nil {
			return trustedEntities{}, false
		}
		depts, err := a.deps.Dept.ListDepts(ctx)
		if err != nil {
			return trustedEntities{}, false
		}
		resolved := resolveDepartment(entityContext{
			Raw:         message,
			Departments: depts,
		})
		if resolved.Status != ResolveResolved || resolved.Department == nil {
			return trustedEntities{}, false
		}
		return trustedEntities{
			Scope:        "department",
			DepartmentID: resolved.Department.DeptID,
		}, true
	default:
		return trustedEntities{}, false
	}
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
func extractParamString(params map[string]any, key string) (string, bool) {
	if params == nil {
		return "", false
	}
	v, ok := params[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// extractParamInt safely extracts an int value from a params map.
func extractParamInt(params map[string]any, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	v, ok := params[key]
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
func extractParamUint(params map[string]any, key string) (uint, bool) {
	if params == nil {
		return 0, false
	}
	v, ok := params[key]
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

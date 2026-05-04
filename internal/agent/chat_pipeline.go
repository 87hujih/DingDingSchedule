package agent

import (
	"context"
	"fmt"
	"time"

	"schedule_server/internal/agent/tools"
	"schedule_server/internal/tenantctx"
	"schedule_server/pkg/dingtalk"
)

// chatAuth 是 resolveChatAuth 的返回值，包含认证后的上下文信息。
type chatAuth struct {
	ctx        context.Context
	uctx       *tools.UserContext
	sessionKey string
}

// resolveChatAuth 解析租户、用户身份，构建 session key 和限流检查。
// 返回空 reply 表示认证通过，非空 reply 表示需要直接返回（错误或拒绝）。
func (a *Agent) resolveChatAuth(ctx context.Context, msg *dingtalk.ChatMessage) (chatAuth, string) {
	// 通过 CorpID 确定租户并注入上下文
	tenantID, err := a.deps.Tenant.FindTenantIDByCorpID(ctx, msg.CorpID)
	if err != nil {
		a.deps.Logger.Errorw("查找租户失败", "corpID", msg.CorpID, "err", err)
		return chatAuth{}, "系统错误，请稍后重试"
	}
	if tenantID == 0 {
		a.deps.Logger.Infow("未找到对应企业，拒绝服务", "corpID", msg.CorpID)
		return chatAuth{}, "未找到对应的企业，请联系管理员"
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)

	// 查用户
	user, err := a.deps.User.FindByDingUserID(ctx, msg.SenderID)
	if err != nil {
		a.deps.Logger.Errorw("查找用户失败", "senderID", msg.SenderID, "err", err)
		return chatAuth{}, "系统错误，请稍后重试"
	}
	if user == nil {
		a.deps.Logger.Infow("用户未绑定账户，拒绝服务", "senderID", msg.SenderID)
		return chatAuth{}, "您尚未绑定账户，请先通过小程序登录"
	}

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

	var sessionKey string
	if msg.ConversationType == "2" {
		sessionKey = fmt.Sprintf("%d:%s:%s", user.TenantID, msg.ConversationID, msg.SenderID)
	} else {
		sessionKey = fmt.Sprintf("%d:%s", user.TenantID, msg.SenderID)
	}

	// 限流检查
	if !a.limiter.Allow(sessionKey) {
		a.deps.Logger.Infow("限流拦截", "user", user.Name, "tenantID", user.TenantID)
		return chatAuth{}, "你发消息太快了，请稍后再试"
	}

	return chatAuth{ctx: ctx, uctx: uctx, sessionKey: sessionKey}, ""
}

// handleConversationEvent 处理对话事件（取消、跟进、未知），返回 (handled, reply, error)。
func (a *Agent) handleConversationEvent(
	ctx context.Context,
	uctx *tools.UserContext,
	sessionKey string,
	msg *dingtalk.ChatMessage,
	userMsg tools.Message,
	startTime time.Time,
	metrics *callMetrics,
	activeTask *ActiveTask,
	beforeTask *ActiveTask,
	conversationDecision conversationDecision,
	toolsCalled []string,
) (bool, string, error) {
	switch conversationDecision.Event {
	case eventCancel:
		recordLegacyPlannerAction(metrics, plannerActionCancelTask)
		a.sessions.clearActiveTask(sessionKey)
		reply := "已取消当前任务。如需继续，请重新告诉我。"
		applyConversationMetrics(metrics, beforeTask, taskWithStatus(beforeTask, taskStatusCanceled), nil)
		metrics.DomainResult = domainIn
		metrics.PlanKind = planKindTool
		metrics.Planner.Reason = conversationDecision.Reason
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil

	case eventTaskFollowUp:
		recordLegacyPlannerAction(metrics, plannerActionContinueTask)
		fill := fillTaskSlots(activeTask, msg.Content)
		nextTask := applySlotFillToTask(activeTask, fill)
		reply, followUpTools, resultingTask, err := a.respondForTaskState(ctx, uctx, sessionKey, nextTask)
		if err != nil {
			applyConversationMetrics(metrics, beforeTask, resultingTaskOrFallback(resultingTask, nextTask), matchedSlotNames(fill))
			a.deps.Logger.Errorw("task follow-up 执行失败", "user", uctx.Name, "question", msg.Content, "err", err)
			a.writeCallLog(ctx, uctx, msg.Content, "", followUpTools, 0, startTime, "failed", err.Error(), *metrics)
			return true, "系统错误，请稍后重试", nil
		}
		afterTask := resultingTask
		if afterTask == nil && nextTask != nil && nextTask.Status == taskStatusReady {
			afterTask = taskWithStatus(nextTask, taskStatusCompleted)
		}
		applyConversationMetrics(metrics, beforeTask, afterTask, matchedSlotNames(fill))
		metrics.DomainResult = domainIn
		metrics.PlanKind = planKindContinueTask
		metrics.KnowledgeStrength = knowledgeStrengthNone
		metrics.Planner.Reason = conversationDecision.Reason
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		toolsCalled = append(toolsCalled, followUpTools...)
		a.writeCallLog(ctx, uctx, msg.Content, reply, toolsCalled, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil

	case eventUnknown:
		recordLegacyPlannerAction(metrics, plannerActionTaskMeta)
		reply := buildUnknownFollowUpReply(activeTask)
		applyConversationMetrics(metrics, beforeTask, beforeTask, nil)
		metrics.DomainResult = domainIn
		metrics.PlanKind = planKindClarify
		metrics.KnowledgeStrength = knowledgeStrengthNone
		metrics.Planner.Reason = conversationDecision.Reason
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return true, reply, nil
	}

	return false, "", nil
}

// handleWithKnowledgeAndPlan 处理知识检索 + 计划决策 + ReAct 循环的完整流程。
func (a *Agent) handleWithKnowledgeAndPlan(
	ctx context.Context,
	uctx *tools.UserContext,
	sessionKey string,
	msg *dingtalk.ChatMessage,
	userMsg tools.Message,
	startTime time.Time,
	metrics *callMetrics,
	history []tools.Message,
	normalized string,
	conversationDecision conversationDecision,
	beforeTask *ActiveTask,
) (string, error) {
	var toolsCalled []string

	// 知识检索
	taskCandidate := buildTaskFromRequest(msg.Content, uctx)
	retrievalResult := RetrievalResult{}
	if a.deps.Knowledge != nil && taskCandidate == nil {
		retrievalStart := time.Now()
		var retrievalErr error
		retrievalResult, retrievalErr = a.retrieveKnowledge(ctx, uctx.TenantID, msg.Content)
		metrics.Retrieval.DurationMs = elapsedMs(retrievalStart)
		if retrievalErr != nil {
			a.deps.Logger.Errorw("知识预检失败", "user", uctx.Name, "question", msg.Content, "err", retrievalErr)
			retrievalResult = RetrievalResult{}
		}
	}
	metrics.Retrieval.HitCount = len(retrievalResult.Hits)
	metrics.Retrieval.CandidateCount = retrievalResult.CandidateCount
	metrics.SourceRefs = append([]string(nil), retrievalResult.TopRefs...)
	metrics.Retrieval.TopRefs = append([]string(nil), retrievalResult.TopRefs...)
	metrics.Retrieval.TopScores = append([]int(nil), retrievalResult.TopScores...)
	metrics.Retrieval.FilteredReason = retrievalResult.FilteredReason
	metrics.Retrieval.DocTypes = append([]string(nil), retrievalResult.KnowledgeDocTypes...)

	// 计划决策
	planDecision := plan(PlanInput{
		Question:          msg.Content,
		UserContext:       uctx,
		History:           history,
		ActiveTask:        nil,
		ConversationEvent: conversationDecision,
		Retrieval:         retrievalResult,
		TaskCandidate:     taskCandidate,
		HasLiveSignal:     hasLiveSignal(normalized),
		HasRuleSignal:     hasRuleSignal(normalized),
		HasActionIntent:   hasActionIntent(normalized),
		HasClarifyIntent:  hasClarifyIntent(normalized),
		HasHelpIntent:     hasHelpIntent(normalized),
	})
	metrics.PlanKind = planDecision.Kind
	metrics.KnowledgeStrength = planDecision.KnowledgeStrength
	metrics.Planner.Reason = planDecision.ClarifyReason

	// 处理 clarify/tool 计划中带有 ActiveTask 的情况
	if (planDecision.Kind == planKindClarify || planDecision.Kind == planKindTool) && planDecision.ActiveTask != nil {
		recordLegacyPlannerAction(metrics, legacyTaskPlannerAction(beforeTask, planDecision.ActiveTask))
		reply, taskTools, resultingTask, err := a.respondForTaskState(ctx, uctx, sessionKey, planDecision.ActiveTask)
		if err != nil {
			applyConversationMetrics(metrics, beforeTask, resultingTaskOrFallback(resultingTask, planDecision.ActiveTask), nil)
			a.deps.Logger.Errorw("planner 执行失败", "user", uctx.Name, "question", msg.Content, "err", err)
			a.writeCallLog(ctx, uctx, msg.Content, "", taskTools, 0, startTime, "failed", err.Error(), *metrics)
			return "系统错误，请稍后重试", nil
		}
		afterTask := resultingTask
		if afterTask == nil && planDecision.ActiveTask.Status == taskStatusReady {
			afterTask = taskWithStatus(planDecision.ActiveTask, taskStatusCompleted)
		}
		applyConversationMetrics(metrics, beforeTask, afterTask, nil)
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		toolsCalled = append(toolsCalled, taskTools...)
		a.writeCallLog(ctx, uctx, msg.Content, reply, toolsCalled, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	if planDecision.Kind == planKindClarify {
		recordLegacyPlannerAction(metrics, plannerActionTaskMeta)
		reply := buildPlannerClarifyReply(planDecision.ClarifyReason, nil)
		metrics.QueryType = queryKindTool
		metrics.AnswerMode = answerModeToolFirst
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	// 确定 answerMode
	answerMode := answerModeToolFirst
	switch planDecision.Kind {
	case planKindRAG:
		answerMode = answerModeKnowledgeOnly
	case planKindMixed:
		answerMode = answerModeMixed
	}
	metrics.QueryType = modeToQueryKind(answerMode)
	metrics.AnswerMode = answerMode

	if answerMode == answerModeKnowledgeOnly && classifyKnowledgeStrength(retrievalResult) != knowledgeStrengthStrong {
		reply := buildPlannerClarifyReply("weak_knowledge_match", nil)
		a.writeCallLog(ctx, uctx, msg.Content, reply, nil, 0, startTime, "success", "", *metrics)
		a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: reply})
		return reply, nil
	}

	// 构建消息列表
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

	// 获取工具列表
	toolDefs := a.registry.ToToolDefs(uctx.UserRole)
	if answerMode == answerModeKnowledgeOnly {
		toolDefs = nil
	}

	// ReAct Loop
	onToolCall := func(toolName, args string) {
		a.deps.Logger.Infow("调用工具", "tool", toolName, "user", uctx.Name, "args", args)
	}
	loopResult, loopErr := runReactLoop(ctx, a.llmClient, a.registry, uctx, messages, toolDefs, onToolCall)
	metrics.LLMDurationMs = loopResult.LLMDuration
	toolsCalled = append(toolsCalled, loopResult.ToolsCalled...)
	if loopErr != nil {
		if loopErr.Error() == "超出最大轮数" {
			a.deps.Logger.Warnw("ReAct Loop 超出最大轮数", "sessionKey", sessionKey)
			a.writeCallLog(ctx, uctx, msg.Content, "", toolsCalled, loopResult.Rounds, startTime, "failed", "超出最大轮数", *metrics)
			return "处理轮次过多，请简化您的问题后重试", nil
		}
		a.deps.Logger.Errorw("LLM 调用失败", "rounds", loopResult.Rounds, "err", loopErr)
		a.writeCallLog(ctx, uctx, msg.Content, "", toolsCalled, loopResult.Rounds, startTime, "failed", loopErr.Error(), *metrics)
		return "AI 服务暂时不可用，请稍后重试", nil
	}

	a.deps.Logger.Infow("回复完成", "user", uctx.Name, "rounds", loopResult.Rounds, "reply", loopResult.Reply)
	a.writeCallLog(ctx, uctx, msg.Content, loopResult.Reply, toolsCalled, loopResult.Rounds, startTime, "success", "", *metrics)
	a.sessions.appendMessages(sessionKey, userMsg, tools.Message{Role: "assistant", Content: loopResult.Reply})
	return loopResult.Reply, nil
}

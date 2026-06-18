package agent

import (
	"context"
	"strings"
)

type protocolInput struct {
	Message        string
	ActiveWorkflow *protocolWorkflowContext
}

type protocolWorkflowContext struct {
	Type          string
	MissingFields []string
}

// compileProtocolWithCompiler delegates draft classification to an injected compiler.
func compileProtocolWithCompiler(ctx context.Context, input protocolInput, compiler IntentCompiler) (ProtocolDraft, error) {
	message := strings.TrimSpace(input.Message)
	if message == "" {
		return ProtocolDraft{Act: ActUnknown, Domain: DomainUnknown, Reason: "empty_message", ClarifyReason: "empty_message"}, nil
	}

	if draft, ok := compileWorkflowCancel(message, input.ActiveWorkflow); ok {
		return draft, nil
	}
	if compiler == nil {
		return unknownIntentDraft("intent_compiler_unavailable"), nil
	}
	draft, err := compiler.Compile(ctx, IntentCompileRequest{
		Message:        message,
		ActiveWorkflow: intentCompileWorkflowContext(input.ActiveWorkflow),
	})
	if err != nil {
		return ProtocolDraft{}, err
	}
	if draft.Act == ActUnknown && input.ActiveWorkflow != nil {
		fallback := compileProtocol(input)
		if fallback.Act != ActUnknown {
			return fallback, nil
		}
	}
	return draft, nil
}

func intentCompileWorkflowContext(workflow *protocolWorkflowContext) *IntentCompileWorkflowContext {
	if workflow == nil {
		return nil
	}
	return &IntentCompileWorkflowContext{
		Type:          workflow.Type,
		MissingFields: append([]string(nil), workflow.MissingFields...),
	}
}

// compileProtocol classifies a user message into the current protocol draft.
func compileProtocol(input protocolInput) ProtocolDraft {
	message := strings.TrimSpace(input.Message)
	if message == "" {
		return ProtocolDraft{Act: ActUnknown, Domain: DomainUnknown, ClarifyReason: "empty_message"}
	}

	if draft, ok := compileWorkflowCancel(message, input.ActiveWorkflow); ok {
		return draft
	}
	if draft, ok := compileWorkflowMeta(message, input.ActiveWorkflow); ok {
		return draft
	}

	if draft, ok := compileCapabilityQuestion(message); ok {
		return draft
	}
	if looksLikeManualSignCapabilityQuestion(message) {
		return ProtocolDraft{
			Act:       ActCapabilityQuestion,
			Domain:    DomainManualSign,
			Operation: "manual_sign.describe_capability",
		}
	}
	if draft, ok := compileRuleQuestion(message); ok {
		return draft
	}
	if draft, ok := compileScheduleReadQuery(message); ok {
		return draft
	}
	if looksLikeManualSignWriteRequest(message) {
		return ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainManualSign,
			Operation:  "manual_sign.create",
			Confidence: 1,
		}
	}

	if looksLikeSubscriptionStatusQuery(message) {
		return ProtocolDraft{
			Act:       ActReadQuery,
			Domain:    DomainSubscription,
			Operation: "subscription.query_status",
		}
	}

	if looksLikeAttendanceReadQuery(message) {
		return ProtocolDraft{
			Act:       ActReadQuery,
			Domain:    DomainAttendance,
			Operation: "attendance.query_status",
		}
	}

	if looksLikeSubscriptionCancelRequest(message) {
		return ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.cancel",
			Confidence: 1,
		}
	}
	if looksLikeSubscriptionWriteRequest(message) {
		return ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 1,
		}
	}

	if draft, ok := compileWorkflowContinue(message, input.ActiveWorkflow); ok {
		return draft
	}

	return ProtocolDraft{
		Act:           ActUnknown,
		Domain:        DomainUnknown,
		ClarifyReason: "unknown_intent",
	}
}

func compileCapabilityQuestion(message string) (ProtocolDraft, bool) {
	normalized := normalizeQuery(message)
	if !containsAny(normalized, []string{"有什么功能", "能做什么", "可以做什么", "支持什么", "能力", "功能"}) {
		return ProtocolDraft{}, false
	}
	candidates := []struct {
		keywords  []string
		domain    BusinessDomain
		operation string
	}{
		{[]string{"订阅", "推送"}, DomainSubscription, "subscription.describe_capability"},
		{[]string{"考勤"}, DomainAttendance, "attendance.describe_capability"},
		{[]string{"课表", "课程"}, DomainSchedule, "schedule.describe_capability"},
		{[]string{"补签", "代签"}, DomainManualSign, "manual_sign.describe_capability"},
	}
	for _, candidate := range candidates {
		if containsAny(normalized, candidate.keywords) {
			return ProtocolDraft{
				Act:        ActCapabilityQuestion,
				Domain:     candidate.domain,
				Operation:  candidate.operation,
				Confidence: 1,
			}, true
		}
	}
	return ProtocolDraft{}, false
}

// compileWorkflowMeta handles bounded read-only workflow meta questions that should not be treated as slot values.
func compileWorkflowMeta(message string, workflow *protocolWorkflowContext) (ProtocolDraft, bool) {
	if workflow == nil {
		return ProtocolDraft{}, false
	}
	if workflow.Type == "subscription.start" && looksLikeSubscriptionDepartmentListQuestion(message) {
		return ProtocolDraft{
			Act:       ActWorkflowContinue,
			Domain:    DomainSubscription,
			Operation: "subscription.list_departments",
		}, true
	}
	return ProtocolDraft{}, false
}

// compileWorkflowContinue handles compile workflow continue.
func compileWorkflowContinue(message string, workflow *protocolWorkflowContext) (ProtocolDraft, bool) {
	if workflow == nil {
		return ProtocolDraft{}, false
	}
	switch workflow.Type {
	case "subscription.start":
		if looksLikeSubscriptionDepartmentListQuestion(message) {
			return ProtocolDraft{
				Act:       ActWorkflowContinue,
				Domain:    DomainSubscription,
				Operation: "subscription.list_departments",
			}, true
		}
		if hasMissingField(workflow.MissingFields, "scope") {
			if containsAny(normalizeQuery(message), []string{"全部人员", "全部", "指定部门", "部分部门"}) {
				return ProtocolDraft{
					Act:       ActWorkflowContinue,
					Domain:    DomainSubscription,
					Operation: "subscription.start",
				}, true
			}
		}
		if hasMissingField(workflow.MissingFields, "dept_names") {
			if strings.Contains(message, "有哪些部门") || strings.Contains(message, "都有哪些部门") {
				return ProtocolDraft{
					Act:       ActWorkflowContinue,
					Domain:    DomainSubscription,
					Operation: "subscription.list_departments",
				}, true
			}
			if looksLikeEntityInput(message) {
				return ProtocolDraft{
					Act:       ActWorkflowContinue,
					Domain:    DomainSubscription,
					Operation: "subscription.start",
				}, true
			}
		}
	case "manual_sign.create":
		if hasMissingField(workflow.MissingFields, "user_id") && extractManualSignUserName(message) != "" {
			return ProtocolDraft{
				Act:       ActWorkflowContinue,
				Domain:    DomainManualSign,
				Operation: "manual_sign.create",
			}, true
		}
		if hasMissingField(workflow.MissingFields, "date") && hasDateSignal(message) {
			return ProtocolDraft{
				Act:       ActWorkflowContinue,
				Domain:    DomainManualSign,
				Operation: "manual_sign.create",
			}, true
		}
		if hasMissingField(workflow.MissingFields, "section") && hasSectionSignal(message) {
			return ProtocolDraft{
				Act:       ActWorkflowContinue,
				Domain:    DomainManualSign,
				Operation: "manual_sign.create",
			}, true
		}
	}
	return ProtocolDraft{}, false
}

// compileWorkflowCancel handles bounded deterministic workflow cancellation.
func compileWorkflowCancel(message string, workflow *protocolWorkflowContext) (ProtocolDraft, bool) {
	if workflow == nil {
		return ProtocolDraft{}, false
	}
	normalized := normalizeQuery(message)
	switch normalized {
	case "取消", "算了", "不用了", "停止", "退出":
	default:
		return ProtocolDraft{}, false
	}
	return ProtocolDraft{
		Act:       ActWorkflowCancel,
		Domain:    workflowDomain(workflow.Type),
		Operation: workflow.Type,
		Reason:    "workflow_cancel",
	}, true
}

func workflowDomain(workflowType string) BusinessDomain {
	if metadata, ok := lookupOperation(workflowType); ok {
		return metadata.Domain
	}
	switch {
	case strings.HasPrefix(workflowType, "subscription."):
		return DomainSubscription
	case strings.HasPrefix(workflowType, "manual_sign."):
		return DomainManualSign
	default:
		return DomainUnknown
	}
}

// hasMissingField reports whether it has missing field.
func hasMissingField(fields []string, target string) bool {
	for _, field := range fields {
		if field == target {
			return true
		}
	}
	return false
}

// looksLikeManualSignCapabilityQuestion reports whether it looks like manual sign capability question.
func looksLikeManualSignCapabilityQuestion(message string) bool {
	normalized := normalizeQuery(message)
	if !containsAny(normalized, []string{"代签", "补签"}) {
		return false
	}
	if hasManualSignActionSignal(normalized) && !containsAny(normalized, []string{"功能", "规则", "流程", "怎么", "如何", "是什么", "支持"}) {
		return false
	}
	return containsAny(normalized, []string{"功能", "可以", "能否", "能不能", "支持", "规则", "流程", "怎么", "如何", "是什么"})
}

// compileRuleQuestion classifies rule explanation questions by catalog domain.
func compileRuleQuestion(message string) (ProtocolDraft, bool) {
	normalized := normalizeQuery(message)
	if !hasRuleSignal(normalized) {
		return ProtocolDraft{}, false
	}

	var domain BusinessDomain
	var operation string
	switch {
	case containsAny(normalized, []string{"订阅", "推送"}):
		domain = DomainSubscription
		operation = "subscription.rule_explain"
	case containsAny(normalized, []string{"课表", "课程", "排课"}):
		domain = DomainSchedule
		operation = "schedule.rule_explain"
	case containsAny(normalized, []string{"考勤", "迟到", "缺勤", "未到", "打卡", "出勤", "旷课"}):
		domain = DomainAttendance
		operation = "attendance.rule_explain"
	default:
		return ProtocolDraft{}, false
	}

	return ProtocolDraft{
		Act:        ActRuleQuestion,
		Domain:     domain,
		Operation:  operation,
		Confidence: 1,
		Slots: map[string]SlotDraft{
			"rule_topic": {Field: "rule_topic", Raw: strings.TrimSpace(message)},
		},
	}, true
}

// compileScheduleReadQuery classifies schedule read questions covered by the operation catalog.
func compileScheduleReadQuery(message string) (ProtocolDraft, bool) {
	normalized := normalizeQuery(message)
	if !containsAny(normalized, []string{"课表", "课程"}) {
		return ProtocolDraft{}, false
	}
	if !containsAny(normalized, []string{"查", "查询", "看", "看看", "本周", "这周", "下周", "第"}) {
		return ProtocolDraft{}, false
	}

	userName := extractScheduleUserName(message)
	if userName != "" {
		return ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainSchedule,
			Operation:  "schedule.query_user_schedule",
			Confidence: 1,
			Slots: map[string]SlotDraft{
				"user_name": {Field: "user_name", Raw: userName},
			},
		}, true
	}
	return ProtocolDraft{
		Act:        ActReadQuery,
		Domain:     DomainSchedule,
		Operation:  "schedule.query_my_schedule",
		Confidence: 1,
	}, true
}

// looksLikeSubscriptionDepartmentListQuestion reports whether it asks for selectable subscription departments.
func looksLikeSubscriptionDepartmentListQuestion(message string) bool {
	normalized := normalizeQuery(message)
	return containsAny(normalized, []string{"有哪些部门", "都有哪些部门", "部门列表", "部门有哪些", "哪些部门"})
}

// looksLikeManualSignWriteRequest reports whether it looks like manual sign write request.
func looksLikeManualSignWriteRequest(message string) bool {
	if !strings.Contains(message, "代签") && !strings.Contains(message, "补签") {
		return false
	}
	return !looksLikeManualSignCapabilityQuestion(message)
}

// looksLikeAttendanceReadQuery reports whether it looks like attendance read query.
func looksLikeAttendanceReadQuery(message string) bool {
	if !strings.Contains(message, "考勤") {
		return false
	}
	return strings.Contains(message, "查询") || strings.Contains(message, "状态") || strings.Contains(message, "谁未到")
}

// looksLikeSubscriptionStatusQuery reports whether it asks for current group subscription status.
func looksLikeSubscriptionStatusQuery(message string) bool {
	normalized := normalizeQuery(message)
	statusSignal := containsAny(normalized, []string{"订阅状态", "有没有订阅", "是否订阅"})
	if !statusSignal && containsAny(normalized, []string{"有开", "开了没", "开没开"}) {
		statusSignal = containsAny(normalized, []string{"订阅", "推送"})
	}
	if !statusSignal {
		return false
	}
	return containsAny(normalized, []string{"本群", "当前群", "这个群", "此群", "群聊", "考勤", "推送"})
}

// looksLikeSubscriptionWriteRequest reports whether it looks like subscription write request.
func looksLikeSubscriptionWriteRequest(message string) bool {
	if !strings.Contains(message, "订阅") {
		return false
	}
	if looksLikeSubscriptionCancelRequest(message) {
		return false
	}
	return strings.Contains(message, "开启") || strings.Contains(message, "开通") || strings.Contains(message, "打开") || strings.Contains(message, "帮我")
}

// looksLikeSubscriptionCancelRequest reports whether it looks like subscription cancel request.
func looksLikeSubscriptionCancelRequest(message string) bool {
	if !strings.Contains(message, "订阅") && !strings.Contains(message, "推送") {
		return false
	}
	return strings.Contains(message, "取消") || strings.Contains(message, "关闭")
}

// hasDateSignal reports whether it has date signal.
func hasDateSignal(message string) bool {
	return strings.Contains(message, "今天") || strings.Contains(message, "昨天") || strings.Contains(message, "明天") || extractDateToken(message) != ""
}

// hasSectionSignal reports whether it has section signal.
func hasSectionSignal(message string) bool {
	for _, token := range []string{"第一节", "第二节", "第三节", "第四节", "第五节"} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

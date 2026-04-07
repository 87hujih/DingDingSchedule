package agent

import "strings"

type protocolInput struct {
	Message        string
	ActiveWorkflow *protocolWorkflowContext
}

type protocolWorkflowContext struct {
	Type          string
	MissingFields []string
}

func compileProtocol(input protocolInput) ProtocolDraft {
	message := strings.TrimSpace(input.Message)
	if message == "" {
		return ProtocolDraft{Act: ActUnknown, Domain: DomainUnknown, ClarifyReason: "empty_message"}
	}

	if looksLikeManualSignCapabilityQuestion(message) {
		return ProtocolDraft{
			Act:       ActCapabilityQuestion,
			Domain:    DomainManualSign,
			Operation: "manual_sign.describe_capability",
		}
	}
	if looksLikeManualSignWriteRequest(message) {
		return ProtocolDraft{
			Act:       ActWriteRequest,
			Domain:    DomainManualSign,
			Operation: "manual_sign.create",
		}
	}

	if looksLikeAttendanceReadQuery(message) {
		return ProtocolDraft{
			Act:       ActReadQuery,
			Domain:    DomainAttendance,
			Operation: "attendance.query_status",
		}
	}

	if looksLikeSubscriptionWriteRequest(message) {
		return ProtocolDraft{
			Act:       ActWriteRequest,
			Domain:    DomainSubscription,
			Operation: "subscription.start",
		}
	}
	if looksLikeSubscriptionCancelRequest(message) {
		return ProtocolDraft{
			Act:       ActWriteRequest,
			Domain:    DomainSubscription,
			Operation: "subscription.cancel",
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

func compileWorkflowContinue(message string, workflow *protocolWorkflowContext) (ProtocolDraft, bool) {
	if workflow == nil {
		return ProtocolDraft{}, false
	}
	switch workflow.Type {
	case "subscription.start":
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

func hasMissingField(fields []string, target string) bool {
	for _, field := range fields {
		if field == target {
			return true
		}
	}
	return false
}

func looksLikeManualSignCapabilityQuestion(message string) bool {
	if !strings.Contains(message, "代签") {
		return false
	}
	return strings.Contains(message, "功能") || strings.Contains(message, "可以") || strings.Contains(message, "能否") || strings.Contains(message, "能不能")
}

func looksLikeManualSignWriteRequest(message string) bool {
	if !strings.Contains(message, "代签") && !strings.Contains(message, "补签") {
		return false
	}
	return !looksLikeManualSignCapabilityQuestion(message)
}

func looksLikeAttendanceReadQuery(message string) bool {
	if !strings.Contains(message, "考勤") {
		return false
	}
	return strings.Contains(message, "查询") || strings.Contains(message, "状态") || strings.Contains(message, "谁未到")
}

func looksLikeSubscriptionWriteRequest(message string) bool {
	if !strings.Contains(message, "订阅") {
		return false
	}
	return strings.Contains(message, "开启") || strings.Contains(message, "开通") || strings.Contains(message, "打开") || strings.Contains(message, "帮我")
}

func looksLikeSubscriptionCancelRequest(message string) bool {
	if !strings.Contains(message, "订阅") && !strings.Contains(message, "推送") {
		return false
	}
	return strings.Contains(message, "取消") || strings.Contains(message, "关闭")
}

func hasDateSignal(message string) bool {
	return strings.Contains(message, "今天") || strings.Contains(message, "昨天") || strings.Contains(message, "明天") || extractDateToken(message) != ""
}

func hasSectionSignal(message string) bool {
	for _, token := range []string{"第一节", "第二节", "第三节", "第四节", "第五节"} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

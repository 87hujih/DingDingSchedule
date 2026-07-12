package agent

import (
	"strings"
	"time"
	"unicode/utf8"
)

const shortFollowUpMaxRunes = 32

type SystemIntent string

const (
	SystemIntentGreeting SystemIntent = "greeting"
	SystemIntentHelp     SystemIntent = "help"
)

type conversationEvent string

const (
	eventGreeting     conversationEvent = "greeting"
	eventTaskFollowUp conversationEvent = "task_follow_up"
	eventNewRequest   conversationEvent = "new_request"
	eventCancel       conversationEvent = "cancel"
	eventUnknown      conversationEvent = "unknown"
)

type conversationDecision struct {
	Event  conversationEvent
	Reason string
}

func interpretSystemIntent(message string) SystemIntent {
	switch normalizeQuery(message) {
	case "你好", "您好", "hello", "hi", "嗨", "哈喽", "早上好", "上午好", "中午好", "下午好", "晚上好":
		return SystemIntentGreeting
	case "你有什么功能", "有什么功能", "你能做什么", "能做什么", "怎么用你", "如何使用你", "你会什么":
		return SystemIntentHelp
	default:
		return ""
	}
}

// interpretConversation handles interpret conversation.
func interpretConversation(question string, task *ActiveTask) conversationDecision {
	normalized := normalizeQuery(question)
	if normalized == "" {
		return conversationDecision{Event: eventUnknown, Reason: "empty_message"}
	}

	if hasGreetingIntent(normalized) {
		return conversationDecision{Event: eventGreeting, Reason: "greeting"}
	}
	if task == nil || task.IsExpired(time.Now()) {
		return conversationDecision{Event: eventNewRequest, Reason: "no_active_task"}
	}
	if isCancelMessage(normalized) {
		return conversationDecision{Event: eventCancel, Reason: "cancel_message"}
	}
	if looksLikeTaskFollowUp(normalized, task) {
		return conversationDecision{Event: eventTaskFollowUp, Reason: "fits_active_task"}
	}
	if isClearlyNewRequest(normalized) {
		return conversationDecision{Event: eventNewRequest, Reason: "clear_business_request"}
	}
	return conversationDecision{Event: eventUnknown, Reason: "ambiguous_follow_up"}
}

// hasGreetingIntent reports whether it has greeting intent.
func hasGreetingIntent(question string) bool {
	return containsAny(question, []string{
		"你好",
		"您好",
		"hello",
		"hi",
		"嗨",
		"哈喽",
		"早上好",
		"上午好",
		"中午好",
		"下午好",
		"晚上好",
	})
}

// isCancelMessage reports whether it is cancel message.
func isCancelMessage(question string) bool {
	return containsAny(question, []string{
		"取消",
		"不用了",
		"算了",
		"先不用",
		"不需要了",
	})
}

// isClearlyNewRequest reports whether it is clearly new request.
func isClearlyNewRequest(question string) bool {
	return hasHelpIntent(question) ||
		hasClarifyIntent(question) ||
		hasActionIntent(question) ||
		hasLiveSignal(question) ||
		hasRuleSignal(question)
}

// looksLikeTaskFollowUp reports whether it looks like task follow up.
func looksLikeTaskFollowUp(question string, task *ActiveTask) bool {
	if task == nil {
		return false
	}
	if utf8.RuneCountInString(strings.TrimSpace(question)) > shortFollowUpMaxRunes {
		return false
	}
	if containsAny(question, []string{"嗯", "哦", "好的", "好", "收到", "ok", "okk"}) {
		return false
	}

	switch task.Type {
	case "subscribe_attendance_push":
		return looksLikeSubscriptionFollowUp(question, task)
	case "sign_for_user":
		return looksLikeManualSignFollowUp(question)
	default:
		return false
	}
}

// looksLikeSubscriptionFollowUp reports whether it looks like subscription follow up.
func looksLikeSubscriptionFollowUp(question string, task *ActiveTask) bool {
	missing := task.MissingSlots()
	if containsAny(question, []string{"全部", "全部人员", "指定部门", "部分部门"}) {
		return true
	}
	if containsAnySlot(missing, "scope") && !containsAny(question, []string{"订阅", "推送", "考勤", "查一下", "谁未到", "有什么功能"}) {
		return true
	}
	if containsAnySlot(missing, "dept_names") {
		return !containsAny(question, []string{"订阅", "推送", "考勤"})
	}
	return false
}

// looksLikeManualSignFollowUp reports whether it looks like manual sign follow up.
func looksLikeManualSignFollowUp(question string) bool {
	return containsAny(question, []string{"今天", "昨天", "明天", "第", "节"}) ||
		!containsAny(question, []string{"补签", "代签", "考勤"})
}

// containsAnySlot reports whether it contains any slot.
func containsAnySlot(slots []string, want string) bool {
	for _, slot := range slots {
		if slot == want {
			return true
		}
	}
	return false
}

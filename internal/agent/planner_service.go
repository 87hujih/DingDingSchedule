package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

var plannerChineseNumberPattern = regexp.MustCompile(`[零〇一二两三四五六七八九十百千]+`)

type PlannerInput struct {
	Message     string
	ActiveTask  *TaskInstance
	UserContext *tools.UserContext
}

func planConversation(input PlannerInput) PlannerDecision {
	normalized := normalizeQuery(input.Message)
	activeTask := activePlannerTask(input.ActiveTask)
	userIntent := strings.TrimSpace(input.Message)

	if normalized == "" {
		return PlannerDecision{
			Action:     plannerActionSocialRefuse,
			UserIntent: userIntent,
			Confidence: 0.4,
			Reason:     "empty_message",
		}
	}

	if activeTask != nil && isCancelMessage(normalized) {
		return PlannerDecision{
			Action:     plannerActionCancelTask,
			TaskType:   activeTask.Type,
			UserIntent: userIntent,
			Confidence: 0.99,
			Reason:     "cancel_message",
		}
	}

	if newDomainGate().Hint(normalized) == domainHintObviousOut {
		return PlannerDecision{
			Action:     plannerActionOffTopicReject,
			UserIntent: userIntent,
			Confidence: 0.98,
			Reason:     "obvious_out_of_domain",
		}
	}

	if hasGreetingIntent(normalized) || looksLikeSocialChat(normalized) {
		return PlannerDecision{
			Action:     plannerActionSocialRefuse,
			UserIntent: userIntent,
			Confidence: 0.8,
			Reason:     "social_chat",
		}
	}

	if activeTask != nil && isPlannerTaskMetaQuestion(normalized, activeTask) {
		return PlannerDecision{
			Action:         plannerActionTaskMeta,
			TaskType:       activeTask.Type,
			UserIntent:     userIntent,
			NeedsReplyOnly: true,
			KeepTaskOpen:   true,
			Confidence:     0.93,
			Reason:         "task_meta_question",
		}
	}

	if activeTask != nil {
		if slots, matchedSlots := plannerFollowUpSlots(input.Message, activeTask); len(matchedSlots) > 0 {
			return PlannerDecision{
				Action:     plannerActionContinueTask,
				TaskType:   activeTask.Type,
				UserIntent: userIntent,
				Slots:      slots,
				Confidence: 0.92,
				Reason:     "parsed_active_task_follow_up",
			}
		}
	}

	if activeTask != nil && looksLikeTaskFollowUp(normalized, legacyTaskFromTaskInstance(activeTask)) {
		return PlannerDecision{
			Action:     plannerActionContinueTask,
			TaskType:   activeTask.Type,
			UserIntent: userIntent,
			Slots:      clonePlannerSlots(activeTask.Slots),
			Confidence: 0.9,
			Reason:     "active_task_follow_up",
		}
	}

	if taskCandidate := buildTaskFromRequest(input.Message, input.UserContext); taskCandidate != nil {
		return PlannerDecision{
			Action:     plannerActionStartTask,
			TaskType:   taskCandidate.Type,
			UserIntent: userIntent,
			Slots:      clonePlannerSlots(taskCandidate.FilledSlots),
			SwitchTask: activeTask != nil && activeTask.Type != taskCandidate.Type,
			Confidence: 0.95,
			Reason:     "explicit_task_request",
		}
	}

	if hasRuleSignal(normalized) || hasLiveSignal(normalized) || hasActionIntent(normalized) || hasClarifyIntent(normalized) || hasHelpIntent(normalized) {
		return PlannerDecision{
			UserIntent: userIntent,
			Confidence: 0.35,
			Reason:     "defer_to_legacy",
		}
	}

	return PlannerDecision{
		UserIntent: userIntent,
		Confidence: 0.55,
		Reason:     "defer_to_legacy",
	}
}

func plannerTaskFromLegacyTask(sessionKey string, task *ActiveTask) *TaskInstance {
	if task == nil {
		return nil
	}

	taskID := ""
	if strings.TrimSpace(sessionKey) != "" {
		taskID = fmt.Sprintf("%s:%s", sessionKey, task.Type)
	}

	return &TaskInstance{
		ID:           taskID,
		Type:         task.Type,
		Status:       string(task.Status),
		Slots:        clonePlannerSlots(task.FilledSlots),
		MissingSlots: append([]string(nil), task.MissingSlots()...),
		UpdatedAt:    time.Now(),
		ExpiresAt:    task.ExpiresAt,
	}
}

func activePlannerTask(task *TaskInstance) *TaskInstance {
	if task == nil {
		return nil
	}
	if !task.ExpiresAt.IsZero() && !task.ExpiresAt.After(time.Now()) {
		return nil
	}
	return cloneTaskInstance(task)
}

func legacyTaskFromTaskInstance(task *TaskInstance) *ActiveTask {
	if task == nil {
		return nil
	}

	legacy := &ActiveTask{
		Type:          task.Type,
		Status:        taskStatus(task.Status),
		RequiredSlots: append([]string(nil), task.MissingSlots...),
		FilledSlots:   clonePlannerSlots(task.Slots),
		ExpiresAt:     task.ExpiresAt,
	}
	if legacy.FilledSlots == nil {
		legacy.FilledSlots = map[string]string{}
	}
	return legacy
}

func isPlannerTaskMetaQuestion(question string, task *TaskInstance) bool {
	if task == nil {
		return false
	}

	if containsAny(question, []string{
		"还缺什么",
		"还缺哪些",
		"缺什么信息",
		"为什么失败",
		"哪里失败",
		"怎么失败",
	}) {
		return true
	}

	return task.Type == "subscribe_attendance_push" && isDepartmentListQuestion(question)
}

func looksLikeSocialChat(question string) bool {
	return containsAny(question, []string{
		"最近怎么样",
		"最近咋样",
		"在吗",
		"忙吗",
		"聊聊",
		"哈哈",
		"哈喽",
	})
}

func clonePlannerSlots(slots map[string]string) map[string]string {
	if len(slots) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(slots))
	for key, value := range slots {
		cloned[key] = value
	}
	return cloned
}

func plannerFollowUpSlots(message string, task *TaskInstance) (map[string]string, []string) {
	if task == nil {
		return nil, nil
	}

	legacyTask := legacyTaskFromTaskInstance(task)
	fill := fillTaskSlots(legacyTask, message)
	slots := cloneTaskSlots(fill.Filled)
	matched := matchedSlotNames(fill)

	if task.Type == "subscribe_attendance_push" {
		if names := extractPlannerDeptNames(message, task); len(names) > 0 {
			if slots == nil {
				slots = cloneTaskSlots(task.Slots)
			}
			if slots == nil {
				slots = make(map[string]string, 2)
			}
			slots["scope"] = "department"
			slots["dept_names"] = strings.Join(names, "、")
			matched = mergeMatchedSlots(matched, []string{"scope", "dept_names"})
		}
	}
	if task.Type == "sign_for_user" {
		if name := extractPlannerUserNameHint(message); name != "" {
			if slots == nil {
				slots = cloneTaskSlots(task.Slots)
			}
			if slots == nil {
				slots = make(map[string]string, 3)
			}
			slots["user_name"] = name
			matched = mergeMatchedSlots(matched, []string{"user_name"})
		}
	}

	if len(matched) == 0 {
		return nil, nil
	}
	return slots, matched
}

func extractPlannerDeptNames(message string, task *TaskInstance) []string {
	if task == nil || task.Type != "subscribe_attendance_push" {
		return nil
	}
	if strings.TrimSpace(message) == "" || isDepartmentListQuestion(normalizeQuery(message)) {
		return nil
	}

	cached := cachedDepartmentNames(task)
	var matches []string
	if len(cached) > 0 {
		normalizedMessage := normalizePlannerDeptName(message)
		if normalizedMessage == "" {
			return nil
		}
		for _, name := range cached {
			if name == "" {
				continue
			}
			if strings.Contains(normalizedMessage, normalizePlannerDeptName(name)) {
				matches = append(matches, name)
			}
		}
	}
	if len(matches) == 0 {
		if hint := extractPlannerDeptHint(message); hint != "" {
			matches = append(matches, hint)
		}
	}
	return matches
}

func mergeMatchedSlots(existing []string, incoming []string) []string {
	merged := append([]string(nil), existing...)
	for _, slot := range incoming {
		found := false
		for _, current := range merged {
			if current == slot {
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, slot)
		}
	}
	return merged
}

func normalizePlannerDeptName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}

	replacer := strings.NewReplacer(
		" ", "",
		"\t", "",
		"\n", "",
		"\r", "",
		"（", "(",
		"）", ")",
	)
	normalized := strings.ToLower(replacer.Replace(trimmed))
	return plannerChineseNumberPattern.ReplaceAllStringFunc(normalized, func(part string) string {
		value, ok := parsePlannerChineseNumber(part)
		if !ok {
			return part
		}
		return strconv.Itoa(value)
	})
}

func parsePlannerChineseNumber(value string) (int, bool) {
	if value == "" {
		return 0, false
	}

	digits := map[rune]int{
		'零': 0,
		'〇': 0,
		'一': 1,
		'二': 2,
		'两': 2,
		'三': 3,
		'四': 4,
		'五': 5,
		'六': 6,
		'七': 7,
		'八': 8,
		'九': 9,
	}
	units := map[rune]int{
		'十': 10,
		'百': 100,
		'千': 1000,
	}

	total := 0
	number := 0
	sawDigit := false
	for _, r := range value {
		if digit, ok := digits[r]; ok {
			number = digit
			sawDigit = true
			continue
		}
		unit, ok := units[r]
		if !ok {
			return 0, false
		}
		if number == 0 {
			number = 1
		}
		total += number * unit
		number = 0
		sawDigit = false
	}

	if !sawDigit && total == 0 {
		return 0, false
	}
	return total + number, true
}

func extractPlannerDeptHint(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}

	candidate := trimmed
	if idx := strings.Index(candidate, "订阅"); idx >= 0 {
		candidate = candidate[idx+len("订阅"):]
	}
	if idx := strings.Index(candidate, "部门"); idx >= 0 {
		candidate = candidate[:idx]
	}
	replacer := strings.NewReplacer(
		"请帮我", "",
		"帮我", "",
		"给我", "",
		"麻烦", "",
		"一下", "",
		"这个", "",
		"考勤", "",
		"推送", "",
		"的", "",
		" ", "",
	)
	candidate = strings.TrimSpace(replacer.Replace(candidate))
	if candidate == "" {
		return ""
	}
	if containsAny(normalizeQuery(candidate), []string{"订阅", "推送", "考勤", "全部"}) {
		return ""
	}
	return candidate
}

func extractPlannerUserNameHint(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}

	candidate := trimmed
	if idx := strings.Index(candidate, "给"); idx >= 0 {
		candidate = candidate[idx+len("给"):]
	}
	if idx := strings.Index(candidate, "补签"); idx >= 0 {
		candidate = candidate[:idx]
	} else if idx := strings.Index(candidate, "代签"); idx >= 0 {
		candidate = candidate[:idx]
	}
	replacer := strings.NewReplacer(
		"请帮我", "",
		"帮我", "",
		"给我", "",
		"麻烦", "",
		"一下", "",
		"考勤", "",
		" ", "",
	)
	candidate = strings.TrimSpace(replacer.Replace(candidate))
	if candidate == "" {
		return ""
	}
	if containsAny(normalizeQuery(candidate), []string{"今天", "昨天", "明天", "第", "节", "补签", "代签"}) {
		return ""
	}
	return candidate
}

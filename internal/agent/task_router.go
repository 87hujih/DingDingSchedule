package agent

import (
	"time"

	"schedule_server/internal/agent/tools"
)

func buildTaskFromRequest(question string, uctx *tools.UserContext) *ActiveTask {
	normalized := normalizeQuery(question)
	expiresAt := time.Now().Add(sessionTTL)

	if shouldQuerySubscriptionStatus(normalized, uctx) {
		return &ActiveTask{
			Type:          "query_subscription_status",
			Status:        taskStatusReady,
			RequiredSlots: nil,
			FilledSlots:   map[string]string{},
			ExpiresAt:     expiresAt,
			LastPrompt:    "ready_query_subscription_status",
		}
	}

	if hasSubscriptionScopeIntent(normalized) && !containsQuotedOrEnumeratedDeptHints(question) {
		return &ActiveTask{
			Type:          "subscribe_attendance_push",
			Status:        taskStatusWaiting,
			RequiredSlots: []string{"dept_names"},
			FilledSlots:   map[string]string{"scope": "department"},
			ExpiresAt:     expiresAt,
			LastPrompt:    "clarify_dept_names",
		}
	}

	if hasSubscriptionActionSignal(normalized) {
		task := &ActiveTask{
			Type:          "subscribe_attendance_push",
			Status:        taskStatusWaiting,
			RequiredSlots: []string{"scope"},
			FilledSlots:   map[string]string{},
			ExpiresAt:     expiresAt,
			LastPrompt:    "clarify_scope",
		}
		return applySlotFillToTask(task, fillTaskSlots(task, question))
	}

	if hasManualSignActionSignal(normalized) {
		task := &ActiveTask{
			Type:          "sign_for_user",
			Status:        taskStatusWaiting,
			RequiredSlots: []string{"user_name", "date", "section"},
			FilledSlots:   map[string]string{},
			ExpiresAt:     expiresAt,
			LastPrompt:    "clarify_manual_sign",
		}
		return applySlotFillToTask(task, fillTaskSlots(task, question))
	}

	return nil
}

func applySlotFillToTask(task *ActiveTask, fill slotFillResult) *ActiveTask {
	if task == nil {
		return nil
	}

	next := cloneActiveTask(task)
	if next == nil {
		return nil
	}
	next.FilledSlots = fill.Filled

	switch next.Type {
	case "subscribe_attendance_push":
		scope := next.FilledSlots["scope"]
		switch scope {
		case "":
			next.RequiredSlots = []string{"scope"}
			next.Status = taskStatusWaiting
			next.LastPrompt = "clarify_scope"
		case "all":
			next.RequiredSlots = []string{"scope"}
			next.Status = taskStatusReady
			next.LastPrompt = "ready_subscribe_all"
		case "department":
			next.RequiredSlots = []string{"dept_names"}
			next.Status = taskStatusWaiting
			next.LastPrompt = "clarify_dept_names"
			if next.FilledSlots["dept_names"] != "" {
				next.RequiredSlots = []string{"scope", "dept_names"}
				next.Status = taskStatusReady
				next.LastPrompt = "ready_subscribe_dept"
			}
		}
	case "sign_for_user":
		next.RequiredSlots = []string{"user_name", "date", "section"}
		missing := missingSlots(next.RequiredSlots, next.FilledSlots)
		if len(missing) == 0 {
			next.Status = taskStatusReady
			next.LastPrompt = "ready_manual_sign"
		} else {
			next.Status = taskStatusWaiting
			next.LastPrompt = "clarify_" + missing[0]
		}
	case "query_subscription_status":
		next.Status = taskStatusReady
		next.LastPrompt = "ready_query_subscription_status"
	}

	return next
}

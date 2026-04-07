package agent

import "schedule_server/internal/agent/tools"

func detectShortCircuitRoute(question string, uctx *tools.UserContext, activeTask *TaskInstance) (RouteDecision, bool) {
	normalized := normalizeQuery(question)
	if shouldUnsubscribeAttendancePush(normalized, uctx) {
		decision := RouteDecision{
			Kind:           RouteTaskStart,
			Confidence:     0.99,
			ReasonCode:     "explicit_unsubscribe_subscription",
			TargetTaskType: "unsubscribe_attendance_push",
			RouteSource:    RouteSourceShortCircuit,
		}
		if activeTask != nil && activeTask.Type != "" && activeTask.Type != decision.TargetTaskType {
			decision.SwitchTask = true
			decision.SoftNoticeCode = "switched_task"
		}
		return decision, true
	}
	return RouteDecision{}, false
}

package agent

import "strings"

func composePlannerReply(decision PlannerDecision, task *TaskInstance, result *TaskResult) string {
	if result != nil && result.Outcome.Retryable {
		switch result.Outcome.ErrorCode {
		case "department_name_not_found":
			return "我没找到这个部门。你可以直接回复部门名称，也可以让我把当前可选部门列出来。"
		case "department_name_ambiguous":
			return "这个部门名存在歧义。你可以直接回复更完整的部门名称，或者让我把当前可选部门列出来。"
		case "user_name_ambiguous":
			return "我找到多个同名用户了，请提供更精确的姓名后我再继续补签。"
		case "user_name_not_found":
			return "我暂时没找到这个用户，请确认姓名后再发我一次。"
		}
	}

	switch decision.Action {
	case plannerActionOffTopicReject:
		return outOfDomainReply
	case plannerActionSocialRefuse:
		return "我主要帮助处理课表、考勤、请假、补签和订阅相关事务，其他闲聊我就不展开了。"
	case plannerActionCancelTask:
		return "已取消当前任务。如需继续，请重新告诉我。"
	case plannerActionTaskMeta:
		if reply := buildTaskMetaReply(task); strings.TrimSpace(reply) != "" {
			return reply
		}
	}

	if result != nil && strings.TrimSpace(result.Reply) != "" {
		return strings.TrimSpace(result.Reply)
	}
	if task != nil {
		return buildTaskClarifyReply(activeTaskFromTaskInstance(task))
	}
	return "请再具体说明你要查询或操作的内容。"
}

func buildTaskMetaReply(task *TaskInstance) string {
	if task == nil {
		return ""
	}
	if task.Type == "subscribe_attendance_push" {
		if reply := buildCachedDepartmentReply(task); reply != "" {
			return reply
		}
	}
	return buildTaskClarifyReply(activeTaskFromTaskInstance(task))
}

func composeRouteReply(decision RouteDecision, task *TaskRouteState) string {
	switch decision.Kind {
	case RouteOffTopicReject:
		return outOfDomainReply
	case RouteSocialRefuse:
		return "我主要帮助处理课表、考勤、请假、补签和订阅相关事务，其他闲聊我就不展开了。"
	case RouteClarify:
		if strings.TrimSpace(decision.ClarifyCode) == "ambiguous_intent" {
			return "我还没完全理解你的意思。你可以直接说明要查哪类课表、考勤、请假、补签或订阅问题。"
		}
	}

	if task != nil && task.Type != "" {
		return buildTaskClarifyReply(&ActiveTask{
			Type:          task.Type,
			Status:        taskStatus(task.Status),
			RequiredSlots: append([]string(nil), task.MissingSlots...),
			FilledSlots:   map[string]string{},
		})
	}

	return "请再具体说明你要查询或操作的内容。"
}

func composeSoftTaskNotice(code, reply string) string {
	reply = strings.TrimSpace(reply)
	switch strings.TrimSpace(code) {
	case "switched_task":
		if reply == "" {
			return "先切到你刚刚这个新请求。"
		}
		return "先切到你刚刚这个新请求。\n" + reply
	default:
		return reply
	}
}

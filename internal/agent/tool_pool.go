package agent

type toolPool struct {
	Name      string
	ToolNames []string
}

// legacy-only: selectToolPool chooses the old ReAct tool pool outside protocol_live.
func selectToolPool(question string, userRole int) toolPool {
	normalized := normalizeQuery(question)

	switch {
	case userRole >= 1 && hasAdminQuerySignal(normalized):
		return toolPool{
			Name: "admin_query",
			ToolNames: []string{
				"get_current_time",
				"subscribe_attendance_push",
				"unsubscribe_attendance_push",
				"query_subscription_status",
				"list_departments",
			},
		}
	case hasAnalyticsQuerySignal(normalized):
		return toolPool{
			Name: "analytics_live",
			ToolNames: []string{
				"get_current_time",
				"query_weekly_absence_ranking",
				"query_weekly_attendance_ranking",
				"query_attendance_stats",
				"query_user_cross",
			},
		}
	case hasScheduleQuerySignal(normalized) && !hasAttendanceQuerySignal(normalized):
		return toolPool{
			Name: "schedule_live",
			ToolNames: []string{
				"get_current_time",
				"query_my_schedule",
				"query_user_schedule",
				"query_free_users_by_slot",
				"query_schedule_info",
			},
		}
	case hasAttendanceQuerySignal(normalized):
		return toolPool{
			Name: "attendance_live",
			ToolNames: []string{
				"get_current_time",
				"query_attendance_status",
				"generate_attendance_text",
				"query_rest_days",
				"query_my_leave",
			},
		}
	default:
		return toolPool{
			Name: "general_live",
			ToolNames: []string{
				"get_current_time",
				"query_my_schedule",
				"query_user_schedule",
				"query_attendance_status",
				"query_schedule_info",
				"query_my_leave",
			},
		}
	}
}

// hasScheduleQuerySignal reports whether it has schedule query signal.
func hasScheduleQuerySignal(question string) bool {
	return containsAny(question, []string{
		"课表",
		"有课",
		"没课",
		"无课",
		"课程",
		"作息",
		"时间安排",
		"排课",
		"空闲",
		"空课",
	})
}

// hasAttendanceQuerySignal reports whether it has attendance query signal.
func hasAttendanceQuerySignal(question string) bool {
	return containsAny(question, []string{
		"考勤",
		"未到",
		"迟到",
		"缺勤",
		"请假",
		"休息日",
		"出勤",
		"打卡",
		"通报",
	})
}

// hasAnalyticsQuerySignal reports whether it has analytics query signal.
func hasAnalyticsQuerySignal(question string) bool {
	return containsAny(question, []string{
		"统计",
		"排行",
		"排名",
		"分析",
		"交叉",
		"同时满足",
		"出勤率",
		"超过",
	})
}

// hasAdminQuerySignal reports whether it has admin query signal.
func hasAdminQuerySignal(question string) bool {
	if isExplicitUnsubscribeAttendancePushText(question) {
		return true
	}
	return containsAny(question, []string{
		"订阅",
		"订阅状态",
		"推送状态",
		"部门列表",
		"有哪些部门",
		"可选部门",
	})
}

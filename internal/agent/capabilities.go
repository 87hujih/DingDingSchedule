package agent

import (
	"fmt"
	"strings"

	"schedule_server/internal/agent/tools"
)

type capability struct {
	Title             string
	Description       string
	ToolNames         []string
	MinRole           int
	ConversationScope string // dm, group, both
	FollowUpHint      string
}

// listCapabilities returns capabilities.
func listCapabilities() []capability {
	return []capability{
		{
			Title:             "课表查询",
			Description:       "查询自己的课表、指定姓名用户的课表、空闲人员和作息时间。",
			ToolNames:         []string{"query_my_schedule", "query_user_schedule", "query_free_users_by_slot", "query_schedule_info"},
			MinRole:           0,
			ConversationScope: "both",
		},
		{
			Title:             "考勤与请假查询",
			Description:       "查询考勤状态、请假记录、休息日和周排行。",
			ToolNames:         []string{"query_attendance_status", "query_my_leave", "query_rest_days", "query_weekly_absence_ranking", "query_weekly_attendance_ranking"},
			MinRole:           0,
			ConversationScope: "both",
		},
		{
			Title:             "人员交叉筛选",
			Description:       "按课表、缺勤和部门条件交叉查询人员。",
			ToolNames:         []string{"query_user_cross"},
			MinRole:           0,
			ConversationScope: "both",
		},
		{
			Title:             "考勤统计分析",
			Description:       "按周次、日期、用户或部门查看考勤统计。",
			ToolNames:         []string{"query_attendance_stats"},
			MinRole:           1,
			ConversationScope: "both",
		},
		{
			Title:             "管理员补签",
			Description:       "为指定用户补签某节次考勤。",
			ToolNames:         []string{"sign_for_user"},
			MinRole:           1,
			ConversationScope: "both",
		},
		{
			Title:             "群考勤订阅管理",
			Description:       "在群聊里开启、取消或查询考勤推送订阅，并可先查看可选部门。",
			ToolNames:         []string{"subscribe_attendance_push", "unsubscribe_attendance_push", "query_subscription_status", "list_departments"},
			MinRole:           1,
			ConversationScope: "group",
			FollowUpHint:      "如果要按部门订阅，我会先列出可选部门再让你确认。",
		},
	}
}

// filterCapabilities filters capabilities.
func filterCapabilities(caps []capability, role int, conversationType string) []capability {
	filtered := make([]capability, 0, len(caps))
	for _, cap := range caps {
		if role < cap.MinRole {
			continue
		}
		if !matchesConversationScope(cap.ConversationScope, conversationType) {
			continue
		}
		filtered = append(filtered, cap)
	}
	return filtered
}

// matchesConversationScope reports whether conversation scope matches.
func matchesConversationScope(scope string, conversationType string) bool {
	switch scope {
	case "both", "":
		return true
	case "dm":
		return conversationType != "2"
	case "group":
		return conversationType == "2"
	default:
		return false
	}
}

// buildHelpReply builds help reply.
func buildHelpReply(uctx *tools.UserContext) string {
	role := 0
	convType := "1"
	if uctx != nil {
		role = uctx.UserRole
		convType = uctx.ConversationType
	}

	allCaps := listCapabilities()
	currentCaps := filterCapabilities(allCaps, role, convType)

	var b strings.Builder
	b.WriteString("我可以帮助处理这些系统能力：\n")
	for _, cap := range allCaps {
		b.WriteString(fmt.Sprintf("- %s：%s\n", cap.Title, cap.Description))
	}

	b.WriteString("\n你当前在这个会话里可直接使用：\n")
	if len(currentCaps) == 0 {
		b.WriteString("- 当前没有可直接执行的聊天能力\n")
		return strings.TrimSpace(b.String())
	}

	for _, cap := range currentCaps {
		b.WriteString(fmt.Sprintf("- %s：%s\n", cap.Title, cap.Description))
		if cap.FollowUpHint != "" {
			b.WriteString(fmt.Sprintf("  提示：%s\n", cap.FollowUpHint))
		}
	}
	return strings.TrimSpace(b.String())
}

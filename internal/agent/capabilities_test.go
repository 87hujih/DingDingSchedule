package agent

import (
	"strings"
	"testing"

	"schedule_server/internal/agent/tools"
)

func TestBuildHelpReplyIncludesSystemOverviewAndCurrentAvailability(t *testing.T) {
	t.Parallel()

	reply := buildHelpReply(&tools.UserContext{
		UserRole:         1,
		ConversationType: "2",
	})

	if !strings.Contains(reply, "我可以帮助处理这些系统能力") {
		t.Fatalf("reply = %q, want system overview section", reply)
	}
	if !strings.Contains(reply, "你当前在这个会话里可直接使用") {
		t.Fatalf("reply = %q, want current availability section", reply)
	}
	if !strings.Contains(reply, "指定姓名用户的课表") {
		t.Fatalf("reply = %q, want other-user schedule capability", reply)
	}
}

func TestBuildGreetingReplyUsesAvailableCatalogCapabilitiesOnly(t *testing.T) {
	t.Parallel()

	reply := buildGreetingReply(&tools.UserContext{
		UserRole:         1,
		ConversationType: "2",
	})

	for _, unavailable := range []string{"请假", "统计", "分析", "周排行", "周排名"} {
		if strings.Contains(reply, unavailable) {
			t.Fatalf("reply = %q, should not advertise unavailable %q capability", reply, unavailable)
		}
	}
	if !strings.Contains(reply, "课表") || !strings.Contains(reply, "考勤") || !strings.Contains(reply, "订阅") {
		t.Fatalf("reply = %q, want available schedule, attendance and group subscription capabilities", reply)
	}
	if !strings.Contains(reply, "不在聊天中直接执行") {
		t.Fatalf("reply = %q, want explicit manual sign answer-only limitation", reply)
	}
}

func TestBuildGreetingReplyRespectsConversationRestrictions(t *testing.T) {
	t.Parallel()

	reply := buildGreetingReply(&tools.UserContext{
		UserRole:         1,
		ConversationType: "1",
	})
	if strings.Contains(reply, "订阅") {
		t.Fatalf("DM greeting = %q, should not advertise group-only subscription", reply)
	}
}

func TestCapabilitiesFilterHidesAdminGroupFeaturesForNormalDMUser(t *testing.T) {
	t.Parallel()

	caps := filterCapabilities(listCapabilities(), 0, "1")
	for _, cap := range caps {
		if hasAnyTool(cap.ToolNames, "subscribe_attendance_push", "query_attendance_stats") {
			t.Fatalf("normal DM user should not see admin-only capability: %+v", cap)
		}
	}
}

func TestCapabilitiesFilterShowsAdminGroupFeaturesForAdminInGroup(t *testing.T) {
	t.Parallel()

	caps := filterCapabilities(listCapabilities(), 1, "2")

	var sawSubscription bool
	var sawAnalytics bool
	for _, cap := range caps {
		if hasAnyTool(cap.ToolNames, "subscribe_attendance_push", "query_subscription_status") {
			sawSubscription = true
		}
		if hasAnyTool(cap.ToolNames, "query_attendance_stats") {
			sawAnalytics = true
		}
	}

	if !sawSubscription {
		t.Fatalf("admin in group should see subscription management capability")
	}
	if !sawAnalytics {
		t.Fatalf("admin in group should see attendance analytics capability")
	}
}

func hasAnyTool(actual []string, want ...string) bool {
	for _, item := range actual {
		for _, target := range want {
			if item == target {
				return true
			}
		}
	}
	return false
}

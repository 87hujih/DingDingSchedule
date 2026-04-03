package agent

import (
	"strings"
	"testing"

	"schedule_server/internal/agent/tools"
)

func TestClarifyPlanListsDepartmentsBeforeAskingForScopedSubscription(t *testing.T) {
	t.Parallel()

	plan := buildClarifyPlan("订阅指定部门考勤", &tools.UserContext{
		UserRole:         1,
		ConversationType: "2",
	})

	if !plan.NeedsToolLookup {
		t.Fatalf("NeedsToolLookup = false, want true")
	}
	if plan.ToolName != "list_departments" {
		t.Fatalf("ToolName = %q, want list_departments", plan.ToolName)
	}
	if !strings.Contains(plan.FollowUpPrompt, "需要订阅哪些部门") {
		t.Fatalf("FollowUpPrompt = %q, want department follow-up", plan.FollowUpPrompt)
	}
}

func TestClarifyPlanAsksForMissingDateAndSectionBeforeManualSign(t *testing.T) {
	t.Parallel()

	plan := buildClarifyPlan("给张三补签", &tools.UserContext{
		UserRole:         1,
		ConversationType: "1",
	})

	if plan.NeedsToolLookup {
		t.Fatalf("NeedsToolLookup = true, want false")
	}
	if plan.ToolName != "" {
		t.Fatalf("ToolName = %q, want empty", plan.ToolName)
	}
	if !strings.Contains(plan.FollowUpPrompt, "日期") || !strings.Contains(plan.FollowUpPrompt, "节次") {
		t.Fatalf("FollowUpPrompt = %q, want missing date and section prompt", plan.FollowUpPrompt)
	}
}

func TestClarifyPlanTreatsSubscriptionStatusInGroupAsDirectLookup(t *testing.T) {
	t.Parallel()

	plan := buildClarifyPlan("查这个群有没有订阅考勤推送", &tools.UserContext{
		UserRole:         1,
		ConversationType: "2",
	})

	if !plan.NeedsToolLookup {
		t.Fatalf("NeedsToolLookup = false, want true")
	}
	if plan.ToolName != "query_subscription_status" {
		t.Fatalf("ToolName = %q, want query_subscription_status", plan.ToolName)
	}
	if plan.FollowUpPrompt != "" {
		t.Fatalf("FollowUpPrompt = %q, want empty", plan.FollowUpPrompt)
	}
}

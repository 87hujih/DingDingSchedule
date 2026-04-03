package agent

import (
	"fmt"
	"regexp"
	"strings"

	"schedule_server/internal/agent/tools"
)

var datePattern = regexp.MustCompile(`\d{4}-\d{1,2}-\d{1,2}`)

type clarifyPlan struct {
	NeedsToolLookup bool
	ToolName        string
	ToolArguments   string
	FollowUpPrompt  string
}

func buildClarifyPlan(question string, uctx *tools.UserContext) clarifyPlan {
	normalized := normalizeQuery(question)

	if shouldQuerySubscriptionStatus(normalized, uctx) {
		return clarifyPlan{
			NeedsToolLookup: true,
			ToolName:        "query_subscription_status",
			ToolArguments:   `{}`,
		}
	}

	if hasSubscriptionScopeIntent(normalized) && !containsQuotedOrEnumeratedDeptHints(question) {
		return clarifyPlan{
			NeedsToolLookup: true,
			ToolName:        "list_departments",
			ToolArguments:   `{}`,
			FollowUpPrompt:  "我先列出当前可选部门。请告诉我需要订阅哪些部门。",
		}
	}

	if hasManualSignActionSignal(normalized) {
		missingFields := missingManualSignFields(question)
		if len(missingFields) > 0 {
			return clarifyPlan{
				FollowUpPrompt: fmt.Sprintf("请补充%s后，我再帮你补签。", strings.Join(missingFields, "和")),
			}
		}
	}

	return clarifyPlan{}
}

func shouldQuerySubscriptionStatus(question string, uctx *tools.UserContext) bool {
	return uctx != nil &&
		uctx.ConversationType == "2" &&
		containsAny(question, []string{"订阅状态", "有没有订阅", "是否订阅", "有开", "开了没", "开没开"}) &&
		containsAny(question, []string{"考勤", "推送"})
}

func missingManualSignFields(question string) []string {
	missing := make([]string, 0, 2)
	if !hasDateReference(question) {
		missing = append(missing, "日期")
	}
	if !hasSectionReference(question) {
		missing = append(missing, "节次")
	}
	return missing
}

func hasDateReference(question string) bool {
	normalized := normalizeQuery(question)
	if datePattern.MatchString(question) {
		return true
	}
	return containsAny(normalized, []string{"今天", "昨天", "明天"})
}

func hasSectionReference(question string) bool {
	normalized := normalizeQuery(question)
	return (strings.Contains(normalized, "第") && strings.Contains(normalized, "节")) ||
		containsAny(normalized, []string{"第一节", "第二节", "第三节", "第四节", "第五节"})
}

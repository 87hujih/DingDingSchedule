package agent

import (
	"strings"
	"testing"
)

func TestBuildTaskClarifyReplyForSubscriptionTaskMentionsAllowedOptions(t *testing.T) {
	t.Parallel()

	reply := buildTaskClarifyReply(&ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"scope"},
		FilledSlots:   map[string]string{},
		LastPrompt:    "clarify_scope",
	})

	if !strings.Contains(reply, "全部人员") || !strings.Contains(reply, "部门") {
		t.Fatalf("reply = %q, want subscription options", reply)
	}
}

func TestBuildTaskClarifyReplyForManualSignTaskListsMissingSlots(t *testing.T) {
	t.Parallel()

	reply := buildTaskClarifyReply(&ActiveTask{
		Type:          "sign_for_user",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"user_name", "date", "section"},
		FilledSlots:   map[string]string{"user_name": "张三"},
		LastPrompt:    "clarify_manual_sign",
	})

	if !strings.Contains(reply, "日期") || !strings.Contains(reply, "节次") {
		t.Fatalf("reply = %q, want missing date and section", reply)
	}
}

func TestBuildUnknownFollowUpReplyAvoidsOutOfDomainReply(t *testing.T) {
	t.Parallel()

	reply := buildUnknownFollowUpReply(&ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"scope"},
		FilledSlots:   map[string]string{},
		LastPrompt:    "clarify_scope",
	})

	if reply == outOfDomainReply {
		t.Fatalf("reply = %q, should not be out-of-domain reply", reply)
	}
	if !strings.Contains(reply, "全部人员") {
		t.Fatalf("reply = %q, want task-scoped guidance", reply)
	}
}

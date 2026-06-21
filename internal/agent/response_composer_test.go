package agent

import (
	"strings"
	"testing"
)

func TestResponseComposerRejectsOffTopicCodingRequest(t *testing.T) {
	t.Parallel()

	reply := composePlannerReply(PlannerDecision{Action: plannerActionOffTopicReject}, nil, nil)
	if !strings.Contains(reply, "考勤") {
		t.Fatalf("reply = %q, want domain rejection guidance", reply)
	}
}

func TestResponseComposerPolitelyRefusesSocialChat(t *testing.T) {
	t.Parallel()

	reply := composePlannerReply(PlannerDecision{Action: plannerActionSocialRefuse}, nil, nil)
	if !strings.Contains(reply, "课表") || !strings.Contains(reply, "考勤") {
		t.Fatalf("reply = %q, want polite domain-scoped refusal", reply)
	}
}

func TestResponseComposerKeepsTaskOpenForTaskMetaReply(t *testing.T) {
	t.Parallel()

	reply := composePlannerReply(PlannerDecision{
		Action:       plannerActionTaskMeta,
		KeepTaskOpen: true,
	}, &TaskInstance{
		Type: "subscribe_attendance_push",
		CandidateCache: map[string]any{
			"departments": []string{"家族7期", "乐知全栈一期"},
		},
		MissingSlots: []string{"dept_names"},
	}, nil)
	if !strings.Contains(reply, "家族7期") || !strings.Contains(reply, "乐知全栈一期") {
		t.Fatalf("reply = %q, want cached department list", reply)
	}
}

func TestResponseComposerRendersRetryableTaskError(t *testing.T) {
	t.Parallel()

	reply := composePlannerReply(PlannerDecision{Action: plannerActionContinueTask}, &TaskInstance{
		Type:          "subscribe_attendance_push",
		LastErrorCode: "department_name_not_found",
	}, &TaskResult{
		Outcome: ToolOutcome{
			OK:        false,
			ErrorCode: "department_name_not_found",
			Retryable: true,
		},
	})
	if !strings.Contains(reply, "部门") || !strings.Contains(reply, "可选部门") {
		t.Fatalf("reply = %q, want correction prompt for retryable department error", reply)
	}
}

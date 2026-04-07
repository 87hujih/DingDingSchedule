package agent

import (
	"testing"

	"schedule_server/internal/agent/tools"
)

func TestBuildRouteContextSummarizesTaskInstance(t *testing.T) {
	t.Parallel()

	history := []tools.Message{
		{Role: "user", Content: "开启考勤订阅"},
		{Role: "assistant", Content: "需要先确认订阅范围。"},
		{Role: "user", Content: "指定部门"},
		{Role: "assistant", Content: "请直接回复需要订阅的部门名称。"},
	}

	task := &TaskInstance{
		ID:            "task-1",
		Type:          "subscribe_attendance_push",
		Status:        "waiting_slots",
		MissingSlots:  []string{"dept_names"},
		LastErrorCode: "department_name_not_found",
		CandidateCache: map[string]any{
			"departments": []string{"家族7期", "乐知全栈一期", "教务处"},
		},
	}

	ctx := buildRouteContext("现在都有哪些部门", &tools.UserContext{
		UserRole:          1,
		ConversationType:  "2",
		ConversationTitle: "考勤群",
	}, history, task)

	if ctx.Message != "现在都有哪些部门" {
		t.Fatalf("Message = %q, want 现在都有哪些部门", ctx.Message)
	}
	if ctx.ActiveTask == nil {
		t.Fatalf("ActiveTask = nil, want summarized task")
	}
	if ctx.ActiveTask.ID != "task-1" {
		t.Fatalf("ActiveTask.ID = %q, want task-1", ctx.ActiveTask.ID)
	}
	if ctx.ActiveTask.Type != "subscribe_attendance_push" {
		t.Fatalf("ActiveTask.Type = %q, want subscribe_attendance_push", ctx.ActiveTask.Type)
	}
	if ctx.ActiveTask.LastErrorCode != "department_name_not_found" {
		t.Fatalf("ActiveTask.LastErrorCode = %q, want department_name_not_found", ctx.ActiveTask.LastErrorCode)
	}
	if len(ctx.ActiveTask.CandidateHints) == 0 {
		t.Fatalf("CandidateHints = empty, want summarized candidates")
	}
	if len(ctx.RecentTurns) != 4 {
		t.Fatalf("RecentTurns = %d, want 4", len(ctx.RecentTurns))
	}
}

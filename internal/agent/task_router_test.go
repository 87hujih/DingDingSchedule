package agent

import (
	"testing"

	"schedule_server/internal/agent/tools"
)

func TestBuildTaskFromRequestCreatesSubscriptionTask(t *testing.T) {
	t.Parallel()

	task := buildTaskFromRequest("开启考勤订阅", &tools.UserContext{
		ConversationType: "2",
	})
	if task == nil {
		t.Fatalf("task = nil, want value")
	}
	if task.Type != "subscribe_attendance_push" {
		t.Fatalf("task type = %q, want subscribe_attendance_push", task.Type)
	}
	if task.Status != taskStatusWaiting {
		t.Fatalf("task status = %q, want %q", task.Status, taskStatusWaiting)
	}
	if len(task.MissingSlots()) != 1 || task.MissingSlots()[0] != "scope" {
		t.Fatalf("missing slots = %v, want [scope]", task.MissingSlots())
	}
}

func TestBuildTaskFromRequestCreatesManualSignTask(t *testing.T) {
	t.Parallel()

	task := buildTaskFromRequest("帮我补签", &tools.UserContext{
		ConversationType: "1",
	})
	if task == nil {
		t.Fatalf("task = nil, want value")
	}
	if task.Type != "sign_for_user" {
		t.Fatalf("task type = %q, want sign_for_user", task.Type)
	}
	if got := task.MissingSlots(); len(got) != 3 {
		t.Fatalf("missing slots = %v, want 3 slots", got)
	}
}

func TestBuildTaskFromRequestCreatesSubscriptionStatusTaskInGroup(t *testing.T) {
	t.Parallel()

	task := buildTaskFromRequest("查这个群有没有订阅考勤推送", &tools.UserContext{
		ConversationType: "2",
	})
	if task == nil {
		t.Fatalf("task = nil, want value")
	}
	if task.Type != "query_subscription_status" {
		t.Fatalf("task type = %q, want query_subscription_status", task.Type)
	}
	if task.Status != taskStatusReady {
		t.Fatalf("task status = %q, want %q", task.Status, taskStatusReady)
	}
}

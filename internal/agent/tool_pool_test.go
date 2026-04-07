package agent

import (
	"testing"
)

func TestToolPoolSelectorRestrictsAttendanceQuestionToAttendanceTools(t *testing.T) {
	t.Parallel()

	pool := selectToolPool("今天第一节谁未到", 1)
	if pool.Name == "" {
		t.Fatalf("pool.Name = empty, want named pool")
	}
	if toolPoolContains(pool.ToolNames, "subscribe_attendance_push") {
		t.Fatalf("tool pool leaked subscription tool into attendance live query: %v", pool.ToolNames)
	}
	if !toolPoolContains(pool.ToolNames, "query_attendance_status") {
		t.Fatalf("tool pool missing attendance tool: %v", pool.ToolNames)
	}
}

func TestToolPoolSelectorRestrictsScheduleQuestionToScheduleTools(t *testing.T) {
	t.Parallel()

	pool := selectToolPool("我这周三下午有课吗", 1)
	if !toolPoolContains(pool.ToolNames, "query_my_schedule") {
		t.Fatalf("tool pool missing schedule tool: %v", pool.ToolNames)
	}
	if toolPoolContains(pool.ToolNames, "subscribe_attendance_push") {
		t.Fatalf("tool pool leaked subscription tool into schedule live query: %v", pool.ToolNames)
	}
}

func toolPoolContains(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

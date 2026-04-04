package agent

import (
	"reflect"
	"testing"
)

func TestFillTaskSlotsAcceptsDepartmentOnlyReply(t *testing.T) {
	t.Parallel()

	result := fillTaskSlots(&ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"dept_names"},
		FilledSlots:   map[string]string{"scope": "department"},
	}, "信工24级")

	if result.Filled["dept_names"] != "信工24级" {
		t.Fatalf("dept_names = %q, want 信工24级", result.Filled["dept_names"])
	}
	if !reflect.DeepEqual(result.MatchedSlots, []string{"dept_names", "scope"}) {
		t.Fatalf("matched slots = %v, want [dept_names scope]", result.MatchedSlots)
	}
	if !result.Ready {
		t.Fatalf("ready = false, want true")
	}
}

func TestFillTaskSlotsAcceptsAllUsersScopeReply(t *testing.T) {
	t.Parallel()

	result := fillTaskSlots(&ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"scope"},
		FilledSlots:   map[string]string{},
	}, "全部人员")

	if result.Filled["scope"] != "all" {
		t.Fatalf("scope = %q, want all", result.Filled["scope"])
	}
	if !reflect.DeepEqual(result.MatchedSlots, []string{"scope"}) {
		t.Fatalf("matched slots = %v, want [scope]", result.MatchedSlots)
	}
	if !result.Ready {
		t.Fatalf("ready = false, want true")
	}
}

func TestFillTaskSlotsAcceptsDateAndSectionInSingleReply(t *testing.T) {
	t.Parallel()

	result := fillTaskSlots(&ActiveTask{
		Type:          "sign_for_user",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"user_name", "date", "section"},
		FilledSlots:   map[string]string{"user_name": "张三"},
	}, "今天第一节")

	if result.Filled["date"] != "today" {
		t.Fatalf("date = %q, want today", result.Filled["date"])
	}
	if result.Filled["section"] != "1" {
		t.Fatalf("section = %q, want 1", result.Filled["section"])
	}
	if !reflect.DeepEqual(result.MatchedSlots, []string{"date", "section"}) {
		t.Fatalf("matched slots = %v, want [date section]", result.MatchedSlots)
	}
	if !result.Ready {
		t.Fatalf("ready = false, want true")
	}
}

func TestFillTaskSlotsLeavesTaskWaitingWhenReplyStillIncomplete(t *testing.T) {
	t.Parallel()

	result := fillTaskSlots(&ActiveTask{
		Type:          "sign_for_user",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"user_name", "date", "section"},
		FilledSlots:   map[string]string{"user_name": "张三"},
	}, "今天")

	if result.Filled["date"] != "today" {
		t.Fatalf("date = %q, want today", result.Filled["date"])
	}
	if _, ok := result.Filled["section"]; ok {
		t.Fatalf("section should not be filled, got %q", result.Filled["section"])
	}
	if !reflect.DeepEqual(result.MatchedSlots, []string{"date"}) {
		t.Fatalf("matched slots = %v, want [date]", result.MatchedSlots)
	}
	if result.Ready {
		t.Fatalf("ready = true, want false")
	}
}

func TestFillTaskSlotsDoesNotTreatDepartmentListQuestionAsDeptName(t *testing.T) {
	t.Parallel()

	result := fillTaskSlots(&ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"dept_names"},
		FilledSlots:   map[string]string{"scope": "department"},
	}, "现在都有哪些部门")

	if _, ok := result.Filled["dept_names"]; ok {
		t.Fatalf("dept_names should stay empty when user is asking for department list, got %q", result.Filled["dept_names"])
	}
	if len(result.MatchedSlots) != 0 {
		t.Fatalf("matched slots = %v, want none", result.MatchedSlots)
	}
	if result.Ready {
		t.Fatalf("ready = true, want false")
	}
}

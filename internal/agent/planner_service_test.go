package agent

import "testing"

func TestPlannerReturnsTaskMetaForDepartmentListQuestionDuringSubscription(t *testing.T) {
	t.Parallel()

	decision := planConversation(PlannerInput{
		Message: "现在有哪些部门",
		ActiveTask: &TaskInstance{
			Type:   "subscribe_attendance_push",
			Status: "waiting_slots",
		},
	})

	if decision.Action != plannerActionTaskMeta {
		t.Fatalf("Action = %q, want %q", decision.Action, plannerActionTaskMeta)
	}
	if !decision.KeepTaskOpen {
		t.Fatalf("KeepTaskOpen = false, want true")
	}
}

func TestPlannerRejectsOffTopicCodingQuestion(t *testing.T) {
	t.Parallel()

	decision := planConversation(PlannerInput{
		Message: "帮我写一个二分查找",
	})

	if decision.Action != plannerActionOffTopicReject {
		t.Fatalf("Action = %q, want %q", decision.Action, plannerActionOffTopicReject)
	}
}

func TestPlannerReturnsSocialRefuseForGenericSocialChat(t *testing.T) {
	t.Parallel()

	decision := planConversation(PlannerInput{
		Message: "最近怎么样",
	})

	if decision.Action != plannerActionSocialRefuse {
		t.Fatalf("Action = %q, want %q", decision.Action, plannerActionSocialRefuse)
	}
}

func TestPlannerReturnsContinueTaskForShortSubscriptionFollowUp(t *testing.T) {
	t.Parallel()

	decision := planConversation(PlannerInput{
		Message: "家族7期",
		ActiveTask: &TaskInstance{
			Type:         "subscribe_attendance_push",
			Status:       "waiting_slots",
			MissingSlots: []string{"dept_names"},
			Slots:        map[string]string{"scope": "department"},
		},
	})

	if decision.Action != plannerActionContinueTask {
		t.Fatalf("Action = %q, want %q", decision.Action, plannerActionContinueTask)
	}
	if decision.TaskType != "subscribe_attendance_push" {
		t.Fatalf("TaskType = %q, want subscribe_attendance_push", decision.TaskType)
	}
}

func TestPlannerReturnsContinueTaskForLongSubscriptionFollowUp(t *testing.T) {
	t.Parallel()

	decision := planConversation(PlannerInput{
		Message: "请帮我订阅家族7期这个部门的考勤推送",
		ActiveTask: &TaskInstance{
			Type:         "subscribe_attendance_push",
			Status:       "waiting_slots",
			MissingSlots: []string{"dept_names"},
			Slots:        map[string]string{"scope": "department"},
			CandidateCache: map[string]any{
				"departments": []string{"家族7期", "乐知全栈一期"},
			},
		},
	})

	if decision.Action != plannerActionContinueTask {
		t.Fatalf("Action = %q, want %q", decision.Action, plannerActionContinueTask)
	}
	if decision.TaskType != "subscribe_attendance_push" {
		t.Fatalf("TaskType = %q, want subscribe_attendance_push", decision.TaskType)
	}
}

func TestPlannerReturnsContinueTaskForLongSubscriptionFollowUpWithoutCachedDepartments(t *testing.T) {
	t.Parallel()

	decision := planConversation(PlannerInput{
		Message: "请帮我订阅家族7期这个部门的考勤推送",
		ActiveTask: &TaskInstance{
			Type:         "subscribe_attendance_push",
			Status:       "waiting_slots",
			MissingSlots: []string{"scope"},
			Slots:        map[string]string{},
		},
	})

	if decision.Action != plannerActionContinueTask {
		t.Fatalf("Action = %q, want %q", decision.Action, plannerActionContinueTask)
	}
	if decision.Slots["scope"] != "department" {
		t.Fatalf("scope = %q, want department", decision.Slots["scope"])
	}
	if decision.Slots["dept_names"] != "家族7期" {
		t.Fatalf("dept_names = %q, want 家族7期", decision.Slots["dept_names"])
	}
}

func TestPlannerReturnsStartTaskForExplicitSubscriptionRequest(t *testing.T) {
	t.Parallel()

	decision := planConversation(PlannerInput{
		Message: "开启考勤订阅",
	})

	if decision.Action != plannerActionStartTask {
		t.Fatalf("Action = %q, want %q", decision.Action, plannerActionStartTask)
	}
	if decision.TaskType != "subscribe_attendance_push" {
		t.Fatalf("TaskType = %q, want subscribe_attendance_push", decision.TaskType)
	}
}

func TestPlannerReturnsCancelTaskForExplicitCancellation(t *testing.T) {
	t.Parallel()

	decision := planConversation(PlannerInput{
		Message: "取消",
		ActiveTask: &TaskInstance{
			Type:   "subscribe_attendance_push",
			Status: "waiting_slots",
		},
	})

	if decision.Action != plannerActionCancelTask {
		t.Fatalf("Action = %q, want %q", decision.Action, plannerActionCancelTask)
	}
}

func TestPlannerReturnsContinueTaskForLongManualSignFollowUp(t *testing.T) {
	t.Parallel()

	decision := planConversation(PlannerInput{
		Message: "请帮我给张三补签今天第一节考勤",
		ActiveTask: &TaskInstance{
			Type:         "sign_for_user",
			Status:       "waiting_slots",
			MissingSlots: []string{"user_name", "date", "section"},
			Slots:        map[string]string{},
		},
	})

	if decision.Action != plannerActionContinueTask {
		t.Fatalf("Action = %q, want %q", decision.Action, plannerActionContinueTask)
	}
	if decision.Slots["user_name"] != "张三" {
		t.Fatalf("user_name = %q, want 张三", decision.Slots["user_name"])
	}
	if decision.Slots["date"] != "today" {
		t.Fatalf("date = %q, want today", decision.Slots["date"])
	}
	if decision.Slots["section"] != "1" {
		t.Fatalf("section = %q, want 1", decision.Slots["section"])
	}
}

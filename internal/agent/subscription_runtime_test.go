package agent

import (
	"context"
	"testing"

	agenttools "schedule_server/internal/agent/tools"
)

func newSubscriptionRuntimeRegistry(groupSub agenttools.GroupSubPort, dept agenttools.DeptPort) *agenttools.Registry {
	registry := agenttools.NewRegistry()
	agenttools.RegisterAdminTools(registry, &testTaskAttendancePort{}, testTaskUserPort{}, groupSub, dept)
	return registry
}

func TestSubscribeTaskPrepareCachesDepartmentList(t *testing.T) {
	t.Parallel()

	handler := newSubscribeTaskHandler()
	task := &TaskInstance{
		Type:         "subscribe_attendance_push",
		Status:       "waiting_slots",
		Slots:        map[string]string{"scope": "department"},
		MissingSlots: []string{"dept_names"},
	}

	toolsCalled, err := handler.Prepare(context.Background(), task, Deps{Dept: testFamilyDeptPort{}})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(toolsCalled) != 1 || toolsCalled[0] != "list_departments" {
		t.Fatalf("Prepare() tools = %v, want [list_departments]", toolsCalled)
	}

	names, ok := task.CandidateCache["departments"].([]string)
	if !ok {
		t.Fatalf("CandidateCache[departments] = %#v, want []string", task.CandidateCache["departments"])
	}
	if len(names) != 2 || names[0] != "家族7期" || names[1] != "乐知全栈一期" {
		t.Fatalf("cached departments = %v, want [家族7期 乐知全栈一期]", names)
	}
}

func TestSubscribeTaskKeepsTaskOpenAfterDepartmentNotFound(t *testing.T) {
	t.Parallel()

	handler := newSubscribeTaskHandler()
	groupSub := &testGroupSubPort{}
	task := &TaskInstance{
		Type:         "subscribe_attendance_push",
		Status:       "ready",
		Slots:        map[string]string{"scope": "department", "dept_names": "家族九期"},
		MissingSlots: []string{"scope", "dept_names"},
	}

	result, toolsCalled, err := handler.Execute(
		context.Background(),
		task,
		&agenttools.UserContext{
			TenantID:          42,
			UserID:            7,
			UserRole:          1,
			ConversationType:  "2",
			ConversationID:    "conv-1",
			ConversationTitle: "测试群",
		},
		newSubscriptionRuntimeRegistry(groupSub, testFamilyDeptPort{}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(toolsCalled) != 1 || toolsCalled[0] != "subscribe_attendance_push" {
		t.Fatalf("Execute() tools = %v, want [subscribe_attendance_push]", toolsCalled)
	}
	if !result.KeepTaskOpen {
		t.Fatalf("KeepTaskOpen = false, want true")
	}
	if task.LastErrorCode != "department_name_not_found" {
		t.Fatalf("LastErrorCode = %q, want department_name_not_found", task.LastErrorCode)
	}
	if task.Status != "waiting_slots" {
		t.Fatalf("Status = %q, want waiting_slots", task.Status)
	}
	if _, ok := task.Slots["dept_names"]; ok {
		t.Fatalf("dept_names slot = %q, want cleared after retryable failure", task.Slots["dept_names"])
	}
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("Subscribe() call count = %d, want 0", groupSub.subscribeCalls)
	}
}

func TestSubscriptionStatusTaskClosesAfterSuccess(t *testing.T) {
	t.Parallel()

	handler := newSubscriptionStatusTaskHandler()
	task := &TaskInstance{
		Type:   "query_subscription_status",
		Status: "ready",
	}

	result, toolsCalled, err := handler.Execute(
		context.Background(),
		task,
		&agenttools.UserContext{
			TenantID:          42,
			UserID:            7,
			UserRole:          1,
			ConversationType:  "2",
			ConversationID:    "conv-1",
			ConversationTitle: "测试群",
		},
		newSubscriptionRuntimeRegistry(&testClarifyGroupSubPort{
			info: &agenttools.GroupSubInfo{Subscribed: true, DeptIDs: []int64{101}},
		}, testClarifyDeptPort{}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(toolsCalled) != 1 || toolsCalled[0] != "query_subscription_status" {
		t.Fatalf("Execute() tools = %v, want [query_subscription_status]", toolsCalled)
	}
	if result.KeepTaskOpen {
		t.Fatalf("KeepTaskOpen = true, want false")
	}
	if task.Status != "completed" {
		t.Fatalf("Status = %q, want completed", task.Status)
	}
}

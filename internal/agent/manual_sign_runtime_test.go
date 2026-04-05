package agent

import (
	"context"
	"testing"

	agenttools "schedule_server/internal/agent/tools"
)

type testAmbiguousTaskUserPort struct{}

func (testAmbiguousTaskUserPort) FindByDingUserID(context.Context, string) (*agenttools.UserInfo, error) {
	return &agenttools.UserInfo{
		ID:         7,
		Name:       "Alice",
		DingUserID: "ding-user",
		Role:       1,
		TenantID:   42,
	}, nil
}

func (testAmbiguousTaskUserPort) SearchByName(context.Context, string) ([]agenttools.UserInfo, error) {
	return []agenttools.UserInfo{
		{ID: 101, Name: "张三", DingUserID: "ding-zhangsan-1", TenantID: 42},
		{ID: 102, Name: "张三", DingUserID: "ding-zhangsan-2", TenantID: 42},
	}, nil
}

func newManualSignRuntimeRegistry(attendance agenttools.AttendancePort, user agenttools.UserPort) *agenttools.Registry {
	registry := agenttools.NewRegistry()
	agenttools.RegisterAdminTools(registry, attendance, user, &testGroupSubPort{}, testClarifyDeptPort{})
	return registry
}

func TestManualSignTaskKeepsTaskOpenWhenUserNameIsAmbiguous(t *testing.T) {
	t.Parallel()

	handler := newManualSignTaskHandler()
	task := &TaskInstance{
		Type:   "sign_for_user",
		Status: "ready",
		Slots: map[string]string{
			"user_name": "张三",
			"date":      "2026-04-04",
			"section":   "1",
		},
	}

	result, toolsCalled, err := handler.Execute(
		context.Background(),
		task,
		&agenttools.UserContext{
			TenantID: 42,
			UserID:   7,
			UserRole: 1,
		},
		newManualSignRuntimeRegistry(&testTaskAttendancePort{}, testAmbiguousTaskUserPort{}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(toolsCalled) != 1 || toolsCalled[0] != "sign_for_user" {
		t.Fatalf("Execute() tools = %v, want [sign_for_user]", toolsCalled)
	}
	if !result.KeepTaskOpen {
		t.Fatalf("KeepTaskOpen = false, want true")
	}
	if task.LastErrorCode != "user_name_ambiguous" {
		t.Fatalf("LastErrorCode = %q, want user_name_ambiguous", task.LastErrorCode)
	}
	candidates, ok := task.CandidateCache["candidate_users"].([]string)
	if !ok || len(candidates) != 2 {
		t.Fatalf("CandidateCache[candidate_users] = %#v, want 2 candidates", task.CandidateCache["candidate_users"])
	}
}

func TestManualSignTaskClosesAfterSuccess(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	handler := newManualSignTaskHandler()
	task := &TaskInstance{
		Type:   "sign_for_user",
		Status: "ready",
		Slots: map[string]string{
			"user_name": "张三",
			"date":      "2026-04-04",
			"section":   "1",
		},
	}

	result, toolsCalled, err := handler.Execute(
		context.Background(),
		task,
		&agenttools.UserContext{
			TenantID: 42,
			UserID:   7,
			UserRole: 1,
		},
		newManualSignRuntimeRegistry(attendance, testTaskUserPort{}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(toolsCalled) != 1 || toolsCalled[0] != "sign_for_user" {
		t.Fatalf("Execute() tools = %v, want [sign_for_user]", toolsCalled)
	}
	if result.KeepTaskOpen {
		t.Fatalf("KeepTaskOpen = true, want false")
	}
	if task.Status != "completed" {
		t.Fatalf("Status = %q, want completed", task.Status)
	}
	if attendance.signCalls != 1 {
		t.Fatalf("sign calls = %d, want 1", attendance.signCalls)
	}
}

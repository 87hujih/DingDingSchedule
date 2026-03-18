package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type testAttendancePort struct{}

func (testAttendancePort) GetAttendanceDetail(context.Context, AttendanceQuery) (*AttendanceResult, error) {
	return nil, nil
}

func (testAttendancePort) GetAttendanceText(context.Context, AttendanceQuery) (string, error) {
	return "", nil
}

func (testAttendancePort) GetWeeklyAbsenceRanking(context.Context) ([]RankItem, error) {
	return nil, nil
}

func (testAttendancePort) GetWeeklyAttendanceRateRanking(context.Context) ([]RankItem, error) {
	return nil, nil
}

func (testAttendancePort) FindRecordByDateSection(context.Context, string, int) (uint, error) {
	return 0, nil
}

func (testAttendancePort) SignForUsers(context.Context, uint, []uint) error {
	return nil
}

type testAdminUserPort struct{}

func (testAdminUserPort) FindByDingUserID(context.Context, string) (*UserInfo, error) {
	return nil, nil
}

func (testAdminUserPort) SearchByName(context.Context, string) ([]UserInfo, error) {
	return nil, nil
}

type testGroupSubPort struct {
	subscribeCalls int
}

func (p *testGroupSubPort) Subscribe(context.Context, uint, string, string, uint, []int64) error {
	p.subscribeCalls++
	return nil
}

func (p *testGroupSubPort) Unsubscribe(context.Context, uint, string) error {
	return nil
}

func (p *testGroupSubPort) GetSubscription(context.Context, uint, string) (*GroupSubInfo, error) {
	return &GroupSubInfo{Subscribed: false}, nil
}

type testDeptPort struct{}

func (testDeptPort) ListDepts(context.Context) ([]DeptItem, error) {
	return nil, nil
}

func TestSubscribeAttendancePushRejectsMalformedParams(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	groupSub := &testGroupSubPort{}
	RegisterAdminTools(registry, testAttendancePort{}, testAdminUserPort{}, groupSub, testDeptPort{})

	result, err := registry.Dispatch(context.Background(), &UserContext{
		TenantID:         42,
		UserID:           7,
		UserRole:         1,
		ConversationType: "2",
		ConversationID:   "conv-1",
	}, "subscribe_attendance_push", json.RawMessage(`{`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if !strings.Contains(result, "参数解析失败") {
		t.Fatalf("Dispatch() result = %s, want parse error", result)
	}
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("Subscribe() call count = %d, want 0", groupSub.subscribeCalls)
	}
}

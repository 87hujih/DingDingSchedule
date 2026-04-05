package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type testAttendancePort struct {
	findRecordID  uint
	signCalls     int
	lastRecordID  uint
	lastUserIDs   []uint
	slotSignCalls int
	lastDate      string
	lastSection   int
}

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

func (p *testAttendancePort) FindRecordByDateSection(context.Context, string, int) (uint, error) {
	return p.findRecordID, nil
}

func (p *testAttendancePort) SignForUsers(_ context.Context, recordID uint, userIDs []uint) error {
	p.signCalls++
	p.lastRecordID = recordID
	p.lastUserIDs = append([]uint(nil), userIDs...)
	return nil
}

func (p *testAttendancePort) SignForUsersBySlot(_ context.Context, date string, section int, userIDs []uint) error {
	p.slotSignCalls++
	p.lastDate = date
	p.lastSection = section
	p.lastUserIDs = append([]uint(nil), userIDs...)
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
	lastDeptIDs    []int64
}

func (p *testGroupSubPort) Subscribe(_ context.Context, _ uint, _ string, _ string, _ uint, deptIDs []int64) error {
	p.subscribeCalls++
	p.lastDeptIDs = append([]int64(nil), deptIDs...)
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

type testAliasDeptPort struct{}

func (testAliasDeptPort) ListDepts(context.Context) ([]DeptItem, error) {
	return []DeptItem{
		{DeptID: 301, Name: "家族7期"},
		{DeptID: 302, Name: "乐知全栈一期"},
	}, nil
}

func TestSubscribeAttendancePushRejectsMalformedParams(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	groupSub := &testGroupSubPort{}
	RegisterAdminTools(registry, &testAttendancePort{}, testAdminUserPort{}, groupSub, testDeptPort{})

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

func TestSubscribeAttendancePushAcceptsChineseNumeralDeptAlias(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	groupSub := &testGroupSubPort{}
	RegisterAdminTools(registry, &testAttendancePort{}, testAdminUserPort{}, groupSub, testAliasDeptPort{})

	result, err := registry.Dispatch(context.Background(), &UserContext{
		TenantID:         42,
		UserID:           7,
		UserRole:         1,
		ConversationType: "2",
		ConversationID:   "conv-1",
	}, "subscribe_attendance_push", json.RawMessage(`{"dept_names":["家族七期"]}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if !strings.Contains(result, `"success":true`) {
		t.Fatalf("Dispatch() result = %s, want success payload", result)
	}
	if groupSub.subscribeCalls != 1 {
		t.Fatalf("Subscribe() call count = %d, want 1", groupSub.subscribeCalls)
	}
	if len(groupSub.lastDeptIDs) != 1 || groupSub.lastDeptIDs[0] != 301 {
		t.Fatalf("Subscribe() dept ids = %v, want [301]", groupSub.lastDeptIDs)
	}
}

func TestSignForUserDoesNotRequirePreexistingRecord(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	attendance := &testAttendancePort{findRecordID: 0}
	RegisterAdminTools(registry, attendance, testAdminUserPortWithSearchResult{
		users: []UserInfo{{ID: 9, Name: "张三"}},
	}, &testGroupSubPort{}, testDeptPort{})

	result, err := registry.Dispatch(context.Background(), &UserContext{
		TenantID: 42,
		UserID:   7,
		UserRole: 1,
	}, "sign_for_user", json.RawMessage(`{"user_name":"张三","date":"2026-03-25","section":1}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if strings.Contains(result, "请等待系统自动统计后再操作") {
		t.Fatalf("Dispatch() result = %s, want realtime sign to proceed without preexisting record", result)
	}
	if !strings.Contains(result, `"success":true`) {
		t.Fatalf("Dispatch() result = %s, want success payload", result)
	}
	if attendance.signCalls != 0 {
		t.Fatalf("legacy SignForUsers() call count = %d, want 0", attendance.signCalls)
	}
	if attendance.slotSignCalls != 1 {
		t.Fatalf("SignForUsersBySlot() call count = %d, want 1", attendance.slotSignCalls)
	}
	if attendance.lastDate != "2026-03-25" {
		t.Fatalf("SignForUsersBySlot() date = %q, want 2026-03-25", attendance.lastDate)
	}
	if attendance.lastSection != 1 {
		t.Fatalf("SignForUsersBySlot() section = %d, want 1", attendance.lastSection)
	}
	if len(attendance.lastUserIDs) != 1 || attendance.lastUserIDs[0] != 9 {
		t.Fatalf("SignForUsersBySlot() userIDs = %v, want [9]", attendance.lastUserIDs)
	}
}

func TestSignForUserReturnsStructuredAmbiguousUserError(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	attendance := &testAttendancePort{}
	RegisterAdminTools(registry, attendance, testAdminUserPortWithSearchResult{
		users: []UserInfo{
			{ID: 9, Name: "张三"},
			{ID: 10, Name: "张三"},
		},
	}, &testGroupSubPort{}, testDeptPort{})

	result, err := registry.Dispatch(context.Background(), &UserContext{
		TenantID: 42,
		UserID:   7,
		UserRole: 1,
	}, "sign_for_user", json.RawMessage(`{"user_name":"张三","date":"2026-03-25","section":1}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if !strings.Contains(result, `"error_code":"user_name_ambiguous"`) {
		t.Fatalf("Dispatch() result = %s, want structured ambiguous error code", result)
	}
	if attendance.slotSignCalls != 0 {
		t.Fatalf("SignForUsersBySlot() call count = %d, want 0", attendance.slotSignCalls)
	}
}

type testAdminUserPortWithSearchResult struct {
	users []UserInfo
}

func (p testAdminUserPortWithSearchResult) FindByDingUserID(context.Context, string) (*UserInfo, error) {
	return nil, nil
}

func (p testAdminUserPortWithSearchResult) SearchByName(context.Context, string) ([]UserInfo, error) {
	return p.users, nil
}

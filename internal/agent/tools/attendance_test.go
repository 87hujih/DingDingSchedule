package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type attendanceToolTestPort struct {
	detailCalls int
	textCalls   int
	lastDetail  AttendanceQuery
	lastText    AttendanceQuery
	detailResp  *AttendanceResult
}

func (p *attendanceToolTestPort) GetAttendanceDetail(_ context.Context, req AttendanceQuery) (*AttendanceResult, error) {
	p.detailCalls++
	p.lastDetail = req
	if p.detailResp != nil {
		return p.detailResp, nil
	}
	return &AttendanceResult{Date: req.Date, Week: req.Week, Section: req.Section}, nil
}

func (p *attendanceToolTestPort) GetAttendanceText(_ context.Context, req AttendanceQuery) (string, error) {
	p.textCalls++
	p.lastText = req
	return "ok", nil
}

func (*attendanceToolTestPort) GetWeeklyAbsenceRanking(context.Context) ([]RankItem, error) {
	return nil, nil
}

func (*attendanceToolTestPort) GetWeeklyAttendanceRateRanking(context.Context) ([]RankItem, error) {
	return nil, nil
}

func (*attendanceToolTestPort) FindRecordByDateSection(context.Context, string, int) (uint, error) {
	return 0, nil
}

func (*attendanceToolTestPort) SignForUsers(context.Context, uint, []uint) error {
	return nil
}

type attendanceToolSemesterPort struct{}

func (attendanceToolSemesterPort) GetCurrentWeek(context.Context) (int, int, error) {
	return 3, 20, nil
}

type attendanceToolRestDayPort struct{}

func (attendanceToolRestDayPort) GetMyRestDay(context.Context, uint) (int, string, error) {
	return 0, "", nil
}

type attendanceToolLeavePort struct{}

func (attendanceToolLeavePort) GetRecentLeaves(context.Context, uint, int) ([]LeaveItem, error) {
	return nil, nil
}

type attendanceToolDeptPort struct {
	depts []DeptItem
}

func (p attendanceToolDeptPort) ListDepts(context.Context) ([]DeptItem, error) {
	return p.depts, nil
}

func TestAttendanceToolSchemasExposeDeptName(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	RegisterAttendanceTools(
		registry,
		&attendanceToolTestPort{},
		attendanceToolSemesterPort{},
		attendanceToolRestDayPort{},
		attendanceToolLeavePort{},
		attendanceToolDeptPort{},
	)

	for _, name := range []string{"query_attendance_status", "generate_attendance_text"} {
		entry, ok := registry.byName[name]
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		params := string(entry.Def.Function.Parameters)
		if !strings.Contains(params, `"dept_name"`) {
			t.Fatalf("tool %s schema missing dept_name: %s", name, params)
		}
		if !strings.Contains(params, `"dept_id"`) {
			t.Fatalf("tool %s schema missing dept_id: %s", name, params)
		}
		if !strings.Contains(params, "优先于 dept_id") {
			t.Fatalf("tool %s schema should prefer dept_name: %s", name, params)
		}
	}
}

func TestAttendanceToolsSupportDeptName(t *testing.T) {
	t.Parallel()

	t.Run("query_attendance_status resolves dept_name to dept_id", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		attendance := &attendanceToolTestPort{}
		RegisterAttendanceTools(
			registry,
			attendance,
			attendanceToolSemesterPort{},
			attendanceToolRestDayPort{},
			attendanceToolLeavePort{},
			attendanceToolDeptPort{depts: []DeptItem{{DeptID: 12, Name: "学生会"}}},
		)

		_, err := registry.Dispatch(context.Background(), &UserContext{}, "query_attendance_status", json.RawMessage(`{"date":"2026-03-18","week":3,"section":1,"dept_name":"学生会"}`))
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if attendance.detailCalls != 1 {
			t.Fatalf("GetAttendanceDetail() call count = %d, want 1", attendance.detailCalls)
		}
		if attendance.lastDetail.DeptID != 12 {
			t.Fatalf("GetAttendanceDetail() DeptID = %d, want 12", attendance.lastDetail.DeptID)
		}
	})

	t.Run("generate_attendance_text resolves dept_name to dept_id", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		attendance := &attendanceToolTestPort{}
		RegisterAttendanceTools(
			registry,
			attendance,
			attendanceToolSemesterPort{},
			attendanceToolRestDayPort{},
			attendanceToolLeavePort{},
			attendanceToolDeptPort{depts: []DeptItem{{DeptID: 21, Name: "办公室"}}},
		)

		_, err := registry.Dispatch(context.Background(), &UserContext{}, "generate_attendance_text", json.RawMessage(`{"date":"2026-03-18","week":3,"section":2,"dept_name":"办公室"}`))
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if attendance.textCalls != 1 {
			t.Fatalf("GetAttendanceText() call count = %d, want 1", attendance.textCalls)
		}
		if attendance.lastText.DeptID != 21 {
			t.Fatalf("GetAttendanceText() DeptID = %d, want 21", attendance.lastText.DeptID)
		}
	})

	t.Run("invalid dept_name short circuits without downstream call", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		attendance := &attendanceToolTestPort{}
		RegisterAttendanceTools(
			registry,
			attendance,
			attendanceToolSemesterPort{},
			attendanceToolRestDayPort{},
			attendanceToolLeavePort{},
			attendanceToolDeptPort{depts: []DeptItem{{DeptID: 12, Name: "学生会"}}},
		)

		result, err := registry.Dispatch(context.Background(), &UserContext{}, "query_attendance_status", json.RawMessage(`{"date":"2026-03-18","week":3,"section":1,"dept_name":"组织部"}`))
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if attendance.detailCalls != 0 {
			t.Fatalf("GetAttendanceDetail() call count = %d, want 0", attendance.detailCalls)
		}
		if !strings.Contains(result, "未找到部门") {
			t.Fatalf("Dispatch() result = %s, want not-found message", result)
		}
	})

	t.Run("legacy dept_id remains supported", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		attendance := &attendanceToolTestPort{}
		RegisterAttendanceTools(
			registry,
			attendance,
			attendanceToolSemesterPort{},
			attendanceToolRestDayPort{},
			attendanceToolLeavePort{},
			attendanceToolDeptPort{depts: []DeptItem{{DeptID: 12, Name: "学生会"}}},
		)

		_, err := registry.Dispatch(context.Background(), &UserContext{}, "generate_attendance_text", json.RawMessage(`{"date":"2026-03-18","week":3,"section":2,"dept_id":66}`))
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if attendance.textCalls != 1 {
			t.Fatalf("GetAttendanceText() call count = %d, want 1", attendance.textCalls)
		}
		if attendance.lastText.DeptID != 66 {
			t.Fatalf("GetAttendanceText() DeptID = %d, want 66", attendance.lastText.DeptID)
		}
	})
}

func TestAttendanceToolOutputsLateAndViewMetadata(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	attendance := &attendanceToolTestPort{
		detailResp: &AttendanceResult{
			Date:         "2026-03-19",
			Week:         3,
			Section:      1,
			SlotStart:    "08:00",
			SlotEnd:      "09:40",
			ShouldAttend: 3,
			OnTimeCount:  1,
			LateCount:    1,
			LeaveCount:   0,
			AbsentCount:  1,
			RestDayCount: 0,
			OnTimeUsers:  []string{"OnTimeUser"},
			LateUsers:    []string{"LateUser"},
			AbsentUsers:  []string{"MissingUser"},
			ViewMode:     "current",
			IsFinalized:  false,
			FinalizeAt:   "2026-03-19 08:30:00",
		},
	}
	RegisterAttendanceTools(
		registry,
		attendance,
		attendanceToolSemesterPort{},
		attendanceToolRestDayPort{},
		attendanceToolLeavePort{},
		attendanceToolDeptPort{},
	)

	raw, err := registry.Dispatch(context.Background(), &UserContext{}, "query_attendance_status", json.RawMessage(`{"date":"2026-03-19","week":3,"section":1}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, raw = %s", err, raw)
	}

	if payload["view_mode"] != "current" {
		t.Fatalf("view_mode = %v, want current", payload["view_mode"])
	}
	if payload["not_arrived_label"] != "当前未到" {
		t.Fatalf("not_arrived_label = %v, want 当前未到", payload["not_arrived_label"])
	}
	if payload["late_count"] != float64(1) {
		t.Fatalf("late_count = %v, want 1", payload["late_count"])
	}
	if !strings.Contains(payload["late_users"].(string), "LateUser") {
		t.Fatalf("late_users = %v, want LateUser", payload["late_users"])
	}
}

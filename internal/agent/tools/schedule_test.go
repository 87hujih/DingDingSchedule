package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWeekdayNumberForTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   time.Weekday
		want int
	}{
		{
			name: "monday keeps one based value",
			in:   time.Monday,
			want: 1,
		},
		{
			name: "sunday maps to seven",
			in:   time.Sunday,
			want: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := weekdayNumberForTool(tt.in); got != tt.want {
				t.Fatalf("weekdayNumberForTool(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

type scheduleToolTestPort struct {
	calls       int
	lastWeek    int
	lastDayFrom int
	lastDayTo   int
	lastDeptID  int64
}

func (p *scheduleToolTestPort) ListMyScheduleByWeek(context.Context, uint, int) ([]CourseItem, error) {
	return nil, nil
}

func (p *scheduleToolTestPort) ListUserScheduleByWeek(context.Context, uint, int, uint, int) ([]CourseItem, error) {
	return nil, nil
}

func (p *scheduleToolTestPort) GetFreeUsersBySlot(_ context.Context, week, dayStart, dayEnd int, deptID int64) ([]FreeSlotResult, error) {
	p.calls++
	p.lastWeek = week
	p.lastDayFrom = dayStart
	p.lastDayTo = dayEnd
	p.lastDeptID = deptID
	return []FreeSlotResult{
		{
			DayOfWeek: 1,
			Section:   1,
			SlotStart: "08:00",
			SlotEnd:   "09:40",
			FreeUsers: []string{"张三"},
			FreeCount: 1,
		},
	}, nil
}

type scheduleToolSemesterPort struct{}

func (scheduleToolSemesterPort) GetCurrentWeek(context.Context) (int, int, error) {
	return 3, 20, nil
}

type scheduleToolPeriodPort struct{}

func (scheduleToolPeriodPort) GetScheduleInfo(context.Context) ([]PeriodInfo, string, error) {
	return []PeriodInfo{{Name: "第1-2节", Start: "08:00", End: "09:40"}}, "school", nil
}

type scheduleToolDeptPort struct {
	depts []DeptItem
}

func (p scheduleToolDeptPort) ListDepts(context.Context) ([]DeptItem, error) {
	return p.depts, nil
}

func TestQueryFreeUsersBySlotSchemaExposeDeptFilter(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	RegisterScheduleTools(
		registry,
		&scheduleToolTestPort{},
		scheduleToolSemesterPort{},
		scheduleToolPeriodPort{},
		scheduleToolDeptPort{},
	)

	entry, ok := registry.byName["query_free_users_by_slot"]
	if !ok {
		t.Fatalf("tool query_free_users_by_slot not registered")
	}

	params := string(entry.Def.Function.Parameters)
	for _, key := range []string{`"dept_name"`, `"dept_id"`} {
		if !json.Valid(entry.Def.Function.Parameters) {
			t.Fatalf("schema is not valid JSON: %s", params)
		}
		if !strings.Contains(params, key) {
			t.Fatalf("schema missing %s: %s", key, params)
		}
	}
	if !strings.Contains(params, "优先于 dept_id") {
		t.Fatalf("schema should mention dept_name priority: %s", params)
	}
}

func TestQueryFreeUsersBySlotSupportsDeptFilter(t *testing.T) {
	t.Parallel()

	t.Run("dept_name resolves to dept_id", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		schedule := &scheduleToolTestPort{}
		RegisterScheduleTools(
			registry,
			schedule,
			scheduleToolSemesterPort{},
			scheduleToolPeriodPort{},
			scheduleToolDeptPort{depts: []DeptItem{{DeptID: 12, Name: "学生会"}}},
		)

		_, err := registry.Dispatch(context.Background(), &UserContext{}, "query_free_users_by_slot", json.RawMessage(`{"week":2,"day_start":1,"day_end":5,"dept_name":"学生会"}`))
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if schedule.calls != 1 {
			t.Fatalf("GetFreeUsersBySlot() call count = %d, want 1", schedule.calls)
		}
		if schedule.lastDeptID != 12 {
			t.Fatalf("GetFreeUsersBySlot() deptID = %d, want 12", schedule.lastDeptID)
		}
	})

	t.Run("invalid dept_name short circuits without downstream call", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		schedule := &scheduleToolTestPort{}
		RegisterScheduleTools(
			registry,
			schedule,
			scheduleToolSemesterPort{},
			scheduleToolPeriodPort{},
			scheduleToolDeptPort{depts: []DeptItem{{DeptID: 12, Name: "学生会"}}},
		)

		result, err := registry.Dispatch(context.Background(), &UserContext{}, "query_free_users_by_slot", json.RawMessage(`{"week":2,"day_start":1,"day_end":5,"dept_name":"组织部"}`))
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if schedule.calls != 0 {
			t.Fatalf("GetFreeUsersBySlot() call count = %d, want 0", schedule.calls)
		}
		if !strings.Contains(result, "未找到部门") {
			t.Fatalf("Dispatch() result = %s, want not-found message", result)
		}
	})

	t.Run("legacy dept_id remains supported", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		schedule := &scheduleToolTestPort{}
		RegisterScheduleTools(
			registry,
			schedule,
			scheduleToolSemesterPort{},
			scheduleToolPeriodPort{},
			scheduleToolDeptPort{},
		)

		_, err := registry.Dispatch(context.Background(), &UserContext{}, "query_free_users_by_slot", json.RawMessage(`{"week":2,"day_start":1,"day_end":5,"dept_id":66}`))
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if schedule.calls != 1 {
			t.Fatalf("GetFreeUsersBySlot() call count = %d, want 1", schedule.calls)
		}
		if schedule.lastDeptID != 66 {
			t.Fatalf("GetFreeUsersBySlot() deptID = %d, want 66", schedule.lastDeptID)
		}
	})
}

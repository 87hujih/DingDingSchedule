package agent

import (
	"context"
	"testing"
	"time"

	agenttools "schedule_server/internal/agent/tools"
)

func TestResolveDepartmentExactMatch(t *testing.T) {
	t.Parallel()

	result := resolveDepartment(entityContext{
		Raw: "信工24级",
		Departments: []agenttools.DeptItem{
			{DeptID: 101, Name: "信工24级"},
		},
	})
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveResolved)
	}
	if result.Department == nil || result.Department.DeptID != 101 {
		t.Fatalf("Department = %+v, want dept 101", result.Department)
	}
}

func TestResolveDepartmentNormalizedUniqueMatch(t *testing.T) {
	t.Parallel()

	result := resolveDepartment(entityContext{
		Raw: "信工 24级",
		Departments: []agenttools.DeptItem{
			{DeptID: 101, Name: "信工24级"},
		},
	})
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveResolved)
	}
	if result.Department == nil || result.Department.DeptID != 101 {
		t.Fatalf("Department = %+v, want dept 101", result.Department)
	}
}

func TestResolveDepartmentReturnsAmbiguousCandidates(t *testing.T) {
	t.Parallel()

	result := resolveDepartment(entityContext{
		Raw: "信工",
		Departments: []agenttools.DeptItem{
			{DeptID: 101, Name: "信工24级"},
			{DeptID: 102, Name: "信工23级"},
		},
	})
	if result.Status != ResolveAmbiguous {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveAmbiguous)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want 2 entries", result.Candidates)
	}
}

func TestResolveDepartmentReturnsAmbiguousForDuplicateExactNames(t *testing.T) {
	t.Parallel()

	result := resolveDepartment(entityContext{
		Raw: "信工24级",
		Departments: []agenttools.DeptItem{
			{DeptID: 101, Name: "信工24级"},
			{DeptID: 102, Name: "信工24级"},
		},
	})
	if result.Status != ResolveAmbiguous {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveAmbiguous)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want 2 entries", result.Candidates)
	}
}

func TestResolveDepartmentReturnsNotFound(t *testing.T) {
	t.Parallel()

	result := resolveDepartment(entityContext{
		Raw: "不存在部门",
		Departments: []agenttools.DeptItem{
			{DeptID: 101, Name: "信工24级"},
		},
	})
	if result.Status != ResolveNotFound {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveNotFound)
	}
}

func TestResolveDepartmentRejectsCapabilitySentenceAsInvalidShape(t *testing.T) {
	t.Parallel()

	result := resolveDepartment(entityContext{
		Raw: "可以执行代签功能吗",
	})
	if result.Status != ResolveInvalidShape {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveInvalidShape)
	}
}

func TestResolveUserExactMatch(t *testing.T) {
	t.Parallel()

	result := resolveUser(entityContext{
		Raw: "张三",
		Users: []agenttools.UserInfo{
			{ID: 7, Name: "张三"},
		},
	})
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveResolved)
	}
	if result.User == nil || result.User.ID != 7 {
		t.Fatalf("User = %+v, want user 7", result.User)
	}
}

func TestResolveUserReturnsAmbiguousCandidates(t *testing.T) {
	t.Parallel()

	result := resolveUser(entityContext{
		Raw: "张",
		Users: []agenttools.UserInfo{
			{ID: 7, Name: "张三"},
			{ID: 8, Name: "张四"},
		},
	})
	if result.Status != ResolveAmbiguous {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveAmbiguous)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want 2 entries", result.Candidates)
	}
}

func TestResolveUserReturnsAmbiguousForDuplicateExactNames(t *testing.T) {
	t.Parallel()

	result := resolveUser(entityContext{
		Raw: "张三",
		Users: []agenttools.UserInfo{
			{ID: 7, Name: "张三"},
			{ID: 8, Name: "张三"},
		},
	})
	if result.Status != ResolveAmbiguous {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveAmbiguous)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want 2 entries", result.Candidates)
	}
}

func TestResolveDateParsesExplicitDate(t *testing.T) {
	t.Parallel()

	got, ok := resolveDate("2026-04-07")
	if !ok {
		t.Fatalf("resolveDate() ok = false, want true")
	}
	if got != "2026-04-07" {
		t.Fatalf("resolveDate() = %q, want 2026-04-07", got)
	}
}

func TestResolveDateSlotDefaultsToTodayWhenMissingForAttendance(t *testing.T) {
	t.Parallel()

	result := resolveDateSlot("", SlotDefaultToday, fixedResolverClock("2026-06-06T09:00:00+08:00"))
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want %q: %+v", result.Status, ResolveResolved, result)
	}
	if result.Value != "2026-06-06" {
		t.Fatalf("Value = %v, want 2026-06-06", result.Value)
	}
}

func TestResolveDateSlotReturnsMissingWhenRequiredWithoutDefault(t *testing.T) {
	t.Parallel()

	result := resolveDateSlot("", SlotDefaultNone, fixedResolverClock("2026-06-06T09:00:00+08:00"))
	if result.Status != ResolveMissing {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveMissing)
	}
}

func TestResolveWeekSlotDefaultsToCurrentTeachingWeek(t *testing.T) {
	t.Parallel()

	result := resolveWeekSlot(context.Background(), "", SlotDefaultCurrentWeek, fakeResolverSemester{week: 10})
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want %q: %+v", result.Status, ResolveResolved, result)
	}
	if result.Value != 10 {
		t.Fatalf("Value = %v, want 10", result.Value)
	}
}

func TestResolveWeekSlotParsesCurrentWeekPhrase(t *testing.T) {
	t.Parallel()

	result := resolveWeekSlot(context.Background(), "本周", SlotDefaultNone, fakeResolverSemester{week: 10})
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want %q: %+v", result.Status, ResolveResolved, result)
	}
	if result.Value != 10 {
		t.Fatalf("Value = %v, want 10", result.Value)
	}
}

func TestResolveSectionSlotParsesChineseAndArabicNumerals(t *testing.T) {
	t.Parallel()

	chinese := resolveSectionSlot("第一节", nil, fixedResolverClock("2026-06-06T09:00:00+08:00"))
	if chinese.Status != ResolveResolved || chinese.Value != 1 {
		t.Fatalf("resolveSectionSlot(第一节) = %+v, want section 1", chinese)
	}

	arabic := resolveSectionSlot("第5节", nil, fixedResolverClock("2026-06-06T09:00:00+08:00"))
	if arabic.Status != ResolveResolved || arabic.Value != 5 {
		t.Fatalf("resolveSectionSlot(第5节) = %+v, want section 5", arabic)
	}
}

func TestResolveSectionSlotResolvesCurrentPeriod(t *testing.T) {
	t.Parallel()

	result := resolveSectionSlot("本节", []agenttools.PeriodInfo{
		{Name: "第一节", Start: "08:00", End: "08:45"},
		{Name: "第二节", Start: "09:00", End: "09:45"},
	}, fixedResolverClock("2026-06-06T09:10:00+08:00"))
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want %q: %+v", result.Status, ResolveResolved, result)
	}
	if result.Value != 2 {
		t.Fatalf("Value = %v, want section 2", result.Value)
	}
}

func TestResolveSectionSlotReturnsMissingOutsideCurrentPeriod(t *testing.T) {
	t.Parallel()

	result := resolveSectionSlot("本节", []agenttools.PeriodInfo{
		{Name: "第一节", Start: "08:00", End: "08:45"},
	}, fixedResolverClock("2026-06-06T09:10:00+08:00"))
	if result.Status != ResolveMissing {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveMissing)
	}
}

func TestResolveUserSlotReturnsStructuredAmbiguousCandidates(t *testing.T) {
	t.Parallel()

	result := resolveUserSlot("张", []agenttools.UserInfo{
		{ID: 7, Name: "张三"},
		{ID: 8, Name: "张四"},
	})
	if result.Status != ResolveAmbiguous {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveAmbiguous)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want 2 entries", result.Candidates)
	}
	if result.Candidates[0].ID != "7" || result.Candidates[0].Label != "张三" {
		t.Fatalf("first candidate = %+v, want user 7 张三", result.Candidates[0])
	}
}

func TestResolveUserSlotReturnsCanonicalUserIDAndName(t *testing.T) {
	t.Parallel()

	result := resolveUserSlot("张三", []agenttools.UserInfo{
		{ID: 7, Name: "张三"},
	})
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want %q: %+v", result.Status, ResolveResolved, result)
	}
	if result.Value != uint(7) {
		t.Fatalf("Value = %v, want user id 7", result.Value)
	}
	if result.Values["user_id"] != uint(7) || result.Values["user_name"] != "张三" {
		t.Fatalf("Values = %v, want user_id and user_name", result.Values)
	}
}

func TestResolveUserSlotReturnsAmbiguousForDuplicateExactNames(t *testing.T) {
	t.Parallel()

	result := resolveUserSlot("张三", []agenttools.UserInfo{
		{ID: 7, Name: "张三"},
		{ID: 8, Name: "张三"},
	})
	if result.Status != ResolveAmbiguous {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveAmbiguous)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want 2 entries", result.Candidates)
	}
}

func TestResolveDepartmentSlotReturnsCanonicalDepartmentID(t *testing.T) {
	t.Parallel()

	result := resolveDepartmentSlot("信工 24级", []agenttools.DeptItem{
		{DeptID: 101, Name: "信工24级"},
	})
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want %q: %+v", result.Status, ResolveResolved, result)
	}
	ids, ok := result.Value.([]int64)
	if !ok || len(ids) != 1 || ids[0] != 101 {
		t.Fatalf("Value = %v, want dept ids [101]", result.Value)
	}
	if result.Values["dept_ids"] == nil {
		t.Fatalf("Values = %v, want dept_ids", result.Values)
	}
}

func TestResolveDepartmentSlotReturnsStructuredAmbiguousCandidates(t *testing.T) {
	t.Parallel()

	result := resolveDepartmentSlot("信工", []agenttools.DeptItem{
		{DeptID: 101, Name: "信工24级"},
		{DeptID: 102, Name: "信工23级"},
	})
	if result.Status != ResolveAmbiguous {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveAmbiguous)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want 2 entries", result.Candidates)
	}
	if result.Candidates[0].ID != "101" || result.Candidates[0].Label != "信工24级" {
		t.Fatalf("first candidate = %+v, want dept 101 信工24级", result.Candidates[0])
	}
}

func TestResolveDepartmentSlotReturnsAmbiguousForDuplicateExactNames(t *testing.T) {
	t.Parallel()

	result := resolveDepartmentSlot("信工24级", []agenttools.DeptItem{
		{DeptID: 101, Name: "信工24级"},
		{DeptID: 102, Name: "信工24级"},
	})
	if result.Status != ResolveAmbiguous {
		t.Fatalf("Status = %q, want %q", result.Status, ResolveAmbiguous)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %v, want 2 entries", result.Candidates)
	}
}

type fakeResolverSemester struct {
	week int
}

func (f fakeResolverSemester) GetCurrentWeek(context.Context) (int, int, error) {
	return f.week, 16, nil
}

func fixedResolverClock(value string) func() time.Time {
	return func() time.Time {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			panic(err)
		}
		return parsed
	}
}

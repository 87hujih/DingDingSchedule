package agent

import (
	"testing"

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

func TestResolveSectionParsesChineseSection(t *testing.T) {
	t.Parallel()

	got, ok := resolveSection("第二节")
	if !ok {
		t.Fatalf("resolveSection() ok = false, want true")
	}
	if got != 2 {
		t.Fatalf("resolveSection() = %d, want 2", got)
	}
}

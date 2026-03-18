package tools

import (
    "context"
    "strings"
    "testing"
)

type deptResolverTestPort struct {
    depts []DeptItem
}

func (p deptResolverTestPort) ListDepts(context.Context) ([]DeptItem, error) {
    return p.depts, nil
}

func TestResolveDeptFilter(t *testing.T) {
    t.Parallel()

    t.Run("resolve by dept_name", func(t *testing.T) {
        t.Parallel()

        gotID, useFilter, payload, err := resolveDeptFilter(context.Background(), deptResolverTestPort{
            depts: []DeptItem{{DeptID: 10, Name: "学生会"}, {DeptID: 20, Name: "办公室"}},
        }, 0, "学生会")
        if err != nil {
            t.Fatalf("resolveDeptFilter() error = %v", err)
        }
        if gotID != 10 || !useFilter {
            t.Fatalf("resolveDeptFilter() = (%d, %v), want (10, true)", gotID, useFilter)
        }
        if payload != "" {
            t.Fatalf("resolveDeptFilter() payload = %q, want empty", payload)
        }
    })

    t.Run("dept_name overrides dept_id", func(t *testing.T) {
        t.Parallel()

        gotID, useFilter, payload, err := resolveDeptFilter(context.Background(), deptResolverTestPort{
            depts: []DeptItem{{DeptID: 30, Name: "纪检部"}},
        }, 99, "纪检部")
        if err != nil {
            t.Fatalf("resolveDeptFilter() error = %v", err)
        }
        if gotID != 30 || !useFilter {
            t.Fatalf("resolveDeptFilter() = (%d, %v), want (30, true)", gotID, useFilter)
        }
        if payload != "" {
            t.Fatalf("resolveDeptFilter() payload = %q, want empty", payload)
        }
    })

    t.Run("fallback to dept_id", func(t *testing.T) {
        t.Parallel()

        gotID, useFilter, payload, err := resolveDeptFilter(context.Background(), deptResolverTestPort{}, 88, "")
        if err != nil {
            t.Fatalf("resolveDeptFilter() error = %v", err)
        }
        if gotID != 88 || !useFilter {
            t.Fatalf("resolveDeptFilter() = (%d, %v), want (88, true)", gotID, useFilter)
        }
        if payload != "" {
            t.Fatalf("resolveDeptFilter() payload = %q, want empty", payload)
        }
    })

    t.Run("no filter when both empty", func(t *testing.T) {
        t.Parallel()

        gotID, useFilter, payload, err := resolveDeptFilter(context.Background(), deptResolverTestPort{}, 0, "   ")
        if err != nil {
            t.Fatalf("resolveDeptFilter() error = %v", err)
        }
        if gotID != 0 || useFilter {
            t.Fatalf("resolveDeptFilter() = (%d, %v), want (0, false)", gotID, useFilter)
        }
        if payload != "" {
            t.Fatalf("resolveDeptFilter() payload = %q, want empty", payload)
        }
    })

    t.Run("unknown dept_name", func(t *testing.T) {
        t.Parallel()

        _, useFilter, payload, err := resolveDeptFilter(context.Background(), deptResolverTestPort{
            depts: []DeptItem{{DeptID: 10, Name: "学生会"}},
        }, 0, "组织部")
        if err != nil {
            t.Fatalf("resolveDeptFilter() error = %v", err)
        }
        if useFilter {
            t.Fatalf("resolveDeptFilter() useFilter = true, want false")
        }
        if !strings.Contains(payload, "未找到部门") {
            t.Fatalf("resolveDeptFilter() payload = %q, want not-found message", payload)
        }
    })

    t.Run("duplicate dept_name", func(t *testing.T) {
        t.Parallel()

        _, useFilter, payload, err := resolveDeptFilter(context.Background(), deptResolverTestPort{
            depts: []DeptItem{{DeptID: 10, Name: "学生会"}, {DeptID: 11, Name: "学生会"}},
        }, 0, "学生会")
        if err != nil {
            t.Fatalf("resolveDeptFilter() error = %v", err)
        }
        if useFilter {
            t.Fatalf("resolveDeptFilter() useFilter = true, want false")
        }
        if !strings.Contains(payload, "不唯一") {
            t.Fatalf("resolveDeptFilter() payload = %q, want duplicate-name message", payload)
        }
    })
}

package tools

import (
    "context"
    "encoding/json"
    "strings"
    "testing"
)

type analyticsToolStatsPort struct {
    calls   int
    lastReq AttendanceStatsQuery
}

func (p *analyticsToolStatsPort) QueryStats(_ context.Context, req AttendanceStatsQuery) ([]AttendanceStatItem, error) {
    p.calls++
    p.lastReq = req
    return []AttendanceStatItem{{Label: "ok"}}, nil
}

type analyticsToolUserCrossPort struct {
    calls   int
    lastReq UserCrossQuery
}

func (p *analyticsToolUserCrossPort) QueryUserCross(_ context.Context, req UserCrossQuery) ([]string, error) {
    p.calls++
    p.lastReq = req
    return []string{"张三"}, nil
}

type analyticsToolDeptPort struct {
    depts []DeptItem
}

func (p analyticsToolDeptPort) ListDepts(context.Context) ([]DeptItem, error) {
    return p.depts, nil
}

func TestAnalyticsToolSchemasExposeDeptName(t *testing.T) {
    t.Parallel()

    registry := NewRegistry()
    RegisterAnalyticsTools(registry, &analyticsToolStatsPort{}, &analyticsToolUserCrossPort{}, analyticsToolDeptPort{})

    for _, name := range []string{"query_attendance_stats", "query_user_cross"} {
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

func TestAnalyticsToolsSupportDeptName(t *testing.T) {
    t.Parallel()

    t.Run("query_attendance_stats resolves dept_name to dept_id", func(t *testing.T) {
        t.Parallel()

        registry := NewRegistry()
        stats := &analyticsToolStatsPort{}
        cross := &analyticsToolUserCrossPort{}
        RegisterAnalyticsTools(registry, stats, cross, analyticsToolDeptPort{depts: []DeptItem{{DeptID: 14, Name: "学生会"}}})

        _, err := registry.Dispatch(context.Background(), &UserContext{}, "query_attendance_stats", json.RawMessage(`{"week":3,"group_by":"user","dept_name":"学生会"}`))
        if err != nil {
            t.Fatalf("Dispatch() error = %v", err)
        }
        if stats.calls != 1 {
            t.Fatalf("QueryStats() call count = %d, want 1", stats.calls)
        }
        if stats.lastReq.DeptID != 14 {
            t.Fatalf("QueryStats() DeptID = %d, want 14", stats.lastReq.DeptID)
        }
    })

    t.Run("query_user_cross resolves dept_name to dept_id", func(t *testing.T) {
        t.Parallel()

        registry := NewRegistry()
        stats := &analyticsToolStatsPort{}
        cross := &analyticsToolUserCrossPort{}
        RegisterAnalyticsTools(registry, stats, cross, analyticsToolDeptPort{depts: []DeptItem{{DeptID: 23, Name: "办公室"}}})

        _, err := registry.Dispatch(context.Background(), &UserContext{}, "query_user_cross", json.RawMessage(`{"dept_name":"办公室","user_names":["张三"]}`))
        if err != nil {
            t.Fatalf("Dispatch() error = %v", err)
        }
        if cross.calls != 1 {
            t.Fatalf("QueryUserCross() call count = %d, want 1", cross.calls)
        }
        if cross.lastReq.DeptID != 23 {
            t.Fatalf("QueryUserCross() DeptID = %d, want 23", cross.lastReq.DeptID)
        }
    })

    t.Run("invalid dept_name short circuits without downstream call", func(t *testing.T) {
        t.Parallel()

        registry := NewRegistry()
        stats := &analyticsToolStatsPort{}
        cross := &analyticsToolUserCrossPort{}
        RegisterAnalyticsTools(registry, stats, cross, analyticsToolDeptPort{depts: []DeptItem{{DeptID: 14, Name: "学生会"}}})

        result, err := registry.Dispatch(context.Background(), &UserContext{}, "query_attendance_stats", json.RawMessage(`{"week":3,"group_by":"user","dept_name":"组织部"}`))
        if err != nil {
            t.Fatalf("Dispatch() error = %v", err)
        }
        if stats.calls != 0 {
            t.Fatalf("QueryStats() call count = %d, want 0", stats.calls)
        }
        if cross.calls != 0 {
            t.Fatalf("QueryUserCross() call count = %d, want 0", cross.calls)
        }
        if !strings.Contains(result, "未找到部门") {
            t.Fatalf("Dispatch() result = %s, want not-found message", result)
        }
    })

    t.Run("legacy dept_id remains supported", func(t *testing.T) {
        t.Parallel()

        registry := NewRegistry()
        stats := &analyticsToolStatsPort{}
        cross := &analyticsToolUserCrossPort{}
        RegisterAnalyticsTools(registry, stats, cross, analyticsToolDeptPort{depts: []DeptItem{{DeptID: 14, Name: "学生会"}}})

        _, err := registry.Dispatch(context.Background(), &UserContext{}, "query_user_cross", json.RawMessage(`{"dept_id":66,"user_names":["张三"]}`))
        if err != nil {
            t.Fatalf("Dispatch() error = %v", err)
        }
        if cross.calls != 1 {
            t.Fatalf("QueryUserCross() call count = %d, want 1", cross.calls)
        }
        if cross.lastReq.DeptID != 66 {
            t.Fatalf("QueryUserCross() DeptID = %d, want 66", cross.lastReq.DeptID)
        }
    })
}

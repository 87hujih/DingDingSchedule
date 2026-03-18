package tools

import (
    "context"
    "strings"
)

func resolveDeptFilter(ctx context.Context, dept DeptPort, deptID int64, deptName string) (resolvedID int64, useFilter bool, payload string, err error) {
    trimmed := strings.TrimSpace(deptName)
    if trimmed == "" {
        if deptID > 0 {
            return deptID, true, "", nil
        }
        return 0, false, "", nil
    }

    depts, err := dept.ListDepts(ctx)
    if err != nil {
        return 0, false, "", err
    }

    matches := make([]DeptItem, 0, 1)
    for _, item := range depts {
        if item.Name == trimmed {
            matches = append(matches, item)
        }
    }

    switch len(matches) {
    case 0:
        payload, err = marshalJSON(map[string]interface{}{
            "error": "未找到部门「" + trimmed + "」，请确认部门名称",
        })
        return 0, false, payload, err
    case 1:
        return matches[0].DeptID, true, "", nil
    default:
        payload, err = marshalJSON(map[string]interface{}{
            "error": "部门名称「" + trimmed + "」不唯一，请改用 dept_id",
        })
        return 0, false, payload, err
    }
}

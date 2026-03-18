package tools

import (
	"context"
	"encoding/json"
)

// RegisterAnalyticsTools 注册通用查询层工具
func RegisterAnalyticsTools(r *Registry, stats AttendanceStatsPort, userCross UserCrossPort, dept DeptPort) {
	registerQueryAttendanceStats(r, stats, dept)
	registerQueryUserCross(r, userCross, dept)
}

// ─────────────────────────────────────────────────────────────────────────────
// query_attendance_stats
// ─────────────────────────────────────────────────────────────────────────────

func registerQueryAttendanceStats(r *Registry, stats AttendanceStatsPort, dept DeptPort) {
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_attendance_stats",
			Description: "考勤统计分析。支持按用户/部门/周次/节次聚合，支持周次范围、日期范围查询，支持对结果二次过滤（如：缺勤超过N次的人）",
			Parameters: json.RawMessage(`{
                "type": "object",
                "properties": {
                    "week":       {"type": "integer", "description": "单周查询"},
                    "week_range": {"type": "array", "items": {"type": "integer"}, "minItems": 2, "maxItems": 2, "description": "周次范围 [起始周, 结束周]，与 week 二选一"},
                    "date":       {"type": "string",  "description": "单日 YYYY-MM-DD"},
                    "date_range": {"type": "array", "items": {"type": "string"}, "minItems": 2, "maxItems": 2, "description": "日期范围 [开始, 结束]，与 date 二选一"},
                    "section":    {"type": "integer", "description": "指定单节次"},
                    "sections":   {"type": "array", "items": {"type": "integer"}, "description": "多节次，与 section 二选一"},
                    "user_name":  {"type": "string",  "description": "按姓名模糊筛选"},
                    "dept_name":  {"type": "string", "description": "按部门名称筛选，优先于 dept_id"},
                    "dept_id":    {"type": "integer", "description": "按部门 ID 筛选，兼容保留字段；当 dept_name 存在时会被忽略"},
                    "group_by":   {"type": "string", "enum": ["user","dept","week","section","day"], "description": "聚合维度，不填则返回逐条明细"},
                    "min_absent_count": {"type": "integer", "description": "HAVING：缺勤次数 >= N，需配合 group_by 使用"},
                    "max_on_time_rate": {"type": "number",  "description": "HAVING：出勤率 <= 0.x（如 0.5 表示 50%），筛出出勤差的，需配合 group_by 使用"},
                    "sort_by":    {"type": "string", "enum": ["absent_count","on_time_count","on_time_rate","leave_count"], "description": "排序字段，默认 absent_count"},
                    "sort_order": {"type": "string", "enum": ["asc","desc"], "description": "排序方向，默认 desc"},
                    "limit":      {"type": "integer", "description": "返回条数上限，默认 20"}
                }
            }`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			Week           int       `json:"week"`
			WeekRange      [2]int    `json:"week_range"`
			Date           string    `json:"date"`
			DateRange      [2]string `json:"date_range"`
			Section        int       `json:"section"`
			Sections       []int     `json:"sections"`
			UserName       string    `json:"user_name"`
			DeptName       string    `json:"dept_name"`
			DeptID         int64     `json:"dept_id"`
			GroupBy        string    `json:"group_by"`
			MinAbsentCount int       `json:"min_absent_count"`
			MaxOnTimeRate  float64   `json:"max_on_time_rate"`
			SortBy         string    `json:"sort_by"`
			SortOrder      string    `json:"sort_order"`
			Limit          int       `json:"limit"`
		}
		_ = json.Unmarshal(params, &p)

		resolvedID, useFilter, payload, err := resolveDeptFilter(ctx, dept, p.DeptID, p.DeptName)
		if err != nil {
			return "", err
		}
		if payload != "" {
			return payload, nil
		}
		if useFilter {
			p.DeptID = resolvedID
		}

		req := AttendanceStatsQuery{
			Week:           p.Week,
			WeekRange:      p.WeekRange,
			Date:           p.Date,
			DateRange:      p.DateRange,
			Section:        p.Section,
			Sections:       p.Sections,
			UserName:       p.UserName,
			DeptID:         p.DeptID,
			GroupBy:        p.GroupBy,
			MinAbsentCount: p.MinAbsentCount,
			MaxOnTimeRate:  p.MaxOnTimeRate,
			SortBy:         p.SortBy,
			SortOrder:      p.SortOrder,
			Limit:          p.Limit,
		}

		items, err := stats.QueryStats(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalJSON(map[string]any{
			"count": len(items),
			"items": items,
		})
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// query_user_cross
// ─────────────────────────────────────────────────────────────────────────────

func registerQueryUserCross(r *Registry, userCross UserCrossPort, dept DeptPort) {
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_user_cross",
			Description: "人员交叉查询：找出同时满足多个课表/考勤条件的人员集合。free_slots/busy_slots 所有条件取 AND，absent_on 任意条件取 OR",
			Parameters: json.RawMessage(`{
                "type": "object",
                "properties": {
                    "free_slots": {
                        "type": "array",
                        "description": "在所有这些时间段都无课（AND 语义）",
                        "items": {
                            "type": "object",
                            "properties": {
                                "week":        {"type": "integer", "description": "周次，0 或不填表示不限周次"},
                                "day_of_week": {"type": "integer", "description": "星期几 1-7，必填"},
                                "section":     {"type": "integer", "description": "节次，必填"}
                            },
                            "required": ["day_of_week", "section"]
                        }
                    },
                    "busy_slots": {
                        "type": "array",
                        "description": "在所有这些时间段都有课（AND 语义）",
                        "items": {
                            "type": "object",
                            "properties": {
                                "week":        {"type": "integer"},
                                "day_of_week": {"type": "integer"},
                                "section":     {"type": "integer"}
                            },
                            "required": ["day_of_week", "section"]
                        }
                    },
                    "absent_on": {
                        "type": "array",
                        "description": "曾在这些时间缺勤（OR 语义，命中任意一条即满足）",
                        "items": {
                            "type": "object",
                            "properties": {
                                "date":    {"type": "string",  "description": "YYYY-MM-DD，与 week 二选一"},
                                "week":    {"type": "integer", "description": "周次，与 date 二选一"},
                                "section": {"type": "integer", "description": "节次，必填"}
                            },
                            "required": ["section"]
                        }
                    },
                    "dept_name":  {"type": "string", "description": "按部门名称筛选，优先于 dept_id"},
                    "dept_id":    {"type": "integer", "description": "只在该部门内查找，兼容保留字段；当 dept_name 存在时会被忽略"},
                    "user_names": {"type": "array", "items": {"type": "string"}, "description": "只在这些人中查找（精确姓名列表）"}
                }
            }`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			FreeSlots []struct {
				Week      int `json:"week"`
				DayOfWeek int `json:"day_of_week"`
				Section   int `json:"section"`
			} `json:"free_slots"`
			BusySlots []struct {
				Week      int `json:"week"`
				DayOfWeek int `json:"day_of_week"`
				Section   int `json:"section"`
			} `json:"busy_slots"`
			AbsentOn []struct {
				Date    string `json:"date"`
				Week    int    `json:"week"`
				Section int    `json:"section"`
			} `json:"absent_on"`
			DeptName  string   `json:"dept_name"`
			DeptID    int64    `json:"dept_id"`
			UserNames []string `json:"user_names"`
		}
		_ = json.Unmarshal(params, &p)

		resolvedID, useFilter, payload, err := resolveDeptFilter(ctx, dept, p.DeptID, p.DeptName)
		if err != nil {
			return "", err
		}
		if payload != "" {
			return payload, nil
		}
		if useFilter {
			p.DeptID = resolvedID
		}

		req := UserCrossQuery{
			DeptID:    p.DeptID,
			UserNames: p.UserNames,
		}
		for _, s := range p.FreeSlots {
			req.FreeSlots = append(req.FreeSlots, SlotCondition{Week: s.Week, DayOfWeek: s.DayOfWeek, Section: s.Section})
		}
		for _, s := range p.BusySlots {
			req.BusySlots = append(req.BusySlots, SlotCondition{Week: s.Week, DayOfWeek: s.DayOfWeek, Section: s.Section})
		}
		for _, a := range p.AbsentOn {
			req.AbsentOn = append(req.AbsentOn, AbsentCondition{Date: a.Date, Week: a.Week, Section: a.Section})
		}

		names, err := userCross.QueryUserCross(ctx, req)
		if err != nil {
			return "", err
		}
		return marshalJSON(map[string]any{
			"count": len(names),
			"users": names,
		})
	})
}

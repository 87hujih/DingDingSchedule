package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RegisterAttendanceTools 注册考勤、休息日、请假相关工具
func RegisterAttendanceTools(r *Registry, attendance AttendancePort, semester SemesterPort, restDay RestDayPort, leave LeavePort, dept DeptPort) {
	// query_attendance_status
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_attendance_status",
			Description: "查询指定日期指定节次的考勤状态",
			Parameters: json.RawMessage(`{
                "type": "object",
                "properties": {
                    "date":      {"type": "string", "description": "日期(YYYY-MM-DD)，默认今天"},
                    "week":      {"type": "integer", "description": "周次，默认自动计算"},
                    "section":   {"type": "integer", "description": "节次(必填)"},
                    "dept_name": {"type": "string", "description": "按部门名称筛选，优先于 dept_id"},
                    "dept_id":   {"type": "integer", "description": "按部门ID筛选，兼容保留字段；当 dept_name 存在时会被忽略"}
                },
                "required": ["section"]
            }`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			Date     string `json:"date"`
			Week     int    `json:"week"`
			Section  int    `json:"section"`
			DeptName string `json:"dept_name"`
			DeptID   int64  `json:"dept_id"`
		}
		_ = json.Unmarshal(params, &p)

		if p.Date == "" {
			p.Date = time.Now().Format("2006-01-02")
		}
		if p.Week <= 0 {
			w, _, err := semester.GetCurrentWeek(ctx)
			if err != nil {
				return marshalJSON(map[string]interface{}{"error": "无法获取当前周次: " + err.Error()})
			}
			p.Week = w
		}

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

		result, err := attendance.GetAttendanceDetail(ctx, AttendanceQuery{
			Date:    p.Date,
			Week:    p.Week,
			Section: p.Section,
			DeptID:  p.DeptID,
		})
		if err != nil {
			return "", err
		}

		return marshalJSON(map[string]interface{}{
			"date":          result.Date,
			"week":          result.Week,
			"section":       result.Section,
			"slot_start":    result.SlotStart,
			"slot_end":      result.SlotEnd,
			"view_mode":     result.ViewMode,
			"is_finalized":  result.IsFinalized,
			"finalize_at":   result.FinalizeAt,
			"should_attend": result.ShouldAttend,
			"on_time_count": result.OnTimeCount,
			"late_count":    result.LateCount,
			"leave_count":   result.LeaveCount,
			"absent_count":  result.AbsentCount,
			"not_arrived_label": map[bool]string{
				true:  "当前未到",
				false: "未到",
			}[result.ViewMode == "current"],
			"rest_day_count": result.RestDayCount,
			"on_time_users":  formatNameList(result.OnTimeUsers),
			"late_users":     formatNameList(result.LateUsers),
			"absent_users":   formatNameList(result.AbsentUsers),
			"rest_day_users": formatNameList(result.RestDayUsers),
			"leave_users":    formatLeaveList(result.LeaveUsers),
		})
	})

	// generate_attendance_text
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "generate_attendance_text",
			Description: "生成可直接群发的考勤通报文本",
			Parameters: json.RawMessage(`{
                "type": "object",
                "properties": {
                    "date":      {"type": "string", "description": "日期(YYYY-MM-DD)，默认今天"},
                    "week":      {"type": "integer", "description": "周次，默认自动计算"},
                    "section":   {"type": "integer", "description": "节次(必填)"},
                    "dept_name": {"type": "string", "description": "按部门名称筛选，优先于 dept_id"},
                    "dept_id":   {"type": "integer", "description": "按部门ID筛选，兼容保留字段；当 dept_name 存在时会被忽略"}
                },
                "required": ["section"]
            }`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			Date     string `json:"date"`
			Week     int    `json:"week"`
			Section  int    `json:"section"`
			DeptName string `json:"dept_name"`
			DeptID   int64  `json:"dept_id"`
		}
		_ = json.Unmarshal(params, &p)

		if p.Date == "" {
			p.Date = time.Now().Format("2006-01-02")
		}
		if p.Week <= 0 {
			w, _, err := semester.GetCurrentWeek(ctx)
			if err != nil {
				return marshalJSON(map[string]interface{}{"error": "无法获取当前周次: " + err.Error()})
			}
			p.Week = w
		}

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

		text, err := attendance.GetAttendanceText(ctx, AttendanceQuery{
			Date:    p.Date,
			Week:    p.Week,
			Section: p.Section,
			DeptID:  p.DeptID,
		})
		if err != nil {
			return "", err
		}

		return text, nil
	})

	// query_weekly_absence_ranking
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_weekly_absence_ranking",
			Description: "查询本周缺勤次数排行（前10名）",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		items, err := attendance.GetWeeklyAbsenceRanking(ctx)
		if err != nil {
			return "", err
		}

		week := 0
		if w, _, err := semester.GetCurrentWeek(ctx); err == nil {
			week = w
		}

		return marshalJSON(map[string]interface{}{
			"week":  week,
			"items": items,
		})
	})

	// query_weekly_attendance_ranking
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_weekly_attendance_ranking",
			Description: "查询本周出勤率排行（前10名）",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		items, err := attendance.GetWeeklyAttendanceRateRanking(ctx)
		if err != nil {
			return "", err
		}

		week := 0
		if w, _, err := semester.GetCurrentWeek(ctx); err == nil {
			week = w
		}

		return marshalJSON(map[string]interface{}{
			"week":  week,
			"items": items,
		})
	})

	// query_rest_days
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_rest_days",
			Description: "查询当前用户的休息日配置",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		dayOfWeek, dayName, err := restDay.GetMyRestDay(ctx, uctx.UserID)
		if err != nil {
			return "", err
		}

		return marshalJSON(map[string]interface{}{
			"day_of_week": dayOfWeek,
			"day_name":    dayName,
		})
	})

	// query_my_leave
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_my_leave",
			Description: "查询当前用户近期请假记录",
			Parameters: json.RawMessage(`{
                "type": "object",
                "properties": {
                    "days": {"type": "integer", "description": "查询天数，默认30"}
                }
            }`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			Days int `json:"days"`
		}
		_ = json.Unmarshal(params, &p)

		days := p.Days
		if days <= 0 {
			days = 30
		}

		items, err := leave.GetRecentLeaves(ctx, uctx.UserID, days)
		if err != nil {
			return "", err
		}

		return marshalJSON(map[string]interface{}{
			"days":  days,
			"count": len(items),
			"items": items,
		})
	})
}

// formatNameList 将姓名切片转为编号列表字符串，供 LLM 直接引用
func formatNameList(names []string) string {
	if len(names) == 0 {
		return "无"
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%d. %s", i+1, n)
	}
	return strings.Join(parts, "\n")
}

// formatLeaveList 将请假用户列表转为编号列表字符串
func formatLeaveList(users []AttendLeave) string {
	if len(users) == 0 {
		return "无"
	}
	parts := make([]string, len(users))
	for i, u := range users {
		parts[i] = fmt.Sprintf("%d. %s（%s）", i+1, u.Name, u.LeaveType)
	}
	return strings.Join(parts, "\n")
}

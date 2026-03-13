package tools

import (
	"context"
	"encoding/json"
	"time"
)

// RegisterAttendanceTools 注册考勤、休息日、请假相关工具
func RegisterAttendanceTools(r *Registry, attendance AttendancePort, semester SemesterPort, restDay RestDayPort, leave LeavePort) {
	// query_attendance_status
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_attendance_status",
			Description: "查询指定日期指定节次的考勤状态",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"date":    {"type": "string", "description": "日期(YYYY-MM-DD)，默认今天"},
					"week":    {"type": "integer", "description": "周次，默认自动计算"},
					"section": {"type": "integer", "description": "节次(必填)"}
				},
				"required": ["section"]
			}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			Date    string `json:"date"`
			Week    int    `json:"week"`
			Section int    `json:"section"`
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

		result, err := attendance.GetAttendanceDetail(ctx, AttendanceQuery{
			Date:    p.Date,
			Week:    p.Week,
			Section: p.Section,
		})
		if err != nil {
			return "", err
		}

		return marshalJSON(result)
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
					"date":    {"type": "string", "description": "日期(YYYY-MM-DD)，默认今天"},
					"week":    {"type": "integer", "description": "周次，默认自动计算"},
					"section": {"type": "integer", "description": "节次(必填)"}
				},
				"required": ["section"]
			}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			Date    string `json:"date"`
			Week    int    `json:"week"`
			Section int    `json:"section"`
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

		text, err := attendance.GetAttendanceText(ctx, AttendanceQuery{
			Date:    p.Date,
			Week:    p.Week,
			Section: p.Section,
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

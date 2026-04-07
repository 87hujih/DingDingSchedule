package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RegisterScheduleTools 注册课表相关工具
func RegisterScheduleTools(r *Registry, schedule SchedulePort, user UserPort, semester SemesterPort, period SchedulePeriodPort, dept DeptPort) {
	// get_current_time
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "get_current_time",
			Description: "获取当前日期、星期、第几周",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		now := time.Now()
		weekdays := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

		result := map[string]interface{}{
			"date":        now.Format("2006-01-02"),
			"weekday":     weekdays[now.Weekday()],
			"weekday_num": weekdayNumberForTool(now.Weekday()),
		}

		week, total, err := semester.GetCurrentWeek(ctx)
		if err != nil {
			result["week"] = 0
			result["error"] = "当前无活跃学期"
		} else {
			result["week"] = week
			result["total_weeks"] = total
		}

		return marshalJSON(result)
	})

	// query_my_schedule
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_my_schedule",
			Description: "查询当前用户指定周的课表",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"week": {"type": "integer", "description": "周次，不传则使用当前周"}
				}
			}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			Week int `json:"week"`
		}
		_ = json.Unmarshal(params, &p)

		week := p.Week
		if week <= 0 {
			w, _, err := semester.GetCurrentWeek(ctx)
			if err != nil {
				return marshalJSON(map[string]interface{}{"error": "无法获取当前周次: " + err.Error()})
			}
			week = w
		}

		courses, err := schedule.ListMyScheduleByWeek(ctx, uctx.UserID, week)
		if err != nil {
			return "", err
		}

		return marshalJSON(map[string]interface{}{
			"week":    week,
			"count":   len(courses),
			"courses": courses,
		})
	})

	// query_user_schedule
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_user_schedule",
			Description: "查询指定用户指定周的课表",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"user_name": {"type": "string", "description": "要查询的用户姓名"},
					"week": {"type": "integer", "description": "周次，不传则使用当前周"}
				},
				"required": ["user_name"]
			}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			UserName string `json:"user_name"`
			Week     int    `json:"week"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return marshalJSON(map[string]interface{}{"error": "参数解析失败"})
		}

		week := p.Week
		if week <= 0 {
			w, _, err := semester.GetCurrentWeek(ctx)
			if err != nil {
				return marshalJSON(map[string]interface{}{"error": "无法获取当前周次: " + err.Error()})
			}
			week = w
		}

		users, err := user.SearchByName(ctx, p.UserName)
		if err != nil {
			return "", err
		}
		if len(users) == 0 {
			return marshalJSON(map[string]interface{}{
				"error":      fmt.Sprintf("找不到用户「%s」，请确认姓名", p.UserName),
				"error_code": "user_name_not_found",
			})
		}
		if len(users) > 1 {
			names := make([]string, len(users))
			for i, item := range users {
				names[i] = item.Name
			}
			return marshalJSON(map[string]interface{}{
				"error":           fmt.Sprintf("找到%d个同名用户，请从以下候选中选择", len(users)),
				"error_code":      "user_name_ambiguous",
				"candidate_users": names,
			})
		}

		targetUser := users[0]
		courses, err := schedule.ListUserScheduleByWeek(ctx, uctx.UserID, uctx.UserRole, targetUser.ID, week)
		if err != nil {
			return "", err
		}

		return marshalJSON(map[string]interface{}{
			"week":      week,
			"user_name": targetUser.Name,
			"count":     len(courses),
			"courses":   courses,
		})
	})

	// query_free_users_by_slot
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_free_users_by_slot",
			Description: "汇总指定周次、指定星期范围各节次的无课人员名单，可按部门筛选",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"week":      {"type": "integer", "description": "周次，不传则使用当前周"},
					"day_start": {"type": "integer", "description": "起始星期(1-7)，默认1"},
					"day_end":   {"type": "integer", "description": "结束星期(1-7)，默认5"},
					"dept_name": {"type": "string", "description": "按部门名称筛选，优先于 dept_id"},
					"dept_id":   {"type": "integer", "description": "按部门ID筛选，兼容保留字段；当 dept_name 存在时会被忽略"}
				}
			}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			Week     int    `json:"week"`
			DayStart int    `json:"day_start"`
			DayEnd   int    `json:"day_end"`
			DeptName string `json:"dept_name"`
			DeptID   int64  `json:"dept_id"`
		}
		_ = json.Unmarshal(params, &p)

		week := p.Week
		if week <= 0 {
			w, _, err := semester.GetCurrentWeek(ctx)
			if err != nil {
				return marshalJSON(map[string]interface{}{"error": "无法获取当前周次: " + err.Error()})
			}
			week = w
		}
		dayStart := p.DayStart
		if dayStart <= 0 {
			dayStart = 1
		}
		dayEnd := p.DayEnd
		if dayEnd <= 0 {
			dayEnd = 5
		}

		resolvedDeptID, useDeptFilter, payload, err := resolveDeptFilter(ctx, dept, p.DeptID, p.DeptName)
		if err != nil {
			return "", err
		}
		if payload != "" {
			return payload, nil
		}
		if !useDeptFilter {
			resolvedDeptID = 0
		}

		slots, err := schedule.GetFreeUsersBySlot(ctx, week, dayStart, dayEnd, resolvedDeptID)
		if err != nil {
			return "", err
		}

		weekdayNames := []string{"", "周一", "周二", "周三", "周四", "周五", "周六", "周日"}
		formattedSlots := make([]map[string]interface{}, len(slots))
		for i, s := range slots {
			dayName := ""
			if s.DayOfWeek >= 1 && s.DayOfWeek <= 7 {
				dayName = weekdayNames[s.DayOfWeek]
			}
			formattedSlots[i] = map[string]interface{}{
				"day_of_week": s.DayOfWeek,
				"day_name":    dayName,
				"section":     s.Section,
				"slot_start":  s.SlotStart,
				"slot_end":    s.SlotEnd,
				"free_count":  s.FreeCount,
				"free_users":  s.FreeUsers,
			}
		}

		return marshalJSON(map[string]interface{}{
			"week":      week,
			"day_start": dayStart,
			"day_end":   dayEnd,
			"slots":     formattedSlots,
		})
	})

	// query_schedule_info
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_schedule_info",
			Description: "查询当前作息模式及各节次时间安排",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		periods, mode, err := period.GetScheduleInfo(ctx)
		if err != nil {
			return "", err
		}

		return marshalJSON(map[string]interface{}{
			"mode":    mode,
			"periods": periods,
		})
	})
}

func weekdayNumberForTool(day time.Weekday) int {
	if day == time.Sunday {
		return 7
	}
	return int(day)
}

func marshalJSON(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}
	return string(b), nil
}

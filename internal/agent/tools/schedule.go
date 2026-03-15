package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RegisterScheduleTools 注册课表相关工具
func RegisterScheduleTools(r *Registry, schedule SchedulePort, semester SemesterPort, period SchedulePeriodPort) {
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
			"weekday_num": int(now.Weekday()),
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

	// query_free_users_by_slot
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_free_users_by_slot",
			Description: "汇总指定周次、指定星期范围各节次的无课人员名单",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"week":      {"type": "integer", "description": "周次，不传则使用当前周"},
					"day_start": {"type": "integer", "description": "起始星期(1-7)，默认1"},
					"day_end":   {"type": "integer", "description": "结束星期(1-7)，默认5"}
				}
			}`),
		},
	}, 0, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			Week     int `json:"week"`
			DayStart int `json:"day_start"`
			DayEnd   int `json:"day_end"`
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

		slots, err := schedule.GetFreeUsersBySlot(ctx, week, dayStart, dayEnd)
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
				"free_users":  formatNameList(s.FreeUsers),
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

func marshalJSON(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}
	return string(b), nil
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RegisterAdminTools 注册管理员工具
func RegisterAdminTools(r *Registry, attendance AttendancePort, user UserPort, groupSub GroupSubPort, dept DeptPort) {
	// sign_for_user
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "sign_for_user",
			Description: "为指定用户补签某节次考勤（仅管理员可用）",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"user_name": {"type": "string", "description": "用户姓名"},
					"date":      {"type": "string", "description": "日期(YYYY-MM-DD)，默认今天"},
					"section":   {"type": "integer", "description": "节次(必填)"}
				},
				"required": ["user_name", "section"]
			}`),
		},
	}, 1, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		var p struct {
			UserName string `json:"user_name"`
			Date     string `json:"date"`
			Section  int    `json:"section"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return marshalJSON(map[string]interface{}{"error": "参数解析失败"})
		}

		if p.Date == "" {
			p.Date = time.Now().Format("2006-01-02")
		}

		// 1. 查找用户
		users, err := user.SearchByName(ctx, p.UserName)
		if err != nil {
			return "", err
		}
		if len(users) == 0 {
			return marshalJSON(map[string]interface{}{
				"error": fmt.Sprintf("找不到用户「%s」，请确认姓名", p.UserName),
			})
		}
		if len(users) > 1 {
			names := make([]string, len(users))
			for i, u := range users {
				names[i] = u.Name
			}
			return marshalJSON(map[string]interface{}{
				"error": fmt.Sprintf("找到%d个同名用户，请提供更精确的姓名", len(users)),
				"users": names,
			})
		}

		targetUser := users[0]

		// 2. 查找考勤记录
		recordID, err := attendance.FindRecordByDateSection(ctx, p.Date, p.Section)
		if err != nil {
			return "", err
		}
		if recordID == 0 {
			return marshalJSON(map[string]interface{}{
				"error": "该节次尚未统计，请等待系统自动统计后再操作",
			})
		}

		// 3. 补签
		if err := attendance.SignForUsers(ctx, recordID, []uint{targetUser.ID}); err != nil {
			return "", err
		}

		return marshalJSON(map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("已为%s补签 %s 第%d节考勤", targetUser.Name, p.Date, p.Section),
		})
	})

	// subscribe_attendance_push
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "subscribe_attendance_push",
			Description: "将当前群聊订阅为考勤自动推送目标（仅管理员，群聊中使用）。可通过 dept_ids 指定只推送特定部门的考勤，不填则推送全部人员的考勤",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"dept_ids": {
						"type": "array",
						"items": {"type": "integer"},
						"description": "只推送这些部门ID的考勤，不填表示推送全部人员"
					}
				}
			}`),
		},
	}, 1, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		if uctx.ConversationType != "2" {
			return marshalJSON(map[string]interface{}{
				"error": "该功能只能在群聊中使用",
			})
		}

		var p struct {
			DeptIDs []int64 `json:"dept_ids"`
		}
		_ = json.Unmarshal(params, &p)

		if err := groupSub.Subscribe(ctx, uctx.TenantID, uctx.ConversationID, uctx.ConversationTitle, uctx.UserID, p.DeptIDs); err != nil {
			return "", err
		}

		msg := "已为此群开启考勤推送"
		if len(p.DeptIDs) > 0 {
			msg = fmt.Sprintf("已为此群开启考勤推送（仅限%d个指定部门）", len(p.DeptIDs))
		}
		return marshalJSON(map[string]interface{}{
			"success": true,
			"message": msg,
		})
	})

	// unsubscribe_attendance_push
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "unsubscribe_attendance_push",
			Description: "取消当前群聊的考勤自动推送（仅管理员，群聊中使用）",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}, 1, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		if uctx.ConversationType != "2" {
			return marshalJSON(map[string]interface{}{
				"error": "该功能只能在群聊中使用",
			})
		}

		if err := groupSub.Unsubscribe(ctx, uctx.TenantID, uctx.ConversationID); err != nil {
			return "", err
		}

		return marshalJSON(map[string]interface{}{
			"success": true,
			"message": "已取消此群的考勤自动推送",
		})
	})

	// query_subscription_status
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "query_subscription_status",
			Description: "查询当前群聊是否已订阅考勤自动推送（群聊中使用）",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}, 1, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		if uctx.ConversationType != "2" {
			return marshalJSON(map[string]interface{}{
				"error": "该功能只能在群聊中使用",
			})
		}

		info, err := groupSub.GetSubscription(ctx, uctx.TenantID, uctx.ConversationID)
		if err != nil {
			return "", err
		}
		return marshalJSON(info)
	})

	// list_departments
	r.Register(ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        "list_departments",
			Description: "查询当前租户下参与考勤的部门列表",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}, 1, func(ctx context.Context, uctx *UserContext, params json.RawMessage) (string, error) {
		depts, err := dept.ListDepts(ctx)
		if err != nil {
			return "", err
		}
		names := make([]string, 0, len(depts))
		for _, d := range depts {
			names = append(names, d.Name)
		}
		return marshalJSON(map[string]interface{}{
			"count": len(names),
			"depts": names,
		})
	})
}

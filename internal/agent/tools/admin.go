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
				"error":      fmt.Sprintf("找不到用户「%s」，请确认姓名", p.UserName),
				"error_code": "user_name_not_found",
			})
		}
		if len(users) > 1 {
			names := make([]string, len(users))
			for i, u := range users {
				names[i] = u.Name
			}
			return marshalJSON(map[string]interface{}{
				"error":           fmt.Sprintf("找到%d个同名用户，请提供更精确的姓名", len(users)),
				"error_code":      "user_name_ambiguous",
				"candidate_users": names,
				"users":           names,
			})
		}

		targetUser := users[0]

		// 2. 直接按日期和节次补签，避免实时阶段依赖已落库快照
		if err := attendance.SignForUsersBySlot(ctx, p.Date, p.Section, []uint{targetUser.ID}); err != nil {
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
			Description: "将当前群聊订阅为考勤自动推送目标（仅管理员，群聊中使用）。可通过 dept_names 指定只推送特定部门的考勤，不填则推送全部人员的考勤。部门名称须先通过 list_departments 查询获得",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"dept_names": {
						"type": "array",
						"items": {"type": "string"},
						"description": "只推送这些部门名称的考勤，不填表示推送全部人员。名称必须与 list_departments 返回的一致"
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
			DeptNames []string `json:"dept_names"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return marshalJSON(map[string]interface{}{"error": "参数解析失败"})
		}

		// 将部门名称解析为真实的 dept_id
		var deptIDs []int64
		if len(p.DeptNames) > 0 {
			allDepts, err := dept.ListDepts(ctx)
			if err != nil {
				return "", err
			}
			var notFound []string
			var ambiguous []string
			seenDeptIDs := make(map[int64]struct{}, len(p.DeptNames))
			for _, name := range p.DeptNames {
				matches := findDeptMatchesByName(allDepts, name)
				switch len(matches) {
				case 0:
					notFound = append(notFound, name)
				case 1:
					if _, seen := seenDeptIDs[matches[0].DeptID]; seen {
						continue
					}
					seenDeptIDs[matches[0].DeptID] = struct{}{}
					deptIDs = append(deptIDs, matches[0].DeptID)
				default:
					ambiguous = append(ambiguous, name)
				}
			}
			if len(ambiguous) > 0 {
				return marshalJSON(map[string]interface{}{
					"error":                fmt.Sprintf("以下部门名称不唯一，请通过 list_departments 确认：%v", ambiguous),
					"error_code":           "department_name_ambiguous",
					"ambiguous_dept_names": ambiguous,
				})
			}
			if len(notFound) > 0 {
				return marshalJSON(map[string]interface{}{
					"error":              fmt.Sprintf("以下部门名称不存在，请通过 list_departments 确认：%v", notFound),
					"error_code":         "department_name_not_found",
					"invalid_dept_names": notFound,
				})
			}
		}

		if err := groupSub.Subscribe(ctx, uctx.TenantID, uctx.ConversationID, uctx.ConversationTitle, uctx.UserID, deptIDs); err != nil {
			return "", err
		}

		msg := "已为此群开启考勤推送"
		if len(p.DeptNames) > 0 {
			msg = fmt.Sprintf("已为此群开启考勤推送（仅限：%v）", p.DeptNames)
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
		type deptInfo struct {
			DeptID int64  `json:"dept_id"`
			Name   string `json:"name"`
		}
		items := make([]deptInfo, 0, len(depts))
		for _, d := range depts {
			items = append(items, deptInfo{DeptID: d.DeptID, Name: d.Name})
		}
		return marshalJSON(map[string]interface{}{
			"count": len(items),
			"depts": items,
		})
	})
}

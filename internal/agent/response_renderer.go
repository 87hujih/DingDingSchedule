package agent

import "strings"

type ResponseOption struct {
	Label string
	Value string
}

type ResponseModel struct {
	Kind          ResponseKind
	Operation     string
	Message       string
	Answer        string
	ClarifyReason string
	MissingFields []string
	Options       []ResponseOption
	ResultText    string
	RefusalReason string
	BusinessError string
}

// renderProtocolResponse renders a structured protocol response into plain text.
func renderProtocolResponse(model ResponseModel) string {
	switch model.Kind {
	case ResponseAnswer:
		if strings.TrimSpace(model.BusinessError) == "no_knowledge_hit" {
			return "当前租户还没有配置可用于回答这个问题的规则说明。"
		}
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		if text := safeProtocolText(model.Answer); text != "" {
			return text
		}
		return "我可以先回答你的能力、规则或查询问题。"
	case ResponseClarify:
		if reply := renderMissingFieldsClarify(model.Operation, model.MissingFields); reply != "" {
			return reply
		}
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		switch strings.TrimSpace(model.ClarifyReason) {
		case "unknown_intent":
			return "请再明确一下你的问题。你可以说：查询今天第二节考勤状态、查我的课表、开启本群考勤订阅。"
		case "subscription_missing_fields":
			return "请选择订阅范围：全部人员 / 指定部门。"
		case "missing_attendance_fields":
			return "请补充要查询的日期和第几节。"
		default:
			return "请再明确一下你的需求。"
		}
	case ResponseSelectOptions:
		if len(model.Options) == 0 {
			return "请从可选项中明确选择后再继续。"
		}
		labels := make([]string, 0, len(model.Options))
		for _, option := range model.Options {
			label := strings.TrimSpace(option.Label)
			if label == "" {
				label = strings.TrimSpace(option.Value)
			}
			if label == "" {
				continue
			}
			labels = append(labels, label)
		}
		if len(labels) == 0 {
			return "请从可选项中明确选择后再继续。"
		}
		return "请从这些选项中明确选择：" + strings.Join(labels, "、")
	case ResponseResult:
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		if text := safeProtocolText(model.ResultText); text != "" {
			return text
		}
		return "操作已完成。"
	case ResponseRefuse:
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		if text := safeProtocolText(model.RefusalReason); text != "" {
			return text
		}
		return "抱歉，我当前不能直接执行这个请求。"
	case ResponseConfirm:
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		return "请确认是否执行该操作。"
	default:
		return "请再明确一下你的需求。"
	}
}

func renderMissingFieldsClarify(operation string, fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	if operation == "attendance.query_status" && containsField(fields, "date") && containsField(fields, "section") {
		return "请补充要查询哪一天和第几节。"
	}
	if operation == "subscription.start" && containsField(fields, "scope") {
		return "请选择订阅范围：全部人员 / 指定部门。"
	}
	if operation == "attendance.query_status" && containsField(fields, "section") {
		return "请补充要查询第几节。"
	}
	if operation == "attendance.query_status" && containsField(fields, "date") {
		return "请补充要查询哪一天。"
	}
	return ""
}

func containsField(fields []string, target string) bool {
	for _, field := range fields {
		if field == target {
			return true
		}
	}
	return false
}

func safeProtocolText(value string) string {
	text := strings.TrimSpace(value)
	if text == "" || looksLikeInternalPayload(text) {
		return ""
	}
	return text
}

func looksLikeInternalPayload(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "error_code") || strings.Contains(lower, "internalerror") {
		return true
	}
	if (strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}")) ||
		(strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]")) {
		return true
	}
	return false
}

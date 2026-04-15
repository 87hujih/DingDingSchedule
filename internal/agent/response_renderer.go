package agent

import "strings"

type ResponseOption struct {
	Label string
	Value string
}

type ResponseModel struct {
	Kind          ResponseKind
	Answer        string
	ClarifyReason string
	Options       []ResponseOption
	ConfirmText   string
	ResultText    string
	RefusalReason string
	InternalError string
}

// renderProtocolResponse renders a structured protocol response into plain text.
func renderProtocolResponse(model ResponseModel) string {
	switch model.Kind {
	case ResponseAnswer:
		if strings.TrimSpace(model.Answer) != "" {
			return strings.TrimSpace(model.Answer)
		}
		return "我可以先回答你的能力、规则或查询问题。"
	case ResponseClarify:
		switch strings.TrimSpace(model.ClarifyReason) {
		case "unknown_intent":
			return "请再明确一下你的问题，我会按明确的查询或操作继续处理。"
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
	case ResponseConfirm:
		if strings.TrimSpace(model.ConfirmText) != "" {
			return strings.TrimSpace(model.ConfirmText)
		}
		return "请确认后我再继续执行。"
	case ResponseResult:
		if strings.TrimSpace(model.ResultText) != "" {
			return strings.TrimSpace(model.ResultText)
		}
		return "操作已完成。"
	case ResponseRefuse:
		if strings.TrimSpace(model.RefusalReason) != "" {
			return strings.TrimSpace(model.RefusalReason)
		}
		return "抱歉，我当前不能直接执行这个请求。"
	default:
		return "请再明确一下你的需求。"
	}
}

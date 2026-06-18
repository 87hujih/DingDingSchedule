package agent

import (
	"strings"

	"schedule_server/internal/agent/tools"
)

func protocolLiveRoleRefusal(uctx *tools.UserContext, draft ProtocolDraft) (bool, ResponseModel) {
	if draft.Act == ActWorkflowCancel {
		return false, ResponseModel{}
	}
	metadata, ok := lookupOperation(draft.Operation)
	if !ok || metadata.MinRole <= 0 {
		return false, ResponseModel{}
	}
	if uctx != nil && uctx.UserRole >= metadata.MinRole {
		return false, ResponseModel{}
	}
	return true, ResponseModel{
		Kind:          ResponseRefuse,
		RefusalReason: "该操作需要管理员权限。",
	}
}

func protocolLiveGuardrailResponse(draft ProtocolDraft, validation ProtocolValidationResult, uctx *tools.UserContext) (ResponseModel, answerMode) {
	switch draft.Act {
	case ActCapabilityQuestion:
		return ResponseModel{Kind: ResponseAnswer, Answer: buildProtocolCapabilityReply(draft.Domain, uctx)}, answerModeToolFirst
	case ActHelp:
		return ResponseModel{Kind: ResponseAnswer, Answer: buildHelpReply(uctx)}, answerModeToolFirst
	case ActUnknown:
		return ResponseModel{Kind: ResponseClarify, ClarifyReason: protocolUnknownIntentReasonCode(draft)}, answerModeReject
	default:
		switch validation.ValidationCode {
		case "conversation_scope_denied":
			if draft.Domain == DomainSubscription {
				return ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅只能在群聊中使用。请在对应群聊里再告诉我。"}, answerModeReject
			}
			return ResponseModel{Kind: ResponseRefuse, RefusalReason: "抱歉，我当前不能直接执行这个请求。"}, answerModeReject
		case "operation_not_allowed", "act_operation_mismatch", "read_query_cannot_write", "unsupported_act", "domain_operation_mismatch", "workflow_operation_mismatch", "role_denied":
			return ResponseModel{Kind: ResponseRefuse, RefusalReason: "抱歉，我当前不能直接执行这个请求。"}, answerModeReject
		case "low_confidence_write":
			return ResponseModel{Kind: ResponseClarify, ClarifyReason: "unknown_intent"}, answerModeReject
		default:
			return ResponseModel{Kind: ResponseClarify, ClarifyReason: "unknown_intent"}, answerModeReject
		}
	}
}

func protocolUnknownIntentReasonCode(draft ProtocolDraft) string {
	switch strings.TrimSpace(firstNonEmpty(draft.ClarifyReason, draft.Reason)) {
	case "empty_message":
		return "empty_message"
	case "intent_parse_failed":
		return "intent_parse_failed"
	case "intent_timeout":
		return "intent_timeout"
	case "intent_compiler_unavailable":
		return "intent_compiler_unavailable"
	case "operation_not_allowed":
		return "operation_not_allowed"
	default:
		return "unknown_intent"
	}
}

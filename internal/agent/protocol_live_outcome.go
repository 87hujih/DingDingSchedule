package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

var protocolLiveRequestSeq uint64

func newProtocolLiveRequestID(now time.Time) string {
	seq := atomic.AddUint64(&protocolLiveRequestSeq, 1)
	if now.IsZero() {
		now = time.Now()
	}
	return fmt.Sprintf("plive-%d-%d", now.UnixNano(), seq)
}

func finalizeProtocolLiveOutcome(outcome *protocolLiveOutcome) {
	if outcome == nil {
		return
	}
	if outcome.RequestID == "" {
		outcome.RequestID = newProtocolLiveRequestID(time.Now())
	}
	if outcome.RendererName == "" {
		outcome.RendererName = "response_renderer"
	}
	if outcome.CatalogValidationCode == "" {
		outcome.CatalogValidationCode = outcome.Validation.ValidationCode
	}
	if outcome.PrePolicyResult == "" {
		outcome.PrePolicyResult = protocolPrePolicyResult(outcome.Validation)
	}
	if outcome.ResourcePolicyResult == "" {
		outcome.ResourcePolicyResult = "not_evaluated"
	}
	if outcome.WorkflowDecision == "" {
		outcome.WorkflowDecision = WorkflowSingleTurn
	}
	if outcome.BlockedReason == "" {
		outcome.BlockedReason = protocolResponseBlockedReason(outcome.Response, outcome.Validation)
	}
	if outcome.EntityResolutionStatus == "" {
		outcome.EntityResolutionStatus = protocolEntityResolutionStatus(*outcome)
	}
	if outcome.WriteGuardResult == "" {
		outcome.WriteGuardResult = protocolWriteGuardResult(*outcome)
	}
	if outcome.ExecutorStatus == "" {
		outcome.ExecutorStatus = protocolExecutorStatus(outcome.Response)
	}
	if outcome.FailureLayer == "" {
		outcome.FailureLayer = inferFailureLayer(*outcome)
	}
}

func compactIntentDraft(draft ProtocolDraft) string {
	data, err := json.Marshal(draft)
	if err != nil {
		return ""
	}
	return string(data)
}

func protocolPrePolicyResult(validation ProtocolValidationResult) string {
	if validation.AllowExecution || validation.ResponseKind == ResponseAnswer || validation.UseActiveWorkflow {
		return "allow"
	}
	if strings.TrimSpace(validation.ValidationCode) == "" {
		return ""
	}
	return "deny:" + validation.ValidationCode
}

func protocolEntityResolutionStatus(outcome protocolLiveOutcome) string {
	if len(outcome.ResolvedSlots) > 0 {
		return string(ResolveResolved)
	}
	if outcome.Response.Kind == ResponseSelectOptions || outcome.CandidateCount > 0 {
		return string(ResolveAmbiguous)
	}
	if strings.HasPrefix(outcome.BlockedReason, "missing_") {
		return string(ResolveNotFound)
	}
	return "not_required"
}

func protocolWriteGuardResult(outcome protocolLiveOutcome) string {
	metadata, ok := lookupOperation(outcome.Draft.Operation)
	if !ok {
		return "not_evaluated"
	}
	if !metadata.IsWrite {
		return "not_required"
	}
	if strings.TrimSpace(outcome.IdempotencyKey) != "" && outcome.FailureLayer != FailureWriteGuardBlocked {
		return "allow"
	}
	if outcome.FailureLayer == FailureWriteGuardBlocked {
		return "block:" + outcome.BlockedReason
	}
	return "not_evaluated"
}

func protocolExecutorStatus(response ResponseModel) string {
	switch response.Kind {
	case ResponseResult, ResponseAnswer, ResponseSelectOptions:
		return "success"
	case ResponseRefuse:
		return "failed"
	case ResponseClarify, ResponseConfirm:
		return "skipped"
	default:
		return ""
	}
}

func inferFailureLayer(outcome protocolLiveOutcome) FailureLayer {
	if outcome.Response.Kind == ResponseResult || outcome.Response.Kind == ResponseAnswer {
		return ""
	}
	reason := firstNonEmpty(outcome.BlockedReason, outcome.Validation.ValidationCode)
	switch reason {
	case "", "missing_scope", "subscription_missing_fields":
		return ""
	case "empty_message", "unknown_intent", "intent_parse_failed", "intent_timeout", "intent_compiler_unavailable":
		return FailureIntent
	case "operation_not_allowed", "act_operation_mismatch", "read_query_cannot_write", "write_request_cannot_read", "unsupported_act", "domain_operation_mismatch":
		return FailureCatalog
	case "workflow_missing", "workflow_operation_mismatch", "workflow_store_failed", "subscription_invalid_shape":
		return FailureWorkflow
	case "role_denied", "conversation_scope_denied":
		return FailurePrePolicyDenied
	case "subscription_conversation_mismatch", "schedule_user_visibility_denied", "department_scope_denied", "department_scope_unverified", "group_chat_required", "subscription_scope_invalid":
		return FailureResourcePolicyDenied
	case "write_confirmation_required", "idempotency_key_missing":
		return FailureWriteGuardBlocked
	default:
		if strings.Contains(reason, "ambiguous") {
			return FailureEntityAmbiguous
		}
		if strings.Contains(reason, "not_found") || strings.HasPrefix(reason, "missing_user") || strings.HasPrefix(reason, "missing_dept") {
			return FailureEntityNotFound
		}
	}
	if outcome.Response.Kind == ResponseRefuse {
		return FailureExecutor
	}
	return ""
}

func setProtocolOutcomeResponse(outcome *protocolLiveOutcome, response ResponseModel, mode answerMode) {
	if outcome == nil {
		return
	}
	outcome.Response = response
	outcome.AnswerMode = mode
	if outcome.BlockedReason == "" {
		outcome.BlockedReason = protocolResponseBlockedReason(response, outcome.Validation)
	}
	if outcome.CandidateCount == 0 {
		outcome.CandidateCount = len(response.Options)
	}
}

func protocolResponseBlockedReason(response ResponseModel, validation ProtocolValidationResult) string {
	switch response.Kind {
	case ResponseClarify:
		if len(response.MissingFields) > 0 {
			return "missing_" + response.MissingFields[0]
		}
		if reason := strings.TrimSpace(response.ClarifyReason); reason != "" {
			return reason
		}
		if validation.ValidationCode != "" && !validation.AllowExecution {
			return validation.ValidationCode
		}
	case ResponseRefuse:
		if validation.ValidationCode != "" {
			return validation.ValidationCode
		}
		return "refused"
	}
	return ""
}

func mergeProtocolResolvedSlots(base map[string]any, next map[string]any) map[string]any {
	if len(next) == 0 {
		return base
	}
	merged := make(map[string]any, len(base)+len(next))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range next {
		merged[key] = value
	}
	return merged
}

func protocolResolvedSlotsFromParams(params map[string]TrustedParam) map[string]any {
	if len(params) == 0 {
		return nil
	}
	slots := make(map[string]any)
	for key, param := range params {
		if key == "conversation_id" {
			continue
		}
		switch key {
		case "date", "week", "section", "user_id", "scope", "dept_ids", "rule_topic", "query_shape":
			slots[key] = param.Value
		}
	}
	if len(slots) == 0 {
		return nil
	}
	return slots
}

func protocolResolvedSlotsFromTrusted(trusted trustedEntities) map[string]any {
	slots := make(map[string]any)
	if trusted.Date != "" {
		slots["date"] = trusted.Date
	}
	if trusted.Week != 0 {
		slots["week"] = trusted.Week
	}
	if trusted.Section != 0 {
		slots["section"] = trusted.Section
	}
	if trusted.UserID != 0 {
		slots["user_id"] = trusted.UserID
	}
	if trusted.Scope != "" {
		slots["scope"] = trusted.Scope
	}
	if len(trusted.DeptIDs) > 0 {
		slots["dept_ids"] = append([]int64(nil), trusted.DeptIDs...)
	} else if trusted.DepartmentID != 0 {
		slots["dept_ids"] = []int64{trusted.DepartmentID}
	}
	if trusted.QueryShape != "" {
		slots["query_shape"] = trusted.QueryShape
	}
	if len(slots) == 0 {
		return nil
	}
	return slots
}

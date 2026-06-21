package agent

import (
	"strings"

	"schedule_server/internal/agent/tools"
)

const (
	operationParamActorRole         = "actor_role"
	operationParamConversationType  = "conversation_type"
	operationParamConversationTitle = "conversation_title"
)

func enrichOperationRequestFromUser(req OperationRequest, uctx *tools.UserContext) OperationRequest {
	if uctx == nil {
		return req
	}
	if req.TenantID == 0 {
		req.TenantID = uctx.TenantID
	}
	if req.ActorUserID == 0 {
		req.ActorUserID = uctx.UserID
	}
	if strings.TrimSpace(req.ConversationID) == "" {
		if strings.TrimSpace(uctx.ConversationID) != "" {
			req.ConversationID = strings.TrimSpace(uctx.ConversationID)
		} else if value, ok := extractParamString(req.TrustedParams, "conversation_id"); ok {
			req.ConversationID = value
		}
	}
	if req.TrustedParams == nil {
		req.TrustedParams = make(map[string]TrustedParam)
	}
	source := TrustedParamSource{Kind: TrustedParamSourceRuntime, Resolver: "user_context_runtime"}
	if _, ok := req.TrustedParams[operationParamActorRole]; !ok {
		req.TrustedParams[operationParamActorRole] = trustedParam(operationParamActorRole, uctx.UserRole, uctx.TenantID, source)
	}
	if _, ok := req.TrustedParams[operationParamConversationType]; !ok && strings.TrimSpace(uctx.ConversationType) != "" {
		req.TrustedParams[operationParamConversationType] = trustedParam(operationParamConversationType, strings.TrimSpace(uctx.ConversationType), uctx.TenantID, source)
	}
	if _, ok := req.TrustedParams[operationParamConversationTitle]; !ok && strings.TrimSpace(uctx.ConversationTitle) != "" {
		req.TrustedParams[operationParamConversationTitle] = trustedParam(operationParamConversationTitle, strings.TrimSpace(uctx.ConversationTitle), uctx.TenantID, source)
	}
	return req
}

func operationRequestActorRole(req OperationRequest) int {
	role, _ := extractParamInt(req.TrustedParams, operationParamActorRole)
	return role
}

func operationRequestConversationType(req OperationRequest) string {
	value, _ := extractParamString(req.TrustedParams, operationParamConversationType)
	return strings.TrimSpace(value)
}

func operationRequestConversationTitle(req OperationRequest) string {
	value, _ := extractParamString(req.TrustedParams, operationParamConversationTitle)
	return strings.TrimSpace(value)
}

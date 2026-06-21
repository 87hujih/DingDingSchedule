package agent

import (
	"strings"

	"schedule_server/internal/agent/tools"
)

func inputUserRole(uctx *tools.UserContext) int {
	if uctx == nil {
		return 0
	}
	return uctx.UserRole
}

func userTenantID(uctx *tools.UserContext) uint {
	if uctx == nil {
		return 0
	}
	return uctx.TenantID
}

func userConversationID(uctx *tools.UserContext) string {
	if uctx == nil {
		return ""
	}
	return uctx.ConversationID
}

func userConversationType(uctx *tools.UserContext) string {
	if uctx == nil {
		return ""
	}
	return uctx.ConversationType
}

func userActorUserID(uctx *tools.UserContext) uint {
	if uctx == nil {
		return 0
	}
	return uctx.UserID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

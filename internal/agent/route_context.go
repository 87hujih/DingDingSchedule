package agent

import (
	"strings"

	"schedule_server/internal/agent/tools"
)

const maxRouteTurns = 6
const maxCandidateHints = 5

type TurnDigest struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type TaskRouteState struct {
	ID             string   `json:"id"`
	Type           string   `json:"type"`
	Status         string   `json:"status"`
	MissingSlots   []string `json:"missing_slots,omitempty"`
	LastErrorCode  string   `json:"last_error_code,omitempty"`
	ResultSummary  string   `json:"result_summary,omitempty"`
	CandidateHints []string `json:"candidate_hints,omitempty"`
}

type RouteContext struct {
	Message           string          `json:"message"`
	ConversationType  string          `json:"conversation_type,omitempty"`
	ConversationTitle string          `json:"conversation_title,omitempty"`
	UserRole          int             `json:"user_role,omitempty"`
	RecentTurns       []TurnDigest    `json:"recent_turns,omitempty"`
	ActiveTask        *TaskRouteState `json:"active_task,omitempty"`
}

// buildRouteContext builds the routing context string fed into the router model.
func buildRouteContext(message string, uctx *tools.UserContext, history []tools.Message, task *TaskInstance) RouteContext {
	ctx := RouteContext{
		Message:     strings.TrimSpace(message),
		RecentTurns: summarizeRecentTurns(history),
		ActiveTask:  summarizeTaskRouteState(task),
	}
	if uctx != nil {
		ctx.ConversationType = uctx.ConversationType
		ctx.ConversationTitle = uctx.ConversationTitle
		ctx.UserRole = uctx.UserRole
	}
	return ctx
}

// summarizeRecentTurns summarizes recent turns.
func summarizeRecentTurns(history []tools.Message) []TurnDigest {
	if len(history) == 0 {
		return nil
	}

	start := 0
	if len(history) > maxRouteTurns {
		start = len(history) - maxRouteTurns
	}
	turns := make([]TurnDigest, 0, len(history)-start)
	for _, msg := range history[start:] {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		turns = append(turns, TurnDigest{
			Role:    msg.Role,
			Content: content,
		})
	}
	return turns
}

// summarizeTaskRouteState summarizes task route state.
func summarizeTaskRouteState(task *TaskInstance) *TaskRouteState {
	if task == nil {
		return nil
	}

	return &TaskRouteState{
		ID:             task.ID,
		Type:           task.Type,
		Status:         task.Status,
		MissingSlots:   append([]string(nil), task.MissingSlots...),
		LastErrorCode:  strings.TrimSpace(task.LastErrorCode),
		ResultSummary:  strings.TrimSpace(task.ResultSummary),
		CandidateHints: summarizeCandidateHints(task.CandidateCache),
	}
}

// summarizeCandidateHints summarizes candidate hints.
func summarizeCandidateHints(cache map[string]any) []string {
	if len(cache) == 0 {
		return nil
	}

	orderedKeys := []string{"departments", "candidate_users"}
	hints := make([]string, 0, maxCandidateHints)
	for _, key := range orderedKeys {
		raw, ok := cache[key]
		if !ok {
			continue
		}
		names, ok := raw.([]string)
		if !ok {
			continue
		}
		for _, name := range names {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			hints = append(hints, trimmed)
			if len(hints) >= maxCandidateHints {
				return hints
			}
		}
	}
	return hints
}

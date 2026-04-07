package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

type manualSignTaskHandler struct{}

func newManualSignTaskHandler() *manualSignTaskHandler {
	return &manualSignTaskHandler{}
}

func (h *manualSignTaskHandler) Type() string {
	return "sign_for_user"
}

func (h *manualSignTaskHandler) CreateTask(message string, uctx *tools.UserContext) (*TaskInstance, TaskApplyResult) {
	task := &TaskInstance{
		Type:      h.Type(),
		Status:    string(taskStatusWaiting),
		Slots:     map[string]string{},
		UpdatedAt: time.Now(),
		ExpiresAt: time.Now().Add(sessionTTL),
	}

	apply, _ := h.ApplyTurn(task, message, uctx)
	return task, apply
}

func (h *manualSignTaskHandler) ApplyTurn(task *TaskInstance, message string, _ *tools.UserContext) (TaskApplyResult, error) {
	if task == nil {
		return TaskApplyResult{}, nil
	}
	if task.Slots == nil {
		task.Slots = make(map[string]string)
	}

	normalized := normalizeQuery(message)
	var matched []string
	switch {
	case containsAny(normalized, []string{"今天"}):
		taskApplySlot(task, &matched, "date", "today")
	case containsAny(normalized, []string{"昨天"}):
		taskApplySlot(task, &matched, "date", "yesterday")
	case containsAny(normalized, []string{"明天"}):
		taskApplySlot(task, &matched, "date", "tomorrow")
	}

	switch {
	case containsAny(normalized, []string{"第一节"}):
		taskApplySlot(task, &matched, "section", "1")
	case containsAny(normalized, []string{"第二节"}):
		taskApplySlot(task, &matched, "section", "2")
	case containsAny(normalized, []string{"第三节"}):
		taskApplySlot(task, &matched, "section", "3")
	case containsAny(normalized, []string{"第四节"}):
		taskApplySlot(task, &matched, "section", "4")
	case containsAny(normalized, []string{"第五节"}):
		taskApplySlot(task, &matched, "section", "5")
	}

	if userName := extractManualSignUserName(message); userName != "" {
		taskApplySlot(task, &matched, "user_name", userName)
	}

	reconcileManualSignTask(task)
	return TaskApplyResult{MatchedSlots: matched}, nil
}

func (h *manualSignTaskHandler) Prepare(context.Context, *TaskInstance, Deps) ([]string, error) {
	return nil, nil
}

func (h *manualSignTaskHandler) Execute(ctx context.Context, task *TaskInstance, uctx *tools.UserContext, registry *tools.Registry) (TaskResult, []string, error) {
	if task == nil {
		return TaskResult{}, nil, nil
	}

	section, err := strconv.Atoi(task.Slots["section"])
	if err != nil {
		return TaskResult{}, []string{"sign_for_user"}, err
	}

	payload := map[string]any{
		"user_name": task.Slots["user_name"],
		"date":      materializeTaskDate(task.Slots["date"]),
		"section":   section,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return TaskResult{}, []string{"sign_for_user"}, err
	}

	toolResult, err := registry.Dispatch(ctx, uctx, "sign_for_user", raw)
	if err != nil {
		return TaskResult{}, []string{"sign_for_user"}, err
	}

	toolErr := parseToolErrorPayload(toolResult)
	if toolErr.ErrorCode == "user_name_not_found" || toolErr.ErrorCode == "user_name_ambiguous" {
		existingDate := task.Slots["date"]
		existingSection := task.Slots["section"]
		task.Status = string(taskStatusWaiting)
		task.MissingSlots = []string{"user_name"}
		task.LastErrorCode = toolErr.ErrorCode
		task.LastErrorText = toolErr.Error
		task.ExpiresAt = time.Now().Add(sessionTTL)
		task.Slots = map[string]string{}
		if existingDate != "" {
			task.Slots["date"] = existingDate
		}
		if existingSection != "" {
			task.Slots["section"] = existingSection
		}
		if task.CandidateCache == nil {
			task.CandidateCache = make(map[string]any)
		}
		candidates := toolErr.CandidateUsers
		if len(candidates) == 0 {
			candidates = toolErr.Users
		}
		if len(candidates) > 0 {
			task.CandidateCache["candidate_users"] = append([]string(nil), candidates...)
		}

		return TaskResult{
			Outcome: ToolOutcome{
				OK:        false,
				ErrorCode: toolErr.ErrorCode,
				Message:   toolErr.Error,
				Retryable: true,
			},
			Reply:        toolErr.Error,
			KeepTaskOpen: true,
		}, []string{"sign_for_user"}, nil
	}

	reply, err := renderToolMessage(toolResult)
	if err != nil {
		return TaskResult{}, []string{"sign_for_user"}, err
	}

	task.Status = string(taskStatusCompleted)
	task.MissingSlots = nil
	task.LastErrorCode = ""
	task.LastErrorText = ""

	return TaskResult{
		Outcome: ToolOutcome{
			OK:      true,
			Message: reply,
		},
		Reply: reply,
	}, []string{"sign_for_user"}, nil
}

func (h *manualSignTaskHandler) BuildClarifyReply(task *TaskInstance) string {
	if reply := h.BuildMetaReply(task); strings.TrimSpace(reply) != "" {
		return reply
	}
	return buildTaskClarifyReply(activeTaskFromTaskInstance(task))
}

func (h *manualSignTaskHandler) BuildMetaReply(task *TaskInstance) string {
	if task == nil || task.CandidateCache == nil {
		return ""
	}
	candidates, ok := task.CandidateCache["candidate_users"].([]string)
	if !ok || len(candidates) == 0 {
		return ""
	}
	return "我找到这些候选用户：" + strings.Join(candidates, "、") + "。请提供更精确的姓名。"
}

func extractManualSignUserName(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}

	if idx := strings.Index(trimmed, "补签"); idx > 0 {
		prefix := strings.TrimSpace(trimmed[:idx])
		for _, marker := range []string{"给", "帮", "为"} {
			if pos := strings.LastIndex(prefix, marker); pos >= 0 {
				candidate := strings.TrimSpace(prefix[pos+len(marker):])
				candidate = strings.Trim(candidate, " ，。！？,!.")
				if candidate == "" || candidate == "我" || candidate == "我自己" {
					return ""
				}
				return candidate
			}
		}
	}

	normalized := normalizeQuery(trimmed)
	if containsAny(normalized, []string{"今天", "昨天", "明天", "第一节", "第二节", "第三节", "第四节", "第五节", "补签", "代签", "考勤"}) {
		return ""
	}
	return trimmed
}

func reconcileManualSignTask(task *TaskInstance) {
	if task == nil {
		return
	}

	required := []string{"user_name", "date", "section"}
	task.MissingSlots = missingSlots(required, task.Slots)
	if len(task.MissingSlots) == 0 {
		task.Status = string(taskStatusReady)
	} else {
		task.Status = string(taskStatusWaiting)
	}
	task.UpdatedAt = time.Now()
	task.ExpiresAt = time.Now().Add(sessionTTL)
}

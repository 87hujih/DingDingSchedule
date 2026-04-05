package agent

import (
	"context"
	"encoding/json"
	"strconv"
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
	return buildTaskClarifyReply(activeTaskFromTaskInstance(task))
}

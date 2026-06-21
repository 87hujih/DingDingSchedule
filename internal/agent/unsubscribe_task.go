package agent

import (
	"context"
	"encoding/json"
	"time"

	"schedule_server/internal/agent/tools"
)

type unsubscribeTaskHandler struct{}

// newUnsubscribeTaskHandler creates unsubscribe task handler.
func newUnsubscribeTaskHandler() *unsubscribeTaskHandler {
	return &unsubscribeTaskHandler{}
}

// Type returns the task type handled by the current handler.
func (h *unsubscribeTaskHandler) Type() string {
	return "unsubscribe_attendance_push"
}

// CreateTask builds the initial task instance from the first user turn.
func (h *unsubscribeTaskHandler) CreateTask(_ string, _ *tools.UserContext) (*TaskInstance, TaskApplyResult) {
	return &TaskInstance{
		Type:      h.Type(),
		Status:    string(taskStatusReady),
		Slots:     map[string]string{},
		UpdatedAt: time.Now(),
		ExpiresAt: time.Now().Add(sessionTTL),
	}, TaskApplyResult{}
}

// ApplyTurn applies the current user turn to the task state.
func (h *unsubscribeTaskHandler) ApplyTurn(task *TaskInstance, _ string, _ *tools.UserContext, _ *ExtractedEntities) (TaskApplyResult, error) {
	if task == nil {
		return TaskApplyResult{}, nil
	}
	task.Status = string(taskStatusReady)
	task.MissingSlots = nil
	return TaskApplyResult{}, nil
}

// Prepare loads any context needed before the task executes or clarifies.
func (h *unsubscribeTaskHandler) Prepare(context.Context, *TaskInstance, Deps) ([]string, error) {
	return nil, nil
}

// Execute runs the current logic and returns the normalized result.
func (h *unsubscribeTaskHandler) Execute(ctx context.Context, task *TaskInstance, uctx *tools.UserContext, registry *tools.Registry) (TaskResult, []string, error) {
	toolResult, err := registry.Dispatch(ctx, uctx, "unsubscribe_attendance_push", json.RawMessage(`{}`))
	if err != nil {
		return TaskResult{}, []string{"unsubscribe_attendance_push"}, err
	}

	reply, err := renderToolMessage(toolResult)
	if err != nil {
		return TaskResult{}, []string{"unsubscribe_attendance_push"}, err
	}

	if task != nil {
		task.Status = string(taskStatusCompleted)
		task.MissingSlots = nil
		task.LastErrorCode = ""
		task.LastErrorText = ""
	}

	return TaskResult{
		Outcome: ToolOutcome{
			OK:      true,
			Message: reply,
		},
		Reply: reply,
	}, []string{"unsubscribe_attendance_push"}, nil
}

// BuildClarifyReply builds the clarification reply for the current task state.
func (h *unsubscribeTaskHandler) BuildClarifyReply(*TaskInstance) string {
	return "我会直接取消当前群的考勤推送。"
}

// BuildMetaReply builds the extra prompt shown for the current task state.
func (h *unsubscribeTaskHandler) BuildMetaReply(task *TaskInstance) string {
	return h.BuildClarifyReply(task)
}

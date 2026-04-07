package agent

import (
	"context"
	"encoding/json"
	"time"

	"schedule_server/internal/agent/tools"
)

type unsubscribeTaskHandler struct{}

func newUnsubscribeTaskHandler() *unsubscribeTaskHandler {
	return &unsubscribeTaskHandler{}
}

func (h *unsubscribeTaskHandler) Type() string {
	return "unsubscribe_attendance_push"
}

func (h *unsubscribeTaskHandler) CreateTask(_ string, _ *tools.UserContext) (*TaskInstance, TaskApplyResult) {
	return &TaskInstance{
		Type:      h.Type(),
		Status:    string(taskStatusReady),
		Slots:     map[string]string{},
		UpdatedAt: time.Now(),
		ExpiresAt: time.Now().Add(sessionTTL),
	}, TaskApplyResult{}
}

func (h *unsubscribeTaskHandler) ApplyTurn(task *TaskInstance, _ string, _ *tools.UserContext) (TaskApplyResult, error) {
	if task == nil {
		return TaskApplyResult{}, nil
	}
	task.Status = string(taskStatusReady)
	task.MissingSlots = nil
	return TaskApplyResult{}, nil
}

func (h *unsubscribeTaskHandler) Prepare(context.Context, *TaskInstance, Deps) ([]string, error) {
	return nil, nil
}

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

func (h *unsubscribeTaskHandler) BuildClarifyReply(*TaskInstance) string {
	return "我会直接取消当前群的考勤推送。"
}

func (h *unsubscribeTaskHandler) BuildMetaReply(task *TaskInstance) string {
	return h.BuildClarifyReply(task)
}

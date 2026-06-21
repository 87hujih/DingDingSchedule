package agent

import (
	"context"
	"encoding/json"
	"time"

	"schedule_server/internal/agent/tools"
)

type subscriptionStatusTaskHandler struct{}

// newSubscriptionStatusTaskHandler creates subscription status task handler.
func newSubscriptionStatusTaskHandler() *subscriptionStatusTaskHandler {
	return &subscriptionStatusTaskHandler{}
}

// Type returns the task type handled by the current handler.
func (h *subscriptionStatusTaskHandler) Type() string {
	return "query_subscription_status"
}

// CreateTask builds the initial task instance from the first user turn.
func (h *subscriptionStatusTaskHandler) CreateTask(_ string, _ *tools.UserContext) (*TaskInstance, TaskApplyResult) {
	return &TaskInstance{
		Type:      h.Type(),
		Status:    string(taskStatusReady),
		Slots:     map[string]string{},
		UpdatedAt: time.Now(),
		ExpiresAt: time.Now().Add(sessionTTL),
	}, TaskApplyResult{}
}

// ApplyTurn applies the current user turn to the task state.
func (h *subscriptionStatusTaskHandler) ApplyTurn(task *TaskInstance, _ string, _ *tools.UserContext, _ *ExtractedEntities) (TaskApplyResult, error) {
	if task == nil {
		return TaskApplyResult{}, nil
	}
	task.Status = string(taskStatusReady)
	task.MissingSlots = nil
	task.UpdatedAt = time.Now()
	task.ExpiresAt = time.Now().Add(sessionTTL)
	return TaskApplyResult{}, nil
}

// Prepare loads any context needed before the task executes or clarifies.
func (h *subscriptionStatusTaskHandler) Prepare(context.Context, *TaskInstance, Deps) ([]string, error) {
	return nil, nil
}

// Execute runs the current logic and returns the normalized result.
func (h *subscriptionStatusTaskHandler) Execute(ctx context.Context, task *TaskInstance, uctx *tools.UserContext, registry *tools.Registry) (TaskResult, []string, error) {
	toolResult, err := registry.Dispatch(ctx, uctx, "query_subscription_status", json.RawMessage(`{}`))
	if err != nil {
		return TaskResult{}, []string{"query_subscription_status"}, err
	}

	reply, err := buildClarifyReply(clarifyPlan{ToolName: "query_subscription_status", ToolArguments: `{}`}, toolResult)
	if err != nil {
		return TaskResult{}, []string{"query_subscription_status"}, err
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
	}, []string{"query_subscription_status"}, nil
}

// BuildClarifyReply builds the clarification reply for the current task state.
func (h *subscriptionStatusTaskHandler) BuildClarifyReply(task *TaskInstance) string {
	return buildTaskClarifyReply(activeTaskFromTaskInstance(task))
}

// BuildMetaReply builds the extra prompt shown for the current task state.
func (h *subscriptionStatusTaskHandler) BuildMetaReply(task *TaskInstance) string {
	return h.BuildClarifyReply(task)
}

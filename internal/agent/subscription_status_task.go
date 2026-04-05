package agent

import (
	"context"
	"encoding/json"

	"schedule_server/internal/agent/tools"
)

type subscriptionStatusTaskHandler struct{}

func newSubscriptionStatusTaskHandler() *subscriptionStatusTaskHandler {
	return &subscriptionStatusTaskHandler{}
}

func (h *subscriptionStatusTaskHandler) Type() string {
	return "query_subscription_status"
}

func (h *subscriptionStatusTaskHandler) Prepare(context.Context, *TaskInstance, Deps) ([]string, error) {
	return nil, nil
}

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

func (h *subscriptionStatusTaskHandler) BuildClarifyReply(task *TaskInstance) string {
	return buildTaskClarifyReply(activeTaskFromTaskInstance(task))
}

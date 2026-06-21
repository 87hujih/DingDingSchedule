package agent

import (
	"context"

	"schedule_server/internal/agent/tools"
)

type TaskHandler interface {
	Type() string
}

type taskRuntime struct {
	handlers map[string]TaskHandler
}

type RuntimeDispatchResult struct {
	Handler        TaskHandler
	Task           *TaskInstance
	FallbackReason string
}

type runtimeTaskHandler interface {
	TaskHandler
	Prepare(ctx context.Context, task *TaskInstance, deps Deps) ([]string, error)
	Execute(ctx context.Context, task *TaskInstance, uctx *tools.UserContext, registry *tools.Registry) (TaskResult, []string, error)
	BuildClarifyReply(task *TaskInstance) string
}

type routeTaskHandler interface {
	runtimeTaskHandler
	CreateTask(message string, uctx *tools.UserContext) (*TaskInstance, TaskApplyResult)
	ApplyTurn(task *TaskInstance, message string, uctx *tools.UserContext, extracted *ExtractedEntities) (TaskApplyResult, error)
	BuildMetaReply(task *TaskInstance) string
}

// newTaskRuntime creates the task runtime and indexes handlers by type.
func newTaskRuntime(handlers []TaskHandler) *taskRuntime {
	registry := make(map[string]TaskHandler, len(handlers))
	for _, handler := range handlers {
		if handler == nil || handler.Type() == "" {
			continue
		}
		registry[handler.Type()] = handler
	}
	return &taskRuntime{handlers: registry}
}

// Dispatch dispatches the task to a runtime or catalog handler.
func (rt *taskRuntime) Dispatch(task TaskInstance) RuntimeDispatchResult {
	if rt == nil {
		return RuntimeDispatchResult{
			Task:           cloneTaskInstance(&task),
			FallbackReason: "runtime_unavailable",
		}
	}
	handler, ok := rt.handlers[task.Type]
	if !ok {
		return RuntimeDispatchResult{
			Task:           cloneTaskInstance(&task),
			FallbackReason: "handler_not_migrated",
		}
	}
	return RuntimeDispatchResult{
		Handler: handler,
		Task:    cloneTaskInstance(&task),
	}
}

// resolveRuntimeHandler resolves runtime handler.
func (rt *taskRuntime) resolveRuntimeHandler(task *TaskInstance) (runtimeTaskHandler, RuntimeDispatchResult) {
	if task == nil {
		return nil, RuntimeDispatchResult{FallbackReason: "empty_task"}
	}

	result := rt.Dispatch(*task)
	if result.FallbackReason != "" {
		return nil, result
	}

	handler, ok := result.Handler.(runtimeTaskHandler)
	if !ok {
		result.FallbackReason = "handler_missing_runtime_contract"
		return nil, result
	}
	return handler, result
}

// resolveCatalogHandler resolves catalog handler.
func (rt *taskRuntime) resolveCatalogHandler(taskType string) (routeTaskHandler, bool) {
	if rt == nil || taskType == "" {
		return nil, false
	}

	handler, ok := rt.handlers[taskType]
	if !ok {
		return nil, false
	}

	catalogHandler, ok := handler.(routeTaskHandler)
	return catalogHandler, ok
}

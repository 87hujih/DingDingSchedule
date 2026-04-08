package agent

import (
	"fmt"

	"schedule_server/internal/agent/tools"
)

type taskCatalog struct {
	runtime *taskRuntime
}

func newTaskCatalog(runtime *taskRuntime) *taskCatalog {
	return &taskCatalog{runtime: runtime}
}

func (c *taskCatalog) Start(taskType, message string, uctx *tools.UserContext) (*TaskInstance, TaskApplyResult, error) {
	handler, ok := c.resolve(taskType)
	if !ok {
		return nil, TaskApplyResult{}, fmt.Errorf("task type not allowlisted: %s", taskType)
	}

	task, apply := handler.CreateTask(message, uctx)
	if task == nil {
		return nil, TaskApplyResult{}, fmt.Errorf("task factory returned nil: %s", taskType)
	}
	return task, apply, nil
}

func (c *taskCatalog) Continue(task *TaskInstance, message string, uctx *tools.UserContext) (*TaskInstance, TaskApplyResult, error) {
	if task == nil {
		return nil, TaskApplyResult{}, fmt.Errorf("empty task")
	}

	handler, ok := c.resolve(task.Type)
	if !ok {
		return nil, TaskApplyResult{}, fmt.Errorf("task type not allowlisted: %s", task.Type)
	}

	next := cloneTaskInstance(task)
	if next == nil {
		return nil, TaskApplyResult{}, fmt.Errorf("clone task failed: %s", task.Type)
	}

	apply, err := handler.ApplyTurn(next, message, uctx)
	if err != nil {
		return nil, TaskApplyResult{}, err
	}
	return next, apply, nil
}

func (c *taskCatalog) BuildMetaReply(task *TaskInstance) string {
	if task == nil {
		return ""
	}

	handler, ok := c.resolve(task.Type)
	if !ok {
		return ""
	}
	return handler.BuildMetaReply(task)
}

func (c *taskCatalog) resolve(taskType string) (routeTaskHandler, bool) {
	if c == nil || c.runtime == nil {
		return nil, false
	}
	return c.runtime.resolveCatalogHandler(taskType)
}

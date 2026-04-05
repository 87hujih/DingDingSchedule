package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

type subscribeTaskHandler struct{}

func newSubscribeTaskHandler() *subscribeTaskHandler {
	return &subscribeTaskHandler{}
}

func (h *subscribeTaskHandler) Type() string {
	return "subscribe_attendance_push"
}

func (h *subscribeTaskHandler) Prepare(ctx context.Context, task *TaskInstance, deps Deps) ([]string, error) {
	if task == nil || deps.Dept == nil || !needsDepartmentCache(task) {
		return nil, nil
	}
	if len(cachedDepartmentNames(task)) > 0 {
		return nil, nil
	}

	depts, err := deps.Dept.ListDepts(ctx)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(depts))
	for _, dept := range depts {
		name := strings.TrimSpace(dept.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if task.CandidateCache == nil {
		task.CandidateCache = make(map[string]any)
	}
	task.CandidateCache["departments"] = names
	return []string{"list_departments"}, nil
}

func (h *subscribeTaskHandler) Execute(ctx context.Context, task *TaskInstance, uctx *tools.UserContext, registry *tools.Registry) (TaskResult, []string, error) {
	if task == nil {
		return TaskResult{}, nil, nil
	}

	payload := map[string]any{}
	if task.Slots["scope"] == "department" {
		payload["dept_names"] = splitTaskValues(task.Slots["dept_names"])
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return TaskResult{}, []string{"subscribe_attendance_push"}, err
	}

	toolResult, err := registry.Dispatch(ctx, uctx, "subscribe_attendance_push", raw)
	if err != nil {
		return TaskResult{}, []string{"subscribe_attendance_push"}, err
	}

	toolErr := parseToolErrorPayload(toolResult)
	if toolErr.ErrorCode == "department_name_not_found" || toolErr.ErrorCode == "department_name_ambiguous" {
		scope := task.Slots["scope"]
		task.Status = string(taskStatusWaiting)
		task.MissingSlots = []string{"dept_names"}
		task.LastErrorCode = toolErr.ErrorCode
		task.LastErrorText = toolErr.Error
		task.ExpiresAt = time.Now().Add(sessionTTL)
		task.Slots = map[string]string{}
		if scope != "" {
			task.Slots["scope"] = scope
		}

		reply := strings.TrimSpace(toolErr.Error)
		if toolErr.ErrorCode == "department_name_not_found" {
			reply = strings.TrimSpace(reply + " 你也可以回复“现在都有哪些部门”，我会把可选部门列给你。")
		}
		return TaskResult{
			Outcome: ToolOutcome{
				OK:        false,
				ErrorCode: toolErr.ErrorCode,
				Message:   toolErr.Error,
				Retryable: true,
			},
			Reply:        reply,
			KeepTaskOpen: true,
		}, []string{"subscribe_attendance_push"}, nil
	}

	reply, err := renderToolMessage(toolResult)
	if err != nil {
		return TaskResult{}, []string{"subscribe_attendance_push"}, err
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
	}, []string{"subscribe_attendance_push"}, nil
}

func (h *subscribeTaskHandler) BuildClarifyReply(task *TaskInstance) string {
	if task == nil {
		return "请再具体说明你要查询或操作的内容。"
	}
	if containsTaskMissingSlot(task, "dept_names") {
		if reply := buildCachedDepartmentReply(task); reply != "" {
			return reply
		}
	}
	return buildTaskClarifyReply(activeTaskFromTaskInstance(task))
}

func needsDepartmentCache(task *TaskInstance) bool {
	if task == nil || task.Type != "subscribe_attendance_push" {
		return false
	}
	if task.Slots["scope"] != "department" {
		return false
	}
	return containsTaskMissingSlot(task, "dept_names")
}

func containsTaskMissingSlot(task *TaskInstance, want string) bool {
	for _, slot := range task.MissingSlots {
		if slot == want {
			return true
		}
	}
	return false
}

func cachedDepartmentNames(task *TaskInstance) []string {
	if task == nil || task.CandidateCache == nil {
		return nil
	}
	names, ok := task.CandidateCache["departments"].([]string)
	if !ok {
		return nil
	}
	return append([]string(nil), names...)
}

func buildCachedDepartmentReply(task *TaskInstance) string {
	names := cachedDepartmentNames(task)
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("当前可选部门有：%s。请告诉我需要订阅哪些部门。", strings.Join(names, "、"))
}

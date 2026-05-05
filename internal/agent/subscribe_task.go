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

// newSubscribeTaskHandler creates subscribe task handler.
func newSubscribeTaskHandler() *subscribeTaskHandler {
	return &subscribeTaskHandler{}
}

// Type returns the task type handled by the current handler.
func (h *subscribeTaskHandler) Type() string {
	return "subscribe_attendance_push"
}

// CreateTask builds the initial task instance from the first user turn.
func (h *subscribeTaskHandler) CreateTask(message string, uctx *tools.UserContext) (*TaskInstance, TaskApplyResult) {
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

// ApplyTurn applies the current user turn to the task state.
func (h *subscribeTaskHandler) ApplyTurn(task *TaskInstance, message string, _ *tools.UserContext) (TaskApplyResult, error) {
	if task == nil {
		return TaskApplyResult{}, nil
	}
	if task.Slots == nil {
		task.Slots = make(map[string]string)
	}

	normalized := normalizeQuery(message)
	var matched []string
	switch {
	case containsAny(normalized, []string{"全部人员", "全部"}):
		taskApplySlot(task, &matched, "scope", "all")
	case containsAny(normalized, []string{"指定部门", "部分部门"}):
		taskApplySlot(task, &matched, "scope", "department")
	default:
		if containsTaskMissingSlot(task, "dept_names") {
			if resolved := matchDepartmentFromCandidates(task, message); resolved != "" {
				taskApplySlot(task, &matched, "dept_names", resolved)
				if task.Slots["scope"] == "" {
					task.Slots["scope"] = "department"
				}
			}
		}
	}

	reconcileSubscriptionTask(task)
	return TaskApplyResult{MatchedSlots: matched}, nil
}

// Prepare loads any context needed before the task executes or clarifies.
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

// Execute runs the current logic and returns the normalized result.
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

// BuildClarifyReply builds the clarification reply for the current task state.
func (h *subscribeTaskHandler) BuildClarifyReply(task *TaskInstance) string {
	if task == nil {
		return "请再具体说明你要查询或操作的内容。"
	}
	if containsTaskMissingSlot(task, "dept_names") || containsTaskMissingSlot(task, "scope") {
		if reply := buildCachedDepartmentReply(task); reply != "" {
			return reply
		}
	}
	return buildTaskClarifyReply(activeTaskFromTaskInstance(task))
}

// BuildMetaReply builds the extra prompt shown for the current task state.
func (h *subscribeTaskHandler) BuildMetaReply(task *TaskInstance) string {
	return h.BuildClarifyReply(task)
}

// matchDepartmentFromCandidates matches a department name from the cached candidate list.
func matchDepartmentFromCandidates(task *TaskInstance, message string) string {
	candidates := cachedDepartmentNames(task)
	if len(candidates) == 0 {
		return ""
	}
	normalized := normalizeQuery(message)
	if normalized == "" {
		return ""
	}
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if strings.Contains(normalized, normalizeQuery(name)) {
			return name
		}
	}
	return ""
}

// needsDepartmentCache reports whether it needs department cache.
func needsDepartmentCache(task *TaskInstance) bool {
	if task == nil || task.Type != "subscribe_attendance_push" {
		return false
	}
	if containsTaskMissingSlot(task, "dept_names") {
		return task.Slots["scope"] == "department"
	}
	return containsTaskMissingSlot(task, "scope")
}

// containsTaskMissingSlot reports whether it contains task missing slot.
func containsTaskMissingSlot(task *TaskInstance, want string) bool {
	for _, slot := range task.MissingSlots {
		if slot == want {
			return true
		}
	}
	return false
}

// cachedDepartmentNames handles cached department names.
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

// buildCachedDepartmentReply builds cached department reply.
func buildCachedDepartmentReply(task *TaskInstance) string {
	names := cachedDepartmentNames(task)
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("当前可选部门有：%s。请告诉我需要订阅哪些部门。", strings.Join(names, "、"))
}

// reconcileSubscriptionTask handles reconcile subscription task.
func reconcileSubscriptionTask(task *TaskInstance) {
	if task == nil {
		return
	}

	scope := strings.TrimSpace(task.Slots["scope"])
	deptNames := strings.TrimSpace(task.Slots["dept_names"])
	switch scope {
	case "all":
		task.Status = string(taskStatusReady)
		task.MissingSlots = nil
	case "department":
		if deptNames == "" {
			task.Status = string(taskStatusWaiting)
			task.MissingSlots = []string{"dept_names"}
		} else {
			task.Status = string(taskStatusReady)
			task.MissingSlots = nil
		}
	default:
		task.Status = string(taskStatusWaiting)
		task.MissingSlots = []string{"scope"}
	}
	task.UpdatedAt = time.Now()
	task.ExpiresAt = time.Now().Add(sessionTTL)
}

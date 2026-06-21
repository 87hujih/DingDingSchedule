package agent

import "time"

type TaskInstance struct {
	ID             string
	Type           string
	Status         string
	Slots          map[string]string
	MissingSlots   []string
	LastErrorCode  string
	LastErrorText  string
	CandidateCache map[string]any
	ResultSummary  string
	UpdatedAt      time.Time
	ExpiresAt      time.Time
}

// cloneTaskInstance clones task instance.
func cloneTaskInstance(task *TaskInstance) *TaskInstance {
	if task == nil {
		return nil
	}

	cloned := *task
	if task.Slots != nil {
		cloned.Slots = make(map[string]string, len(task.Slots))
		for k, v := range task.Slots {
			cloned.Slots[k] = v
		}
	}
	if len(task.MissingSlots) > 0 {
		cloned.MissingSlots = append([]string(nil), task.MissingSlots...)
	}
	if task.CandidateCache != nil {
		cloned.CandidateCache = make(map[string]any, len(task.CandidateCache))
		for k, v := range task.CandidateCache {
			cloned.CandidateCache[k] = v
		}
	}
	return &cloned
}

// taskInstanceFromActiveTask handles task instance from active task.
func taskInstanceFromActiveTask(task *ActiveTask) *TaskInstance {
	if task == nil {
		return nil
	}

	return &TaskInstance{
		Type:         task.Type,
		Status:       string(task.Status),
		Slots:        cloneTaskSlots(task.FilledSlots),
		MissingSlots: append([]string(nil), task.MissingSlots()...),
		ExpiresAt:    task.ExpiresAt,
	}
}

// activeTaskFromTaskInstance returns the active task from task instance.
func activeTaskFromTaskInstance(task *TaskInstance) *ActiveTask {
	if task == nil {
		return nil
	}

	legacy := &ActiveTask{
		Type:          task.Type,
		Status:        taskStatus(task.Status),
		RequiredSlots: append([]string(nil), task.MissingSlots...),
		FilledSlots:   cloneTaskSlots(task.Slots),
		ExpiresAt:     task.ExpiresAt,
	}
	if legacy.FilledSlots == nil {
		legacy.FilledSlots = map[string]string{}
	}
	return legacy
}

// cloneTaskSlots clones task slots.
func cloneTaskSlots(slots map[string]string) map[string]string {
	if len(slots) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(slots))
	for key, value := range slots {
		cloned[key] = value
	}
	return cloned
}

// taskApplySlot handles task apply slot.
func taskApplySlot(task *TaskInstance, matched *[]string, key, value string) {
	if task == nil || key == "" {
		return
	}
	if task.Slots == nil {
		task.Slots = make(map[string]string)
	}
	if task.Slots[key] == value {
		return
	}
	task.Slots[key] = value
	recordMatchedSlot(matched, key)
}

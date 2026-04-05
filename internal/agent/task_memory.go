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

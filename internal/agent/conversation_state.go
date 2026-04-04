package agent

import "time"

type taskStatus string

const (
	taskStatusWaiting   taskStatus = "waiting_slots"
	taskStatusReady     taskStatus = "ready"
	taskStatusCompleted taskStatus = "completed"
	taskStatusCanceled  taskStatus = "canceled"
	taskStatusExpired   taskStatus = "expired"
)

type ActiveTask struct {
	Type          string
	Status        taskStatus
	RequiredSlots []string
	FilledSlots   map[string]string
	ExpiresAt     time.Time
	LastPrompt    string
}

func (t *ActiveTask) IsExpired(now time.Time) bool {
	if t == nil {
		return false
	}
	return !t.ExpiresAt.IsZero() && !t.ExpiresAt.After(now)
}

func (t *ActiveTask) MissingSlots() []string {
	if t == nil || len(t.RequiredSlots) == 0 {
		return nil
	}

	missing := make([]string, 0, len(t.RequiredSlots))
	for _, slot := range t.RequiredSlots {
		if _, ok := t.FilledSlots[slot]; ok {
			continue
		}
		missing = append(missing, slot)
	}
	return missing
}

func cloneActiveTask(task *ActiveTask) *ActiveTask {
	if task == nil {
		return nil
	}

	cloned := *task
	if len(task.RequiredSlots) > 0 {
		cloned.RequiredSlots = append([]string(nil), task.RequiredSlots...)
	}
	if task.FilledSlots != nil {
		cloned.FilledSlots = make(map[string]string, len(task.FilledSlots))
		for k, v := range task.FilledSlots {
			cloned.FilledSlots[k] = v
		}
	}
	return &cloned
}

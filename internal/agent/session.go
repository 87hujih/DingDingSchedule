package agent

import (
	"sync"
	"time"

	"schedule_server/internal/agent/tools"
)

const (
	maxHistory = 20
	sessionTTL = 30 * time.Minute
)

type session struct {
	messages   []tools.Message
	activeTask *ActiveTask
	taskMemory *TaskInstance
	workflow   *WorkflowSnapshot
	updatedAt  time.Time
}

type sessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*session
}

// newSessionManager creates the in-memory session manager.
func newSessionManager() *sessionManager {
	return &sessionManager{
		sessions: make(map[string]*session),
	}
}

// getSessionState handles get session state.
func (sm *sessionManager) getSessionState(key string) ([]tools.Message, *ActiveTask) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		return nil, nil
	}
	if s.activeTask != nil && s.activeTask.IsExpired(time.Now()) {
		s.activeTask.Status = taskStatusExpired
		s.activeTask = nil
	}
	if s.taskMemory != nil && !s.taskMemory.ExpiresAt.IsZero() && !s.taskMemory.ExpiresAt.After(time.Now()) {
		s.taskMemory = nil
	}
	if s.activeTask == nil && s.taskMemory != nil {
		s.activeTask = activeTaskFromTaskInstance(s.taskMemory)
	}
	if s.taskMemory == nil && s.activeTask != nil {
		s.taskMemory = taskInstanceFromActiveTask(s.activeTask)
	}

	msgs := make([]tools.Message, len(s.messages))
	copy(msgs, s.messages)
	return msgs, cloneActiveTask(s.activeTask)
}

// getTaskState handles get task state.
func (sm *sessionManager) getTaskState(key string) ([]tools.Message, *TaskInstance) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		return nil, nil
	}
	if s.taskMemory != nil && !s.taskMemory.ExpiresAt.IsZero() && !s.taskMemory.ExpiresAt.After(time.Now()) {
		s.taskMemory = nil
	}
	if s.taskMemory == nil && s.activeTask != nil && !s.activeTask.IsExpired(time.Now()) {
		s.taskMemory = taskInstanceFromActiveTask(s.activeTask)
	}
	if s.activeTask == nil && s.taskMemory != nil {
		s.activeTask = activeTaskFromTaskInstance(s.taskMemory)
	}

	msgs := make([]tools.Message, len(s.messages))
	copy(msgs, s.messages)
	return msgs, cloneTaskInstance(s.taskMemory)
}

// getWorkflowState handles get workflow state.
func (sm *sessionManager) getWorkflowState(key string) ([]tools.Message, *WorkflowSnapshot) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		return nil, nil
	}

	msgs := make([]tools.Message, len(s.messages))
	copy(msgs, s.messages)
	return msgs, cloneWorkflowSnapshot(s.workflow)
}

// appendMessages 追加消息到 session，并裁剪超长历史
func (sm *sessionManager) appendMessages(key string, msgs ...tools.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		s = &session{
			messages: make([]tools.Message, 0, maxHistory),
		}
		sm.sessions[key] = s
	}

	s.messages = append(s.messages, msgs...)
	s.updatedAt = time.Now()

	// 裁剪：保留最近 maxHistory 条消息
	if len(s.messages) > maxHistory {
		s.messages = s.messages[len(s.messages)-maxHistory:]
	}
}

// setActiveTask handles set active task.
func (sm *sessionManager) setActiveTask(key string, task *ActiveTask) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		s = &session{
			messages: make([]tools.Message, 0, maxHistory),
		}
		sm.sessions[key] = s
	}

	s.activeTask = cloneActiveTask(task)
	s.taskMemory = taskInstanceFromActiveTask(task)
	s.updatedAt = time.Now()
}

// setTaskInstance handles set task instance.
func (sm *sessionManager) setTaskInstance(key string, task *TaskInstance) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		s = &session{
			messages: make([]tools.Message, 0, maxHistory),
		}
		sm.sessions[key] = s
	}

	s.taskMemory = cloneTaskInstance(task)
	s.activeTask = activeTaskFromTaskInstance(task)
	s.updatedAt = time.Now()
}

// setWorkflowState handles set workflow state.
func (sm *sessionManager) setWorkflowState(key string, workflow *WorkflowSnapshot) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		s = &session{
			messages: make([]tools.Message, 0, maxHistory),
		}
		sm.sessions[key] = s
	}

	s.workflow = cloneWorkflowSnapshot(workflow)
	s.updatedAt = time.Now()
}

// clearActiveTask handles clear active task.
func (sm *sessionManager) clearActiveTask(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		return
	}
	s.activeTask = nil
	s.taskMemory = nil
	s.workflow = nil
	s.updatedAt = time.Now()
}

// clearTaskInstance handles clear task instance.
func (sm *sessionManager) clearTaskInstance(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		return
	}
	s.activeTask = nil
	s.taskMemory = nil
	s.workflow = nil
	s.updatedAt = time.Now()
}

// clearWorkflowState handles clear workflow state.
func (sm *sessionManager) clearWorkflowState(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[key]
	if !ok {
		return
	}
	s.workflow = nil
	s.updatedAt = time.Now()
}

// purgeExpired 清理过期 session
func (sm *sessionManager) purgeExpired() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for key, s := range sm.sessions {
		if s.activeTask != nil && s.activeTask.IsExpired(now) {
			s.activeTask.Status = taskStatusExpired
			s.activeTask = nil
		}
		if s.taskMemory != nil && !s.taskMemory.ExpiresAt.IsZero() && !s.taskMemory.ExpiresAt.After(now) {
			s.taskMemory = nil
		}
		if now.Sub(s.updatedAt) > sessionTTL {
			delete(sm.sessions, key)
		}
	}
}

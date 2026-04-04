package agent

import (
	"testing"
	"time"

	"schedule_server/internal/agent/tools"
)

func TestSessionManagerStoresHistoryAndActiveTask(t *testing.T) {
	t.Parallel()

	sm := newSessionManager()
	key := "tenant:user"

	sm.appendMessages(key,
		tools.Message{Role: "user", Content: "开启考勤订阅"},
		tools.Message{Role: "assistant", Content: "请选择订阅范围"},
	)
	sm.setActiveTask(key, &ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"scope"},
		FilledSlots:   map[string]string{},
		ExpiresAt:     time.Now().Add(sessionTTL),
		LastPrompt:    "clarify_scope",
	})

	history, task := sm.getSessionState(key)
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if task == nil {
		t.Fatalf("active task = nil, want value")
	}
	if task.Type != "subscribe_attendance_push" {
		t.Fatalf("task type = %q, want subscribe_attendance_push", task.Type)
	}
	if task.Status != taskStatusWaiting {
		t.Fatalf("task status = %q, want %q", task.Status, taskStatusWaiting)
	}
}

func TestSessionManagerClearsActiveTaskWithoutDroppingHistory(t *testing.T) {
	t.Parallel()

	sm := newSessionManager()
	key := "tenant:user"

	sm.appendMessages(key,
		tools.Message{Role: "user", Content: "帮我补签"},
		tools.Message{Role: "assistant", Content: "请补充姓名"},
	)
	sm.setActiveTask(key, &ActiveTask{
		Type:          "sign_for_user",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"user_name", "date", "section"},
		FilledSlots:   map[string]string{},
		ExpiresAt:     time.Now().Add(sessionTTL),
		LastPrompt:    "clarify_user_name",
	})

	sm.clearActiveTask(key)

	history, task := sm.getSessionState(key)
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if task != nil {
		t.Fatalf("active task = %#v, want nil", task)
	}
}

func TestSessionManagerPurgesExpiredTaskStateWithSession(t *testing.T) {
	t.Parallel()

	sm := newSessionManager()
	key := "tenant:user"

	sm.appendMessages(key, tools.Message{Role: "user", Content: "开启考勤订阅"})
	sm.setActiveTask(key, &ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"scope"},
		FilledSlots:   map[string]string{},
		ExpiresAt:     time.Now().Add(-time.Minute),
		LastPrompt:    "clarify_scope",
	})

	sm.sessions[key].updatedAt = time.Now().Add(-sessionTTL - time.Minute)

	sm.purgeExpired()

	history, task := sm.getSessionState(key)
	if len(history) != 0 {
		t.Fatalf("history len = %d, want 0", len(history))
	}
	if task != nil {
		t.Fatalf("active task = %#v, want nil", task)
	}
}

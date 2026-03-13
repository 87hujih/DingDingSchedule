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
	messages  []tools.Message
	updatedAt time.Time
}

type sessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*session
}

func newSessionManager() *sessionManager {
	return &sessionManager{
		sessions: make(map[string]*session),
	}
}

// getMessages 获取 session 的历史消息（不包含 system prompt）
func (sm *sessionManager) getMessages(key string) []tools.Message {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	s, ok := sm.sessions[key]
	if !ok {
		return nil
	}
	msgs := make([]tools.Message, len(s.messages))
	copy(msgs, s.messages)
	return msgs
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

// purgeExpired 清理过期 session
func (sm *sessionManager) purgeExpired() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for key, s := range sm.sessions {
		if now.Sub(s.updatedAt) > sessionTTL {
			delete(sm.sessions, key)
		}
	}
}

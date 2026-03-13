package agent

import (
	"sync"
	"time"
)

const (
	rateLimitWindow = 1 * time.Minute
	rateLimitMax    = 10
)

// rateLimiter 简单的固定窗口计数器
type rateLimiter struct {
	mu       sync.Mutex
	counters map[string]*counter
}

type counter struct {
	count    int
	windowAt time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{counters: make(map[string]*counter)}
}

// Allow 检查是否允许请求。key 格式：corpID:dingUserID
func (r *rateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	c, ok := r.counters[key]
	if !ok || now.Sub(c.windowAt) > rateLimitWindow {
		r.counters[key] = &counter{count: 1, windowAt: now}
		return true
	}
	if c.count >= rateLimitMax {
		return false
	}
	c.count++
	return true
}

// purgeExpired 清理过期的计数器
func (r *rateLimiter) purgeExpired() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for k, c := range r.counters {
		if now.Sub(c.windowAt) > rateLimitWindow {
			delete(r.counters, k)
		}
	}
}

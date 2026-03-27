package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "schedule_server/internal/agent/tools"
	"schedule_server/pkg/dingtalk"

	"go.uber.org/zap"
)

type testKnowledgePort struct {
	mu       sync.Mutex
	calls    int
	tenantID uint
	query    string
	topK     int
	hits     []agenttools.KnowledgeHit
	err      error
}

// Search 返回测试预置的知识命中结果，并记录调用参数。
func (p *testKnowledgePort) Search(_ context.Context, tenantID uint, query string, topK int) ([]agenttools.KnowledgeHit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.tenantID = tenantID
	p.query = query
	p.topK = topK
	if p.err != nil {
		return nil, p.err
	}
	return p.hits, nil
}

type testCallLogPort struct {
	ch chan agenttools.CallLog
}

// newTestCallLogPort 创建测试用调用日志接收器。
func newTestCallLogPort() *testCallLogPort {
	return &testCallLogPort{ch: make(chan agenttools.CallLog, 1)}
}

// Write 接收一次 Agent 调用日志。
func (p *testCallLogPort) Write(_ context.Context, log agenttools.CallLog) {
	select {
	case p.ch <- log:
	default:
	}
}

// Wait 在超时前等待一条调用日志写入。
func (p *testCallLogPort) Wait(timeout time.Duration) (agenttools.CallLog, bool) {
	select {
	case log := <-p.ch:
		return log, true
	case <-time.After(timeout):
		return agenttools.CallLog{}, false
	}
}

type capturedChatRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Tools []json.RawMessage `json:"tools"`
}

// TestAgentChatUsesKnowledgeOnlyPathForRuleQuestions 验证纯规则问题会关闭工具并注入知识上下文。
func TestAgentChatUsesKnowledgeOnlyPathForRuleQuestions(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:     "考勤规则",
				Heading:   "迟到判定",
				Body:      "上课开始后超过 10 分钟打卡视为迟到。",
				SourceRef: "考勤规则#1",
			},
		},
	}

	var requests []capturedChatRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"根据规则，超过 10 分钟打卡算迟到。"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "考勤迟到怎么判定？",
		ConversationID:   "conv-1",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply != "根据规则，超过 10 分钟打卡算迟到。" {
		t.Fatalf("Chat() reply = %q", reply)
	}

	knowledge.mu.Lock()
	if knowledge.calls != 1 {
		t.Fatalf("knowledge search calls = %d, want 1", knowledge.calls)
	}
	if knowledge.tenantID != 42 {
		t.Fatalf("knowledge tenantID = %d, want 42", knowledge.tenantID)
	}
	if knowledge.topK <= 0 {
		t.Fatalf("knowledge topK = %d, want positive", knowledge.topK)
	}
	knowledge.mu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if len(requests[0].Tools) != 0 {
		t.Fatalf("rag request tools len = %d, want 0", len(requests[0].Tools))
	}
	if !requestContains(requests[0], "考勤规则#1") {
		t.Fatalf("rag request missing source ref, messages = %+v", requests[0].Messages)
	}
	if !requestContains(requests[0], "上课开始后超过 10 分钟打卡视为迟到") {
		t.Fatalf("rag request missing knowledge body, messages = %+v", requests[0].Messages)
	}
}

// TestAgentChatWritesKnowledgeMetricsToCallLog 验证规则问答会把检索指标写入调用日志。
func TestAgentChatWritesKnowledgeMetricsToCallLog(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:     "考勤规则",
				Heading:   "迟到判定",
				Body:      "上课开始后超过 10 分钟打卡视为迟到。",
				SourceRef: "考勤规则#1",
			},
		},
	}
	callLog := newTestCallLogPort()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"根据规则，超过 10 分钟打卡算迟到。"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		Knowledge:      knowledge,
		CallLog:        callLog,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "考勤迟到怎么判定？",
		ConversationID:   "conv-1",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log to be written")
	}
	if log.QueryType != "rag" {
		t.Fatalf("QueryType = %q, want rag", log.QueryType)
	}
	if log.ToolCallCount != 0 {
		t.Fatalf("ToolCallCount = %d, want 0", log.ToolCallCount)
	}
	if log.RetrievalHitCount != 1 {
		t.Fatalf("RetrievalHitCount = %d, want 1", log.RetrievalHitCount)
	}
	if len(log.SourceRefs) != 1 || log.SourceRefs[0] != "考勤规则#1" {
		t.Fatalf("SourceRefs = %v, want [考勤规则#1]", log.SourceRefs)
	}
	if log.LLMDurationMs <= 0 {
		t.Fatalf("LLMDurationMs = %d, want positive", log.LLMDurationMs)
	}
	if log.RetrievalDurationMs <= 0 {
		t.Fatalf("RetrievalDurationMs = %d, want positive", log.RetrievalDurationMs)
	}
}

// TestAgentChatInjectsKnowledgeContextBeforeToolCallsForMixedQuestions 验证 mixed 路径会在保留工具时注入知识上下文。
func TestAgentChatInjectsKnowledgeContextBeforeToolCallsForMixedQuestions(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:     "考勤规则",
				Heading:   "迟到判定",
				Body:      "上课开始后超过 10 分钟打卡视为迟到。",
				SourceRef: "考勤规则#1",
			},
		},
	}

	var requests []capturedChatRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requests = append(requests, req)
		current := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_current_time","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"今天第一节还未到的人员已查询，同时迟到以开课后 10 分钟为界。"},"finish_reason":"stop"}]}`))
		default:
			http.Error(w, `{"error":{"message":"unexpected extra request"}}`, http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "今天第一节谁未到，并说明迟到判定规则",
		ConversationID:   "conv-1",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == "" {
		t.Fatalf("Chat() reply should not be empty")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if len(requests[0].Tools) == 0 {
		t.Fatalf("mixed request should keep tools")
	}
	if !requestContains(requests[0], "考勤规则#1") {
		t.Fatalf("mixed request missing source ref, messages = %+v", requests[0].Messages)
	}
	if !requestContains(requests[0], "上课开始后超过 10 分钟打卡视为迟到") {
		t.Fatalf("mixed request missing knowledge body, messages = %+v", requests[0].Messages)
	}
}

// requestContains 判断发给模型的请求里是否包含指定片段。
func requestContains(req capturedChatRequest, needle string) bool {
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}

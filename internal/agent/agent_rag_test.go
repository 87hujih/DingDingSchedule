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

type testClarifyDeptPort struct{}

func (testClarifyDeptPort) ListDepts(context.Context) ([]agenttools.DeptItem, error) {
	return []agenttools.DeptItem{
		{DeptID: 101, Name: "教务处"},
		{DeptID: 102, Name: "学工处"},
	}, nil
}

type testClarifyGroupSubPort struct {
	info *agenttools.GroupSubInfo
}

func (p testClarifyGroupSubPort) Subscribe(context.Context, uint, string, string, uint, []int64) error {
	return nil
}

func (p testClarifyGroupSubPort) Unsubscribe(context.Context, uint, string) error {
	return nil
}

func (p testClarifyGroupSubPort) GetSubscription(context.Context, uint, string) (*agenttools.GroupSubInfo, error) {
	return p.info, nil
}

// TestAgentChatUsesKnowledgeOnlyForLeaveSyncFailureQuestion 验证真实口语化规则问法会走 knowledge-only。
func TestAgentChatUsesKnowledgeOnlyForLeaveSyncFailureQuestion(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:      "请假同步说明",
				SourcePath: "agent-knowledge/leave-sync-guide.md",
				DocType:    "rule",
				Audience:   "shared",
				Intent:     "leave",
				Heading:    "同步失败处理",
				Body:       "同步失败不会直接覆盖已经生成的考勤快照；排障后应重试同步，再由管理员复核结果。",
				SourceRef:  "请假同步说明#3",
				Score:      18,
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
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"同步失败不会直接覆盖已生成的考勤快照，排障后应重试同步并复核。"},"finish_reason":"stop"}]}`))
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
		Content:          "如果请假信息没能同步到位，会出现什么情况",
		ConversationID:   "conv-knowledge",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == "" {
		t.Fatalf("Chat() reply should not be empty")
	}

	knowledge.mu.Lock()
	if knowledge.calls != 1 {
		t.Fatalf("knowledge search calls = %d, want 1", knowledge.calls)
	}
	knowledge.mu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if len(requests[0].Tools) != 0 {
		t.Fatalf("knowledge-only request tools len = %d, want 0", len(requests[0].Tools))
	}
	if !requestContains(requests[0], "请假同步说明#3") {
		t.Fatalf("knowledge-only request missing source ref, messages = %+v", requests[0].Messages)
	}
	if !requestContains(requests[0], "同步失败不会直接覆盖已经生成的考勤快照") {
		t.Fatalf("knowledge-only request missing knowledge body, messages = %+v", requests[0].Messages)
	}
}

// TestAgentChatUsesMixedAnswerModeForRealtimePlusRuleQuestion 验证实时查询加规则说明会走 mixed。
func TestAgentChatUsesMixedAnswerModeForRealtimePlusRuleQuestion(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:      "考勤规则",
				SourcePath: "agent-knowledge/attendance-rules.md",
				DocType:    "rule",
				Audience:   "shared",
				Intent:     "attendance",
				Heading:    "迟到判定",
				Body:       "上课开始后超过 10 分钟打卡视为迟到。",
				SourceRef:  "考勤规则#1",
				Score:      18,
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
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"今天第一节未到人员已查询；迟到规则以上课后 10 分钟为界。"},"finish_reason":"stop"}]}`))
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
		Content:          "今天第一节谁未到，并说明迟到规则",
		ConversationID:   "conv-mixed",
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
	if !requestContains(requests[0], "先回答实时查询结果") {
		t.Fatalf("mixed request missing answer-order instruction, messages = %+v", requests[0].Messages)
	}
	if !requestContains(requests[0], "考勤规则#1") {
		t.Fatalf("mixed request missing source ref, messages = %+v", requests[0].Messages)
	}
}

// TestAgentChatRejectsOutOfDomainBeforeRetrieval 验证站外问题会在领域门禁处被拒绝。
func TestAgentChatRejectsOutOfDomainBeforeRetrieval(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
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
		Content:          "今天上海天气怎么样",
		ConversationID:   "conv-reject",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply != outOfDomainReply {
		t.Fatalf("Chat() reply = %q, want %q", reply, outOfDomainReply)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
	}
}

// TestAgentChatKeepsToolFirstForLiveQueryWithoutRuleSignal 验证纯实时查询即使命中知识也不应被抬成 mixed。
func TestAgentChatKeepsToolFirstForLiveQueryWithoutRuleSignal(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:      "系统总览",
				SourcePath: "agent-knowledge/system-overview.md",
				DocType:    "overview",
				Audience:   "shared",
				Intent:     "system",
				Heading:    "课表与考勤链路",
				Body:       "系统支持课表查询和考勤结果查看。",
				SourceRef:  "系统总览#17",
				Score:      18,
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
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"今天第一节未到人员已查询。"},"finish_reason":"stop"}]}`))
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
		Content:          "今天第一节谁未到？",
		ConversationID:   "conv-tool-first",
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
		t.Fatalf("tool-first request should keep tools")
	}
	if requestContains(requests[0], "系统总览#17") {
		t.Fatalf("tool-first request should not inject knowledge summary, messages = %+v", requests[0].Messages)
	}
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
				Score:     18,
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
				Score:     18,
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
				Score:     18,
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

func TestAgentChatAnswersCapabilityQuestionWithoutKnowledgeLookup(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
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
		Content:          "你有什么功能",
		ConversationID:   "conv-help",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "我可以帮助处理这些系统能力") {
		t.Fatalf("Chat() reply = %q, want capability overview", reply)
	}
	if !strings.Contains(reply, "你当前在这个会话里可直接使用") {
		t.Fatalf("Chat() reply = %q, want current availability section", reply)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
	}
}

func TestAgentChatClarifiesDepartmentScopedSubscriptionByListingDepartmentsFirst(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		GroupSub:       testClarifyGroupSubPort{},
		Dept:           testClarifyDeptPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "订阅指定部门考勤",
		ConversationID:    "conv-clarify",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "教务处") || !strings.Contains(reply, "学工处") {
		t.Fatalf("Chat() reply = %q, want listed departments", reply)
	}
	if !strings.Contains(reply, "需要订阅哪些部门") {
		t.Fatalf("Chat() reply = %q, want follow-up prompt", reply)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
	}
}

func TestAgentChatChecksSubscriptionStatusDirectlyInGroup(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		GroupSub: testClarifyGroupSubPort{
			info: &agenttools.GroupSubInfo{
				Subscribed: true,
				GroupName:  "测试群",
			},
		},
		Dept:   testClarifyDeptPort{},
		Tenant: testTenantPort{},
		Logger: zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "查这个群有没有订阅考勤推送",
		ConversationID:    "conv-status",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "已订阅") {
		t.Fatalf("Chat() reply = %q, want subscribed status", reply)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
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

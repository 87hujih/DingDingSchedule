package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	agenttools "schedule_server/internal/agent/tools"
	"schedule_server/pkg/dingtalk"

	"go.uber.org/zap"
)

type testTenantPort struct{}

func (testTenantPort) FindTenantIDByCorpID(context.Context, string) (uint, error) {
	return 42, nil
}

type testUserPort struct{}

func (testUserPort) FindByDingUserID(context.Context, string) (*agenttools.UserInfo, error) {
	return &agenttools.UserInfo{
		ID:         7,
		Name:       "Alice",
		DingUserID: "ding-user",
		Role:       1,
		TenantID:   42,
	}, nil
}

func (testUserPort) SearchByName(context.Context, string) ([]agenttools.UserInfo, error) {
	return nil, nil
}

type testSemesterPort struct{}

func (testSemesterPort) GetCurrentWeek(context.Context) (int, int, error) {
	return 3, 20, nil
}

type testSchedulePeriodPort struct{}

func (testSchedulePeriodPort) GetScheduleInfo(context.Context) ([]agenttools.PeriodInfo, string, error) {
	return []agenttools.PeriodInfo{
		{Name: "第一节", Start: "08:00", End: "08:45"},
	}, "default", nil
}

type testSchedulePort struct{}

func (testSchedulePort) ListMyScheduleByWeek(context.Context, uint, int) ([]agenttools.CourseItem, error) {
	return nil, nil
}

func (testSchedulePort) ListUserScheduleByWeek(context.Context, uint, int, uint, int) ([]agenttools.CourseItem, error) {
	return nil, nil
}

func (testSchedulePort) GetFreeUsersBySlot(context.Context, int, int, int, int64) ([]agenttools.FreeSlotResult, error) {
	return nil, nil
}

func TestAgentChatAllowsFollowUpToolCalls(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requestCount++
		current := requestCount
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		switch current {
		case 1:
			if len(req.Tools) == 0 {
				http.Error(w, `{"error":{"message":"missing tools on first request"}}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_current_time","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			if len(req.Tools) == 0 {
				http.Error(w, `{"error":{"message":"missing tools on follow-up request"}}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_2","type":"function","function":{"name":"query_schedule_info","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		case 3:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done","tool_calls":[]},"finish_reason":"stop"}]}`))
		default:
			http.Error(w, `{"error":{"message":"unexpected extra request"}}`, http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		Schedule:       testSchedulePort{},
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
		Content:          "帮我连续查两次今天第一节谁未到",
		ConversationID:   "conv-1",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply != "done" {
		t.Fatalf("Chat() reply = %q, want %q", reply, "done")
	}

	mu.Lock()
	defer mu.Unlock()
	if requestCount != 3 {
		t.Fatalf("LLM request count = %d, want 3", requestCount)
	}
}

func TestNewAgentUsesDedicatedRouterModelWhenConfigured(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://main-llm",
		LLMAPIKey:        "main-key",
		LLMModel:         "main-model",
		RouterLLMBaseURL: "http://router-llm",
		RouterLLMAPIKey:  "router-key",
		RouterLLMModel:   "router-model",
		RouteMode:        "shadow",
		User:             testUserPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	if a.routerClient == nil {
		t.Fatalf("routerClient = nil, want dedicated client")
	}
	if a.routerClient == a.llmClient {
		t.Fatalf("routerClient and llmClient share the same pointer, want dedicated client")
	}
	if a.routerClient.model != "router-model" {
		t.Fatalf("routerClient.model = %q, want router-model", a.routerClient.model)
	}
	if a.routeMode != "shadow" {
		t.Fatalf("routeMode = %q, want shadow", a.routeMode)
	}
}

func TestNewAgentDefaultsToLegacyProtocolMode(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		User:   testUserPort{},
		Tenant: testTenantPort{},
		Logger: zap.NewNop().Sugar(),
	})
	defer a.Stop()

	if a.protocolMode != ProtocolModeLegacy {
		t.Fatalf("protocolMode = %q, want %q", a.protocolMode, ProtocolModeLegacy)
	}
}

func TestNewAgentDefaultsToLiveRouteMode(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		User:   testUserPort{},
		Tenant: testTenantPort{},
		Logger: zap.NewNop().Sugar(),
	})
	defer a.Stop()

	if a.routeMode != string(RouteModeLive) {
		t.Fatalf("routeMode = %q，期望 %q", a.routeMode, RouteModeLive)
	}
}

func TestNewAgentCreatesProtocolLiveIntentCompilerWhenLLMConfigured(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:   "http://llm.example.test/v1/chat/completions",
		LLMModel:     "intent-model",
		ProtocolMode: string(ProtocolModeLive),
		User:         testUserPort{},
		Tenant:       testTenantPort{},
		Logger:       zap.NewNop().Sugar(),
	})
	defer a.Stop()

	if a.intentCompiler == nil {
		t.Fatalf("intentCompiler = nil, want protocol-live compiler")
	}
}

func TestNewAgentAppliesConfiguredIntentCompilerTimeout(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:            "http://llm.example.test/v1/chat/completions",
		LLMModel:              "intent-model",
		ProtocolMode:          string(ProtocolModeLive),
		IntentCompilerTimeout: 3 * time.Second,
		User:                  testUserPort{},
		Tenant:                testTenantPort{},
		Logger:                zap.NewNop().Sugar(),
	})
	defer a.Stop()

	compiler, ok := a.intentCompiler.(*llmIntentCompiler)
	if !ok {
		t.Fatalf("intentCompiler = %T, want *llmIntentCompiler", a.intentCompiler)
	}
	if compiler.timeout != 3*time.Second {
		t.Fatalf("compiler.timeout = %s, want 3s", compiler.timeout)
	}
}

func TestNewAgentDoesNotCreateProtocolLiveIntentCompilerForPortZeroURLWithPath(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:   "http://127.0.0.1:0/v1/chat/completions",
		LLMModel:     "intent-model",
		ProtocolMode: string(ProtocolModeLive),
		User:         testUserPort{},
		Tenant:       testTenantPort{},
		Logger:       zap.NewNop().Sugar(),
	})
	defer a.Stop()

	if a.intentCompiler != nil {
		t.Fatalf("intentCompiler = %T, want nil for port-zero LLM URL", a.intentCompiler)
	}
}

func TestApplyProtocolLiveOutcomeRecordsDiagnostics(t *testing.T) {
	t.Parallel()

	a := &Agent{sessions: newSessionManager()}
	sessionKey := "corp:user:conv"
	a.sessions.setWorkflowState(sessionKey, &WorkflowSnapshot{
		ID:           "wf-before",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectScope,
		MissingSlots: []string{"scope"},
	})

	metrics := callMetrics{}
	a.applyProtocolLiveOutcome(sessionKey, &metrics, protocolLiveOutcome{
		Draft: ProtocolDraft{
			Act:       ActWorkflowContinue,
			Domain:    DomainSubscription,
			Operation: "subscription.start",
		},
		Validation: ProtocolValidationResult{
			AllowExecution: true,
			ValidationCode: "workflow_continue_allowed",
			ResponseKind:   ResponseClarify,
		},
		Response:         ResponseModel{Kind: ResponseClarify, Operation: "subscription.start", MissingFields: []string{"dept_names"}},
		AnswerMode:       answerModeToolFirst,
		BlockedReason:    "missing_dept_names",
		ResolvedSlots:    map[string]any{"scope": "department", "dept_ids": []int64{101, 102}},
		CandidateCount:   2,
		WorkflowDecision: WorkflowContinueDecision,
		WorkflowAfter: &WorkflowSnapshot{
			ID:           "wf-after",
			Type:         WorkflowSubscriptionStart,
			State:        WorkflowCollectDepartments,
			MissingSlots: []string{"dept_names"},
		},
	})

	if metrics.Proto.BlockedReason != "missing_dept_names" {
		t.Fatalf("BlockedReason = %q, want missing_dept_names", metrics.Proto.BlockedReason)
	}
	if metrics.Proto.ResolvedSlots != `{"dept_ids":[101,102],"scope":"department"}` {
		t.Fatalf("ResolvedSlots = %q, want compact JSON", metrics.Proto.ResolvedSlots)
	}
	if metrics.Proto.CandidateCount != 2 {
		t.Fatalf("CandidateCount = %d, want 2", metrics.Proto.CandidateCount)
	}
	if metrics.Wf.StateBefore != string(WorkflowCollectScope) {
		t.Fatalf("WorkflowStateBefore = %q, want %q", metrics.Wf.StateBefore, WorkflowCollectScope)
	}
	if metrics.Wf.StateAfter != string(WorkflowCollectDepartments) {
		t.Fatalf("WorkflowStateAfter = %q, want %q", metrics.Wf.StateAfter, WorkflowCollectDepartments)
	}
}

func TestApplyProtocolLiveOutcomeRecordsTerminalWorkflowState(t *testing.T) {
	t.Parallel()

	a := &Agent{sessions: newSessionManager()}
	sessionKey := "corp:user:terminal"
	a.sessions.setWorkflowState(sessionKey, &WorkflowSnapshot{
		ID:    "wf-ready",
		Type:  WorkflowSubscriptionStart,
		State: WorkflowReady,
	})

	metrics := callMetrics{}
	a.applyProtocolLiveOutcome(sessionKey, &metrics, protocolLiveOutcome{
		Draft: ProtocolDraft{
			Act:       ActWorkflowContinue,
			Domain:    DomainSubscription,
			Operation: "subscription.start",
		},
		Validation: ProtocolValidationResult{
			AllowExecution: true,
			ValidationCode: "workflow_continue_allowed",
			ResponseKind:   ResponseResult,
		},
		Response:         ResponseModel{Kind: ResponseResult, ResultText: "已完成"},
		AnswerMode:       answerModeToolFirst,
		WorkflowDecision: WorkflowCompletedDecision,
		ClearWorkflow:    true,
	})

	if metrics.Wf.StateBefore != string(WorkflowReady) {
		t.Fatalf("WorkflowStateBefore = %q, want %q", metrics.Wf.StateBefore, WorkflowReady)
	}
	if metrics.Wf.StateAfter != string(WorkflowCompleted) {
		t.Fatalf("WorkflowStateAfter = %q, want %q", metrics.Wf.StateAfter, WorkflowCompleted)
	}
	_, active := a.sessions.getWorkflowState(sessionKey)
	if active != nil {
		t.Fatalf("active workflow = %+v, want cleared", active)
	}
}

func TestProtocolLiveChatUsesWorkflowStoreLock(t *testing.T) {
	t.Parallel()

	store := newRecordingWorkflowStore()
	a := NewAgent(Deps{
		LLMBaseURL:   "http://127.0.0.1:0",
		LLMAPIKey:    "test-key",
		LLMModel:     "test-model",
		ProtocolMode: string(ProtocolModeLive),
		IntentCompiler: fixedIntentCompiler{draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.96,
		}},
		WorkflowStore: store,
		User:          testUserPort{},
		Tenant:        testTenantPort{},
		Logger:        zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "开启本群考勤订阅",
		ConversationID:    "conv-lock",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if store.withLockCalls() != 1 {
		t.Fatalf("WithLock calls = %d, want 1", store.withLockCalls())
	}
	workflow, err := store.Load(context.Background(), WorkflowKey{TenantID: 42, ConversationID: "conv-lock", ActorUserID: 7})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if workflow == nil || workflow.State != WorkflowCollectScope {
		t.Fatalf("workflow = %+v, want collect_scope saved under structured key", workflow)
	}
}

type fixedIntentCompiler struct {
	draft ProtocolDraft
}

func (c fixedIntentCompiler) Compile(context.Context, IntentCompileRequest) (IntentDraft, error) {
	return c.draft, nil
}

type recordingWorkflowStore struct {
	inner *memoryWorkflowStore
	mu    sync.Mutex
	locks int
}

func newRecordingWorkflowStore() *recordingWorkflowStore {
	return &recordingWorkflowStore{inner: newMemoryWorkflowStore(nil)}
}

func (s *recordingWorkflowStore) Load(ctx context.Context, key WorkflowKey) (*WorkflowSnapshot, error) {
	return s.inner.Load(ctx, key)
}

func (s *recordingWorkflowStore) Save(ctx context.Context, workflow *WorkflowSnapshot) error {
	return s.inner.Save(ctx, workflow)
}

func (s *recordingWorkflowStore) Clear(ctx context.Context, key WorkflowKey, reason string) error {
	return s.inner.Clear(ctx, key, reason)
}

func (s *recordingWorkflowStore) WithLock(ctx context.Context, key WorkflowKey, fn func(*WorkflowSnapshot) (*WorkflowSnapshot, error)) error {
	s.mu.Lock()
	s.locks++
	s.mu.Unlock()
	return s.inner.WithLock(ctx, key, fn)
}

func (s *recordingWorkflowStore) withLockCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locks
}

func TestChatUsesRouteAsSinglePrimaryChainWhenProtocolIsShadow(t *testing.T) {
	t.Parallel()

	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("解析请求失败: %v", err)
			http.Error(w, "解析请求失败: "+err.Error(), http.StatusBadRequest)
			return
		}

		lastContent := ""
		if len(req.Messages) > 0 {
			lastContent = req.Messages[len(req.Messages)-1].Content
		}
		requests <- lastContent

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"kind\":\"social_refuse\",\"confidence\":0.91,\"reason_code\":\"test_route_primary\"}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://unused-main-llm",
		LLMAPIKey:        "test-key",
		LLMModel:         "main-model",
		RouterLLMBaseURL: server.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		ProtocolMode:     string(ProtocolModeShadow),
		Schedule:         testSchedulePort{},
		User:             testUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "代签功能可以用吗",
		ConversationID:   "conv-1",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() 返回错误: %v", err)
	}
	if reply != "我主要帮助处理课表、考勤、请假、补签和订阅相关事务，其他闲聊我就不展开了。" {
		t.Fatalf("Chat() 回复 = %q，期望语义路由器的闲聊拒绝回复", reply)
	}

	select {
	case <-requests:
	default:
		t.Fatalf("语义路由器未被调用")
	}
	select {
	case extra := <-requests:
		t.Fatalf("出现非预期的额外 LLM 调用，payload=%q", extra)
	default:
	}
}

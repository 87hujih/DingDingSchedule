package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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

package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"schedule_server/internal/agent/tools"

	"go.uber.org/zap"
)

func TestRejectExecutorDoesNotExposeTools(t *testing.T) {
	t.Parallel()

	result := (rejectExecutor{}).Execute()
	if len(result.ToolDefs) != 0 {
		t.Fatalf("ToolDefs = %d, want 0", len(result.ToolDefs))
	}
	if result.Reply != outOfDomainReply {
		t.Fatalf("Reply = %q, want %q", result.Reply, outOfDomainReply)
	}
}

func TestSocialExecutorDoesNotExposeTools(t *testing.T) {
	t.Parallel()

	result := (socialExecutor{}).Execute()
	if len(result.ToolDefs) != 0 {
		t.Fatalf("ToolDefs = %d, want 0", len(result.ToolDefs))
	}
	if result.AnswerMode != answerModeReject {
		t.Fatalf("AnswerMode = %q, want reject", result.AnswerMode)
	}
}

func TestClarifyExecutorUsesClarifyCodeWithoutRetrieval(t *testing.T) {
	t.Parallel()

	result := (clarifyExecutor{}).Execute(RouteDecision{
		Kind:        RouteClarify,
		ClarifyCode: "ambiguous_intent",
	}, nil)

	if len(result.ToolDefs) != 0 {
		t.Fatalf("ToolDefs = %d, want 0", len(result.ToolDefs))
	}
	if result.Retrieval.Hits != nil {
		t.Fatalf("Retrieval.Hits = %#v, want nil", result.Retrieval.Hits)
	}
	if result.Reply == "" {
		t.Fatalf("Reply = empty, want clarify reply")
	}
}

func TestRAGExecutorDoesNotExposeTools(t *testing.T) {
	t.Parallel()

	var captured struct {
		Tools []json.RawMessage `json:"tools"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"这是规则说明","tool_calls":[]},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL: server.URL,
		LLMAPIKey:  "test-key",
		LLMModel:   "test-model",
		Knowledge: &testKnowledgePort{
			hits: []tools.KnowledgeHit{
				{SourceRef: "考勤规则#1", Body: "迟到按开始时间判定。", Score: 12},
			},
		},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		User:           testUserPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	result, err := (ragExecutor{agent: a}).Execute(context.Background(), &tools.UserContext{
		TenantID:         42,
		UserRole:         1,
		Name:             "Alice",
		ConversationType: "1",
	}, nil, "迟到怎么判")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(result.ToolDefs) != 0 {
		t.Fatalf("ToolDefs = %d, want 0", len(result.ToolDefs))
	}
	if len(captured.Tools) != 0 {
		t.Fatalf("captured tools = %d, want 0", len(captured.Tools))
	}
	if result.Reply != "这是规则说明" {
		t.Fatalf("Reply = %q, want 这是规则说明", result.Reply)
	}
}

func TestToolQueryExecutorUsesRestrictedToolPool(t *testing.T) {
	t.Parallel()

	var captured capturedChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"今天第一节未到人员已查询。","tool_calls":[]},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       server.URL,
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: server.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		Knowledge: &testKnowledgePort{
			hits: []tools.KnowledgeHit{
				{SourceRef: "考勤规则#1", Body: "上课开始后超过 10 分钟打卡视为迟到。", Score: 18},
			},
		},
		Attendance:     &testTaskAttendancePort{},
		User:           testTaskUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Dept:           testClarifyDeptPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	result, toolsCalled, err := (toolQueryExecutor{agent: a}).Execute(context.Background(), &tools.UserContext{
		TenantID:         42,
		UserID:           7,
		UserRole:         1,
		Name:             "Alice",
		ConversationType: "1",
	}, nil, "今天第一节谁未到")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.ExecutorName != "tool_query_executor" {
		t.Fatalf("ExecutorName = %q, want tool_query_executor", result.ExecutorName)
	}
	if result.ToolPool == "" {
		t.Fatalf("ToolPool = empty, want selected pool")
	}
	if len(toolsCalled) != 0 {
		t.Fatalf("toolsCalled = %v, want no actual tool dispatch in this test", toolsCalled)
	}
	names := requestToolNames(captured)
	if len(names) == 0 {
		t.Fatalf("request tool names = empty, want restricted pool")
	}
	if !toolPoolContains(names, "query_attendance_status") {
		t.Fatalf("request tool names missing attendance tool: %v", names)
	}
	if toolPoolContains(names, "subscribe_attendance_push") {
		t.Fatalf("request tool names leaked admin subscription tool: %v", names)
	}
	if requestContains(captured, "考勤规则#1") || requestContains(captured, "上课开始后超过 10 分钟打卡视为迟到") {
		t.Fatalf("tool_query request should not inject knowledge prompt, messages = %+v", captured.Messages)
	}
	if strings.TrimSpace(result.Reply) == "" {
		t.Fatalf("Reply = empty, want tool query reply")
	}
}

func TestTaskMetaExecutorKeepsSubscriptionTaskOpenAndListsDepartments(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		GroupSub:       &testGroupSubPort{},
		Dept:           testFamilyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	result, err := (taskMetaExecutor{agent: a}).Execute(context.Background(), &TaskInstance{
		ID:           "task-sub-meta",
		Type:         "subscribe_attendance_push",
		Status:       "waiting_slots",
		Slots:        map[string]string{"scope": "department"},
		MissingSlots: []string{"dept_names"},
		ExpiresAt:    time.Now().Add(sessionTTL),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.KeepTaskOpen {
		t.Fatalf("KeepTaskOpen = false, want true")
	}
	if result.Task == nil || result.Task.Type != "subscribe_attendance_push" {
		t.Fatalf("Task = %#v, want open subscription task", result.Task)
	}
	if !strings.Contains(result.Reply, "家族7期") {
		t.Fatalf("Reply = %q, want cached departments", result.Reply)
	}
}

func TestTaskStartExecutorCreatesSubscriptionTask(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		GroupSub:       &testGroupSubPort{},
		Dept:           testClarifyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	result, err := (taskStartExecutor{agent: a}).Execute(context.Background(), RouteDecision{
		Kind:           RouteTaskStart,
		TargetTaskType: "subscribe_attendance_push",
	}, "开启考勤订阅", &tools.UserContext{
		TenantID:          42,
		UserID:            7,
		UserRole:          1,
		ConversationType:  "2",
		ConversationID:    "conv-start",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.KeepTaskOpen {
		t.Fatalf("KeepTaskOpen = false, want open task waiting for scope")
	}
	if result.Task == nil || result.Task.Type != "subscribe_attendance_push" {
		t.Fatalf("Task = %#v, want created subscription task", result.Task)
	}
	if !strings.Contains(result.Reply, "信工24级") {
		t.Fatalf("Reply = %q, want department list", result.Reply)
	}
}

func TestTaskStartExecutorAddsSoftSwitchNotice(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL: "http://127.0.0.1:0",
		LLMAPIKey:  "test-key",
		LLMModel:   "test-model",
		GroupSub: testClarifyGroupSubPort{
			info: &tools.GroupSubInfo{},
		},
		Dept:           testClarifyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	result, err := (taskStartExecutor{agent: a}).Execute(context.Background(), RouteDecision{
		Kind:           RouteTaskStart,
		TargetTaskType: "query_subscription_status",
		SwitchTask:     true,
		SoftNoticeCode: "switched_task",
	}, "帮我看看订阅状态", &tools.UserContext{
		TenantID:          42,
		UserID:            7,
		UserRole:          1,
		ConversationType:  "2",
		ConversationID:    "conv-switch",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(result.Reply, "先切到") {
		t.Fatalf("Reply = %q, want soft switch notice", result.Reply)
	}
	if !strings.Contains(result.Reply, "还没有订阅") {
		t.Fatalf("Reply = %q, want subscription status response", result.Reply)
	}
}

func TestTaskCancelExecutorClearsTask(t *testing.T) {
	t.Parallel()

	result := (taskCancelExecutor{}).Execute(&TaskInstance{
		ID:   "task-cancel",
		Type: "subscribe_attendance_push",
	})
	if result.KeepTaskOpen {
		t.Fatalf("KeepTaskOpen = true, want false")
	}
	if result.Task != nil {
		t.Fatalf("Task = %#v, want nil", result.Task)
	}
	if !strings.Contains(result.Reply, "取消") {
		t.Fatalf("Reply = %q, want cancel acknowledgement", result.Reply)
	}
}

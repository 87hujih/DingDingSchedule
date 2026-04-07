package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"schedule_server/internal/agent/tools"
)

func TestSemanticRouterFallsBackToClarifyOnTimeout(t *testing.T) {
	t.Parallel()

	router := newSemanticRouter(NewLLMClient("http://127.0.0.1:0", "test-key", "router-model"))

	decision := router.Route(context.Background(), RouteContext{
		Message: "这个怎么算",
	})

	if decision.Kind != RouteClarify {
		t.Fatalf("Kind = %q, want %q", decision.Kind, RouteClarify)
	}
	if decision.RouteSource != RouteSourceFallback {
		t.Fatalf("RouteSource = %q, want %q", decision.RouteSource, RouteSourceFallback)
	}
}

func TestSemanticRouterFallsBackToClarifyOnInvalidJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"not-json"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	router := newSemanticRouter(NewLLMClient(server.URL, "test-key", "router-model"))

	decision := router.Route(context.Background(), RouteContext{
		Message: "今天第一节张三为什么迟到",
	})

	if decision.Kind != RouteClarify {
		t.Fatalf("Kind = %q, want %q", decision.Kind, RouteClarify)
	}
	if decision.RouteSource != RouteSourceFallback {
		t.Fatalf("RouteSource = %q, want %q", decision.RouteSource, RouteSourceFallback)
	}
}

func TestSemanticRouterParsesExplicitTaskSwitchDecision(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"kind\":\"task_start\",\"confidence\":0.93,\"reason_code\":\"explicit_new_task\",\"target_task_type\":\"query_subscription_status\",\"switch_task\":true,\"soft_notice_code\":\"task_switched\"}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	router := newSemanticRouter(NewLLMClient(server.URL, "test-key", "router-model"))

	decision := router.Route(context.Background(), RouteContext{
		Message: "帮我看看订阅状态",
		ActiveTask: &TaskRouteState{
			ID:     "task-1",
			Type:   "sign_for_user",
			Status: "waiting_slots",
		},
		RecentTurns: []TurnDigest{
			{Role: "user", Content: "帮我给张三补签今天第一节考勤"},
			{Role: "assistant", Content: "我还缺少姓名，请补充后我再帮你补签。"},
		},
	})

	if decision.Kind != RouteTaskStart {
		t.Fatalf("Kind = %q, want %q", decision.Kind, RouteTaskStart)
	}
	if decision.TargetTaskType != "query_subscription_status" {
		t.Fatalf("TargetTaskType = %q, want query_subscription_status", decision.TargetTaskType)
	}
	if !decision.SwitchTask {
		t.Fatalf("SwitchTask = false, want true")
	}
	if decision.RouteSource != RouteSourceSemanticRouter {
		t.Fatalf("RouteSource = %q, want %q", decision.RouteSource, RouteSourceSemanticRouter)
	}
}

func TestSemanticRouterParsesTaskMetaDecision(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"kind\":\"task_meta\",\"confidence\":0.88,\"reason_code\":\"task_option_question\",\"target_task_id\":\"task-1\"}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	router := newSemanticRouter(NewLLMClient(server.URL, "test-key", "router-model"))

	decision := router.Route(context.Background(), RouteContext{
		Message: "现在都有哪些部门",
		ActiveTask: &TaskRouteState{
			ID:             "task-1",
			Type:           "subscribe_attendance_push",
			Status:         "waiting_slots",
			MissingSlots:   []string{"dept_names"},
			CandidateHints: []string{"家族7期", "乐知全栈一期"},
		},
	})

	if decision.Kind != RouteTaskMeta {
		t.Fatalf("Kind = %q, want %q", decision.Kind, RouteTaskMeta)
	}
	if decision.TargetTaskID != "task-1" {
		t.Fatalf("TargetTaskID = %q, want task-1", decision.TargetTaskID)
	}
}

func TestBuildRouteContextCarriesConversationMetadata(t *testing.T) {
	t.Parallel()

	ctx := buildRouteContext("今天第一节谁未到", &tools.UserContext{
		UserRole:          1,
		ConversationType:  "2",
		ConversationTitle: "考勤群",
	}, nil, nil)

	if ctx.ConversationType != "2" {
		t.Fatalf("ConversationType = %q, want 2", ctx.ConversationType)
	}
	if ctx.ConversationTitle != "考勤群" {
		t.Fatalf("ConversationTitle = %q, want 考勤群", ctx.ConversationTitle)
	}
}

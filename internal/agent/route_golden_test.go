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

func TestRouteGoldenCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		question          string
		activeTask        *TaskInstance
		decision          RouteDecision
		wantHandled       bool
		wantReplyContains []string
		wantTaskType      string
		wantTaskStatus    string
	}{
		{
			name:     "规则问题走 RAG",
			question: "迟到怎么判定",
			decision: RouteDecision{
				Kind:       RouteRAGQuery,
				Confidence: 0.86,
				ReasonCode: "rule_question",
			},
			wantHandled:       true,
			wantReplyContains: []string{"迟到按上课开始时间判定"},
		},
		{
			name:     "实时数据问题走工具查询",
			question: "今天第一节谁未到",
			decision: RouteDecision{
				Kind:       RouteToolQuery,
				Confidence: 0.88,
				ReasonCode: "attendance_query",
			},
			wantHandled:       true,
			wantReplyContains: []string{"今天第一节未到人员已查询"},
		},
		{
			name:     "规则加实时数据走混合查询",
			question: "今天第一节谁迟到了，迟到怎么算",
			decision: RouteDecision{
				Kind:       RouteMixedQuery,
				Confidence: 0.91,
				ReasonCode: "mixed_attendance_rule",
			},
			wantHandled:       true,
			wantReplyContains: []string{"迟到按上课开始时间判定", "今天第一节"},
		},
		{
			name:     "明确补签请求启动补签任务",
			question: "帮张三补签今天第一节",
			decision: RouteDecision{
				Kind:           RouteTaskStart,
				Confidence:     0.94,
				ReasonCode:     "manual_sign_request",
				TargetTaskType: "sign_for_user",
			},
			wantHandled:       true,
			wantReplyContains: []string{"已为张三补签"},
		},
		{
			name:     "订阅任务中询问选项走任务元信息",
			question: "都有哪些部门",
			activeTask: &TaskInstance{
				ID:             "task-subscribe",
				Type:           "subscribe_attendance_push",
				Status:         string(taskStatusWaiting),
				Slots:          map[string]string{"scope": "department"},
				MissingSlots:   []string{"dept_names"},
				CandidateCache: map[string]any{"departments": []string{"信工24级", "信工23级"}},
				ExpiresAt:      time.Now().Add(sessionTTL),
			},
			decision: RouteDecision{
				Kind:       RouteTaskMeta,
				Confidence: 0.9,
				ReasonCode: "task_option_question",
			},
			wantHandled:       true,
			wantReplyContains: []string{"信工24级", "信工23级"},
			wantTaskType:      "subscribe_attendance_push",
			wantTaskStatus:    string(taskStatusWaiting),
		},
		{
			name:     "取消当前任务只取消任务不执行工具",
			question: "取消",
			activeTask: &TaskInstance{
				ID:           "task-sign",
				Type:         "sign_for_user",
				Status:       string(taskStatusWaiting),
				Slots:        map[string]string{"user_name": "张三"},
				MissingSlots: []string{"date", "section"},
				ExpiresAt:    time.Now().Add(sessionTTL),
			},
			decision: RouteDecision{
				Kind:       RouteTaskCancel,
				Confidence: 0.93,
				ReasonCode: "explicit_cancel",
			},
			wantHandled:       true,
			wantReplyContains: []string{"取消"},
		},
		{
			name:     "越界问题直接拒绝",
			question: "帮我写一份营销方案",
			decision: RouteDecision{
				Kind:       RouteOffTopicReject,
				Confidence: 0.97,
				ReasonCode: "off_topic",
			},
			wantHandled:       true,
			wantReplyContains: []string{"考勤", "请假"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := newRouteGoldenAgent(t)
			defer a.Stop()

			sessionKey := "42:golden:" + tc.name
			if tc.activeTask != nil {
				a.sessions.setTaskInstance(sessionKey, tc.activeTask)
			}

			metrics := callMetrics{}
			beforeTask := activeTaskFromTaskInstance(tc.activeTask)
			handled, reply, err := a.tryHandleRoutePrimary(
				context.Background(),
				routeGoldenUserContext(),
				sessionKey,
				tc.question,
				nil,
				tools.Message{Role: "user", Content: tc.question},
				time.Now(),
				beforeTask,
				&metrics,
				tc.decision,
			)
			if err != nil {
				t.Fatalf("tryHandleRoutePrimary() 返回错误: %v", err)
			}
			if handled != tc.wantHandled {
				t.Fatalf("handled = %v，期望 %v", handled, tc.wantHandled)
			}
			for _, want := range tc.wantReplyContains {
				if !strings.Contains(reply, want) {
					t.Fatalf("回复 = %q，期望包含 %q", reply, want)
				}
			}

			_, task := a.sessions.getTaskState(sessionKey)
			if tc.wantTaskType == "" {
				if task != nil {
					t.Fatalf("任务 = %#v，期望没有保留任务", task)
				}
				return
			}
			if task == nil {
				t.Fatalf("任务为空，期望保留 %s", tc.wantTaskType)
			}
			if task.Type != tc.wantTaskType {
				t.Fatalf("任务类型 = %q，期望 %q", task.Type, tc.wantTaskType)
			}
			if tc.wantTaskStatus != "" && task.Status != tc.wantTaskStatus {
				t.Fatalf("任务状态 = %q，期望 %q", task.Status, tc.wantTaskStatus)
			}
		})
	}
}

func newRouteGoldenAgent(t *testing.T) *Agent {
	t.Helper()

	llmServer := newGoldenLLMServer(t)
	t.Cleanup(llmServer.Close)

	return NewAgent(Deps{
		LLMBaseURL: llmServer.URL,
		LLMAPIKey:  "test-key",
		LLMModel:   "test-model",
		RouteMode:  string(RouteModeLive),
		Knowledge: &testKnowledgePort{
			hits: []tools.KnowledgeHit{
				{SourceRef: "考勤规则#1", Body: "迟到按上课开始时间判定。", Score: 18},
			},
		},
		Attendance:     &testTaskAttendancePort{},
		User:           testTaskUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Dept:           testClarifyDeptPort{},
		GroupSub:       &testGroupSubPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
}

func routeGoldenUserContext() *tools.UserContext {
	return &tools.UserContext{
		TenantID:          42,
		UserID:            7,
		UserRole:          1,
		DingUserID:        "ding-user",
		Name:              "Alice",
		ConversationType:  "2",
		ConversationID:    "golden",
		ConversationTitle: "测试群",
	}
}

func newGoldenLLMServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("解析请求失败: %v", err)
			http.Error(w, "解析请求失败: "+err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case requestContains(req, "迟到按上课开始时间判定") && requestContains(req, "今天第一节"):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"今天第一节查询完成。迟到按上课开始时间判定。"},"finish_reason":"stop"}]}`))
		case requestContains(req, "迟到按上课开始时间判定"):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"迟到按上课开始时间判定。"},"finish_reason":"stop"}]}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"今天第一节未到人员已查询。"},"finish_reason":"stop"}]}`))
		}
	}))
}

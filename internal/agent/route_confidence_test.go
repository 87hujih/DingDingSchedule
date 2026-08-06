package agent

import (
	"context"
	"strings"
	"testing"

	"schedule_server/pkg/dingtalk"

	"go.uber.org/zap"
)

func TestAgentChatClarifiesLowConfidenceTaskStartInLiveRoute(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	routerServer := newRouteDecisionServer(t, RouteDecision{
		Kind:           RouteTaskStart,
		Confidence:     0.42,
		ReasonCode:     "weak_task_start",
		TargetTaskType: "sign_for_user",
	})
	defer routerServer.Close()

	a := mustNewTestAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		Attendance:       attendance,
		User:             testTaskUserPort{},
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
		Content:          "帮张三补签今天第一节",
		ConversationID:   "conv-low-confidence-start",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() 返回错误: %v", err)
	}
	if !strings.Contains(reply, "还没完全理解") {
		t.Fatalf("回复 = %q，期望低置信度任务启动被澄清", reply)
	}

	_, task := a.sessions.getTaskState("42:ding-user")
	if task != nil {
		t.Fatalf("任务 = %#v，期望低置信度时不创建任务", task)
	}
	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.signCalls != 0 {
		t.Fatalf("补签调用次数 = %d，期望低置信度时不执行补签", attendance.signCalls)
	}
}

func TestAgentChatClarifiesLowConfidenceTaskContinueInLiveRoute(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	routerServer := newRouteDecisionServer(t,
		RouteDecision{
			Kind:           RouteTaskStart,
			Confidence:     0.95,
			ReasonCode:     "explicit_task_start",
			TargetTaskType: "sign_for_user",
		},
		RouteDecision{
			Kind:           RouteTaskContinue,
			Confidence:     0.51,
			ReasonCode:     "weak_task_continue",
			TargetTaskType: "sign_for_user",
			ExtractedEntities: &ExtractedEntities{
				UserName: "张三",
				Date:     "今天",
				Section:  1,
			},
		},
	)
	defer routerServer.Close()

	a := mustNewTestAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		Attendance:       attendance,
		User:             testTaskUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})

	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "帮我补签",
		ConversationID:   "conv-low-confidence-continue",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("首次 Chat() 返回错误: %v", err)
	}

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "张三今天第一节",
		ConversationID:   "conv-low-confidence-continue",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("二次 Chat() 返回错误: %v", err)
	}
	if !strings.Contains(reply, "还没完全理解") {
		t.Fatalf("回复 = %q，期望低置信度任务继续被澄清", reply)
	}

	_, task := a.sessions.getTaskState("42:ding-user")
	if task == nil || task.Type != "sign_for_user" {
		t.Fatalf("任务 = %#v，期望保留原补签任务等待用户确认", task)
	}
	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.signCalls != 0 {
		t.Fatalf("补签调用次数 = %d，期望低置信度时不执行补签", attendance.signCalls)
	}
}

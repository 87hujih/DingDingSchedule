package agent

import (
	"testing"
	"time"
)

func TestInterpretSystemIntentMatchesOnlyExactShortSystemMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		message string
		want    SystemIntent
	}{
		{message: "你好", want: SystemIntentGreeting},
		{message: " 您好！ ", want: SystemIntentGreeting},
		{message: "你有什么功能？", want: SystemIntentHelp},
		{message: "你好，帮我取消订阅", want: ""},
		{message: "你好，查一下我的课表", want: ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.message, func(t *testing.T) {
			t.Parallel()
			if got := interpretSystemIntent(tt.message); got != tt.want {
				t.Fatalf("interpretSystemIntent(%q) = %q, want %q", tt.message, got, tt.want)
			}
		})
	}
}

func TestInterpretConversationReturnsGreetingForHello(t *testing.T) {
	t.Parallel()

	decision := interpretConversation("你好", nil)
	if decision.Event != eventGreeting {
		t.Fatalf("event = %q, want %q", decision.Event, eventGreeting)
	}
}

func TestInterpretConversationReturnsTaskFollowUpForDeptOnlyReply(t *testing.T) {
	t.Parallel()

	decision := interpretConversation("信工24级", &ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"dept_names"},
		FilledSlots:   map[string]string{"scope": "department"},
		ExpiresAt:     time.Now().Add(sessionTTL),
		LastPrompt:    "clarify_dept_names",
	})
	if decision.Event != eventTaskFollowUp {
		t.Fatalf("event = %q, want %q", decision.Event, eventTaskFollowUp)
	}
}

func TestInterpretConversationReturnsCancelForCancelReply(t *testing.T) {
	t.Parallel()

	decision := interpretConversation("取消", &ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"scope"},
		FilledSlots:   map[string]string{},
		ExpiresAt:     time.Now().Add(sessionTTL),
		LastPrompt:    "clarify_scope",
	})
	if decision.Event != eventCancel {
		t.Fatalf("event = %q, want %q", decision.Event, eventCancel)
	}
}

func TestInterpretConversationReturnsNewRequestForClearBusinessQuestion(t *testing.T) {
	t.Parallel()

	decision := interpretConversation("查一下今天第一节谁未到", &ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"scope"},
		FilledSlots:   map[string]string{},
		ExpiresAt:     time.Now().Add(sessionTTL),
		LastPrompt:    "clarify_scope",
	})
	if decision.Event != eventNewRequest {
		t.Fatalf("event = %q, want %q", decision.Event, eventNewRequest)
	}
}

func TestInterpretConversationReturnsUnknownForAmbiguousShortReply(t *testing.T) {
	t.Parallel()

	decision := interpretConversation("嗯", &ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"scope"},
		FilledSlots:   map[string]string{},
		ExpiresAt:     time.Now().Add(sessionTTL),
		LastPrompt:    "clarify_scope",
	})
	if decision.Event != eventUnknown {
		t.Fatalf("event = %q, want %q", decision.Event, eventUnknown)
	}
}

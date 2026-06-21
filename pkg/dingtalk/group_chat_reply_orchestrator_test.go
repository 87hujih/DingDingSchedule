package dingtalk

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestGroupChatReplyOrchestratorUsesWebhookWhenAvailable(t *testing.T) {
	t.Parallel()

	webhookReplies := make(chan string, 1)

	orch := &groupChatReplyOrchestrator{
		logger: zap.NewNop().Sugar(),
		sema:   make(chan struct{}, 1),
		chatHandler: func(context.Context, *ChatMessage) (string, error) {
			return "final reply", nil
		},
		asyncReplyHandler: func(context.Context, *ChatMessage, string) {
			t.Fatalf("async fallback should not be called when webhook succeeds")
		},
		replyText: func(context.Context, string, string) error {
			webhookReplies <- "final reply"
			return nil
		},
		processTimeout:  2 * time.Second,
		ackDelay:        time.Hour,
		webhookTimeout:  time.Second,
		fallbackTimeout: time.Second,
	}

	_, err := orch.handle(groupChatReplyRequest{
		senderID:       "user-1",
		senderNick:     "张三",
		sessionWebhook: "https://webhook.test",
	}, &ChatMessage{SenderID: "user-1", SenderNick: "张三"})
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}

	select {
	case reply := <-webhookReplies:
		if reply != "final reply" {
			t.Fatalf("webhook reply = %q, want %q", reply, "final reply")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook reply")
	}
}

func TestGroupChatReplyOrchestratorFallsBackWhenWebhookFails(t *testing.T) {
	t.Parallel()

	fallbackReplies := make(chan string, 1)

	orch := &groupChatReplyOrchestrator{
		logger: zap.NewNop().Sugar(),
		sema:   make(chan struct{}, 1),
		chatHandler: func(context.Context, *ChatMessage) (string, error) {
			return "final reply", nil
		},
		asyncReplyHandler: func(_ context.Context, _ *ChatMessage, reply string) {
			fallbackReplies <- reply
		},
		replyText: func(context.Context, string, string) error {
			return errors.New("webhook expired")
		},
		processTimeout:  2 * time.Second,
		ackDelay:        time.Hour,
		webhookTimeout:  time.Second,
		fallbackTimeout: time.Second,
	}

	_, err := orch.handle(groupChatReplyRequest{
		senderID:       "user-1",
		senderNick:     "张三",
		sessionWebhook: "https://webhook.test",
	}, &ChatMessage{SenderID: "user-1", SenderNick: "张三"})
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}

	select {
	case reply := <-fallbackReplies:
		if reply != "final reply" {
			t.Fatalf("fallback reply = %q, want %q", reply, "final reply")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fallback reply")
	}
}

func TestGroupChatReplyOrchestratorReturnsBusyWhenConcurrencyFull(t *testing.T) {
	t.Parallel()

	busyReplies := make(chan string, 1)
	var chatCalled atomic.Bool

	sema := make(chan struct{}, 1)
	sema <- struct{}{}

	orch := &groupChatReplyOrchestrator{
		logger: zap.NewNop().Sugar(),
		sema:   sema,
		chatHandler: func(context.Context, *ChatMessage) (string, error) {
			chatCalled.Store(true)
			return "", nil
		},
		asyncReplyHandler: func(context.Context, *ChatMessage, string) {
			t.Fatalf("async fallback should not be called on busy path")
		},
		replyText: func(_ context.Context, _ string, reply string) error {
			busyReplies <- reply
			return nil
		},
		processTimeout:  2 * time.Second,
		ackDelay:        time.Hour,
		webhookTimeout:  time.Second,
		fallbackTimeout: time.Second,
	}

	_, err := orch.handle(groupChatReplyRequest{
		senderID:       "user-1",
		senderNick:     "张三",
		sessionWebhook: "https://webhook.test",
	}, &ChatMessage{SenderID: "user-1", SenderNick: "张三"})
	if err != nil {
		t.Fatalf("handle() error = %v", err)
	}

	select {
	case reply := <-busyReplies:
		want := "@张三 服务繁忙，请稍后重试"
		if reply != want {
			t.Fatalf("busy reply = %q, want %q", reply, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for busy reply")
	}

	if chatCalled.Load() {
		t.Fatal("chatHandler should not be called when concurrency is full")
	}
}

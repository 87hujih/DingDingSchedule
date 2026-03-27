package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	agenttool "schedule_server/internal/agent/tools"
	"schedule_server/pkg/dingtalk"
)

type fakeEvalRunner struct {
	reply    string
	recorder *recordingCallLog
	toolName string
	stopFn   func()
}

// Chat 模拟一次 Agent 问答，并把工具调用结果写入录制器。
func (r *fakeEvalRunner) Chat(_ context.Context, msg *dingtalk.ChatMessage) (string, error) {
	if r.recorder != nil {
		r.recorder.Write(context.Background(), agenttool.CallLog{
			Question:    msg.Content,
			Reply:       r.reply,
			ToolsCalled: []string{r.toolName},
		})
	}
	return r.reply, nil
}

// Stop 记录一次 runner 关闭。
func (r *fakeEvalRunner) Stop() {
	if r.stopFn != nil {
		r.stopFn()
	}
}

// TestValidateWithAgentPrerequisitesRejectsPlaceholderKey 验证 with-agent 模式会在明显无效的 LLM key 下提前失败。
func TestValidateWithAgentPrerequisitesRejectsPlaceholderKey(t *testing.T) {
	t.Parallel()

	err := validateWithAgentPrerequisites("sk-placeholder", "corp-id", "sender-id")
	if err == nil {
		t.Fatalf("validateWithAgentPrerequisites() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "LLM API Key") {
		t.Fatalf("error = %q, want mention LLM API Key", err.Error())
	}
}

// TestNewCaseScopedObserverBuildsFreshRunnerPerCall 验证每条评测样本都会创建独立的 Agent runner，避免会话和限流串样本。
func TestNewCaseScopedObserverBuildsFreshRunnerPerCall(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	buildCount := 0
	stopCount := 0

	observer := newCaseScopedObserver(func() (evalChatRunner, *recordingCallLog, error) {
		mu.Lock()
		defer mu.Unlock()

		buildCount++
		recorder := newRecordingCallLog()
		runner := &fakeEvalRunner{
			reply:    "ok",
			recorder: recorder,
			toolName: "tool-" + string(rune('0'+buildCount)),
			stopFn: func() {
				mu.Lock()
				defer mu.Unlock()
				stopCount++
			},
		}
		return runner, recorder, nil
	}, "corp-id", "sender-id", "EvalUser")

	first, err := observer(context.Background(), "问题 1")
	if err != nil {
		t.Fatalf("first observer call error = %v", err)
	}
	second, err := observer(context.Background(), "问题 2")
	if err != nil {
		t.Fatalf("second observer call error = %v", err)
	}

	if buildCount != 2 {
		t.Fatalf("buildCount = %d, want 2", buildCount)
	}
	if stopCount != 2 {
		t.Fatalf("stopCount = %d, want 2", stopCount)
	}
	if len(first.Tools) != 1 || first.Tools[0] != "tool-1" {
		t.Fatalf("first tools = %v, want [tool-1]", first.Tools)
	}
	if len(second.Tools) != 1 || second.Tools[0] != "tool-2" {
		t.Fatalf("second tools = %v, want [tool-2]", second.Tools)
	}
}

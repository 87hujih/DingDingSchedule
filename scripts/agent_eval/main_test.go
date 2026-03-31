package main

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"schedule_server/internal/agent"
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

// TestPrintSummaryIncludesDomainAndModeAccuracy 验证评测脚本会输出领域与模式准确率。
func TestPrintSummaryIncludesDomainAndModeAccuracy(t *testing.T) {
	t.Parallel()

	output := captureStdout(t, func() {
		printSummary(agent.EvalSummary{
			TotalCases:        2,
			DomainPassed:      2,
			DomainAccuracy:    100,
			ModePassed:        2,
			ModeAccuracy:      100,
			RoutePassed:       2,
			RouteAccuracy:     100,
			RetrievalCases:    1,
			RetrievalPassed:   1,
			RetrievalAccuracy: 100,
			AverageLatencyMs:  86,
		}, false)
	})

	if !strings.Contains(output, "领域准确率") {
		t.Fatalf("summary output missing domain accuracy: %q", output)
	}
	if !strings.Contains(output, "模式准确率") {
		t.Fatalf("summary output missing mode accuracy: %q", output)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = writer

	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	return string(data)
}

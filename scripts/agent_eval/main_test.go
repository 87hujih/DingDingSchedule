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
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/pkg/dingtalk"
)

type fakeEvalRunner struct {
	reply       string
	recorder    *recordingCallLog
	toolName    string
	protocolAct string
	onChat      func(*dingtalk.ChatMessage)
	stopFn      func()
}

var captureStdoutMu sync.Mutex

// Chat 模拟一次 Agent 问答，并把工具调用结果写入录制器。
func (r *fakeEvalRunner) Chat(_ context.Context, msg *dingtalk.ChatMessage) (string, error) {
	if r.onChat != nil {
		r.onChat(msg)
	}
	if r.recorder != nil {
		r.recorder.Write(context.Background(), agenttool.CallLog{
			Question:    msg.Content,
			Reply:       r.reply,
			ToolsCalled: []string{r.toolName},
			ProtocolAct: r.protocolAct,
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
	conversationTypes := []string(nil)

	observer := newCaseScopedObserver(func() (evalChatRunner, *recordingCallLog, error) {
		mu.Lock()
		defer mu.Unlock()

		buildCount++
		recorder := newRecordingCallLog()
		runner := &fakeEvalRunner{
			reply:       "ok",
			recorder:    recorder,
			toolName:    "tool-" + string(rune('0'+buildCount)),
			protocolAct: "help",
			onChat: func(msg *dingtalk.ChatMessage) {
				mu.Lock()
				defer mu.Unlock()
				conversationTypes = append(conversationTypes, msg.ConversationType)
			},
			stopFn: func() {
				mu.Lock()
				defer mu.Unlock()
				stopCount++
			},
		}
		return runner, recorder, nil
	}, "corp-id", "sender-id", "EvalUser")

	first, err := observer(context.Background(), agent.EvalCase{Question: "问题 1"})
	if err != nil {
		t.Fatalf("first observer call error = %v", err)
	}
	second, err := observer(context.Background(), agent.EvalCase{Question: "问题 2", ConversationType: "1"})
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
	if first.ProtocolAct != "help" {
		t.Fatalf("first ProtocolAct = %q, want help", first.ProtocolAct)
	}
	if len(second.Tools) != 1 || second.Tools[0] != "tool-2" {
		t.Fatalf("second tools = %v, want [tool-2]", second.Tools)
	}
	if len(conversationTypes) != 2 || conversationTypes[0] != "2" || conversationTypes[1] != "1" {
		t.Fatalf("conversationTypes = %v, want [2 1]", conversationTypes)
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

func TestPrintSummaryIncludesProtocolAccuracy(t *testing.T) {
	t.Parallel()

	output := captureStdout(t, func() {
		printSummary(agent.EvalSummary{
			TotalCases:       2,
			DomainPassed:     2,
			DomainAccuracy:   100,
			ModePassed:       2,
			ModeAccuracy:     100,
			RoutePassed:      2,
			RouteAccuracy:    100,
			ProtocolCases:    2,
			ProtocolPassed:   1,
			ProtocolAccuracy: 50,
		}, false)
	})

	if !strings.Contains(output, "协议准确率: 50.0% (1/2)") {
		t.Fatalf("summary output missing protocol accuracy: %q", output)
	}
}

func TestPrintFailuresIncludesProtocolDetails(t *testing.T) {
	t.Parallel()

	output := captureStdout(t, func() {
		printFailures([]agent.EvalCaseResult{{
			Category:              "protocol",
			Question:              "开启本群考勤订阅",
			DomainResult:          "in_domain",
			DomainMatched:         true,
			AnswerMode:            "tool-first",
			ModeMatched:           true,
			Route:                 "tool",
			RouteMatched:          true,
			ProtocolChecked:       true,
			ProtocolMatched:       false,
			ProtocolAct:           "write_request",
			ProtocolDomain:        "subscription",
			ProtocolOperation:     "subscription.start",
			ResponseKind:          "clarify",
			ProtocolBlockedReason: "missing_scope",
		}})
	})

	for _, want := range []string{"protocol=false", "act=write_request", "domain=subscription", "operation=subscription.start", "response=clarify", "blocked=missing_scope"} {
		if !strings.Contains(output, want) {
			t.Fatalf("failure output %q missing %q", output, want)
		}
	}
}

func TestAllActiveTenantTargetsExpandsActiveTenants(t *testing.T) {
	t.Parallel()

	targets, err := resolveEvalTenantTargets(0, true, []model.Tenant{
		{ID: 2, Name: "租户二"},
		{ID: 3, Name: "租户三"},
	})
	if err != nil {
		t.Fatalf("resolveEvalTenantTargets() error = %v", err)
	}
	if len(targets) != 2 || targets[0].ID != 2 || targets[0].Name != "租户二" || targets[1].ID != 3 {
		t.Fatalf("targets = %+v", targets)
	}
}

func TestKnowledgeAuditDetectsMissingRequiredIntents(t *testing.T) {
	t.Parallel()

	results := auditRequiredKnowledgeIntents([]evalTenantTarget{
		{ID: 2, Name: "租户二"},
		{ID: 3, Name: "租户三"},
	}, map[uint][]repository.AgentKnowledgeSearchRow{
		2: {
			{TenantID: 2, Intent: "attendance"},
			{TenantID: 2, Intent: "schedule"},
			{TenantID: 2, Intent: "subscription"},
		},
		3: {
			{TenantID: 3, Intent: "attendance"},
			{TenantID: 3, Intent: "schedule"},
		},
	}, []string{"attendance", "schedule", "subscription"})

	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}
	if !results[0].Passed || len(results[0].MissingIntents) != 0 {
		t.Fatalf("tenant 2 audit = %+v, want passed", results[0])
	}
	if results[1].Passed || len(results[1].MissingIntents) != 1 || results[1].MissingIntents[0] != "subscription" {
		t.Fatalf("tenant 3 audit = %+v, want missing subscription", results[1])
	}
}

func TestLoadMarkdownDocumentsAppliesManifestMetadata(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := parent + string(os.PathSeparator) + "agent-knowledge"
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	if err := os.WriteFile(root+string(os.PathSeparator)+"manifest.yaml", []byte(`
agent-knowledge/attendance-rules.md:
  doc_type: rule
  audience: shared
  intent: attendance
`), 0o644); err != nil {
		t.Fatalf("WriteFile manifest error = %v", err)
	}
	if err := os.WriteFile(root+string(os.PathSeparator)+"attendance-rules.md", []byte("# 考勤规则\n内容\n"), 0o644); err != nil {
		t.Fatalf("WriteFile markdown error = %v", err)
	}

	docs, err := loadMarkdownDocuments(root, []string{"attendance-rules.md"})
	if err != nil {
		t.Fatalf("loadMarkdownDocuments() error = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("doc count = %d, want 1", len(docs))
	}
	meta := docs[0].Metadata
	if docs[0].SourcePath != "agent-knowledge/attendance-rules.md" || meta.DocType != "rule" || meta.Audience != "shared" || meta.Intent != "attendance" {
		t.Fatalf("doc = %+v metadata = %+v, want manifest metadata", docs[0], meta)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	captureStdoutMu.Lock()
	defer captureStdoutMu.Unlock()

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

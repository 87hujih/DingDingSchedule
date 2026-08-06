package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "schedule_server/internal/agent/tools"
	"schedule_server/pkg/dingtalk"

	"go.uber.org/zap"
)

func TestProtocolLiveGoldenHelpDoesNotCallBusinessTools(t *testing.T) {
	t.Parallel()

	callLog := newTestCallLogPort()
	attendance := &testTaskAttendancePort{}
	groupSub := &testGroupSubPort{}
	a := mustNewTestAgent(Deps{
		LLMBaseURL:   "http://127.0.0.1:0",
		LLMAPIKey:    "test-key",
		LLMModel:     "test-model",
		ProtocolMode: string(ProtocolModeLive),
		IntentCompiler: &protocolLiveGoldenCompiler{drafts: []ProtocolDraft{{
			Act:        ActHelp,
			Domain:     DomainSystem,
			Operation:  "system.describe_capability",
			Confidence: 0.95,
		}}},
		CallLog:        callLog,
		Attendance:     attendance,
		GroupSub:       groupSub,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})

	defer a.Stop()

	reply, err := a.Chat(context.Background(), protocolLiveGoldenMessage("你有什么功能", "conv-protocol-golden-help", "1"))
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "系统能力") {
		t.Fatalf("reply = %q, want capability overview", reply)
	}

	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log")
	}
	if log.ProtocolOperation != "system.describe_capability" || log.ExecutorName != "operation_executor" {
		t.Fatalf("log operation=%q executor=%q, want help through operation executor", log.ProtocolOperation, log.ExecutorName)
	}
	assertProtocolLiveGoldenCallLog(t, log)

	if attendance.signCalls != 0 {
		t.Fatalf("SignForUsersBySlot calls = %d, want 0", attendance.signCalls)
	}
	if groupSub.subscribeCalls != 0 || groupSub.unsubscribeCalls != 0 {
		t.Fatalf("group sub calls subscribe=%d unsubscribe=%d, want 0", groupSub.subscribeCalls, groupSub.unsubscribeCalls)
	}
}

func TestProtocolLiveGoldenRuleExplainUsesKnowledgeOnly(t *testing.T) {
	t.Parallel()

	callLog := newTestCallLogPort()
	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{{Heading: "缺勤规则", Body: "超过上课时间未打卡会判为缺勤。", SourceRef: "attendance#absence"}},
	}
	groupSub := &testGroupSubPort{}
	a := mustNewTestAgent(Deps{
		LLMBaseURL:   "http://127.0.0.1:0",
		LLMAPIKey:    "test-key",
		LLMModel:     "test-model",
		ProtocolMode: string(ProtocolModeLive),
		IntentCompiler: &protocolLiveGoldenCompiler{drafts: []ProtocolDraft{{
			Act:        ActRuleQuestion,
			Domain:     DomainAttendance,
			Operation:  "attendance.rule_explain",
			Confidence: 0.91,
			Slots: map[string]SlotDraft{
				"rule_topic": {Field: "rule_topic", Raw: "为什么判我缺勤"},
			},
		}}},
		CallLog:        callLog,
		Knowledge:      knowledge,
		GroupSub:       groupSub,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})

	defer a.Stop()

	reply, err := a.Chat(context.Background(), protocolLiveGoldenMessage("为什么判我缺勤", "conv-protocol-golden-rule", "1"))
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "超过上课时间未打卡") {
		t.Fatalf("reply = %q, want knowledge answer", reply)
	}

	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log")
	}
	if log.ExecutorName != "operation_executor" || log.ToolPool != "operation" {
		t.Fatalf("executor=%q toolPool=%q, want operation executor", log.ExecutorName, log.ToolPool)
	}
	assertProtocolLiveGoldenCallLog(t, log)
	if log.RetrievalHitCount != 1 {
		t.Fatalf("RetrievalHitCount = %d, want 1", log.RetrievalHitCount)
	}
	if groupSub.subscribeCalls != 0 || groupSub.unsubscribeCalls != 0 {
		t.Fatalf("group sub calls subscribe=%d unsubscribe=%d, want 0", groupSub.subscribeCalls, groupSub.unsubscribeCalls)
	}
}

func TestProtocolLiveGoldenMyScheduleUsesOperationExecutor(t *testing.T) {
	t.Parallel()

	callLog := newTestCallLogPort()
	schedule := &protocolLiveGoldenSchedulePort{
		courses: []agenttools.CourseItem{{CourseName: "高等数学", DayOfWeek: 1, Section: 2, Location: "A101"}},
	}
	a := mustNewTestAgent(Deps{
		LLMBaseURL:   "http://127.0.0.1:0",
		LLMAPIKey:    "test-key",
		LLMModel:     "test-model",
		ProtocolMode: string(ProtocolModeLive),
		IntentCompiler: &protocolLiveGoldenCompiler{drafts: []ProtocolDraft{{
			Act:        ActReadQuery,
			Domain:     DomainSchedule,
			Operation:  "schedule.query_my_schedule",
			Confidence: 0.9,
		}}},
		CallLog:        callLog,
		Schedule:       schedule,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})

	defer a.Stop()

	reply, err := a.Chat(context.Background(), protocolLiveGoldenMessage("查我的课表", "conv-protocol-golden-schedule", "1"))
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "高等数学") {
		t.Fatalf("reply = %q, want schedule result", reply)
	}
	if schedule.listMyCalls != 1 || schedule.lastWeek != 3 {
		t.Fatalf("ListMyScheduleByWeek calls=%d week=%d, want current week 3", schedule.listMyCalls, schedule.lastWeek)
	}

	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log")
	}
	if log.ProtocolOperation != "schedule.query_my_schedule" || log.ExecutorName != "operation_executor" {
		t.Fatalf("operation=%q executor=%q, want schedule operation executor", log.ProtocolOperation, log.ExecutorName)
	}
	assertProtocolLiveGoldenCallLog(t, log)
}

func TestProtocolLiveGoldenUnknownIntentRecordsV2FailureFields(t *testing.T) {
	t.Parallel()

	callLog := newTestCallLogPort()
	a := mustNewTestAgent(Deps{
		LLMBaseURL:   "http://127.0.0.1:0",
		LLMAPIKey:    "test-key",
		LLMModel:     "test-model",
		ProtocolMode: string(ProtocolModeLive),
		IntentCompiler: &protocolLiveGoldenCompiler{drafts: []ProtocolDraft{{
			Act:        ActUnknown,
			Domain:     DomainUnknown,
			Operation:  "",
			Confidence: 0.2,
			Reason:     "unknown_intent",
		}}},
		CallLog:        callLog,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})

	defer a.Stop()

	if _, err := a.Chat(context.Background(), protocolLiveGoldenMessage("火星天气怎么样", "conv-protocol-golden-unknown", "1")); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log")
	}

	required := map[string]string{
		"RequestID":              log.RequestID,
		"CompilerStatus":         log.CompilerStatus,
		"CatalogValidationCode":  log.CatalogValidationCode,
		"WorkflowDecision":       log.WorkflowDecision,
		"EntityResolutionStatus": log.EntityResolutionStatus,
		"PrePolicyResult":        log.PrePolicyResult,
		"ResourcePolicyResult":   log.ResourcePolicyResult,
		"WriteGuardResult":       log.WriteGuardResult,
		"ExecutorStatus":         log.ExecutorStatus,
		"RendererName":           log.RendererName,
		"ResponseKind":           log.ResponseKind,
		"FailureLayer":           log.FailureLayer,
		"ReplayCaseID":           log.ReplayCaseID,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("%s is empty in protocol_live failure call log: %+v", name, log)
		}
	}
	if log.FailureLayer != string(FailureIntent) {
		t.Fatalf("FailureLayer = %q, want %q", log.FailureLayer, FailureIntent)
	}
	if log.LegacyCalled {
		t.Fatalf("LegacyCalled = true, want false for protocol_live failure")
	}
	if log.ReplayCaseID != log.RequestID {
		t.Fatalf("ReplayCaseID = %q, want request id %q", log.ReplayCaseID, log.RequestID)
	}
}

func assertProtocolLiveGoldenCallLog(t *testing.T, log agenttools.CallLog) {
	t.Helper()

	if log.RequestID == "" {
		t.Fatalf("RequestID is empty in protocol_live call log: %+v", log)
	}
	required := map[string]string{
		"CompilerStatus":         log.CompilerStatus,
		"CatalogValidationCode":  log.CatalogValidationCode,
		"WorkflowDecision":       log.WorkflowDecision,
		"EntityResolutionStatus": log.EntityResolutionStatus,
		"PrePolicyResult":        log.PrePolicyResult,
		"ResourcePolicyResult":   log.ResourcePolicyResult,
		"WriteGuardResult":       log.WriteGuardResult,
		"ExecutorStatus":         log.ExecutorStatus,
		"RendererName":           log.RendererName,
		"ResponseKind":           log.ResponseKind,
		"ReplayCaseID":           log.ReplayCaseID,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("%s is empty in protocol_live call log: %+v", name, log)
		}
	}
	if log.LegacyCalled {
		t.Fatalf("LegacyCalled = true, want false for protocol_live golden case: %+v", log)
	}
	if log.FailureLayer != "" {
		t.Fatalf("FailureLayer = %q, want empty for successful golden case", log.FailureLayer)
	}
	if log.RendererName != "response_renderer" {
		t.Fatalf("RendererName = %q, want response_renderer", log.RendererName)
	}
}

type protocolLiveGoldenCompiler struct {
	mu     sync.Mutex
	drafts []ProtocolDraft
}

func (c *protocolLiveGoldenCompiler) Compile(context.Context, IntentCompileRequest) (IntentCompileResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.drafts) == 0 {
		return staticIntentCompileResult(unknownIntentDraft("unknown_intent")), nil
	}
	draft := c.drafts[0]
	c.drafts = c.drafts[1:]
	return staticIntentCompileResult(draft), nil
}

type protocolLiveGoldenSchedulePort struct {
	listMyCalls int
	lastWeek    int
	courses     []agenttools.CourseItem
}

func (p *protocolLiveGoldenSchedulePort) ListMyScheduleByWeek(_ context.Context, _ uint, week int) ([]agenttools.CourseItem, error) {
	p.listMyCalls++
	p.lastWeek = week
	return p.courses, nil
}

func (p *protocolLiveGoldenSchedulePort) ListUserScheduleByWeek(context.Context, uint, int, uint, int) ([]agenttools.CourseItem, error) {
	return nil, nil
}

func (p *protocolLiveGoldenSchedulePort) GetFreeUsersBySlot(context.Context, int, int, int, int64) ([]agenttools.FreeSlotResult, error) {
	return nil, nil
}

func protocolLiveGoldenMessage(content, conversationID, conversationType string) *dingtalk.ChatMessage {
	return &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          content,
		ConversationID:   conversationID,
		ConversationType: conversationType,
	}
}

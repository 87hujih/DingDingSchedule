package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"schedule_server/internal/agent/tools"
	"schedule_server/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReplayCaseFromCallLogUsesStructuredFields(t *testing.T) {
	t.Parallel()

	row := model.AgentCallLog{
		ID:                  99,
		TenantID:            42,
		UserID:              7,
		ConversationID:      "conv-replay",
		Question:            "开启本群考勤订阅",
		ProtocolMode:        string(ProtocolModeLive),
		ProtocolAct:         string(ActWriteRequest),
		ProtocolDomain:      string(DomainSubscription),
		ProtocolOperation:   "subscription.start",
		ResponseKind:        string(ResponseClarify),
		BlockedReason:       "missing_scope",
		FailureLayer:        string(FailureWorkflow),
		LegacyCalled:        false,
		WorkflowStateBefore: string(WorkflowCollectScope),
		WorkflowIDBefore:    "wf-before",
		WorkflowDecision:    string(WorkflowStartNew),
		ReplayCaseID:        "replay-subscription-start-99",
		CreatedAt:           time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC),
	}

	tc := ReplayCaseFromCallLog(row)

	if tc.ID != "replay-subscription-start-99" {
		t.Fatalf("ID = %q, want replay id from log", tc.ID)
	}
	if tc.Question != row.Question || tc.TenantID != 42 || tc.UserID != 7 || tc.ConversationID != "conv-replay" {
		t.Fatalf("context mismatch: %+v", tc)
	}
	if tc.Expected.Operation != "subscription.start" || tc.Expected.ResponseKind != string(ResponseClarify) {
		t.Fatalf("expected protocol mismatch: %+v", tc.Expected)
	}
	if tc.Expected.BlockedReason != "missing_scope" || tc.Expected.FailureLayer != string(FailureWorkflow) {
		t.Fatalf("expected failure mismatch: %+v", tc.Expected)
	}
	if tc.Expected.LegacyCalled {
		t.Fatalf("Expected.LegacyCalled = true, want false")
	}
	if tc.ActiveWorkflowBefore == nil || tc.ActiveWorkflowBefore.ID != "wf-before" || tc.ActiveWorkflowBefore.State != WorkflowCollectScope {
		t.Fatalf("ActiveWorkflowBefore = %+v, want workflow snapshot from log", tc.ActiveWorkflowBefore)
	}
}

func TestReplayCaseFromCallLogRestoresWorkflowTypeStateAndCandidates(t *testing.T) {
	t.Parallel()

	row := model.AgentCallLog{
		ID:                         100,
		TenantID:                   42,
		UserID:                     7,
		ConversationID:             "conv-replay",
		Question:                   "都有哪些部门",
		ProtocolMode:               string(ProtocolModeLive),
		ProtocolAct:                string(ActReadQuery),
		ProtocolDomain:             string(DomainSubscription),
		ProtocolOperation:          "subscription.list_departments",
		ResponseKind:               string(ResponseSelectOptions),
		WorkflowTypeBefore:         string(WorkflowSubscriptionStart),
		WorkflowStateBefore:        string(WorkflowCollectScope),
		WorkflowIDBefore:           "wf-before",
		WorkflowSnapshotBeforeJSON: `{"id":"wf-before","type":"subscription.start","state":"collect_scope","tenant_id":42,"actor_user_id":7,"conversation_id":"conv-replay","missing_fields":["scope"],"candidates":{"dept_ids":[{"id":"101","label":"信工25级","value":"101","tenant_id":42}]}}`,
	}

	tc := ReplayCaseFromCallLog(row)

	if tc.ActiveWorkflowBefore == nil {
		t.Fatalf("ActiveWorkflowBefore = nil, want restored workflow")
	}
	if tc.ActiveWorkflowBefore.Type != WorkflowSubscriptionStart {
		t.Fatalf("workflow type = %q, want %q", tc.ActiveWorkflowBefore.Type, WorkflowSubscriptionStart)
	}
	if tc.ActiveWorkflowBefore.State != WorkflowCollectScope {
		t.Fatalf("workflow state = %q, want %q", tc.ActiveWorkflowBefore.State, WorkflowCollectScope)
	}
	candidates := tc.ActiveWorkflowBefore.Candidates["dept_ids"]
	if len(candidates) != 1 || candidates[0].ID != "101" || candidates[0].TenantID != 42 {
		t.Fatalf("workflow candidates = %+v, want restored dept candidate", candidates)
	}
}

func TestReplayCaseFromOldCallLogInfersWorkflowTypeFromState(t *testing.T) {
	t.Parallel()

	row := model.AgentCallLog{
		ID:                  102,
		TenantID:            42,
		UserID:              7,
		ConversationID:      "conv-replay",
		Question:            "有哪些部门",
		ProtocolMode:        string(ProtocolModeLive),
		ProtocolAct:         string(ActWorkflowContinue),
		ProtocolDomain:      string(DomainSubscription),
		ProtocolOperation:   "subscription.list_departments",
		ResponseKind:        string(ResponseSelectOptions),
		WorkflowStateBefore: string(WorkflowCollectScope),
		WorkflowIDBefore:    "wf-before",
	}

	tc := ReplayCaseFromCallLog(row)

	if tc.ActiveWorkflowBefore == nil {
		t.Fatalf("ActiveWorkflowBefore = nil, want restored workflow")
	}
	if tc.ActiveWorkflowBefore.Type != WorkflowSubscriptionStart {
		t.Fatalf("workflow type = %q, want inferred %q for legacy log", tc.ActiveWorkflowBefore.Type, WorkflowSubscriptionStart)
	}
	if got := tc.ActiveWorkflowBefore.MissingFields; len(got) != 1 || got[0] != "scope" {
		t.Fatalf("MissingFields = %v, want [scope]", got)
	}
}

func TestLoadReplayCasesFromDBFiltersProtocolLogs(t *testing.T) {
	db := newReplayTestDB(t)
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	rows := []model.AgentCallLog{
		{TenantID: 1, UserID: 1, ConversationID: "conv-1", Question: "q1", ProtocolMode: string(ProtocolModeLive), ProtocolOperation: "subscription.start", FailureLayer: string(FailureWorkflow), CreatedAt: now},
		{TenantID: 1, UserID: 1, ConversationID: "conv-2", Question: "q2", ProtocolMode: string(ProtocolModeLegacy), ProtocolOperation: "subscription.start", FailureLayer: string(FailureWorkflow), CreatedAt: now},
		{TenantID: 2, UserID: 2, ConversationID: "conv-3", Question: "q3", ProtocolMode: string(ProtocolModeLive), ProtocolOperation: "schedule.query_my_schedule", FailureLayer: "", CreatedAt: now},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed replay logs: %v", err)
	}

	cases, err := LoadReplayCasesFromDB(context.Background(), db, ReplayFilter{
		TenantID:     1,
		FailureLayer: string(FailureWorkflow),
		Operation:    "subscription.start",
		Since:        now.Add(-time.Hour),
		Until:        now.Add(time.Hour),
	}, 10)
	if err != nil {
		t.Fatalf("LoadReplayCasesFromDB() error = %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("case count = %d, want 1: %+v", len(cases), cases)
	}
	if cases[0].ConversationID != "conv-1" {
		t.Fatalf("case = %+v, want protocol_live tenant/operation/failure filtered row", cases[0])
	}
}

func TestLoadReplayCasesFromDBThenRunCaseReplaysProtocolLivePipeline(t *testing.T) {
	db := newReplayTestDB(t)
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	row := model.AgentCallLog{
		TenantID:          42,
		UserID:            7,
		UserRole:          1,
		ConvType:          "2",
		ConversationID:    "conv-replay",
		Question:          "查这个群有没有开启考勤订阅",
		ProtocolMode:      string(ProtocolModeLive),
		ProtocolAct:       string(ActReadQuery),
		ProtocolDomain:    string(DomainSubscription),
		ProtocolOperation: "subscription.query_status",
		ResponseKind:      string(ResponseResult),
		FailureLayer:      "",
		LegacyCalled:      false,
		WorkflowDecision:  string(WorkflowSingleTurn),
		ReplayCaseID:      "replay-loaded-query",
		IntentDraftJSON:   `{"Act":"read_query","Domain":"subscription","Operation":"subscription.query_status","Confidence":0.97}`,
		CreatedAt:         now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed replay log: %v", err)
	}

	cases, err := LoadReplayCasesFromDB(context.Background(), db, ReplayFilter{
		TenantID:  42,
		Operation: "subscription.query_status",
		Since:     now.Add(-time.Hour),
		Until:     now.Add(time.Hour),
	}, 5)
	if err != nil {
		t.Fatalf("LoadReplayCasesFromDB() error = %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("case count = %d, want 1: %+v", len(cases), cases)
	}

	groupSub := &executorFakeGroupSubPort{info: &tools.GroupSubInfo{Subscribed: true, PushEnabled: true}}
	result := NewReplayRunner(ReplayRunnerOptions{GroupSub: groupSub}).RunCase(context.Background(), cases[0])

	if result.Status != ReplayMatched {
		t.Fatalf("Status = %q mismatches=%+v actual=%+v, want %q", result.Status, result.Mismatches, result.Actual, ReplayMatched)
	}
	if groupSub.getCalls != 1 {
		t.Fatalf("GetSubscription calls = %d, want replay to execute protocol_live pipeline", groupSub.getCalls)
	}
	if result.DryRun != true || result.RealWriteAttempted {
		t.Fatalf("dry-run state = dry_run:%t real_write:%t, want dry-run with no real write", result.DryRun, result.RealWriteAttempted)
	}
}

func TestFailureLayerReportSummarizesWhereFailuresCluster(t *testing.T) {
	t.Parallel()

	report := BuildFailureLayerReport([]model.AgentCallLog{
		{ProtocolOperation: "subscription.start", Status: "failed", FailureLayer: string(FailureIntent), CompilerStatus: "timeout", LegacyCalled: true},
		{ProtocolOperation: "subscription.start", Status: "success", FailureLayer: "", CompilerStatus: "ok"},
		{ProtocolOperation: "schedule.query_user_schedule", Status: "failed", FailureLayer: string(FailureEntityAmbiguous), BlockedReason: "entity_ambiguous"},
		{ProtocolOperation: "attendance.query_status", Status: "failed", FailureLayer: string(FailurePrePolicyDenied), PrePolicyResult: "policy_denied"},
	})

	if report.FailureLayerCounts[string(FailureIntent)] != 1 || report.FailureLayerCounts[string(FailureEntityAmbiguous)] != 1 {
		t.Fatalf("FailureLayerCounts = %+v", report.FailureLayerCounts)
	}
	if report.LegacyCalledCount != 1 || report.CompilerTimeoutCount != 1 {
		t.Fatalf("legacy/compiler counts = %+v", report)
	}
	if report.OperationStats["subscription.start"].SuccessRate != 0.5 {
		t.Fatalf("subscription success rate = %+v, want 0.5", report.OperationStats["subscription.start"])
	}
	if report.CommonReasonCounts["entity_ambiguous"] != 1 || report.CommonReasonCounts["policy_denied"] != 1 {
		t.Fatalf("CommonReasonCounts = %+v", report.CommonReasonCounts)
	}
	text := report.Text()
	if !strings.Contains(text, "intent_failed") || !strings.Contains(text, "subscription.start") {
		t.Fatalf("Text() = %q, want failure layer and operation summary", text)
	}
}

func TestReplayRunnerReplaysProtocolLiveCaseAndComparesStableFields(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{info: &tools.GroupSubInfo{Subscribed: true, PushEnabled: true}}
	runner := NewReplayRunner(ReplayRunnerOptions{GroupSub: groupSub})
	result := runner.RunCase(context.Background(), ReplayCase{
		Question:         "查这个群有没有开启考勤订阅",
		TenantID:         42,
		UserID:           7,
		UserRole:         1,
		ConversationID:   "conv-replay",
		ConversationType: "2",
		ProtocolMode:     string(ProtocolModeLive),
		IntentDraft: ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainSubscription,
			Operation:  "subscription.query_status",
			Confidence: 0.97,
		},
		Expected: ReplayExpected{
			Act:              string(ActReadQuery),
			Domain:           string(DomainSubscription),
			Operation:        "subscription.query_status",
			ResponseKind:     string(ResponseResult),
			FailureLayer:     "",
			LegacyCalled:     false,
			WorkflowDecision: string(WorkflowSingleTurn),
		},
	})

	if result.Status != ReplayMatched {
		t.Fatalf("Status = %q mismatches=%+v, want %q", result.Status, result.Mismatches, ReplayMatched)
	}
	if groupSub.getCalls != 1 {
		t.Fatalf("GetSubscription calls = %d, want protocol_live pipeline replay", groupSub.getCalls)
	}
	if result.Actual.Operation != "subscription.query_status" || result.Actual.ResponseKind != string(ResponseResult) {
		t.Fatalf("Actual = %+v, want replayed protocol fields", result.Actual)
	}
}

func TestReplayRunnerReportsStableProtocolFieldMismatches(t *testing.T) {
	t.Parallel()

	runner := NewReplayRunner(ReplayRunnerOptions{
		GroupSub: &executorFakeGroupSubPort{info: &tools.GroupSubInfo{Subscribed: true, PushEnabled: true}},
	})
	result := runner.RunCase(context.Background(), ReplayCase{
		Question:         "查这个群有没有开启考勤订阅",
		TenantID:         42,
		UserID:           7,
		UserRole:         1,
		ConversationID:   "conv-replay",
		ConversationType: "2",
		ProtocolMode:     string(ProtocolModeLive),
		IntentDraft: ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainSubscription,
			Operation:  "subscription.query_status",
			Confidence: 0.97,
		},
		Expected: ReplayExpected{
			Act:           string(ActWriteRequest),
			Domain:        string(DomainAttendance),
			Operation:     "attendance.query_status",
			ResponseKind:  string(ResponseClarify),
			BlockedReason: "missing_scope",
			FailureLayer:  string(FailureIntent),
			LegacyCalled:  false,
		},
	})

	if result.Status != ReplayMismatched {
		t.Fatalf("Status = %q, want %q", result.Status, ReplayMismatched)
	}
	for _, field := range []string{"act", "domain", "operation", "response_kind", "blocked_reason", "failure_layer"} {
		if !replayMismatchContains(result.Mismatches, field) {
			t.Fatalf("mismatches = %+v, want field %q", result.Mismatches, field)
		}
	}
}

func TestReplayRunnerComparesWorkflowDecisionEvenWhenExpectedEmpty(t *testing.T) {
	t.Parallel()

	runner := NewReplayRunner(ReplayRunnerOptions{
		GroupSub: &executorFakeGroupSubPort{info: &tools.GroupSubInfo{Subscribed: true, PushEnabled: true}},
	})
	result := runner.RunCase(context.Background(), ReplayCase{
		Question:         "查这个群有没有开启考勤订阅",
		TenantID:         42,
		UserID:           7,
		UserRole:         1,
		ConversationID:   "conv-replay",
		ConversationType: "2",
		ProtocolMode:     string(ProtocolModeLive),
		IntentDraft: ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainSubscription,
			Operation:  "subscription.query_status",
			Confidence: 0.97,
		},
		Expected: ReplayExpected{
			Act:              string(ActReadQuery),
			Domain:           string(DomainSubscription),
			Operation:        "subscription.query_status",
			ResponseKind:     string(ResponseResult),
			FailureLayer:     "",
			LegacyCalled:     false,
			WorkflowDecision: "",
		},
	})

	if result.Status != ReplayMismatched {
		t.Fatalf("Status = %q mismatches=%+v actual=%+v, want %q when workflow_decision expectation is missing", result.Status, result.Mismatches, result.Actual, ReplayMismatched)
	}
	if !replayMismatchContains(result.Mismatches, "workflow_decision") {
		t.Fatalf("mismatches = %+v, want workflow_decision mismatch", result.Mismatches)
	}
}

func TestReplayRunnerFailsWhenLegacyCalledTrue(t *testing.T) {
	t.Parallel()

	runner := NewReplayRunner(ReplayRunnerOptions{})
	result := runner.RunCase(context.Background(), ReplayCase{
		ProtocolMode: string(ProtocolModeLive),
		Expected: ReplayExpected{
			Operation:    "subscription.query_status",
			LegacyCalled: true,
		},
	})

	if result.Status != ReplayMismatched {
		t.Fatalf("Status = %q, want %q", result.Status, ReplayMismatched)
	}
	if !replayMismatchContains(result.Mismatches, "legacy_called") {
		t.Fatalf("mismatches = %+v, want legacy_called failure", result.Mismatches)
	}
}

func TestReplayRunnerDryRunWriteRerunsPipelineWithoutCallingWritePort(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	runner := NewReplayRunner(ReplayRunnerOptions{GroupSub: groupSub})
	result := runner.RunCase(context.Background(), ReplayCase{
		Question:         "开启本群全部人员考勤订阅",
		TenantID:         42,
		UserID:           7,
		UserRole:         1,
		ConversationID:   "conv-replay",
		ConversationType: "2",
		ProtocolMode:     string(ProtocolModeLive),
		IntentDraft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.97,
			Slots: map[string]SlotDraft{
				"scope": {Field: "scope", Raw: "全部人员"},
			},
		},
		Expected: ReplayExpected{
			Act:              string(ActWriteRequest),
			Domain:           string(DomainSubscription),
			Operation:        "subscription.start",
			ResponseKind:     string(ResponseResult),
			FailureLayer:     "",
			LegacyCalled:     false,
			WorkflowDecision: string(WorkflowCompletedDecision),
		},
	})

	if !result.DryRun {
		t.Fatalf("DryRun = false, want true by default")
	}
	if result.RealWriteAttempted {
		t.Fatalf("RealWriteAttempted = true, replay must not execute real writes by default")
	}
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("Subscribe calls = %d, want dry-run replay to avoid real write port", groupSub.subscribeCalls)
	}
	if result.Status != ReplayMatched {
		t.Fatalf("Status = %q mismatches=%+v, want %q", result.Status, result.Mismatches, ReplayMatched)
	}
}

func TestReplayRunnerDryRunSubscriptionCancelDoesNotCallWritePort(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{info: &tools.GroupSubInfo{Subscribed: true, PushEnabled: true}}
	runner := NewReplayRunner(ReplayRunnerOptions{GroupSub: groupSub})
	result := runner.RunCase(context.Background(), ReplayCase{
		Question:         "关闭本群考勤订阅",
		TenantID:         42,
		UserID:           7,
		UserRole:         1,
		ConversationID:   "conv-replay",
		ConversationType: "2",
		ProtocolMode:     string(ProtocolModeLive),
		IntentDraft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.cancel",
			Confidence: 0.97,
		},
		Expected: ReplayExpected{
			Act:              string(ActWriteRequest),
			Domain:           string(DomainSubscription),
			Operation:        "subscription.cancel",
			ResponseKind:     string(ResponseResult),
			FailureLayer:     "",
			LegacyCalled:     false,
			WorkflowDecision: string(WorkflowSingleTurn),
		},
	})

	if !result.DryRun {
		t.Fatalf("DryRun = false, want true by default")
	}
	if result.RealWriteAttempted {
		t.Fatalf("RealWriteAttempted = true, replay must not execute real writes by default")
	}
	if groupSub.unsubscribeCalls != 0 {
		t.Fatalf("Unsubscribe calls = %d, want dry-run replay to avoid real write port", groupSub.unsubscribeCalls)
	}
	if result.Status != ReplayMatched {
		t.Fatalf("Status = %q mismatches=%+v actual=%+v, want %q", result.Status, result.Mismatches, result.Actual, ReplayMatched)
	}
}

func TestReplayRunnerReplaysWorkflowContinueFromCallLog(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	row := model.AgentCallLog{
		ID:                         101,
		TenantID:                   42,
		UserID:                     7,
		UserRole:                   1,
		ConvType:                   "2",
		ConversationID:             "conv-replay",
		Question:                   "全部人员",
		ProtocolMode:               string(ProtocolModeLive),
		ProtocolAct:                string(ActWorkflowContinue),
		ProtocolDomain:             string(DomainSubscription),
		ProtocolOperation:          "subscription.start",
		ResponseKind:               string(ResponseResult),
		WorkflowDecision:           string(WorkflowCompletedDecision),
		WorkflowTypeBefore:         string(WorkflowSubscriptionStart),
		WorkflowStateBefore:        string(WorkflowCollectScope),
		WorkflowIDBefore:           "wf-before",
		WorkflowSnapshotBeforeJSON: `{"id":"wf-before","type":"subscription.start","state":"collect_scope","tenant_id":42,"actor_user_id":7,"conversation_id":"conv-replay","missing_fields":["scope"]}`,
		IntentDraftJSON:            `{"Act":"workflow_continue","Domain":"subscription","Operation":"subscription.start","Confidence":0.97}`,
	}
	tc := ReplayCaseFromCallLog(row)
	runner := NewReplayRunner(ReplayRunnerOptions{GroupSub: groupSub})

	result := runner.RunCase(context.Background(), tc)

	if result.Status != ReplayMatched {
		t.Fatalf("Status = %q mismatches=%+v actual=%+v, want %q", result.Status, result.Mismatches, result.Actual, ReplayMatched)
	}
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("Subscribe calls = %d, want dry-run replay to avoid real write port", groupSub.subscribeCalls)
	}
	if result.Actual.WorkflowDecision != string(WorkflowCompletedDecision) {
		t.Fatalf("WorkflowDecision = %q, want %q", result.Actual.WorkflowDecision, WorkflowCompletedDecision)
	}
}

func TestReplayRunnerReplaysWorkflowCancelFromCase(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	runner := NewReplayRunner(ReplayRunnerOptions{Clock: func() time.Time { return now }})
	result := runner.RunCase(context.Background(), ReplayCase{
		Question:             "取消",
		TenantID:             42,
		UserID:               7,
		UserRole:             0,
		ConversationID:       "conv-replay",
		ConversationType:     "2",
		ProtocolMode:         string(ProtocolModeLive),
		ActiveWorkflowBefore: replayTestSubscriptionWorkflow(now.Add(time.Minute)),
		IntentDraft: ProtocolDraft{
			Act:        ActWorkflowCancel,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.97,
		},
		Expected: ReplayExpected{
			Act:              string(ActWorkflowCancel),
			Domain:           string(DomainSubscription),
			Operation:        "subscription.start",
			ResponseKind:     string(ResponseResult),
			FailureLayer:     "",
			LegacyCalled:     false,
			WorkflowDecision: string(WorkflowCanceled),
		},
	})

	if result.Status != ReplayMatched {
		t.Fatalf("Status = %q mismatches=%+v actual=%+v, want %q", result.Status, result.Mismatches, result.Actual, ReplayMatched)
	}
}

func TestReplayRunnerReplaysWorkflowInterruptFromCase(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	runner := NewReplayRunner(ReplayRunnerOptions{Clock: func() time.Time { return now }})
	result := runner.RunCase(context.Background(), ReplayCase{
		Question:             "手工签到能做什么",
		TenantID:             42,
		UserID:               7,
		UserRole:             0,
		ConversationID:       "conv-replay",
		ConversationType:     "2",
		ProtocolMode:         string(ProtocolModeLive),
		ActiveWorkflowBefore: replayTestSubscriptionWorkflow(now.Add(time.Minute)),
		IntentDraft: ProtocolDraft{
			Act:        ActCapabilityQuestion,
			Domain:     DomainManualSign,
			Operation:  "manual_sign.describe_capability",
			Confidence: 0.97,
		},
		Expected: ReplayExpected{
			Act:              string(ActCapabilityQuestion),
			Domain:           string(DomainManualSign),
			Operation:        "manual_sign.describe_capability",
			ResponseKind:     string(ResponseAnswer),
			FailureLayer:     "",
			LegacyCalled:     false,
			WorkflowDecision: string(WorkflowInterrupted),
		},
	})

	if result.Status != ReplayMatched {
		t.Fatalf("Status = %q mismatches=%+v actual=%+v, want %q", result.Status, result.Mismatches, result.Actual, ReplayMatched)
	}
}

func TestReplayRunnerReplaysWorkflowExpireFromCase(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	runner := NewReplayRunner(ReplayRunnerOptions{Clock: func() time.Time { return now }})
	result := runner.RunCase(context.Background(), ReplayCase{
		Question:             "全部人员",
		TenantID:             42,
		UserID:               7,
		UserRole:             1,
		ConversationID:       "conv-replay",
		ConversationType:     "2",
		ProtocolMode:         string(ProtocolModeLive),
		ActiveWorkflowBefore: replayTestSubscriptionWorkflow(now.Add(-time.Second)),
		IntentDraft: ProtocolDraft{
			Act:        ActWorkflowContinue,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.97,
		},
		Expected: ReplayExpected{
			Act:              string(ActWorkflowContinue),
			Domain:           string(DomainSubscription),
			Operation:        "subscription.start",
			ResponseKind:     string(ResponseClarify),
			BlockedReason:    "unknown_intent",
			FailureLayer:     string(FailureIntent),
			LegacyCalled:     false,
			WorkflowDecision: string(WorkflowSingleTurn),
		},
	})

	if result.Status != ReplayMatched {
		t.Fatalf("Status = %q mismatches=%+v actual=%+v, want %q", result.Status, result.Mismatches, result.Actual, ReplayMatched)
	}
}

func replayMismatchContains(mismatches []ReplayFieldMismatch, field string) bool {
	for _, mismatch := range mismatches {
		if mismatch.Field == field {
			return true
		}
	}
	return false
}

func replayTestSubscriptionWorkflow(expiresAt time.Time) *WorkflowSnapshot {
	return &WorkflowSnapshot{
		ID:             "wf-before",
		Type:           WorkflowSubscriptionStart,
		State:          WorkflowCollectScope,
		TenantID:       42,
		ActorUserID:    7,
		ConversationID: "conv-replay",
		MissingFields:  []string{"scope"},
		MissingSlots:   []string{"scope"},
		ExpiresAt:      expiresAt,
	}
}

func newReplayTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.AgentCallLog{}); err != nil {
		t.Fatalf("migrate AgentCallLog: %v", err)
	}
	return db
}

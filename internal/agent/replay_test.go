package agent

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestReplayRunnerDoesNotExecuteWritesByDefault(t *testing.T) {
	t.Parallel()

	runner := NewReplayRunner(ReplayRunnerOptions{})
	result := runner.RunCase(context.Background(), ReplayCase{
		Question: "开启本群全部人员考勤订阅",
		Expected: ReplayExpected{
			Operation: "subscription.start",
		},
	})

	if !result.DryRun {
		t.Fatalf("DryRun = false, want true by default")
	}
	if result.RealWriteAttempted {
		t.Fatalf("RealWriteAttempted = true, replay must not execute real writes by default")
	}
	if result.Status != ReplaySkippedWrite {
		t.Fatalf("Status = %q, want %q", result.Status, ReplaySkippedWrite)
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

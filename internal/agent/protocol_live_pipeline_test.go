package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"schedule_server/internal/agent/tools"
)

func TestProtocolLivePipelineDoesNotSwitchOnOperation(t *testing.T) {
	t.Parallel()

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	pipelinePath := filepath.Join(filepath.Dir(testFile), "protocol_live_pipeline.go")
	source, err := os.ReadFile(pipelinePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", pipelinePath, err)
	}
	operationSwitch := regexp.MustCompile(`switch\s+(draft\.)?Operation|switch\s+.*Operation`)
	if operationSwitch.Match(source) {
		t.Fatalf("protocol_live_pipeline.go must dispatch operations through OperationCatalog, not switch on operation names")
	}
}

func TestProtocolLivePipelineExplicitNewRequestInterruptsWorkflow(t *testing.T) {
	t.Parallel()

	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActCapabilityQuestion,
			Domain:     DomainManualSign,
			Operation:  "manual_sign.describe_capability",
			Confidence: 0.94,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{}),
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "可以补签吗",
		User:    executorUserContext(),
		ActiveWorkflow: &WorkflowSnapshot{
			ID:           "wf-sub",
			Type:         WorkflowSubscriptionStart,
			State:        WorkflowCollectDepartments,
			MissingSlots: []string{"dept_ids"},
		},
	})

	if outcome.WorkflowDecision != WorkflowInterrupted {
		t.Fatalf("WorkflowDecision = %q, want %q", outcome.WorkflowDecision, WorkflowInterrupted)
	}
	if outcome.Response.Kind != ResponseAnswer {
		t.Fatalf("Response = %+v, want answer", outcome.Response)
	}
	if !outcome.ClearWorkflow {
		t.Fatalf("ClearWorkflow = false, want true")
	}
}

func TestProtocolLivePipelineExecutesAttendanceSlotQuery(t *testing.T) {
	t.Parallel()

	attendance := &executorFakeAttendancePort{detailResp: &tools.AttendanceResult{
		Date:         "2026-06-06",
		Week:         10,
		Section:      2,
		ShouldAttend: 3,
		OnTimeCount:  2,
		LateCount:    1,
	}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainAttendance,
			Operation:  "attendance.query_status",
			Confidence: 0.92,
			Slots: map[string]SlotDraft{
				"date":    {Field: "date", Raw: "2026-06-06"},
				"section": {Field: "section", Raw: "第二节"},
			},
		}},
		Executor: newOperationExecutor(operationExecutorDeps{
			Attendance: attendance,
			Semester:   &executorFakeSemesterPort{week: 10},
		}),
		Semester: &executorFakeSemesterPort{week: 10},
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "查询 2026-06-06 第二节考勤状态",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseResult {
		t.Fatalf("Response = %+v, want result", outcome.Response)
	}
	if attendance.detailCalls != 1 {
		t.Fatalf("detailCalls = %d, want 1", attendance.detailCalls)
	}
	if attendance.lastQuery.Date != "2026-06-06" || attendance.lastQuery.Section != 2 || attendance.lastQuery.Week != 10 {
		t.Fatalf("lastQuery = %+v", attendance.lastQuery)
	}
	if outcome.ExecutionMetrics.ExecutorName != "operation_executor" || outcome.ExecutionMetrics.ToolPool != "operation" {
		t.Fatalf("ExecutionMetrics = %+v, want operation executor", outcome.ExecutionMetrics)
	}
	if outcome.ResolvedSlots["date"] != "2026-06-06" || outcome.ResolvedSlots["section"] != 2 || outcome.ResolvedSlots["week"] != 10 {
		t.Fatalf("ResolvedSlots = %#v, want date/section/week", outcome.ResolvedSlots)
	}
}

func TestProtocolLivePipelineIgnoresTrustedIDSlotFromDraft(t *testing.T) {
	t.Parallel()

	attendance := &executorFakeAttendancePort{detailResp: &tools.AttendanceResult{Date: "2026-06-06", Week: 10, Section: 2}}
	user := &pipelineSearchUserPort{users: []tools.UserInfo{{ID: 42, Name: "张三"}}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainAttendance,
			Operation:  "attendance.query_status",
			Confidence: 0.92,
			Slots: map[string]SlotDraft{
				"date":        {Field: "date", Raw: "2026-06-06"},
				"user_id":     {Field: "user_id", Raw: "张三"},
				"query_shape": {Field: "query_shape", Raw: "user_day_status"},
			},
		}},
		Executor: newOperationExecutor(operationExecutorDeps{
			Attendance: attendance,
			Semester:   &executorFakeSemesterPort{week: 10},
		}),
		User: user,
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "查张三今天考勤",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseClarify {
		t.Fatalf("Response = %+v, want clarify because user_id slot is not raw user input", outcome.Response)
	}
	if user.searchCalls != 0 {
		t.Fatalf("SearchByName calls = %d, want 0 for trusted ID slot", user.searchCalls)
	}
	if attendance.detailCalls != 0 {
		t.Fatalf("detailCalls = %d, want 0 without trusted user resolver output", attendance.detailCalls)
	}
}

func TestProtocolLivePipelineSubscriptionWorkflowClarifiesAndExecutesAllScope(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	startPipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.96,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{GroupSub: groupSub}),
	})

	start := startPipeline.Handle(context.Background(), protocolLiveInput{
		Message: "开启本群考勤订阅",
		User:    executorUserContext(),
	})
	if start.Response.Kind != ResponseClarify {
		t.Fatalf("start response = %+v, want clarify", start.Response)
	}
	if start.WorkflowAfter == nil || start.WorkflowAfter.State != WorkflowCollectScope {
		t.Fatalf("WorkflowAfter = %+v, want collect_scope", start.WorkflowAfter)
	}
	if start.BlockedReason != "missing_scope" {
		t.Fatalf("BlockedReason = %q, want missing_scope", start.BlockedReason)
	}

	continuePipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWorkflowContinue,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.95,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{GroupSub: groupSub}),
	})
	done := continuePipeline.Handle(context.Background(), protocolLiveInput{
		Message:        "全部人员",
		User:           executorUserContext(),
		ActiveWorkflow: start.WorkflowAfter,
	})

	if done.Response.Kind != ResponseResult {
		t.Fatalf("done response = %+v, want result", done.Response)
	}
	if done.WorkflowDecision != WorkflowCompletedDecision || !done.ClearWorkflow {
		t.Fatalf("workflow decision=%q clear=%v, want completed and clear", done.WorkflowDecision, done.ClearWorkflow)
	}
	if groupSub.subscribeCalls != 1 {
		t.Fatalf("Subscribe calls = %d, want 1", groupSub.subscribeCalls)
	}
}

func TestProtocolLivePipelineSubscriptionStartExecutesCompleteAllScopeFirstTurn(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.96,
			Slots: map[string]SlotDraft{
				"scope": {Field: "scope", Raw: "全部人员"},
			},
		}},
		Executor: newOperationExecutor(operationExecutorDeps{GroupSub: groupSub}),
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "开启本群全部人员考勤订阅",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseResult {
		t.Fatalf("Response = %+v, want result", outcome.Response)
	}
	if outcome.WorkflowAfter != nil || !outcome.ClearWorkflow {
		t.Fatalf("WorkflowAfter=%+v ClearWorkflow=%v, want no retained workflow", outcome.WorkflowAfter, outcome.ClearWorkflow)
	}
	if groupSub.subscribeCalls != 1 || len(groupSub.lastDeptIDs) != 0 {
		t.Fatalf("Subscribe calls=%d deptIDs=%v, want all scope execution", groupSub.subscribeCalls, groupSub.lastDeptIDs)
	}
}

func TestProtocolLivePipelineListsDepartmentsWithoutActiveWorkflow(t *testing.T) {
	t.Parallel()

	dept := executorFakeDeptPort{depts: []tools.DeptItem{{TenantID: 42, DeptID: 101, Name: "信工24级"}}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainSubscription,
			Operation:  "subscription.list_departments",
			Confidence: 0.9,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{Dept: dept}),
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "都有哪些部门可以订阅",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseSelectOptions {
		t.Fatalf("Response = %+v, want select options", outcome.Response)
	}
	if !strings.Contains(renderProtocolResponse(outcome.Response), "信工24级") {
		t.Fatalf("reply = %q, want department option", renderProtocolResponse(outcome.Response))
	}
	if outcome.ExecutionMetrics.ExecutorName != "operation_executor" {
		t.Fatalf("ExecutorName = %q, want operation_executor", outcome.ExecutionMetrics.ExecutorName)
	}
	if outcome.CandidateCount != 1 {
		t.Fatalf("CandidateCount = %d, want 1", outcome.CandidateCount)
	}
}

func TestProtocolLivePipelineListsDepartmentsInDMWithoutGroupGate(t *testing.T) {
	t.Parallel()

	dept := executorFakeDeptPort{depts: []tools.DeptItem{{TenantID: 42, DeptID: 101, Name: "信工24级"}}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainSubscription,
			Operation:  "subscription.list_departments",
			Confidence: 0.9,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{Dept: dept}),
	})
	uctx := executorUserContext()
	uctx.ConversationType = "1"
	uctx.ConversationID = "single-conv"

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "都有哪些部门可以订阅",
		User:    uctx,
	})

	if outcome.Response.Kind != ResponseSelectOptions {
		t.Fatalf("Response = %+v, want select options", outcome.Response)
	}
	if !strings.Contains(renderProtocolResponse(outcome.Response), "信工24级") {
		t.Fatalf("reply = %q, want department option", renderProtocolResponse(outcome.Response))
	}
}

func TestProtocolLivePipelineAttendanceCurrentSectionUsesSchedulePeriods(t *testing.T) {
	t.Parallel()

	attendance := &executorFakeAttendancePort{detailResp: &tools.AttendanceResult{
		Date:         "2026-06-06",
		Week:         10,
		Section:      1,
		ShouldAttend: 3,
		OnTimeCount:  2,
	}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainAttendance,
			Operation:  "attendance.query_status",
			Confidence: 0.92,
			Slots: map[string]SlotDraft{
				"date":    {Field: "date", Raw: "2026-06-06"},
				"section": {Field: "section", Raw: "本节"},
			},
		}},
		Executor:       newOperationExecutor(operationExecutorDeps{Attendance: attendance, Semester: &executorFakeSemesterPort{week: 10}}),
		Semester:       &executorFakeSemesterPort{week: 10},
		SchedulePeriod: pipelineFakeSchedulePeriodPort{periods: []tools.PeriodInfo{{Name: "全天", Start: "00:00", End: "23:59"}}},
		Clock: func() time.Time {
			return time.Date(2026, 6, 6, 10, 0, 0, 0, time.Local)
		},
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "查询本节考勤状态",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseResult {
		t.Fatalf("Response = %+v, want result", outcome.Response)
	}
	if attendance.detailCalls != 1 || attendance.lastQuery.Section != 1 {
		t.Fatalf("detailCalls=%d section=%d, want current section 1", attendance.detailCalls, attendance.lastQuery.Section)
	}
}

func TestProtocolLivePipelineWithoutCompilerFailsClosed(t *testing.T) {
	t.Parallel()

	attendance := &executorFakeAttendancePort{detailResp: &tools.AttendanceResult{Date: "2026-06-06", Week: 10, Section: 2}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Executor: newOperationExecutor(operationExecutorDeps{Attendance: attendance, Semester: &executorFakeSemesterPort{week: 10}}),
		Semester: &executorFakeSemesterPort{week: 10},
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "查询今天第二节考勤状态",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseClarify {
		t.Fatalf("Response = %+v, want clarify", outcome.Response)
	}
	if outcome.Draft.Act != ActUnknown {
		t.Fatalf("Draft = %+v, want unknown without compiler", outcome.Draft)
	}
	if attendance.detailCalls != 0 {
		t.Fatalf("detailCalls = %d, want 0 without compiler", attendance.detailCalls)
	}
}

func TestProtocolLivePipelineCompilerTimeoutFailsClosed(t *testing.T) {
	t.Parallel()

	attendance := &executorFakeAttendancePort{detailResp: &tools.AttendanceResult{Date: "2026-06-06", Week: 10, Section: 2}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{err: context.DeadlineExceeded},
		Executor: newOperationExecutor(operationExecutorDeps{Attendance: attendance, Semester: &executorFakeSemesterPort{week: 10}}),
		Semester: &executorFakeSemesterPort{week: 10},
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "查询今天第二节考勤状态",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseClarify {
		t.Fatalf("Response = %+v, want clarify", outcome.Response)
	}
	if outcome.Draft.Act != ActUnknown || outcome.Draft.Reason != "intent_timeout" {
		t.Fatalf("Draft = %+v, want timeout unknown draft", outcome.Draft)
	}
	if outcome.BlockedReason != "intent_timeout" {
		t.Fatalf("BlockedReason = %q, want intent_timeout", outcome.BlockedReason)
	}
	if attendance.detailCalls != 0 {
		t.Fatalf("detailCalls = %d, want 0 after compiler timeout", attendance.detailCalls)
	}
}

func TestProtocolLivePipelineRoleDeniedRequestInterruptsActiveWorkflow(t *testing.T) {
	t.Parallel()

	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.96,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{}),
	})
	uctx := executorUserContext()
	uctx.UserRole = 0

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "开启本群考勤订阅",
		User:    uctx,
		ActiveWorkflow: &WorkflowSnapshot{
			ID:           "wf-sub",
			Type:         WorkflowSubscriptionStart,
			State:        WorkflowCollectDepartments,
			MissingSlots: []string{"dept_ids"},
		},
	})

	if outcome.Response.Kind != ResponseRefuse {
		t.Fatalf("Response = %+v, want refuse", outcome.Response)
	}
	if outcome.WorkflowDecision != WorkflowInterrupted || !outcome.ClearWorkflow {
		t.Fatalf("WorkflowDecision=%q ClearWorkflow=%v, want interrupted and clear", outcome.WorkflowDecision, outcome.ClearWorkflow)
	}
	if outcome.Validation.ValidationCode != "role_denied" {
		t.Fatalf("ValidationCode = %q, want role_denied", outcome.Validation.ValidationCode)
	}
	if outcome.BlockedReason != "role_denied" {
		t.Fatalf("BlockedReason = %q, want role_denied", outcome.BlockedReason)
	}
}

func TestProtocolLivePipelineResourcePolicyDenialStopsBeforeExecutor(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.cancel",
			Confidence: 0.96,
		}},
		Executor:       newOperationExecutor(operationExecutorDeps{GroupSub: groupSub}),
		ResourcePolicy: pipelineDenyResourcePolicy{reason: "subscription_conversation_mismatch"},
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "取消本群考勤订阅",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseRefuse {
		t.Fatalf("Response = %+v, want refuse", outcome.Response)
	}
	if outcome.BlockedReason != "subscription_conversation_mismatch" {
		t.Fatalf("BlockedReason = %q, want subscription_conversation_mismatch", outcome.BlockedReason)
	}
	if groupSub.unsubscribeCalls != 0 {
		t.Fatalf("Unsubscribe calls = %d, want 0 when resource policy denies", groupSub.unsubscribeCalls)
	}
}

func TestProtocolLivePipelineCarriesWriteGuardIdempotencyKey(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{info: &tools.GroupSubInfo{Subscribed: true}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.cancel",
			Confidence: 0.96,
		}},
		Executor:   newOperationExecutor(operationExecutorDeps{GroupSub: groupSub}),
		WriteGuard: pipelineAllowWriteGuard{key: "idem-subscription-cancel"},
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "取消本群考勤订阅",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseResult {
		t.Fatalf("Response = %+v, want result", outcome.Response)
	}
	if outcome.IdempotencyKey != "idem-subscription-cancel" {
		t.Fatalf("IdempotencyKey = %q, want fake write guard key", outcome.IdempotencyKey)
	}
	if groupSub.unsubscribeCalls != 1 {
		t.Fatalf("Unsubscribe calls = %d, want 1", groupSub.unsubscribeCalls)
	}
}

func TestProtocolLivePipelineUnsupportedExplicitWriteInterruptsActiveWorkflow(t *testing.T) {
	t.Parallel()

	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainManualSign,
			Operation:  "manual_sign.create",
			Confidence: 0.96,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{}),
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "帮张三补签今天第二节",
		User:    executorUserContext(),
		ActiveWorkflow: &WorkflowSnapshot{
			ID:           "wf-sub",
			Type:         WorkflowSubscriptionStart,
			State:        WorkflowCollectScope,
			MissingSlots: []string{"scope"},
		},
	})

	if outcome.Response.Kind != ResponseRefuse {
		t.Fatalf("Response = %+v, want refuse", outcome.Response)
	}
	if outcome.WorkflowDecision != WorkflowInterrupted || !outcome.ClearWorkflow {
		t.Fatalf("WorkflowDecision=%q ClearWorkflow=%v, want interrupted and clear", outcome.WorkflowDecision, outcome.ClearWorkflow)
	}
}

func TestProtocolLivePipelineDepartmentListChoosesDepartmentScopeDuringScopeCollection(t *testing.T) {
	t.Parallel()

	dept := executorFakeDeptPort{depts: []tools.DeptItem{{TenantID: 42, DeptID: 101, Name: "信工24级"}}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWorkflowContinue,
			Domain:     DomainSubscription,
			Operation:  "subscription.list_departments",
			Confidence: 0.9,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{Dept: dept}),
	})
	workflow := &WorkflowSnapshot{
		ID:           "wf-sub",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectScope,
		MissingSlots: []string{"scope"},
	}

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message:        "都有哪些部门",
		User:           executorUserContext(),
		ActiveWorkflow: workflow,
	})

	if outcome.Response.Kind != ResponseSelectOptions {
		t.Fatalf("Response = %+v, want select options", outcome.Response)
	}
	if outcome.WorkflowDecision != WorkflowContinueDecision {
		t.Fatalf("WorkflowDecision = %q, want %q", outcome.WorkflowDecision, WorkflowContinueDecision)
	}
	if outcome.WorkflowAfter == nil || outcome.WorkflowAfter.ID != "wf-sub" || outcome.WorkflowAfter.State != WorkflowCollectDepartments {
		t.Fatalf("WorkflowAfter = %+v, want collect_departments", outcome.WorkflowAfter)
	}
	if len(outcome.WorkflowAfter.MissingSlots) != 1 || outcome.WorkflowAfter.MissingSlots[0] != "dept_names" {
		t.Fatalf("MissingSlots = %v, want [dept_names]", outcome.WorkflowAfter.MissingSlots)
	}
	if outcome.WorkflowAfter.Trusted.Scope != "department" {
		t.Fatalf("Trusted.Scope = %q, want department", outcome.WorkflowAfter.Trusted.Scope)
	}
	if outcome.ResolvedSlots["scope"] != "department" {
		t.Fatalf("ResolvedSlots = %#v, want department scope", outcome.ResolvedSlots)
	}
	candidates := outcome.WorkflowAfter.Candidates["dept_ids"]
	if len(candidates) != 1 || candidates[0].ID != "101" || candidates[0].Label != "信工24级" {
		t.Fatalf("WorkflowAfter candidates = %+v, want persisted department candidate", candidates)
	}
}

func TestProtocolLivePipelineDepartmentNameChoosesDepartmentScopeDuringScopeCollection(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	dept := executorFakeDeptPort{depts: []tools.DeptItem{{TenantID: 42, DeptID: 125, Name: "信工25级"}}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWorkflowContinue,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.9,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{GroupSub: groupSub}),
		Dept:     dept,
	})
	workflow := &WorkflowSnapshot{
		ID:           "wf-sub",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectScope,
		MissingSlots: []string{"scope"},
	}

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message:        "信工25级",
		User:           executorUserContext(),
		ActiveWorkflow: workflow,
	})

	if outcome.Response.Kind != ResponseResult {
		t.Fatalf("Response = %+v, want result", outcome.Response)
	}
	if outcome.WorkflowDecision != WorkflowCompletedDecision || !outcome.ClearWorkflow {
		t.Fatalf("WorkflowDecision=%q ClearWorkflow=%v, want completed and clear", outcome.WorkflowDecision, outcome.ClearWorkflow)
	}
	if groupSub.subscribeCalls != 1 {
		t.Fatalf("Subscribe calls = %d, want 1", groupSub.subscribeCalls)
	}
	if len(groupSub.lastDeptIDs) != 1 || groupSub.lastDeptIDs[0] != 125 {
		t.Fatalf("lastDeptIDs = %v, want [125]", groupSub.lastDeptIDs)
	}
	if outcome.ResolvedSlots["scope"] != "department" {
		t.Fatalf("ResolvedSlots = %#v, want department scope", outcome.ResolvedSlots)
	}
}

func TestProtocolLivePipelineDepartmentAmbiguitySelectsCandidatesForWrite(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	dept := executorFakeDeptPort{depts: []tools.DeptItem{
		{TenantID: 42, DeptID: 101, Name: "信工24级"},
		{TenantID: 42, DeptID: 125, Name: "信工25级"},
	}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWorkflowContinue,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.9,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{GroupSub: groupSub}),
		Dept:     dept,
	})
	workflow := &WorkflowSnapshot{
		ID:             "wf-sub",
		TenantID:       42,
		ActorUserID:    7,
		ConversationID: "conv-1",
		Type:           WorkflowSubscriptionStart,
		State:          WorkflowCollectDepartments,
		MissingSlots:   []string{"dept_names"},
		Trusted: trustedEntities{
			Scope: "department",
		},
	}

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message:        "信工",
		User:           executorUserContext(),
		ActiveWorkflow: workflow,
	})

	if outcome.Response.Kind != ResponseSelectOptions {
		t.Fatalf("Response = %+v, want select options for ambiguous write target", outcome.Response)
	}
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("Subscribe calls = %d, want 0 before user chooses candidate", groupSub.subscribeCalls)
	}
	if outcome.WorkflowAfter == nil || outcome.WorkflowAfter.State != WorkflowCollectDepartments {
		t.Fatalf("WorkflowAfter = %+v, want workflow still collecting departments", outcome.WorkflowAfter)
	}
	candidates := outcome.WorkflowAfter.Candidates["dept_ids"]
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want two tenant-bound choices", candidates)
	}
	for _, candidate := range candidates {
		if candidate.TenantID != 42 {
			t.Fatalf("candidate = %+v, want tenant 42 only", candidate)
		}
	}
	if outcome.CandidateCount != 2 {
		t.Fatalf("CandidateCount = %d, want 2", outcome.CandidateCount)
	}
}

func TestProtocolLivePipelineDepartmentOrdinalUsesCurrentWorkflowCandidates(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWorkflowContinue,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.9,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{GroupSub: groupSub}),
		Dept: executorFakeDeptPort{depts: []tools.DeptItem{
			{TenantID: 42, DeptID: 999, Name: "当前库里的第二项"},
		}},
	})
	workflow := &WorkflowSnapshot{
		ID:           "wf-sub",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectDepartments,
		MissingSlots: []string{"dept_names"},
		Trusted: trustedEntities{
			Scope: "department",
		},
		Candidates: map[string][]Candidate{
			"dept_ids": {
				{ID: "101", Label: "信工24级", Value: int64(101), TenantID: 42},
				{ID: "125", Label: "信工25级", Value: int64(125), TenantID: 42},
			},
		},
	}

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message:        "第 2 个",
		User:           executorUserContext(),
		ActiveWorkflow: workflow,
	})

	if outcome.Response.Kind != ResponseResult {
		t.Fatalf("Response = %+v, want result", outcome.Response)
	}
	if groupSub.subscribeCalls != 1 {
		t.Fatalf("Subscribe calls = %d, want 1", groupSub.subscribeCalls)
	}
	if len(groupSub.lastDeptIDs) != 1 || groupSub.lastDeptIDs[0] != 125 {
		t.Fatalf("lastDeptIDs = %v, want [125] from workflow candidates", groupSub.lastDeptIDs)
	}
}

func TestProtocolLivePipelineDepartmentOrdinalRejectsCrossTenantWorkflowCandidate(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActWorkflowContinue,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.9,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{GroupSub: groupSub}),
	})
	workflow := &WorkflowSnapshot{
		ID:             "wf-sub",
		TenantID:       42,
		ActorUserID:    7,
		ConversationID: "conv-1",
		Type:           WorkflowSubscriptionStart,
		State:          WorkflowCollectDepartments,
		MissingSlots:   []string{"dept_names"},
		Trusted: trustedEntities{
			Scope: "department",
		},
		Candidates: map[string][]Candidate{
			"dept_ids": {
				{ID: "125", Label: "其他租户部门", Value: int64(125), TenantID: 99},
			},
		},
	}

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message:        "第 1 个",
		User:           executorUserContext(),
		ActiveWorkflow: workflow,
	})

	if outcome.Response.Kind == ResponseResult {
		t.Fatalf("Response = %+v, want no execution for cross-tenant candidate", outcome.Response)
	}
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("Subscribe calls = %d, want 0 for cross-tenant candidate", groupSub.subscribeCalls)
	}
}

func TestProtocolLivePipelineUnknownIntentUsesShortBlockedReason(t *testing.T) {
	t.Parallel()

	longReason := strings.Repeat("输入是可能的部门标识片段但当前上下文无法执行", 8)
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:    ActUnknown,
			Domain: DomainUnknown,
			Reason: longReason,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{}),
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "信工25级",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseClarify {
		t.Fatalf("Response = %+v, want clarify", outcome.Response)
	}
	if outcome.Response.ClarifyReason != "unknown_intent" {
		t.Fatalf("ClarifyReason = %q, want unknown_intent", outcome.Response.ClarifyReason)
	}
	if outcome.BlockedReason != "unknown_intent" {
		t.Fatalf("BlockedReason = %q, want unknown_intent", outcome.BlockedReason)
	}
	if len([]rune(outcome.BlockedReason)) > 64 {
		t.Fatalf("BlockedReason length = %d, want <= 64", len([]rune(outcome.BlockedReason)))
	}
}

func TestProtocolLivePipelineExplainsRuleThroughKnowledge(t *testing.T) {
	t.Parallel()

	knowledge := &executorFakeKnowledgePort{
		hits: []tools.KnowledgeHit{{Heading: "缺勤规则", Body: "超过上课时间未打卡会判为缺勤。", SourceRef: "attendance#absence"}},
	}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActRuleQuestion,
			Domain:     DomainAttendance,
			Operation:  "attendance.rule_explain",
			Confidence: 0.9,
			Slots: map[string]SlotDraft{
				"rule_topic": {Field: "rule_topic", Raw: "为什么判我缺勤"},
			},
		}},
		Executor: newOperationExecutor(operationExecutorDeps{Knowledge: knowledge}),
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "为什么判我缺勤",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseAnswer {
		t.Fatalf("Response = %+v, want answer", outcome.Response)
	}
	if !strings.Contains(renderProtocolResponse(outcome.Response), "超过上课时间未打卡") {
		t.Fatalf("reply = %q, want knowledge answer", renderProtocolResponse(outcome.Response))
	}
	if knowledge.calls != 1 {
		t.Fatalf("knowledge calls = %d, want 1", knowledge.calls)
	}
}

func TestProtocolLivePipelineDefaultsCurrentWeekForMySchedule(t *testing.T) {
	t.Parallel()

	schedule := &executorFakeSchedulePort{courses: []tools.CourseItem{{CourseName: "高等数学", DayOfWeek: 1, Section: 2}}}
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Compiler: pipelineFakeIntentCompiler{draft: ProtocolDraft{
			Act:        ActReadQuery,
			Domain:     DomainSchedule,
			Operation:  "schedule.query_my_schedule",
			Confidence: 0.9,
		}},
		Executor: newOperationExecutor(operationExecutorDeps{Schedule: schedule}),
		Semester: &executorFakeSemesterPort{week: 11},
	})

	outcome := pipeline.Handle(context.Background(), protocolLiveInput{
		Message: "查我的课表",
		User:    executorUserContext(),
	})

	if outcome.Response.Kind != ResponseResult {
		t.Fatalf("Response = %+v, want result", outcome.Response)
	}
	if schedule.listMyCalls != 1 || schedule.lastWeek != 11 {
		t.Fatalf("listMyCalls=%d lastWeek=%d, want week 11", schedule.listMyCalls, schedule.lastWeek)
	}
}

type pipelineFakeIntentCompiler struct {
	draft ProtocolDraft
	err   error
}

func (c pipelineFakeIntentCompiler) Compile(context.Context, IntentCompileRequest) (IntentDraft, error) {
	return c.draft, c.err
}

type pipelineFakeSchedulePeriodPort struct {
	periods []tools.PeriodInfo
}

func (p pipelineFakeSchedulePeriodPort) GetScheduleInfo(context.Context) ([]tools.PeriodInfo, string, error) {
	return append([]tools.PeriodInfo(nil), p.periods...), "test", nil
}

type pipelineSearchUserPort struct {
	users       []tools.UserInfo
	searchCalls int
}

func (p *pipelineSearchUserPort) FindByDingUserID(context.Context, string) (*tools.UserInfo, error) {
	return nil, nil
}

func (p *pipelineSearchUserPort) SearchByName(context.Context, string) ([]tools.UserInfo, error) {
	p.searchCalls++
	return append([]tools.UserInfo(nil), p.users...), nil
}

type pipelineDenyResourcePolicy struct {
	reason string
}

func (p pipelineDenyResourcePolicy) Validate(context.Context, ResourcePolicyGateInput) ResourcePolicyGateResult {
	return ResourcePolicyGateResult{
		Allow:         false,
		BlockedReason: p.reason,
		ResponseKind:  ResponseRefuse,
	}
}

type pipelineAllowWriteGuard struct {
	key string
}

func (p pipelineAllowWriteGuard) Check(WriteGuardInput) WriteGuardResult {
	return WriteGuardResult{
		Allow:          true,
		ResponseKind:   ResponseResult,
		IdempotencyKey: p.key,
	}
}

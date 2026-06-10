package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"schedule_server/internal/agent/tools"
)

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

	dept := executorFakeDeptPort{depts: []tools.DeptItem{{DeptID: 101, Name: "信工24级"}}}
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

	dept := executorFakeDeptPort{depts: []tools.DeptItem{{DeptID: 101, Name: "信工24级"}}}
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
	if outcome.Validation.ValidationCode != "allowed_write_request" {
		t.Fatalf("ValidationCode = %q, want allowed_write_request", outcome.Validation.ValidationCode)
	}
	if outcome.BlockedReason != "role_denied" {
		t.Fatalf("BlockedReason = %q, want role_denied", outcome.BlockedReason)
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

func TestProtocolLivePipelineDepartmentListMetaKeepsCollectScopeWorkflow(t *testing.T) {
	t.Parallel()

	dept := executorFakeDeptPort{depts: []tools.DeptItem{{DeptID: 101, Name: "信工24级"}}}
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
	if outcome.WorkflowDecision != WorkflowMetaResult {
		t.Fatalf("WorkflowDecision = %q, want %q", outcome.WorkflowDecision, WorkflowMetaResult)
	}
	if outcome.WorkflowAfter == nil || outcome.WorkflowAfter.ID != "wf-sub" || outcome.WorkflowAfter.State != WorkflowCollectScope {
		t.Fatalf("WorkflowAfter = %+v, want collect_scope retained", outcome.WorkflowAfter)
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

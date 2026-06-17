package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"schedule_server/internal/agent/tools"
)

func TestOperationExecutorAttendanceSlotStatusUsesAttendancePort(t *testing.T) {
	t.Parallel()

	attendance := &executorFakeAttendancePort{
		detailResp: &tools.AttendanceResult{
			Date:         "2026-06-06",
			Week:         10,
			Section:      2,
			ShouldAttend: 3,
			OnTimeCount:  2,
			LateCount:    1,
			LateUsers:    []string{"王志伟"},
		},
	}
	executor := newOperationExecutor(operationExecutorDeps{Attendance: attendance})

	result := executor.Execute(context.Background(), OperationRequest{
		Operation: "attendance.query_status",
		TrustedParams: executorTrustedParams(map[string]any{
			"query_shape": "slot_status",
			"date":        "2026-06-06",
			"week":        10,
			"section":     2,
		}),
	})

	if result.Response.Kind != ResponseResult {
		t.Fatalf("Kind = %q, want %q", result.Response.Kind, ResponseResult)
	}
	if result.Metrics.ExecutorName != "operation_executor" || result.Metrics.ToolPool != "operation" {
		t.Fatalf("metrics = %+v, want operation executor metrics", result.Metrics)
	}
	if attendance.detailCalls != 1 {
		t.Fatalf("detailCalls = %d, want 1", attendance.detailCalls)
	}
	if attendance.lastQuery.Date != "2026-06-06" || attendance.lastQuery.Week != 10 || attendance.lastQuery.Section != 2 {
		t.Fatalf("lastQuery = %+v", attendance.lastQuery)
	}
	payload, ok := result.Response.Payload.(AttendanceStatusPayload)
	if !ok || payload.Result == nil {
		t.Fatalf("Payload = %#v, want AttendanceStatusPayload", result.Response.Payload)
	}
	if result.Response.ResultText != "" {
		t.Fatalf("ResultText = %q, want renderer-owned text", result.Response.ResultText)
	}
	if !strings.Contains(renderProtocolResponse(result.Response), "2026-06-06第2节考勤状态") {
		t.Fatalf("reply = %q, want deterministic attendance status", renderProtocolResponse(result.Response))
	}
}

func TestOperationExecutorExecuteSignatureConsumesOnlyRequest(t *testing.T) {
	t.Parallel()

	method, ok := reflect.TypeOf(operationExecutor{}).MethodByName("Execute")
	if !ok {
		t.Fatalf("operationExecutor.Execute missing")
	}
	if method.Type.NumIn() != 3 {
		t.Fatalf("Execute inputs = %d, want receiver, context.Context, OperationRequest", method.Type.NumIn())
	}
	if method.Type.In(1) != reflect.TypeOf((*context.Context)(nil)).Elem() {
		t.Fatalf("Execute arg1 = %v, want context.Context", method.Type.In(1))
	}
	if method.Type.In(2) != reflect.TypeOf(OperationRequest{}) {
		t.Fatalf("Execute arg2 = %v, want OperationRequest", method.Type.In(2))
	}
}

func TestOperationExecutorHasDomainBindingsForActiveOperations(t *testing.T) {
	t.Parallel()

	bindings := operationDomainBindings()
	for _, domain := range []BusinessDomain{DomainSystem, DomainAttendance, DomainSchedule, DomainSubscription, DomainManualSign} {
		if _, ok := bindings[domain]; !ok {
			t.Fatalf("operationDomainBindings missing %s binding", domain)
		}
	}
}

func TestOperationExecutorAttendanceSlotStatusRequiresTrustedWeek(t *testing.T) {
	t.Parallel()

	attendance := &executorFakeAttendancePort{
		detailResp: &tools.AttendanceResult{
			Date:         "2026-06-06",
			Week:         3,
			Section:      2,
			ShouldAttend: 1,
			OnTimeCount:  1,
		},
	}
	semester := &executorFakeSemesterPort{week: 3}
	executor := newOperationExecutor(operationExecutorDeps{
		Attendance: attendance,
		Semester:   semester,
	})

	result := executor.Execute(context.Background(), OperationRequest{
		Operation: "attendance.query_status",
		TrustedParams: executorTrustedParams(map[string]any{
			"query_shape": "slot_status",
			"date":        "2026-06-06",
			"section":     2,
		}),
	})

	if result.Response.Kind != ResponseClarify {
		t.Fatalf("Kind = %q, want %q; reply=%q", result.Response.Kind, ResponseClarify, renderProtocolResponse(result.Response))
	}
	if semester.calls != 0 {
		t.Fatalf("semester calls = %d, want 0 because defaults must be resolver trusted params", semester.calls)
	}
	if attendance.detailCalls != 0 {
		t.Fatalf("detailCalls = %d, want 0 without trusted week", attendance.detailCalls)
	}
}

func TestOperationExecutorAttendanceUserDayStatusUsesUserDayPort(t *testing.T) {
	t.Parallel()

	attendance := &executorFakeAttendancePort{}
	userDay := &executorFakeAttendanceUserDayStatusPort{
		resp: &tools.UserDayAttendanceStatus{
			Date:     "2026-06-06",
			UserID:   9,
			UserName: "张三",
			Slots: []tools.UserDayAttendanceSlot{
				{Section: 1, Status: "late"},
				{Section: 2, Status: "on_time"},
			},
		},
	}
	executor := newOperationExecutor(operationExecutorDeps{
		Attendance:              attendance,
		AttendanceUserDayStatus: userDay,
	})

	result := executor.Execute(context.Background(), OperationRequest{
		Operation: "attendance.query_status",
		TrustedParams: executorTrustedParams(map[string]any{
			"query_shape": "user_day_status",
			"date":        "2026-06-06",
			"user_id":     uint(9),
		}),
	})

	if result.Response.Kind != ResponseResult {
		t.Fatalf("Kind = %q, want %q", result.Response.Kind, ResponseResult)
	}
	if userDay.calls != 1 || userDay.lastDate != "2026-06-06" || userDay.lastUserID != 9 {
		t.Fatalf("user day calls=%d date=%q userID=%d", userDay.calls, userDay.lastDate, userDay.lastUserID)
	}
	if attendance.detailCalls != 0 {
		t.Fatalf("GetAttendanceDetail calls = %d, want 0 for user_day_status", attendance.detailCalls)
	}
	reply := renderProtocolResponse(result.Response)
	if !strings.Contains(reply, "张三") || !strings.Contains(reply, "第1节迟到") || !strings.Contains(reply, "第2节正常") {
		t.Fatalf("reply = %q, want user day status summary", reply)
	}
}

func TestOperationExecutorScheduleQueriesUseSchedulePort(t *testing.T) {
	t.Parallel()

	schedule := &executorFakeSchedulePort{
		courses: []tools.CourseItem{
			{CourseName: "高等数学", DayOfWeek: 1, Section: 1, Location: "A101"},
		},
	}
	executor := newOperationExecutor(operationExecutorDeps{Schedule: schedule})
	uctx := executorUserContext()

	myResult := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "schedule.query_my_schedule",
		TrustedParams: executorTrustedParams(map[string]any{
			"week": 6,
		}),
	}, uctx))
	if myResult.Response.Kind != ResponseResult {
		t.Fatalf("my schedule Kind = %q, want %q", myResult.Response.Kind, ResponseResult)
	}
	if schedule.listMyCalls != 1 || schedule.lastUserID != uctx.UserID || schedule.lastWeek != 6 {
		t.Fatalf("ListMyScheduleByWeek calls=%d user=%d week=%d", schedule.listMyCalls, schedule.lastUserID, schedule.lastWeek)
	}
	if !strings.Contains(renderProtocolResponse(myResult.Response), "高等数学") {
		t.Fatalf("my schedule reply = %q, want course name", renderProtocolResponse(myResult.Response))
	}

	userResult := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "schedule.query_user_schedule",
		TrustedParams: executorTrustedParams(map[string]any{
			"user_id": uint(9),
			"week":    6,
		}),
	}, uctx))
	if userResult.Response.Kind != ResponseResult {
		t.Fatalf("user schedule Kind = %q, want %q", userResult.Response.Kind, ResponseResult)
	}
	if schedule.listUserCalls != 1 || schedule.lastViewerID != uctx.UserID || schedule.lastViewerRole != uctx.UserRole || schedule.lastTargetUserID != 9 || schedule.lastWeek != 6 {
		t.Fatalf("ListUserScheduleByWeek calls=%d viewer=%d role=%d target=%d week=%d", schedule.listUserCalls, schedule.lastViewerID, schedule.lastViewerRole, schedule.lastTargetUserID, schedule.lastWeek)
	}
}

func TestOperationExecutorSubscriptionOperationsUseNarrowPorts(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{
		info: &tools.GroupSubInfo{Subscribed: true, DeptIDs: []int64{101}},
	}
	dept := executorFakeDeptPort{
		depts: []tools.DeptItem{{DeptID: 101, Name: "信工24级"}},
	}
	executor := newOperationExecutor(operationExecutorDeps{GroupSub: groupSub, Dept: dept})
	uctx := executorUserContext()
	uctx.ConversationID = "conv-runtime"
	uctx.ConversationTitle = "测试群"

	startAll := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.start",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": "conv-runtime",
			"scope":           "all",
		}),
	}, uctx))
	if startAll.Response.Kind != ResponseResult {
		t.Fatalf("startAll Kind = %q, want %q", startAll.Response.Kind, ResponseResult)
	}
	if groupSub.subscribeCalls != 1 || groupSub.lastConversationID != "conv-runtime" || len(groupSub.lastDeptIDs) != 0 {
		t.Fatalf("Subscribe all calls=%d conversation=%q deptIDs=%v", groupSub.subscribeCalls, groupSub.lastConversationID, groupSub.lastDeptIDs)
	}

	startDept := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.start",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": "conv-runtime",
			"scope":           "department",
			"dept_ids":        []int64{101, 102},
		}),
	}, uctx))
	if startDept.Response.Kind != ResponseResult {
		t.Fatalf("startDept Kind = %q, want %q", startDept.Response.Kind, ResponseResult)
	}
	if groupSub.subscribeCalls != 2 || !reflect.DeepEqual(groupSub.lastDeptIDs, []int64{101, 102}) {
		t.Fatalf("Subscribe dept calls=%d deptIDs=%v", groupSub.subscribeCalls, groupSub.lastDeptIDs)
	}

	status := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.query_status",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": "conv-runtime",
		}),
	}, uctx))
	if status.Response.Kind != ResponseResult || !strings.Contains(renderProtocolResponse(status.Response), "已订阅") {
		t.Fatalf("status = %+v reply=%q, want subscribed result", status, renderProtocolResponse(status.Response))
	}
	if groupSub.getCalls != 3 {
		t.Fatalf("GetSubscription calls = %d, want 3", groupSub.getCalls)
	}

	options := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{Operation: "subscription.list_departments"}, uctx))
	if options.Response.Kind != ResponseSelectOptions {
		t.Fatalf("department list Kind = %q, want %q", options.Response.Kind, ResponseSelectOptions)
	}
	if len(options.Response.Options) != 1 || options.Response.Options[0].Label != "信工24级" {
		t.Fatalf("department options = %+v", options.Response.Options)
	}

	cancel := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.cancel",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": "conv-runtime",
		}),
	}, uctx))
	if cancel.Response.Kind != ResponseResult {
		t.Fatalf("cancel Kind = %q, want %q", cancel.Response.Kind, ResponseResult)
	}
	if groupSub.unsubscribeCalls != 1 || groupSub.lastConversationID != "conv-runtime" {
		t.Fatalf("Unsubscribe calls=%d conversation=%q", groupSub.unsubscribeCalls, groupSub.lastConversationID)
	}
}

func TestOperationExecutorSubscriptionStartRejectsInvalidScope(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	executor := newOperationExecutor(operationExecutorDeps{GroupSub: groupSub})
	uctx := executorUserContext()

	result := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.start",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": uctx.ConversationID,
			"scope":           "everyone",
		}),
	}, uctx))

	if result.Response.Kind != ResponseRefuse {
		t.Fatalf("Kind = %q, want %q", result.Response.Kind, ResponseRefuse)
	}
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("Subscribe calls = %d, want 0", groupSub.subscribeCalls)
	}
}

func TestOperationExecutorSubscriptionStartReturnsStableWriteStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		existing           *tools.GroupSubInfo
		scope              string
		deptIDs            []int64
		wantStatus         WriteStatus
		wantSubscribeCalls int
	}{
		{
			name:               "creates missing subscription",
			existing:           &tools.GroupSubInfo{Subscribed: false},
			scope:              "all",
			wantStatus:         WriteStatusCreated,
			wantSubscribeCalls: 1,
		},
		{
			name:               "already exists with same all scope",
			existing:           &tools.GroupSubInfo{Subscribed: true},
			scope:              "all",
			wantStatus:         WriteStatusAlreadyExists,
			wantSubscribeCalls: 0,
		},
		{
			name:               "updates changed department scope",
			existing:           &tools.GroupSubInfo{Subscribed: true, DeptIDs: []int64{101}},
			scope:              "department",
			deptIDs:            []int64{102, 103},
			wantStatus:         WriteStatusUpdated,
			wantSubscribeCalls: 1,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			groupSub := &executorFakeGroupSubPort{info: tt.existing}
			executor := newOperationExecutor(operationExecutorDeps{GroupSub: groupSub})
			uctx := executorUserContext()
			params := map[string]any{
				"conversation_id": uctx.ConversationID,
				"scope":           tt.scope,
			}
			if len(tt.deptIDs) > 0 {
				params["dept_ids"] = tt.deptIDs
			}

			result := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
				Operation:     "subscription.start",
				TrustedParams: executorTrustedParams(params),
			}, uctx))

			payload, ok := result.Response.Payload.(OperationStatusPayload)
			if !ok {
				t.Fatalf("Payload = %T, want OperationStatusPayload", result.Response.Payload)
			}
			if payload.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q", payload.Status, tt.wantStatus)
			}
			if groupSub.subscribeCalls != tt.wantSubscribeCalls {
				t.Fatalf("Subscribe() calls = %d, want %d", groupSub.subscribeCalls, tt.wantSubscribeCalls)
			}
		})
	}
}

func TestOperationExecutorSubscriptionCancelReturnsStableWriteStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		existing             *tools.GroupSubInfo
		wantStatus           WriteStatus
		wantUnsubscribeCalls int
	}{
		{
			name:                 "no active subscription is no op",
			existing:             &tools.GroupSubInfo{Subscribed: false},
			wantStatus:           WriteStatusNoOp,
			wantUnsubscribeCalls: 0,
		},
		{
			name:                 "active subscription is updated",
			existing:             &tools.GroupSubInfo{Subscribed: true},
			wantStatus:           WriteStatusUpdated,
			wantUnsubscribeCalls: 1,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			groupSub := &executorFakeGroupSubPort{info: tt.existing}
			executor := newOperationExecutor(operationExecutorDeps{GroupSub: groupSub})
			uctx := executorUserContext()

			result := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
				Operation: "subscription.cancel",
				TrustedParams: executorTrustedParams(map[string]any{
					"conversation_id": uctx.ConversationID,
				}),
			}, uctx))

			payload, ok := result.Response.Payload.(OperationStatusPayload)
			if !ok {
				t.Fatalf("Payload = %T, want OperationStatusPayload", result.Response.Payload)
			}
			if payload.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q", payload.Status, tt.wantStatus)
			}
			if groupSub.unsubscribeCalls != tt.wantUnsubscribeCalls {
				t.Fatalf("Unsubscribe() calls = %d, want %d", groupSub.unsubscribeCalls, tt.wantUnsubscribeCalls)
			}
		})
	}
}

func TestOperationExecutorSubscriptionOperationsBindConversationIDToRuntimeGroup(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	executor := newOperationExecutor(operationExecutorDeps{GroupSub: groupSub})
	uctx := executorUserContext()
	uctx.ConversationID = "conv-runtime"

	start := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.start",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": "conv-other",
			"scope":           "all",
		}),
	}, uctx))
	if start.Response.Kind != ResponseRefuse {
		t.Fatalf("start Kind = %q, want %q", start.Response.Kind, ResponseRefuse)
	}
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("Subscribe calls = %d, want 0", groupSub.subscribeCalls)
	}

	status := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.query_status",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": "conv-other",
		}),
	}, uctx))
	if status.Response.Kind != ResponseRefuse {
		t.Fatalf("status Kind = %q, want %q", status.Response.Kind, ResponseRefuse)
	}
	if groupSub.getCalls != 0 {
		t.Fatalf("GetSubscription calls = %d, want 0", groupSub.getCalls)
	}

	cancel := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.cancel",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": "conv-other",
		}),
	}, uctx))
	if cancel.Response.Kind != ResponseRefuse {
		t.Fatalf("cancel Kind = %q, want %q", cancel.Response.Kind, ResponseRefuse)
	}
	if groupSub.unsubscribeCalls != 0 {
		t.Fatalf("Unsubscribe calls = %d, want 0", groupSub.unsubscribeCalls)
	}
}

func TestOperationExecutorSubscriptionCancelRequiresGroupChat(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	executor := newOperationExecutor(operationExecutorDeps{GroupSub: groupSub})
	uctx := executorUserContext()
	uctx.ConversationType = "1"
	uctx.ConversationID = "single-conv"

	result := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.cancel",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": "single-conv",
		}),
	}, uctx))

	if result.Response.Kind != ResponseRefuse {
		t.Fatalf("Kind = %q, want %q", result.Response.Kind, ResponseRefuse)
	}
	if groupSub.unsubscribeCalls != 0 {
		t.Fatalf("Unsubscribe calls = %d, want 0", groupSub.unsubscribeCalls)
	}
}

func TestOperationExecutorSubscriptionStartRequiresGroupChat(t *testing.T) {
	t.Parallel()

	groupSub := &executorFakeGroupSubPort{}
	executor := newOperationExecutor(operationExecutorDeps{GroupSub: groupSub})
	uctx := executorUserContext()
	uctx.ConversationType = "1"
	uctx.ConversationID = "single-conv"

	result := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "subscription.start",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": "single-conv",
			"scope":           "all",
		}),
	}, uctx))

	if result.Response.Kind != ResponseRefuse {
		t.Fatalf("Kind = %q, want %q", result.Response.Kind, ResponseRefuse)
	}
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("Subscribe calls = %d, want 0", groupSub.subscribeCalls)
	}
}

func TestOperationExecutorCapabilityAndRuleAnswersDoNotUseBusinessTools(t *testing.T) {
	t.Parallel()

	knowledge := &executorFakeKnowledgePort{
		hits: []tools.KnowledgeHit{{Heading: "迟到规则", Body: "迟到按上课开始时间判定。", SourceRef: "考勤规则#1"}},
	}
	attendance := &executorFakeAttendancePort{}
	executor := newOperationExecutor(operationExecutorDeps{
		Attendance: attendance,
		Knowledge:  knowledge,
	})
	uctx := executorUserContext()

	capability := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{Operation: "manual_sign.describe_capability"}, uctx))
	if capability.Response.Kind != ResponseAnswer || !strings.Contains(renderProtocolResponse(capability.Response), "代签") {
		t.Fatalf("capability = %+v reply=%q, want manual sign answer", capability, renderProtocolResponse(capability.Response))
	}
	if attendance.detailCalls != 0 {
		t.Fatalf("attendance detail calls = %d, want 0 for capability answer", attendance.detailCalls)
	}

	rule := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "attendance.rule_explain",
		TrustedParams: executorTrustedParams(map[string]any{
			"rule_topic": "迟到规则",
		}),
	}, uctx))
	if rule.Response.Kind != ResponseAnswer || !strings.Contains(renderProtocolResponse(rule.Response), "迟到按上课开始时间判定") {
		t.Fatalf("rule = %+v reply=%q, want knowledge answer", rule, renderProtocolResponse(rule.Response))
	}
	if knowledge.calls != 1 || knowledge.lastTenantID != uctx.TenantID || knowledge.lastQuery != "迟到规则" {
		t.Fatalf("knowledge calls=%d tenant=%d query=%q", knowledge.calls, knowledge.lastTenantID, knowledge.lastQuery)
	}

	knowledge.hits = nil
	noHit := executor.Execute(context.Background(), enrichOperationRequestFromUser(OperationRequest{
		Operation: "attendance.rule_explain",
		TrustedParams: executorTrustedParams(map[string]any{
			"rule_topic": "迟到规则",
		}),
	}, uctx))
	if noHit.Response.Kind != ResponseAnswer || noHit.Response.BusinessError != "no_knowledge_hit" {
		t.Fatalf("noHit = %+v, want no_knowledge_hit answer", noHit)
	}
}

func TestOperationExecutorUnsupportedOperationRefuses(t *testing.T) {
	t.Parallel()

	executor := newOperationExecutor(operationExecutorDeps{})

	result := executor.Execute(context.Background(), OperationRequest{
		Operation: "manual_sign.create",
	})

	if result.Response.Kind != ResponseRefuse {
		t.Fatalf("Kind = %q, want %q", result.Response.Kind, ResponseRefuse)
	}
	if strings.TrimSpace(renderProtocolResponse(result.Response)) == "" {
		t.Fatalf("reply should be non-empty")
	}
}

func TestOperationExecutorDepsExposeNoFullToolPoolOrLLM(t *testing.T) {
	t.Parallel()

	depsType := reflect.TypeOf(operationExecutorDeps{})
	for _, forbidden := range []string{"Registry", "ToolDefs", "LLMClient", "Client"} {
		if _, ok := depsType.FieldByName(forbidden); ok {
			t.Fatalf("operationExecutorDeps should not expose %s", forbidden)
		}
	}
}

func executorUserContext() *tools.UserContext {
	return &tools.UserContext{
		TenantID:          42,
		UserID:            7,
		Name:              "Alice",
		UserRole:          1,
		ConversationID:    "conv-1",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	}
}

func executorTrustedParams(values map[string]any) map[string]TrustedParam {
	return trustedParamsFromValues(42, TrustedParamSource{
		Kind:     TrustedParamSourceWorkflow,
		Resolver: "executor_test",
	}, values)
}

type executorFakeAttendancePort struct {
	detailResp  *tools.AttendanceResult
	detailCalls int
	lastQuery   tools.AttendanceQuery
}

func (p *executorFakeAttendancePort) GetAttendanceDetail(_ context.Context, req tools.AttendanceQuery) (*tools.AttendanceResult, error) {
	p.detailCalls++
	p.lastQuery = req
	return p.detailResp, nil
}

func (p *executorFakeAttendancePort) GetAttendanceText(context.Context, tools.AttendanceQuery) (string, error) {
	return "", nil
}

func (p *executorFakeAttendancePort) GetWeeklyAbsenceRanking(context.Context) ([]tools.RankItem, error) {
	return nil, nil
}

func (p *executorFakeAttendancePort) GetWeeklyAttendanceRateRanking(context.Context) ([]tools.RankItem, error) {
	return nil, nil
}

func (p *executorFakeAttendancePort) FindRecordByDateSection(context.Context, string, int) (uint, error) {
	return 0, nil
}

func (p *executorFakeAttendancePort) SignForUsers(context.Context, uint, []uint) error {
	return nil
}

func (p *executorFakeAttendancePort) SignForUsersBySlot(context.Context, string, int, []uint) error {
	return nil
}

type executorFakeAttendanceUserDayStatusPort struct {
	resp       *tools.UserDayAttendanceStatus
	calls      int
	lastDate   string
	lastUserID uint
}

func (p *executorFakeAttendanceUserDayStatusPort) GetUserDayAttendanceStatus(_ context.Context, date string, userID uint) (*tools.UserDayAttendanceStatus, error) {
	p.calls++
	p.lastDate = date
	p.lastUserID = userID
	return p.resp, nil
}

type executorFakeSemesterPort struct {
	week  int
	calls int
}

func (p *executorFakeSemesterPort) GetCurrentWeek(context.Context) (int, int, error) {
	p.calls++
	return p.week, 20, nil
}

type executorFakeSchedulePort struct {
	courses          []tools.CourseItem
	listMyCalls      int
	listUserCalls    int
	lastUserID       uint
	lastViewerID     uint
	lastViewerRole   int
	lastTargetUserID uint
	lastWeek         int
}

func (p *executorFakeSchedulePort) ListMyScheduleByWeek(_ context.Context, userID uint, week int) ([]tools.CourseItem, error) {
	p.listMyCalls++
	p.lastUserID = userID
	p.lastWeek = week
	return p.courses, nil
}

func (p *executorFakeSchedulePort) ListUserScheduleByWeek(_ context.Context, viewerID uint, viewerRole int, targetUserID uint, week int) ([]tools.CourseItem, error) {
	p.listUserCalls++
	p.lastViewerID = viewerID
	p.lastViewerRole = viewerRole
	p.lastTargetUserID = targetUserID
	p.lastWeek = week
	return p.courses, nil
}

func (p *executorFakeSchedulePort) GetFreeUsersBySlot(context.Context, int, int, int, int64) ([]tools.FreeSlotResult, error) {
	return nil, nil
}

type executorFakeGroupSubPort struct {
	info               *tools.GroupSubInfo
	subscribeCalls     int
	unsubscribeCalls   int
	getCalls           int
	lastTenantID       uint
	lastConversationID string
	lastGroupName      string
	lastEnabledByUID   uint
	lastDeptIDs        []int64
}

func (p *executorFakeGroupSubPort) Subscribe(_ context.Context, tenantID uint, conversationID, groupName string, enabledByUID uint, deptIDs []int64) error {
	p.subscribeCalls++
	p.lastTenantID = tenantID
	p.lastConversationID = conversationID
	p.lastGroupName = groupName
	p.lastEnabledByUID = enabledByUID
	p.lastDeptIDs = append([]int64(nil), deptIDs...)
	return nil
}

func (p *executorFakeGroupSubPort) Unsubscribe(_ context.Context, tenantID uint, conversationID string) error {
	p.unsubscribeCalls++
	p.lastTenantID = tenantID
	p.lastConversationID = conversationID
	return nil
}

func (p *executorFakeGroupSubPort) GetSubscription(_ context.Context, tenantID uint, conversationID string) (*tools.GroupSubInfo, error) {
	p.getCalls++
	p.lastTenantID = tenantID
	p.lastConversationID = conversationID
	if p.info != nil {
		return p.info, nil
	}
	return &tools.GroupSubInfo{}, nil
}

type executorFakeDeptPort struct {
	depts []tools.DeptItem
}

func (p executorFakeDeptPort) ListDepts(context.Context) ([]tools.DeptItem, error) {
	return p.depts, nil
}

type executorFakeKnowledgePort struct {
	hits         []tools.KnowledgeHit
	calls        int
	lastTenantID uint
	lastQuery    string
	lastTopK     int
}

func (p *executorFakeKnowledgePort) Search(_ context.Context, tenantID uint, query string, topK int) ([]tools.KnowledgeHit, error) {
	p.calls++
	p.lastTenantID = tenantID
	p.lastQuery = query
	p.lastTopK = topK
	return p.hits, nil
}

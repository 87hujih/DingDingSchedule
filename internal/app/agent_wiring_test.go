package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"schedule_server/config"
	"schedule_server/global"
	agentpkg "schedule_server/internal/agent"
	agenttool "schedule_server/internal/agent/tools"
	"schedule_server/internal/dto"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/service"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAttendanceAdapterUsesRealtimeViewForCurrentSlot(t *testing.T) {
	service := &fakeAttendanceDetailService{
		detailResp: &dto.AttendanceDetailResponse{
			Date:        "2026-03-19",
			Week:        3,
			Section:     1,
			ViewMode:    "current",
			IsFinalized: false,
			FinalizeAt:  time.Date(2026, 3, 19, 8, 30, 0, 0, time.Local),
			SlotTime: dto.SlotTimeInfo{
				Start: "08:00",
				End:   "09:40",
			},
			Statistics: dto.AttendanceStatistics{
				ShouldAttend: 3,
				OnTime:       1,
				Late:         1,
				NotArrived:   1,
			},
			Users: dto.AttendanceUserLists{
				OnTime:     []dto.AttendanceUserCheck{{Name: "OnTimeUser"}},
				Late:       []dto.AttendanceUserCheck{{Name: "LateUser"}},
				NotArrived: []dto.AttendanceUserBasic{{Name: "MissingUser"}},
			},
		},
	}

	adapter := &attendanceAdapter{srv: service}
	result, err := adapter.GetAttendanceDetail(context.Background(), agenttool.AttendanceQuery{
		Date:    "2026-03-19",
		Week:    3,
		Section: 1,
	})
	if err != nil {
		t.Fatalf("GetAttendanceDetail() error = %v", err)
	}

	if service.detailCalls != 1 {
		t.Fatalf("service detail call count = %d, want 1", service.detailCalls)
	}
	if result.ViewMode != "current" {
		t.Fatalf("ViewMode = %q, want current", result.ViewMode)
	}
	if result.IsFinalized {
		t.Fatalf("IsFinalized = true, want false")
	}
	if result.LateCount != 1 {
		t.Fatalf("LateCount = %d, want 1", result.LateCount)
	}
	if !slices.Equal(result.LateUsers, []string{"LateUser"}) {
		t.Fatalf("LateUsers = %v, want [LateUser]", result.LateUsers)
	}
}

func TestGroupSubAdapterIncludesPushEnabledInSubscriptionInfo(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:group-sub-adapter-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.Migrator().DropTable(&model.GroupAttendanceSubscription{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if err := db.AutoMigrate(&model.GroupAttendanceSubscription{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sub := model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-1",
		GroupName:      "测试群",
	}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err := db.Model(&model.GroupAttendanceSubscription{}).
		Where("tenant_id = ? AND conversation_id = ?", 1, "conv-1").
		Update("push_enabled", false).Error; err != nil {
		t.Fatalf("disable push: %v", err)
	}

	adapter := &groupSubAdapter{repo: repository.NewGroupAttendanceSubscriptionRepository(db)}
	info, err := adapter.GetSubscription(context.Background(), 1, "conv-1")
	if err != nil {
		t.Fatalf("GetSubscription() error = %v", err)
	}
	if !info.Subscribed {
		t.Fatalf("Subscribed = false, want true")
	}
	if info.PushEnabled {
		t.Fatalf("PushEnabled = true, want false")
	}
}

func TestAttendanceAdapterUsesSnapshotForHistoryQueries(t *testing.T) {
	service := &fakeAttendanceDetailService{
		detailResp: &dto.AttendanceDetailResponse{
			Date:        "2026-03-18",
			Week:        3,
			Section:     1,
			ViewMode:    "final",
			IsFinalized: true,
			FinalizeAt:  time.Date(2026, 3, 18, 8, 30, 0, 0, time.Local),
			SlotTime: dto.SlotTimeInfo{
				Start: "08:00",
				End:   "09:40",
			},
			Statistics: dto.AttendanceStatistics{
				ShouldAttend: 2,
				OnTime:       1,
				Late:         1,
				NotArrived:   0,
			},
			Users: dto.AttendanceUserLists{
				OnTime: []dto.AttendanceUserCheck{{Name: "OnTimeUser"}},
				Late:   []dto.AttendanceUserCheck{{Name: "LateUser"}},
			},
		},
	}

	adapter := &attendanceAdapter{srv: service}
	result, err := adapter.GetAttendanceDetail(context.Background(), agenttool.AttendanceQuery{
		Date:    "2026-03-18",
		Week:    3,
		Section: 1,
	})
	if err != nil {
		t.Fatalf("GetAttendanceDetail() error = %v", err)
	}

	if service.detailCalls != 1 {
		t.Fatalf("service detail call count = %d, want 1", service.detailCalls)
	}
	if result.ViewMode != "final" {
		t.Fatalf("ViewMode = %q, want final", result.ViewMode)
	}
	if !result.IsFinalized {
		t.Fatalf("IsFinalized = false, want true")
	}
	if result.FinalizeAt != "2026-03-18 08:30:00" {
		t.Fatalf("FinalizeAt = %q, want 2026-03-18 08:30:00", result.FinalizeAt)
	}
}

func TestAttendanceAdapterSignForUsersBySlotUsesDateAndSection(t *testing.T) {
	service := &fakeAttendanceDetailService{}
	adapter := &attendanceAdapter{srv: service}

	err := adapter.SignForUsersBySlot(context.Background(), "2026-03-25", 1, []uint{9})
	if err != nil {
		t.Fatalf("SignForUsersBySlot() error = %v", err)
	}

	if service.lastSignReq == nil {
		t.Fatalf("service sign request was not captured")
	}
	if service.lastSignReq.RecordID != 0 {
		t.Fatalf("RecordID = %d, want 0", service.lastSignReq.RecordID)
	}
	if service.lastSignReq.Date != "2026-03-25" {
		t.Fatalf("Date = %q, want 2026-03-25", service.lastSignReq.Date)
	}
	if service.lastSignReq.Section != 1 {
		t.Fatalf("Section = %d, want 1", service.lastSignReq.Section)
	}
	if !slices.Equal(service.lastSignReq.TargetUserIDs, []uint{9}) {
		t.Fatalf("TargetUserIDs = %v, want [9]", service.lastSignReq.TargetUserIDs)
	}
}

func TestAttendanceAdapterUserDayStatusUsesUserIDFromDateRecords(t *testing.T) {
	service := &fakeAttendanceDetailService{
		recordsByDateResp: []*dto.AttendanceDetailResponse{
			{
				Date:    "2026-06-06",
				Section: 1,
				Users: dto.AttendanceUserLists{
					Late: []dto.AttendanceUserCheck{{
						ID:        9,
						Name:      "张三",
						CheckTime: time.Date(2026, 6, 6, 8, 5, 0, 0, time.Local),
					}},
				},
			},
			{
				Date:    "2026-06-06",
				Section: 2,
				Users: dto.AttendanceUserLists{
					OnTime: []dto.AttendanceUserCheck{{
						ID:        10,
						Name:      "张三",
						CheckTime: time.Date(2026, 6, 6, 10, 0, 0, 0, time.Local),
					}},
				},
			},
			{
				Date:    "2026-06-06",
				Section: 3,
				Users: dto.AttendanceUserLists{
					Leave: []dto.AttendanceUserLeave{{
						ID:        9,
						Name:      "张三",
						LeaveType: "事假",
					}},
				},
			},
			{
				Date:    "2026-06-06",
				Section: 4,
				Users: dto.AttendanceUserLists{
					NotArrived: []dto.AttendanceUserBasic{{
						ID:   9,
						Name: "张三",
					}},
				},
			},
		},
	}
	adapter := &attendanceAdapter{srv: service}

	result, err := adapter.GetUserDayAttendanceStatus(context.Background(), "2026-06-06", 9)
	if err != nil {
		t.Fatalf("GetUserDayAttendanceStatus() error = %v", err)
	}

	if service.recordsByDateCalls != 1 {
		t.Fatalf("records by date calls = %d, want 1", service.recordsByDateCalls)
	}
	if service.lastRecordsDate.Format("2006-01-02") != "2026-06-06" {
		t.Fatalf("last records date = %s, want 2026-06-06", service.lastRecordsDate.Format("2006-01-02"))
	}
	if result.UserID != 9 || result.UserName != "张三" {
		t.Fatalf("result user = %d/%q, want 9/张三", result.UserID, result.UserName)
	}
	if len(result.Slots) != 3 {
		t.Fatalf("slots = %+v, want only target user statuses", result.Slots)
	}
	statusBySection := make(map[int]string)
	for _, slot := range result.Slots {
		statusBySection[slot.Section] = slot.Status
	}
	if statusBySection[1] != "late" || statusBySection[3] != "leave" || statusBySection[4] != "not_arrived" {
		t.Fatalf("statusBySection = %+v", statusBySection)
	}
	if _, ok := statusBySection[2]; ok {
		t.Fatalf("section 2 belongs to same-name different user and should not be included: %+v", result.Slots)
	}
}

func TestScheduleAdapterListUserScheduleByWeekUsesViewerAndTargetIDs(t *testing.T) {
	service := &fakeScheduleService{
		listByWeekResp: &service.WeekScheduleResult{
			Courses: []model.Course{{CourseName: "高等数学"}},
		},
	}
	adapter := &scheduleAdapter{srv: service}

	courses, err := adapter.ListUserScheduleByWeek(context.Background(), 7, 0, 9, 6)
	if err != nil {
		t.Fatalf("ListUserScheduleByWeek() error = %v", err)
	}
	if service.lastViewerID != 7 {
		t.Fatalf("lastViewerID = %d, want 7", service.lastViewerID)
	}
	if service.lastViewerRole != 0 {
		t.Fatalf("lastViewerRole = %d, want 0", service.lastViewerRole)
	}
	if service.lastTargetUserID != 9 {
		t.Fatalf("lastTargetUserID = %d, want 9", service.lastTargetUserID)
	}
	if service.lastWeek != 6 {
		t.Fatalf("lastWeek = %d, want 6", service.lastWeek)
	}
	if len(courses) != 1 || courses[0].CourseName != "高等数学" {
		t.Fatalf("courses = %+v, want converted course result", courses)
	}
}

func TestBuildAgentUsesConfiguredProtocolMode(t *testing.T) {
	t.Parallel()

	prevConfig := global.AppConfig
	prevDB := global.DB
	t.Cleanup(func() {
		global.AppConfig = prevConfig
		global.DB = prevDB
	})

	global.AppConfig = config.Config{
		LLM: config.LLM{
			ProtocolMode: string(agentpkg.ProtocolModeLive),
		},
	}

	a := BuildAgent(&repository.Repository{}, nil, nil, nil, nil, nil, nil, nil, nil)
	if a == nil {
		t.Fatalf("BuildAgent() = nil, want agent")
	}
	t.Cleanup(a.Stop)

	field := reflect.ValueOf(a).Elem().FieldByName("protocolMode")
	if !field.IsValid() {
		t.Fatalf("protocolMode field not found")
	}
	if got := field.String(); got != string(agentpkg.ProtocolModeLive) {
		t.Fatalf("protocolMode = %q, want %q", got, agentpkg.ProtocolModeLive)
	}

	workflowStore := reflect.ValueOf(a).Elem().FieldByName("workflowStore")
	if !workflowStore.IsValid() || workflowStore.IsNil() {
		t.Fatalf("workflowStore = nil, want memory fallback")
	}
	if got := workflowStore.Elem().Type().String(); got != "*agent.memoryWorkflowStore" {
		t.Fatalf("workflowStore type = %q, want memory workflow store", got)
	}
}

func TestIntentCompilerTimeoutFromConfig(t *testing.T) {
	t.Parallel()

	if got := intentCompilerTimeoutFromConfig(config.LLM{IntentCompilerTimeout: "4s"}); got != 4*time.Second {
		t.Fatalf("intentCompilerTimeoutFromConfig(valid) = %s, want 4s", got)
	}
	if got := intentCompilerTimeoutFromConfig(config.LLM{IntentCompilerTimeout: "bad-value"}); got != 0 {
		t.Fatalf("intentCompilerTimeoutFromConfig(invalid) = %s, want 0", got)
	}
}

func TestCallLogAdapterPersistsDomainModeAndRetrievalDetails(t *testing.T) {
	db := newCallLogTestDB(t)
	adapter := &callLogAdapter{db: db}

	adapter.Write(context.Background(), agenttool.CallLog{
		TenantID:                   1,
		UserID:                     7,
		UserRole:                   1,
		UserName:                   "Alice",
		ConvType:                   "1",
		QueryType:                  "rag",
		ConversationEvent:          "task_follow_up",
		ActiveTaskType:             "subscribe_attendance_push",
		TaskStatusBefore:           "waiting_slots",
		TaskStatusAfter:            "completed",
		DomainResult:               "in_domain",
		DomainHint:                 "unknown",
		PlanKind:                   "clarify",
		KnowledgeStrength:          "weak",
		PlannerReason:              "weak_domain_match",
		PlannerAction:              "continue_task",
		PlannerConfidence:          0.82,
		TaskID:                     "task-123",
		TaskKeepOpen:               true,
		TaskSwitch:                 false,
		LastErrorCode:              "department_name_not_found",
		ShadowPlannerAction:        "clarify",
		ShadowPlannerMatched:       false,
		RouteKind:                  "rag_query",
		RouteConfidence:            0.91,
		RouteReasonCode:            "rule_query",
		RouteSource:                "semantic_router",
		ClarifyCode:                "ambiguous_intent",
		SoftNoticeCode:             "task_switched",
		ExecutorName:               "rag_executor",
		ToolPool:                   "knowledge_only",
		RouterLatencyMs:            7,
		ExecutorLatencyMs:          28,
		ShadowRouteKind:            "tool_query",
		ShadowRouteMatched:         false,
		ProtocolMode:               "protocol_shadow",
		ProtocolAct:                "read_query",
		ProtocolDomain:             "attendance",
		ProtocolOperation:          "attendance.query_status",
		ProtocolValidationCode:     "allowed_read_query",
		ProtocolBlockedReason:      "missing_scope",
		ProtocolResolvedSlots:      `{"date":"2026-06-06","section":2}`,
		ProtocolCandidateCount:     2,
		RequestID:                  "req-v2-1",
		ConversationID:             "conv-v2-1",
		CompilerStatus:             "ok",
		CompilerSource:             "fallback",
		CompilerFallbackReason:     "llm_timeout",
		CompilerLatencyMs:          11,
		IntentDraftJSON:            `{"operation":"attendance.query_status","raw":"手机号 13812345678"}`,
		CatalogValidationCode:      "allowed_read_query",
		WorkflowDecision:           "single_turn",
		WorkflowInterruptReason:    "new_read_query",
		ResolvedSlotsJSON:          `{"date":"2026-06-06","section":2}`,
		EntityResolutionStatus:     "resolved",
		PrePolicyResult:            "allow",
		ResourcePolicyResult:       "allow",
		BlockedReason:              "missing_scope",
		WriteGuardResult:           "not_required",
		IdempotencyKey:             "idem-1",
		ExecutorStatus:             "success",
		RendererName:               "response_renderer",
		FailureLayer:               "",
		LegacyCalled:               false,
		ReplayCaseID:               "replay-1",
		WorkflowIDBefore:           "wf-before",
		WorkflowIDAfter:            "wf-after",
		WorkflowTypeBefore:         "subscription.start",
		WorkflowTypeAfter:          "subscription.start",
		WorkflowStateBefore:        "collect_scope",
		WorkflowStateAfter:         "ready",
		WorkflowSnapshotBeforeJSON: `{"type":"subscription.start","state":"collect_scope","candidates":{"dept_ids":[{"id":"101","label":"信工25级","tenant_id":1}]}}`,
		WorkflowSnapshotAfterJSON:  `{"type":"subscription.start","state":"ready"}`,
		ResponseKind:               "clarify",
		ExecutionAllowed:           false,
		AnswerMode:                 "knowledge-only",
		Question:                   "如果请假信息没能同步到位，会出现什么情况",
		ToolsCalled:                []string{"get_current_time"},
		ToolCallCount:              1,
		Reply:                      "同步失败不会直接覆盖已生成快照。",
		SourceRefs:                 []string{"请假同步说明#3"},
		RetrievalHitCount:          1,
		RetrievalCandidateCount:    3,
		RetrievalTopRefs:           []string{"请假同步说明#3", "系统总览#1"},
		RetrievalScores:            []int{18, 9},
		FollowUpMatchedSlots:       []string{"dept_names", "scope"},
		RetrievalFilteredReason:    "no_hits",
		KnowledgeDocTypes:          []string{"rule", "overview"},
		RetrievalDurationMs:        12,
		LLMDurationMs:              34,
		Rounds:                     1,
		DurationMs:                 56,
		Status:                     "success",
	})

	var row model.AgentCallLog
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("query call log: %v", err)
	}
	if row.DomainResult != "in_domain" {
		t.Fatalf("DomainResult = %q, want in_domain", row.DomainResult)
	}
	if row.UserRole != 1 {
		t.Fatalf("UserRole = %d, want 1", row.UserRole)
	}
	if row.DomainHint != "unknown" {
		t.Fatalf("DomainHint = %q, want unknown", row.DomainHint)
	}
	if row.PlanKind != "clarify" {
		t.Fatalf("PlanKind = %q, want clarify", row.PlanKind)
	}
	if row.KnowledgeStrength != "weak" {
		t.Fatalf("KnowledgeStrength = %q, want weak", row.KnowledgeStrength)
	}
	if row.PlannerReason != "weak_domain_match" {
		t.Fatalf("PlannerReason = %q, want weak_domain_match", row.PlannerReason)
	}
	if row.PlannerAction != "continue_task" {
		t.Fatalf("PlannerAction = %q, want continue_task", row.PlannerAction)
	}
	if row.PlannerConfidence != 0.82 {
		t.Fatalf("PlannerConfidence = %v, want 0.82", row.PlannerConfidence)
	}
	if row.TaskID != "task-123" {
		t.Fatalf("TaskID = %q, want task-123", row.TaskID)
	}
	if !row.TaskKeepOpen {
		t.Fatalf("TaskKeepOpen = false, want true")
	}
	if row.TaskSwitch {
		t.Fatalf("TaskSwitch = true, want false")
	}
	if row.LastErrorCode != "department_name_not_found" {
		t.Fatalf("LastErrorCode = %q, want department_name_not_found", row.LastErrorCode)
	}
	if row.ShadowPlannerAction != "clarify" {
		t.Fatalf("ShadowPlannerAction = %q, want clarify", row.ShadowPlannerAction)
	}
	if row.ShadowPlannerMatched {
		t.Fatalf("ShadowPlannerMatched = true, want false")
	}
	if row.AnswerMode != "knowledge-only" {
		t.Fatalf("AnswerMode = %q, want knowledge-only", row.AnswerMode)
	}
	if row.RouteKind != "rag_query" {
		t.Fatalf("RouteKind = %q, want rag_query", row.RouteKind)
	}
	if row.RouteConfidence != 0.91 {
		t.Fatalf("RouteConfidence = %v, want 0.91", row.RouteConfidence)
	}
	if row.RouteReasonCode != "rule_query" {
		t.Fatalf("RouteReasonCode = %q, want rule_query", row.RouteReasonCode)
	}
	if row.RouteSource != "semantic_router" {
		t.Fatalf("RouteSource = %q, want semantic_router", row.RouteSource)
	}
	if row.ClarifyCode != "ambiguous_intent" {
		t.Fatalf("ClarifyCode = %q, want ambiguous_intent", row.ClarifyCode)
	}
	if row.SoftNoticeCode != "task_switched" {
		t.Fatalf("SoftNoticeCode = %q, want task_switched", row.SoftNoticeCode)
	}
	if row.ExecutorName != "rag_executor" {
		t.Fatalf("ExecutorName = %q, want rag_executor", row.ExecutorName)
	}
	if row.ToolPool != "knowledge_only" {
		t.Fatalf("ToolPool = %q, want knowledge_only", row.ToolPool)
	}
	if row.RouterLatencyMs != 7 {
		t.Fatalf("RouterLatencyMs = %d, want 7", row.RouterLatencyMs)
	}
	if row.ExecutorLatencyMs != 28 {
		t.Fatalf("ExecutorLatencyMs = %d, want 28", row.ExecutorLatencyMs)
	}
	if row.ShadowRouteKind != "tool_query" {
		t.Fatalf("ShadowRouteKind = %q, want tool_query", row.ShadowRouteKind)
	}
	if row.ShadowRouteMatched {
		t.Fatalf("ShadowRouteMatched = true, want false")
	}
	if row.ProtocolMode != "protocol_shadow" {
		t.Fatalf("ProtocolMode = %q, want protocol_shadow", row.ProtocolMode)
	}
	if row.ProtocolAct != "read_query" {
		t.Fatalf("ProtocolAct = %q, want read_query", row.ProtocolAct)
	}
	if row.ProtocolDomain != "attendance" {
		t.Fatalf("ProtocolDomain = %q, want attendance", row.ProtocolDomain)
	}
	if row.ProtocolOperation != "attendance.query_status" {
		t.Fatalf("ProtocolOperation = %q, want attendance.query_status", row.ProtocolOperation)
	}
	if row.ProtocolValidationCode != "allowed_read_query" {
		t.Fatalf("ProtocolValidationCode = %q, want allowed_read_query", row.ProtocolValidationCode)
	}
	if row.ProtocolBlockedReason != "missing_scope" {
		t.Fatalf("ProtocolBlockedReason = %q, want missing_scope", row.ProtocolBlockedReason)
	}
	if row.ProtocolResolvedSlots != `{"date":"2026-06-06","section":2}` {
		t.Fatalf("ProtocolResolvedSlots = %q, want compact resolved slot JSON", row.ProtocolResolvedSlots)
	}
	if row.ProtocolCandidateCount != 2 {
		t.Fatalf("ProtocolCandidateCount = %d, want 2", row.ProtocolCandidateCount)
	}
	if row.RequestID != "req-v2-1" {
		t.Fatalf("RequestID = %q, want req-v2-1", row.RequestID)
	}
	if row.ConversationID != "conv-v2-1" {
		t.Fatalf("ConversationID = %q, want conv-v2-1", row.ConversationID)
	}
	if row.CompilerStatus != "ok" || row.CompilerLatencyMs != 11 {
		t.Fatalf("compiler fields = %q/%d, want ok/11", row.CompilerStatus, row.CompilerLatencyMs)
	}
	if row.CompilerSource != "fallback" || row.CompilerFallbackReason != "llm_timeout" {
		t.Fatalf("compiler fallback fields = %q/%q, want fallback/llm_timeout", row.CompilerSource, row.CompilerFallbackReason)
	}
	if strings.Contains(row.IntentDraftJSON, "13812345678") {
		t.Fatalf("IntentDraftJSON was not sanitized: %q", row.IntentDraftJSON)
	}
	if row.CatalogValidationCode != "allowed_read_query" {
		t.Fatalf("CatalogValidationCode = %q, want allowed_read_query", row.CatalogValidationCode)
	}
	if row.WorkflowDecision != "single_turn" || row.WorkflowInterruptReason != "new_read_query" {
		t.Fatalf("workflow v2 fields = %q/%q, want single_turn/new_read_query", row.WorkflowDecision, row.WorkflowInterruptReason)
	}
	if row.ResolvedSlotsJSON != `{"date":"2026-06-06","section":2}` {
		t.Fatalf("ResolvedSlotsJSON = %q, want compact resolved slot JSON", row.ResolvedSlotsJSON)
	}
	if row.EntityResolutionStatus != "resolved" {
		t.Fatalf("EntityResolutionStatus = %q, want resolved", row.EntityResolutionStatus)
	}
	if row.PrePolicyResult != "allow" || row.ResourcePolicyResult != "allow" {
		t.Fatalf("policy fields = %q/%q, want allow/allow", row.PrePolicyResult, row.ResourcePolicyResult)
	}
	if row.BlockedReason != "missing_scope" {
		t.Fatalf("BlockedReason = %q, want missing_scope", row.BlockedReason)
	}
	if row.WriteGuardResult != "not_required" || row.IdempotencyKey != "idem-1" {
		t.Fatalf("write guard fields = %q/%q, want not_required/idem-1", row.WriteGuardResult, row.IdempotencyKey)
	}
	if row.ExecutorStatus != "success" || row.RendererName != "response_renderer" {
		t.Fatalf("executor/renderer fields = %q/%q, want success/response_renderer", row.ExecutorStatus, row.RendererName)
	}
	if row.FailureLayer != "" {
		t.Fatalf("FailureLayer = %q, want empty success layer", row.FailureLayer)
	}
	if row.LegacyCalled {
		t.Fatalf("LegacyCalled = true, want false")
	}
	if row.ReplayCaseID != "replay-1" {
		t.Fatalf("ReplayCaseID = %q, want replay-1", row.ReplayCaseID)
	}
	if row.WorkflowIDBefore != "wf-before" {
		t.Fatalf("WorkflowIDBefore = %q, want wf-before", row.WorkflowIDBefore)
	}
	if row.WorkflowIDAfter != "wf-after" {
		t.Fatalf("WorkflowIDAfter = %q, want wf-after", row.WorkflowIDAfter)
	}
	if row.WorkflowTypeBefore != "subscription.start" || row.WorkflowTypeAfter != "subscription.start" {
		t.Fatalf("workflow types = %q/%q, want subscription.start/subscription.start", row.WorkflowTypeBefore, row.WorkflowTypeAfter)
	}
	if row.WorkflowStateBefore != "collect_scope" {
		t.Fatalf("WorkflowStateBefore = %q, want collect_scope", row.WorkflowStateBefore)
	}
	if row.WorkflowStateAfter != "ready" {
		t.Fatalf("WorkflowStateAfter = %q, want ready", row.WorkflowStateAfter)
	}
	if !strings.Contains(row.WorkflowSnapshotBeforeJSON, `"type":"subscription.start"`) ||
		!strings.Contains(row.WorkflowSnapshotBeforeJSON, `"dept_ids"`) {
		t.Fatalf("WorkflowSnapshotBeforeJSON = %q, want replay type and candidates", row.WorkflowSnapshotBeforeJSON)
	}
	if !strings.Contains(row.WorkflowSnapshotAfterJSON, `"state":"ready"`) {
		t.Fatalf("WorkflowSnapshotAfterJSON = %q, want ready state", row.WorkflowSnapshotAfterJSON)
	}
	if row.ResponseKind != "clarify" {
		t.Fatalf("ResponseKind = %q, want clarify", row.ResponseKind)
	}
	if row.ExecutionAllowed {
		t.Fatalf("ExecutionAllowed = true, want false")
	}
	if row.ConversationEvent != "task_follow_up" {
		t.Fatalf("ConversationEvent = %q, want task_follow_up", row.ConversationEvent)
	}
	if row.ActiveTaskType != "subscribe_attendance_push" {
		t.Fatalf("ActiveTaskType = %q, want subscribe_attendance_push", row.ActiveTaskType)
	}
	if row.TaskStatusBefore != "waiting_slots" {
		t.Fatalf("TaskStatusBefore = %q, want waiting_slots", row.TaskStatusBefore)
	}
	if row.TaskStatusAfter != "completed" {
		t.Fatalf("TaskStatusAfter = %q, want completed", row.TaskStatusAfter)
	}
	if row.RetrievalCandidateCount != 3 {
		t.Fatalf("RetrievalCandidateCount = %d, want 3", row.RetrievalCandidateCount)
	}
	if row.RetrievalTopRefs != "请假同步说明#3,系统总览#1" {
		t.Fatalf("RetrievalTopRefs = %q, want %q", row.RetrievalTopRefs, "请假同步说明#3,系统总览#1")
	}
	if row.RetrievalScores != "18,9" {
		t.Fatalf("RetrievalScores = %q, want 18,9", row.RetrievalScores)
	}
	if row.FollowUpMatchedSlots != "dept_names,scope" {
		t.Fatalf("FollowUpMatchedSlots = %q, want dept_names,scope", row.FollowUpMatchedSlots)
	}
	if row.RetrievalFilteredReason != "no_hits" {
		t.Fatalf("RetrievalFilteredReason = %q, want no_hits", row.RetrievalFilteredReason)
	}
	if row.KnowledgeDocTypes != "rule,overview" {
		t.Fatalf("KnowledgeDocTypes = %q, want rule,overview", row.KnowledgeDocTypes)
	}
}

func TestCallLogAdapterBoundsAndSanitizesV2JSONFields(t *testing.T) {
	db := newCallLogTestDB(t)
	adapter := &callLogAdapter{db: db}

	raw := `{"message":"` + strings.Repeat("x", 5000) + `","phone":"13812345678","email":"alice@example.com"}`
	adapter.Write(context.Background(), agenttool.CallLog{
		TenantID:          1,
		UserID:            7,
		ProtocolMode:      "protocol_live",
		IntentDraftJSON:   raw,
		ResolvedSlotsJSON: raw,
		Status:            "success",
	})

	var row model.AgentCallLog
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("query call log: %v", err)
	}
	for name, value := range map[string]string{
		"IntentDraftJSON":   row.IntentDraftJSON,
		"ResolvedSlotsJSON": row.ResolvedSlotsJSON,
	} {
		if len([]rune(value)) > 4000 {
			t.Fatalf("%s length = %d, want <= 4000", name, len([]rune(value)))
		}
		if strings.Contains(value, "13812345678") || strings.Contains(value, "alice@example.com") {
			t.Fatalf("%s was not sanitized: %q", name, value)
		}
	}
}

func TestCallLogAdapterBoundsAndSanitizesNamedLongTextFields(t *testing.T) {
	db := newCallLogTestDB(t)
	adapter := &callLogAdapter{db: db}

	rawText := `token=secret-token 手机 13812345678 邮箱 alice@example.com 身份证 11010519491231002X ` + strings.Repeat("x", 5000)
	adapter.Write(context.Background(), agenttool.CallLog{
		TenantID:              1,
		UserID:                7,
		ProtocolMode:          "protocol_live",
		ProtocolResolvedSlots: `{"raw":"` + rawText + `"}`,
		Question:              rawText,
		Reply:                 rawText,
		Status:                "success",
	})

	var row model.AgentCallLog
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("query call log: %v", err)
	}
	for name, value := range map[string]string{
		"ProtocolResolvedSlots": row.ProtocolResolvedSlots,
		"Question":              row.Question,
		"Reply":                 row.Reply,
	} {
		if len([]rune(value)) > agentCallLogMaxJSONRunes {
			t.Fatalf("%s length = %d, want <= %d", name, len([]rune(value)), agentCallLogMaxJSONRunes)
		}
		if strings.Contains(value, "13812345678") ||
			strings.Contains(value, "alice@example.com") ||
			strings.Contains(value, "11010519491231002X") ||
			strings.Contains(value, "secret-token") {
			t.Fatalf("%s was not sanitized: %q", name, value)
		}
	}
}

func TestCallLogAdapterBoundsProtocolBlockedReason(t *testing.T) {
	db := newCallLogTestDB(t)
	adapter := &callLogAdapter{db: db}

	longReason := strings.Repeat("unknown-intent-reason-", 6)
	adapter.Write(context.Background(), agenttool.CallLog{
		TenantID:              1,
		UserID:                7,
		ProtocolMode:          "protocol_live",
		ProtocolBlockedReason: longReason,
		Status:                "success",
	})

	var row model.AgentCallLog
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("query call log: %v", err)
	}
	if len([]rune(row.ProtocolBlockedReason)) > 64 {
		t.Fatalf("ProtocolBlockedReason length = %d, want <= 64", len([]rune(row.ProtocolBlockedReason)))
	}
	if row.ProtocolBlockedReason == longReason {
		t.Fatalf("ProtocolBlockedReason was not bounded")
	}
}

func TestCallLogAdapterKeepsLargeWorkflowSnapshotJSONValid(t *testing.T) {
	db := newCallLogTestDB(t)
	adapter := &callLogAdapter{db: db}

	var snapshot strings.Builder
	snapshot.WriteString(`{"type":"subscription.start","state":"collect_departments","candidates":{"dept_ids":[`)
	for i := 0; i < 120; i++ {
		if i > 0 {
			snapshot.WriteString(",")
		}
		snapshot.WriteString(`{"id":"`)
		snapshot.WriteString(strconv.Itoa(1000 + i))
		snapshot.WriteString(`","label":"信工25级 replay candidate `)
		snapshot.WriteString(strconv.Itoa(i))
		snapshot.WriteString(`","tenant_id":1}`)
	}
	snapshot.WriteString(`]}}`)

	adapter.Write(context.Background(), agenttool.CallLog{
		TenantID:                   1,
		UserID:                     7,
		WorkflowSnapshotBeforeJSON: snapshot.String(),
		Status:                     "success",
	})

	var row model.AgentCallLog
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("query call log: %v", err)
	}
	if !json.Valid([]byte(row.WorkflowSnapshotBeforeJSON)) {
		t.Fatalf("WorkflowSnapshotBeforeJSON is invalid after persistence: length=%d", len([]rune(row.WorkflowSnapshotBeforeJSON)))
	}
	if !strings.Contains(row.WorkflowSnapshotBeforeJSON, `"dept_ids"`) {
		t.Fatalf("WorkflowSnapshotBeforeJSON = %q, want persisted candidates", row.WorkflowSnapshotBeforeJSON)
	}
}

func TestBoundedAgentCallLogWorkflowSnapshotJSONCapsMySQLTextBytes(t *testing.T) {
	raw := `{"type":"subscription.start","state":"collect_departments","candidates":{"dept_ids":[{"id":"1","label":"` +
		strings.Repeat("信", 25000) +
		`","tenant_id":1}]}}`

	got := boundedAgentCallLogWorkflowSnapshotJSON(raw)

	if len([]byte(got)) > agentCallLogMaxWorkflowSnapshotJSONBytes {
		t.Fatalf("workflow snapshot bytes = %d, want <= %d", len([]byte(got)), agentCallLogMaxWorkflowSnapshotJSONBytes)
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("workflow snapshot JSON is invalid after bounding")
	}
}

type fakeAttendanceDetailService struct {
	detailResp         *dto.AttendanceDetailResponse
	detailErr          error
	textResp           *dto.AttendanceTextResponse
	textErr            error
	rankingResp        *dto.WeeklyAttendanceRankingResponse
	rankingErr         error
	rateResp           *dto.WeeklyAttendanceRateRankingResponse
	rateErr            error
	signErr            error
	detailCalls        int
	recordsByDateCalls int
	lastReq            *dto.AttendanceDetailRequest
	lastSignReq        *dto.SignForUserRequest
	lastRecordsDate    time.Time
	lastRecordsDeptIDs []int64
	recordsByDateResp  []*dto.AttendanceDetailResponse
	recordsByDateErr   error
}

type fakeScheduleService struct {
	listByWeekResp   *service.WeekScheduleResult
	listByWeekErr    error
	lastViewerID     uint
	lastViewerRole   int
	lastTargetUserID uint
	lastWeek         int
}

func (f *fakeScheduleService) ListByWeek(_ context.Context, viewerID uint, viewerRole int, targetUserID uint, week int) (*service.WeekScheduleResult, error) {
	f.lastViewerID = viewerID
	f.lastViewerRole = viewerRole
	f.lastTargetUserID = targetUserID
	f.lastWeek = week
	if f.listByWeekErr != nil {
		return nil, f.listByWeekErr
	}
	return f.listByWeekResp, nil
}

func (f *fakeScheduleService) GetFreeUsersBySlot(context.Context, int, int, int, int64, []config.Period) ([]service.FreeUserSlot, error) {
	return nil, errors.New("unexpected call")
}

func (f *fakeAttendanceDetailService) GetAttendanceDetail(_ context.Context, req *dto.AttendanceDetailRequest) (*dto.AttendanceDetailResponse, error) {
	f.detailCalls++
	if req != nil {
		copied := *req
		f.lastReq = &copied
	}
	if f.detailErr != nil {
		return nil, f.detailErr
	}
	return f.detailResp, nil
}

func (f *fakeAttendanceDetailService) GetAttendanceText(context.Context, *dto.AttendanceTextRequest) (*dto.AttendanceTextResponse, error) {
	if f.textErr != nil {
		return nil, f.textErr
	}
	return f.textResp, nil
}

func (f *fakeAttendanceDetailService) GetWeeklyRanking(context.Context, *dto.WeeklyAttendanceRankingRequest) (*dto.WeeklyAttendanceRankingResponse, error) {
	if f.rankingErr != nil {
		return nil, f.rankingErr
	}
	return f.rankingResp, nil
}

func (f *fakeAttendanceDetailService) GetWeeklyAttendanceRateRanking(context.Context, *dto.WeeklyAttendanceRankingRequest) (*dto.WeeklyAttendanceRateRankingResponse, error) {
	if f.rateErr != nil {
		return nil, f.rateErr
	}
	return f.rateResp, nil
}

func (f *fakeAttendanceDetailService) SignForUsers(_ context.Context, req *dto.SignForUserRequest) (*dto.SignForUserResponse, error) {
	if req != nil {
		copied := *req
		f.lastSignReq = &copied
	}
	if f.signErr != nil {
		return nil, f.signErr
	}
	return &dto.SignForUserResponse{}, nil
}

func (f *fakeAttendanceDetailService) GetAttendanceRecordsByDate(_ context.Context, date time.Time, deptIDs []int64) ([]*dto.AttendanceDetailResponse, error) {
	f.recordsByDateCalls++
	f.lastRecordsDate = date
	f.lastRecordsDeptIDs = append([]int64(nil), deptIDs...)
	if f.recordsByDateErr != nil {
		return nil, f.recordsByDateErr
	}
	return f.recordsByDateResp, nil
}

type fakeAttendanceRecordRepo struct {
	record *model.AttendanceRecord
	err    error
}

func (f *fakeAttendanceRecordRepo) Upsert(context.Context, *model.AttendanceRecord) error {
	return errors.New("unexpected call")
}
func (f *fakeAttendanceRecordRepo) FindByDateSection(context.Context, time.Time, int) (*model.AttendanceRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.record, nil
}
func (f *fakeAttendanceRecordRepo) ListByDate(context.Context, time.Time) ([]model.AttendanceRecord, error) {
	return nil, errors.New("unexpected call")
}
func (f *fakeAttendanceRecordRepo) ListByDateRange(context.Context, time.Time, time.Time) ([]model.AttendanceRecord, error) {
	return nil, errors.New("unexpected call")
}
func (f *fakeAttendanceRecordRepo) FindLatest(context.Context) (*model.AttendanceRecord, error) {
	return nil, errors.New("unexpected call")
}
func (f *fakeAttendanceRecordRepo) FindByID(context.Context, uint) (*model.AttendanceRecord, error) {
	return nil, errors.New("unexpected call")
}

func newCallLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := "file:agent-call-log-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.AgentCallLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

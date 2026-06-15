package agent

import (
	"context"
	"fmt"
	"strings"

	"schedule_server/internal/agent/tools"
)

const operationExecutorKnowledgeTopK = 3

type operationExecutorDeps struct {
	Schedule                SchedulePort
	Attendance              AttendancePort
	AttendanceUserDayStatus AttendanceUserDayStatusPort
	Semester                SemesterPort
	GroupSub                GroupSubPort
	Dept                    DeptPort
	Knowledge               KnowledgePort
}

type OperationExecutionMetrics struct {
	ExecutorName            string
	ToolPool                string
	AnswerMode              answerMode
	RetrievalHitCount       int
	RetrievalCandidateCount int
	SourceRefs              []string
}

type OperationExecutionResult struct {
	Response ResponseModel
	Metrics  OperationExecutionMetrics
}

type operationExecutor struct {
	deps operationExecutorDeps
}

func newOperationExecutor(deps operationExecutorDeps) operationExecutor {
	return operationExecutor{deps: deps}
}

func (e operationExecutor) Execute(ctx context.Context, req OperationRequest) OperationExecutionResult {
	manifest, ok := lookupOperation(req.Operation)
	if !ok {
		return operationExecutionResult(unsupportedOperationResponse(), answerModeReject)
	}
	binding, ok := operationDomainBindings()[manifest.Domain]
	if !ok {
		return operationExecutionResult(unsupportedOperationResponse(), answerModeReject)
	}
	result, handled := binding.Execute(ctx, e.deps, req)
	if !handled {
		return operationExecutionResult(unsupportedOperationResponse(), answerModeReject)
	}
	return result
}

func (e operationExecutor) executeAttendanceQuery(ctx context.Context, req OperationRequest) OperationExecutionResult {
	shape, _ := extractParamString(req.TrustedParams, "query_shape")
	if shape == "user_day_status" {
		return e.executeAttendanceUserDayQuery(ctx, req)
	}
	if shape != "" && shape != "slot_status" {
		return operationExecutionResult(unsupportedOperationResponse(), answerModeReject)
	}
	if e.deps.Attendance == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	date, ok := extractParamString(req.TrustedParams, "date")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"date"}), answerModeToolFirst)
	}
	week, ok := extractParamInt(req.TrustedParams, "week")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"week"}), answerModeToolFirst)
	}
	section, ok := extractParamInt(req.TrustedParams, "section")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"section"}), answerModeToolFirst)
	}
	result, err := e.deps.Attendance.GetAttendanceDetail(ctx, tools.AttendanceQuery{
		Date:    date,
		Week:    week,
		Section: section,
	})
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, Payload: AttendanceStatusPayload{Result: result}}, answerModeToolFirst)
}

func (e operationExecutor) executeAttendanceUserDayQuery(ctx context.Context, req OperationRequest) OperationExecutionResult {
	if e.deps.AttendanceUserDayStatus == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	date, ok := extractParamString(req.TrustedParams, "date")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"date"}), answerModeToolFirst)
	}
	userID, ok := extractParamUint(req.TrustedParams, "user_id")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"user_id"}), answerModeToolFirst)
	}
	status, err := e.deps.AttendanceUserDayStatus.GetUserDayAttendanceStatus(ctx, date, userID)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, Payload: UserDayAttendanceStatusPayload{Status: status}}, answerModeToolFirst)
}

func (e operationExecutor) executeMyScheduleQuery(ctx context.Context, req OperationRequest) OperationExecutionResult {
	if e.deps.Schedule == nil || req.ActorUserID == 0 {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	week, ok := extractParamInt(req.TrustedParams, "week")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"week"}), answerModeToolFirst)
	}
	courses, err := e.deps.Schedule.ListMyScheduleByWeek(ctx, req.ActorUserID, week)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, Payload: ScheduleResultPayload{Week: week, Courses: courses}}, answerModeToolFirst)
}

func (e operationExecutor) executeUserScheduleQuery(ctx context.Context, req OperationRequest) OperationExecutionResult {
	if e.deps.Schedule == nil || req.ActorUserID == 0 {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	week, ok := extractParamInt(req.TrustedParams, "week")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"week"}), answerModeToolFirst)
	}
	userID, ok := extractParamUint(req.TrustedParams, "user_id")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"user_id"}), answerModeToolFirst)
	}
	courses, err := e.deps.Schedule.ListUserScheduleByWeek(ctx, req.ActorUserID, operationRequestActorRole(req), userID, week)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, Payload: ScheduleResultPayload{Week: week, Courses: courses}}, answerModeToolFirst)
}

func (e operationExecutor) executeSubscriptionStart(ctx context.Context, req OperationRequest) OperationExecutionResult {
	if e.deps.GroupSub == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	if conversationType := operationRequestConversationType(req); conversationType != "" && conversationType != "2" {
		return operationExecutionResult(ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅只能在群聊中开启。请在对应群聊里再告诉我。"}, answerModeReject)
	}
	conversationID, ok := extractParamString(req.TrustedParams, "conversation_id")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"conversation_id"}), answerModeToolFirst)
	}
	if req.ConversationID != "" && conversationID != strings.TrimSpace(req.ConversationID) {
		return operationExecutionResult(subscriptionConversationMismatchResponse(), answerModeReject)
	}
	scope, ok := extractParamString(req.TrustedParams, "scope")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"scope"}), answerModeToolFirst)
	}
	var deptIDs []int64
	switch scope {
	case "all":
	case "department":
		deptIDs, ok = extractParamInt64Slice(req.TrustedParams, "dept_ids")
		if !ok {
			return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"dept_ids"}), answerModeToolFirst)
		}
	default:
		return operationExecutionResult(ResponseModel{Kind: ResponseRefuse, RefusalReason: "订阅范围只能是全部人员或指定部门。请重新说明要订阅的范围。"}, answerModeReject)
	}
	if err := e.deps.GroupSub.Subscribe(ctx, req.TenantID, conversationID, operationRequestConversationTitle(req), req.ActorUserID, deptIDs); err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, Payload: OperationStatusPayload{Code: "subscription_started"}}, answerModeToolFirst)
}

func (e operationExecutor) executeSubscriptionCancel(ctx context.Context, req OperationRequest) OperationExecutionResult {
	if e.deps.GroupSub == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	if conversationType := operationRequestConversationType(req); conversationType != "" && conversationType != "2" {
		return operationExecutionResult(ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅只能在群聊中取消。请在对应群聊里再告诉我。"}, answerModeReject)
	}
	conversationID, ok := extractParamString(req.TrustedParams, "conversation_id")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"conversation_id"}), answerModeToolFirst)
	}
	if req.ConversationID != "" && conversationID != strings.TrimSpace(req.ConversationID) {
		return operationExecutionResult(subscriptionConversationMismatchResponse(), answerModeReject)
	}
	if err := e.deps.GroupSub.Unsubscribe(ctx, req.TenantID, conversationID); err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, Payload: OperationStatusPayload{Code: "subscription_cancelled"}}, answerModeToolFirst)
}

func (e operationExecutor) executeSubscriptionStatus(ctx context.Context, req OperationRequest) OperationExecutionResult {
	if conversationType := operationRequestConversationType(req); conversationType != "" && conversationType != "2" {
		return operationExecutionResult(ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅状态只能在群聊中查询。请在对应群聊里再问我。"}, answerModeReject)
	}
	if e.deps.GroupSub == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	conversationID, ok := extractParamString(req.TrustedParams, "conversation_id")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"conversation_id"}), answerModeToolFirst)
	}
	if req.ConversationID != "" && conversationID != strings.TrimSpace(req.ConversationID) {
		return operationExecutionResult(subscriptionConversationMismatchResponse(), answerModeReject)
	}
	info, err := e.deps.GroupSub.GetSubscription(ctx, req.TenantID, conversationID)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, Payload: SubscriptionStatusPayload{Info: info}}, answerModeToolFirst)
}

func subscriptionConversationMismatchResponse() ResponseModel {
	return ResponseModel{Kind: ResponseRefuse, RefusalReason: "只能操作当前群聊的考勤订阅。请在对应群聊里再告诉我。"}
}

func (e operationExecutor) executeListDepartments(ctx context.Context) OperationExecutionResult {
	if e.deps.Dept == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	depts, err := e.deps.Dept.ListDepts(ctx)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	options := make([]ResponseOption, 0, len(depts))
	for _, dept := range depts {
		name := strings.TrimSpace(dept.Name)
		if name == "" {
			continue
		}
		options = append(options, ResponseOption{
			Label: name,
			Value: fmt.Sprint(dept.DeptID),
		})
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseSelectOptions, Options: options}, answerModeToolFirst)
}

func (e operationExecutor) executeRuleExplain(ctx context.Context, req OperationRequest) OperationExecutionResult {
	if e.deps.Knowledge == nil || req.TenantID == 0 {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	topic, ok := extractParamString(req.TrustedParams, "rule_topic")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"rule_topic"}), answerModeToolFirst)
	}
	hits, err := e.deps.Knowledge.Search(ctx, req.TenantID, topic, operationExecutorKnowledgeTopK)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	if len(hits) == 0 {
		result := operationExecutionResult(ResponseModel{Kind: ResponseAnswer, BusinessError: "no_knowledge_hit"}, answerModeKnowledgeOnly)
		result.Metrics.RetrievalCandidateCount = 0
		return result
	}
	result := operationExecutionResult(ResponseModel{Kind: ResponseAnswer, Payload: KnowledgeAnswerPayload{Hits: hits}}, answerModeKnowledgeOnly)
	result.Metrics.RetrievalHitCount = len(hits)
	result.Metrics.RetrievalCandidateCount = len(hits)
	result.Metrics.SourceRefs = collectSourceRefs(hits)
	return result
}

func operationExecutionResult(response ResponseModel, mode answerMode) OperationExecutionResult {
	return OperationExecutionResult{
		Response: response,
		Metrics: OperationExecutionMetrics{
			ExecutorName: "operation_executor",
			ToolPool:     "operation",
			AnswerMode:   mode,
		},
	}
}

func extractParamInt64Slice(params map[string]TrustedParam, key string) ([]int64, bool) {
	value, ok := trustedParamConcreteValue(params, key)
	if !ok {
		return nil, false
	}
	switch typed := value.(type) {
	case []int64:
		if len(typed) == 0 {
			return nil, false
		}
		return append([]int64(nil), typed...), true
	case []int:
		if len(typed) == 0 {
			return nil, false
		}
		result := make([]int64, 0, len(typed))
		for _, item := range typed {
			result = append(result, int64(item))
		}
		return result, true
	default:
		return nil, false
	}
}

func missingOperationParamsResponse(operation string, fields []string) ResponseModel {
	return ResponseModel{Kind: ResponseClarify, Operation: operation, MissingFields: fields}
}

func unavailableOperationResponse() ResponseModel {
	return ResponseModel{Kind: ResponseRefuse, RefusalReason: "抱歉，我当前不能直接执行这个请求。"}
}

func unsupportedOperationResponse() ResponseModel {
	return ResponseModel{Kind: ResponseRefuse, RefusalReason: "抱歉，我当前不能直接执行这个请求。"}
}

func operationErrorResponse() ResponseModel {
	return ResponseModel{Kind: ResponseRefuse, RefusalReason: "执行请求时遇到问题，请稍后再试。"}
}

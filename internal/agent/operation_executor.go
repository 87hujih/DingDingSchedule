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

func (e operationExecutor) Execute(ctx context.Context, uctx *tools.UserContext, req OperationRequest) OperationExecutionResult {
	switch req.Operation {
	case "attendance.query_status":
		return e.executeAttendanceQuery(ctx, req)
	case "schedule.query_my_schedule":
		return e.executeMyScheduleQuery(ctx, uctx, req)
	case "schedule.query_user_schedule":
		return e.executeUserScheduleQuery(ctx, uctx, req)
	case "subscription.start":
		return e.executeSubscriptionStart(ctx, uctx, req)
	case "subscription.cancel":
		return e.executeSubscriptionCancel(ctx, uctx, req)
	case "subscription.query_status":
		return e.executeSubscriptionStatus(ctx, uctx, req)
	case "subscription.list_departments":
		return e.executeListDepartments(ctx)
	case "system.describe_capability":
		return operationExecutionResult(ResponseModel{Kind: ResponseAnswer, Answer: buildHelpReply(uctx)}, answerModeToolFirst)
	case "attendance.describe_capability", "schedule.describe_capability", "subscription.describe_capability", "manual_sign.describe_capability":
		metadata, ok := lookupOperation(req.Operation)
		if !ok {
			return operationExecutionResult(unsupportedOperationResponse(), answerModeReject)
		}
		return operationExecutionResult(ResponseModel{Kind: ResponseAnswer, Answer: buildProtocolCapabilityReply(metadata.Domain, uctx)}, answerModeToolFirst)
	case "attendance.rule_explain", "schedule.rule_explain", "subscription.rule_explain":
		return e.executeRuleExplain(ctx, uctx, req)
	default:
		return operationExecutionResult(unsupportedOperationResponse(), answerModeReject)
	}
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
	week, ok, err := e.resolveOperationWeek(ctx, req.TrustedParams)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
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
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, ResultText: buildAttendanceStatusReply(result)}, answerModeToolFirst)
}

func (e operationExecutor) resolveOperationWeek(ctx context.Context, params map[string]any) (int, bool, error) {
	week, ok := extractParamInt(params, "week")
	if ok {
		return week, true, nil
	}
	if e.deps.Semester == nil {
		return 0, false, nil
	}
	week, _, err := e.deps.Semester.GetCurrentWeek(ctx)
	if err != nil || week <= 0 {
		return 0, false, err
	}
	return week, true, nil
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
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, ResultText: buildUserDayAttendanceStatusReply(status)}, answerModeToolFirst)
}

func (e operationExecutor) executeMyScheduleQuery(ctx context.Context, uctx *tools.UserContext, req OperationRequest) OperationExecutionResult {
	if e.deps.Schedule == nil || uctx == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	week, ok := extractParamInt(req.TrustedParams, "week")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"week"}), answerModeToolFirst)
	}
	courses, err := e.deps.Schedule.ListMyScheduleByWeek(ctx, uctx.UserID, week)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, ResultText: buildScheduleResultReply(week, "", courses)}, answerModeToolFirst)
}

func (e operationExecutor) executeUserScheduleQuery(ctx context.Context, uctx *tools.UserContext, req OperationRequest) OperationExecutionResult {
	if e.deps.Schedule == nil || uctx == nil {
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
	courses, err := e.deps.Schedule.ListUserScheduleByWeek(ctx, uctx.UserID, uctx.UserRole, userID, week)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, ResultText: buildScheduleResultReply(week, "", courses)}, answerModeToolFirst)
}

func (e operationExecutor) executeSubscriptionStart(ctx context.Context, uctx *tools.UserContext, req OperationRequest) OperationExecutionResult {
	if e.deps.GroupSub == nil || uctx == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	if uctx.ConversationType != "2" {
		return operationExecutionResult(ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅只能在群聊中开启。请在对应群聊里再告诉我。"}, answerModeReject)
	}
	conversationID, ok := extractParamString(req.TrustedParams, "conversation_id")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"conversation_id"}), answerModeToolFirst)
	}
	if conversationID != strings.TrimSpace(uctx.ConversationID) {
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
	if err := e.deps.GroupSub.Subscribe(ctx, uctx.TenantID, conversationID, uctx.ConversationTitle, uctx.UserID, deptIDs); err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, ResultText: "已为此群开启考勤推送"}, answerModeToolFirst)
}

func (e operationExecutor) executeSubscriptionCancel(ctx context.Context, uctx *tools.UserContext, req OperationRequest) OperationExecutionResult {
	if e.deps.GroupSub == nil || uctx == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	if uctx.ConversationType != "2" {
		return operationExecutionResult(ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅只能在群聊中取消。请在对应群聊里再告诉我。"}, answerModeReject)
	}
	conversationID, ok := extractParamString(req.TrustedParams, "conversation_id")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"conversation_id"}), answerModeToolFirst)
	}
	if conversationID != strings.TrimSpace(uctx.ConversationID) {
		return operationExecutionResult(subscriptionConversationMismatchResponse(), answerModeReject)
	}
	if err := e.deps.GroupSub.Unsubscribe(ctx, uctx.TenantID, conversationID); err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, ResultText: "已取消此群的考勤自动推送"}, answerModeToolFirst)
}

func (e operationExecutor) executeSubscriptionStatus(ctx context.Context, uctx *tools.UserContext, req OperationRequest) OperationExecutionResult {
	if uctx == nil || uctx.ConversationType != "2" {
		return operationExecutionResult(ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅状态只能在群聊中查询。请在对应群聊里再问我。"}, answerModeReject)
	}
	if e.deps.GroupSub == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	conversationID, ok := extractParamString(req.TrustedParams, "conversation_id")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"conversation_id"}), answerModeToolFirst)
	}
	if conversationID != strings.TrimSpace(uctx.ConversationID) {
		return operationExecutionResult(subscriptionConversationMismatchResponse(), answerModeReject)
	}
	info, err := e.deps.GroupSub.GetSubscription(ctx, uctx.TenantID, conversationID)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	return operationExecutionResult(ResponseModel{Kind: ResponseResult, ResultText: buildSubscriptionStatusReply(info)}, answerModeToolFirst)
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

func (e operationExecutor) executeRuleExplain(ctx context.Context, uctx *tools.UserContext, req OperationRequest) OperationExecutionResult {
	if e.deps.Knowledge == nil || uctx == nil {
		return operationExecutionResult(unavailableOperationResponse(), answerModeReject)
	}
	topic, ok := extractParamString(req.TrustedParams, "rule_topic")
	if !ok {
		return operationExecutionResult(missingOperationParamsResponse(req.Operation, []string{"rule_topic"}), answerModeToolFirst)
	}
	hits, err := e.deps.Knowledge.Search(ctx, uctx.TenantID, topic, operationExecutorKnowledgeTopK)
	if err != nil {
		return operationExecutionResult(operationErrorResponse(), answerModeReject)
	}
	if len(hits) == 0 {
		result := operationExecutionResult(ResponseModel{Kind: ResponseAnswer, BusinessError: "no_knowledge_hit"}, answerModeKnowledgeOnly)
		result.Metrics.RetrievalCandidateCount = 0
		return result
	}
	result := operationExecutionResult(ResponseModel{Kind: ResponseAnswer, Answer: buildKnowledgeAnswer(hits)}, answerModeKnowledgeOnly)
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

func buildScheduleResultReply(week int, userName string, courses []tools.CourseItem) string {
	prefix := fmt.Sprintf("第%d周课表", week)
	if strings.TrimSpace(userName) != "" {
		prefix = fmt.Sprintf("%s第%d周课表", strings.TrimSpace(userName), week)
	}
	if len(courses) == 0 {
		return prefix + "没有查询到课程。"
	}
	parts := make([]string, 0, len(courses))
	for _, course := range courses {
		name := strings.TrimSpace(course.CourseName)
		if name == "" {
			name = "未命名课程"
		}
		detail := name
		if course.DayOfWeek > 0 || course.Section > 0 || strings.TrimSpace(course.Location) != "" {
			detail += "（"
			segments := make([]string, 0, 3)
			if course.DayOfWeek > 0 {
				segments = append(segments, weekdayName(course.DayOfWeek))
			}
			if course.Section > 0 {
				segments = append(segments, fmt.Sprintf("第%d节", course.Section))
			}
			if strings.TrimSpace(course.Location) != "" {
				segments = append(segments, strings.TrimSpace(course.Location))
			}
			detail += strings.Join(segments, " ")
			detail += "）"
		}
		parts = append(parts, detail)
	}
	return prefix + "：" + strings.Join(parts, "；")
}

func buildKnowledgeAnswer(hits []tools.KnowledgeHit) string {
	parts := make([]string, 0, len(hits))
	for _, hit := range hits {
		body := strings.TrimSpace(hit.Body)
		if body == "" {
			continue
		}
		if ref := strings.TrimSpace(hit.SourceRef); ref != "" {
			body += "（来源：" + ref + "）"
		}
		parts = append(parts, body)
	}
	if len(parts) == 0 {
		return "当前租户还没有配置可用于回答这个问题的规则说明。"
	}
	return strings.Join(parts, "\n")
}

func buildUserDayAttendanceStatusReply(status *tools.UserDayAttendanceStatus) string {
	if status == nil {
		return "未查询到该用户当天考勤状态。"
	}
	name := strings.TrimSpace(status.UserName)
	if name == "" {
		name = fmt.Sprintf("用户%d", status.UserID)
	}
	if len(status.Slots) == 0 {
		return fmt.Sprintf("%s %s 未查询到考勤状态。", name, status.Date)
	}
	parts := make([]string, 0, len(status.Slots))
	for _, slot := range status.Slots {
		label := userDayAttendanceStatusLabel(slot)
		if label == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("第%d节%s", slot.Section, label))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%s %s 未查询到考勤状态。", name, status.Date)
	}
	return fmt.Sprintf("%s %s 考勤状态：%s。", name, status.Date, strings.Join(parts, "，"))
}

func userDayAttendanceStatusLabel(slot tools.UserDayAttendanceSlot) string {
	switch slot.Status {
	case "on_time":
		return "正常"
	case "late":
		return "迟到"
	case "leave":
		if strings.TrimSpace(slot.LeaveType) != "" {
			return "请假（" + strings.TrimSpace(slot.LeaveType) + "）"
		}
		return "请假"
	case "not_arrived":
		return "未到"
	case "rest_day":
		return "休息日"
	case "has_course":
		return "有课"
	case "should_attend":
		return "应到"
	default:
		return ""
	}
}

func weekdayName(day int) string {
	switch day {
	case 1:
		return "周一"
	case 2:
		return "周二"
	case 3:
		return "周三"
	case 4:
		return "周四"
	case 5:
		return "周五"
	case 6:
		return "周六"
	case 7:
		return "周日"
	default:
		return fmt.Sprintf("周%d", day)
	}
}

func extractParamInt64Slice(params map[string]any, key string) ([]int64, bool) {
	if params == nil {
		return nil, false
	}
	value, ok := params[key]
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

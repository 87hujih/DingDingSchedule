package agent

import (
	"fmt"
	"strings"

	"schedule_server/internal/agent/tools"
)

type ResponseOption struct {
	Label string
	Value string
}

type ResponseModel struct {
	Kind          ResponseKind
	Operation     string
	Message       string
	Answer        string
	ClarifyReason string
	MissingFields []string
	Options       []ResponseOption
	ResultText    string
	RefusalReason string
	BusinessError string
	Payload       any
}

type AttendanceStatusPayload struct {
	Result *tools.AttendanceResult
}

type UserDayAttendanceStatusPayload struct {
	Status *tools.UserDayAttendanceStatus
}

type ScheduleResultPayload struct {
	Week     int
	UserName string
	Courses  []tools.CourseItem
}

type SubscriptionStatusPayload struct {
	Info *tools.GroupSubInfo
}

type WriteStatus string

const (
	WriteStatusCreated       WriteStatus = "created"
	WriteStatusAlreadyExists WriteStatus = "already_exists"
	WriteStatusUpdated       WriteStatus = "updated"
	WriteStatusNoOp          WriteStatus = "no_op"
)

type OperationStatusPayload struct {
	Code        string
	Status      WriteStatus
	PushEnabled *bool
}

type KnowledgeAnswerPayload struct {
	Hits []tools.KnowledgeHit
}

type CapabilityAnswerPayload struct {
	Domain           BusinessDomain
	UserRole         int
	ConversationType string
}

// renderProtocolResponse renders a structured protocol response into plain text.
func renderProtocolResponse(model ResponseModel) string {
	switch model.Kind {
	case ResponseAnswer:
		if strings.TrimSpace(model.BusinessError) == "no_knowledge_hit" {
			return "当前租户还没有配置可用于回答这个问题的规则说明。"
		}
		if text := renderAnswerPayload(model.Payload); text != "" {
			return text
		}
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		if text := safeProtocolText(model.Answer); text != "" {
			return text
		}
		return "我可以先回答你的能力、规则或查询问题。"
	case ResponseClarify:
		if reply := renderMissingFieldsClarify(model.Operation, model.MissingFields); reply != "" {
			return reply
		}
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		switch strings.TrimSpace(model.ClarifyReason) {
		case "unknown_intent":
			return "请再明确一下你的问题。你可以说：查询今天第二节考勤状态、查我的课表、开启本群考勤订阅。"
		case "subscription_missing_fields":
			return "请选择订阅范围：全部人员 / 指定部门。"
		case "missing_attendance_fields":
			return "请补充要查询的日期和第几节。"
		default:
			return "请再明确一下你的需求。"
		}
	case ResponseSelectOptions:
		if len(model.Options) == 0 {
			return "请从可选项中明确选择后再继续。"
		}
		labels := make([]string, 0, len(model.Options))
		for _, option := range model.Options {
			label := strings.TrimSpace(option.Label)
			if label == "" {
				label = strings.TrimSpace(option.Value)
			}
			if label == "" {
				continue
			}
			labels = append(labels, label)
		}
		if len(labels) == 0 {
			return "请从可选项中明确选择后再继续。"
		}
		return "请从这些选项中明确选择：\n" + renderNumberedOptions(labels)
	case ResponseResult:
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		if text := renderResultPayload(model.Payload); text != "" {
			return text
		}
		if text := safeProtocolText(model.ResultText); text != "" {
			return text
		}
		return "操作已完成。"
	case ResponseRefuse:
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		if text := safeProtocolText(model.RefusalReason); text != "" {
			return text
		}
		return "抱歉，我当前不能直接执行这个请求。"
	case ResponseConfirm:
		if text := safeProtocolText(model.Message); text != "" {
			return text
		}
		return "请确认是否执行该操作。"
	default:
		return "请再明确一下你的需求。"
	}
}

func renderAnswerPayload(payload any) string {
	switch typed := payload.(type) {
	case CapabilityAnswerPayload:
		uctx := &tools.UserContext{
			UserRole:         typed.UserRole,
			ConversationType: typed.ConversationType,
		}
		if typed.Domain == DomainSystem {
			return buildHelpReply(uctx)
		}
		return buildProtocolCapabilityReply(typed.Domain, uctx)
	case KnowledgeAnswerPayload:
		return buildKnowledgeAnswer(typed.Hits)
	default:
		return ""
	}
}

func renderResultPayload(payload any) string {
	switch typed := payload.(type) {
	case AttendanceStatusPayload:
		return buildAttendanceStatusReply(typed.Result)
	case UserDayAttendanceStatusPayload:
		return buildUserDayAttendanceStatusReply(typed.Status)
	case ScheduleResultPayload:
		return buildScheduleResultReply(typed.Week, typed.UserName, typed.Courses)
	case SubscriptionStatusPayload:
		return buildSubscriptionStatusReply(typed.Info)
	case OperationStatusPayload:
		return renderOperationStatus(typed)
	default:
		return ""
	}
}

func renderNumberedOptions(labels []string) string {
	lines := make([]string, 0, len(labels))
	for idx, label := range labels {
		lines = append(lines, fmt.Sprintf("%d. %s", idx+1, label))
	}
	return strings.Join(lines, "\n")
}

func renderOperationStatus(payload OperationStatusPayload) string {
	switch payload.Code {
	case "subscription_started":
		if payload.PushEnabled != nil && !*payload.PushEnabled {
			switch payload.Status {
			case WriteStatusUpdated:
				return "已更新此群考勤推送范围，但后台已暂停自动推送。请联系管理员在后台恢复。"
			default:
				return "此群已订阅考勤推送，但后台已暂停自动推送。请联系管理员在后台恢复。"
			}
		}
		switch payload.Status {
		case WriteStatusAlreadyExists:
			return "此群已经开启考勤推送，无需重复开启。"
		case WriteStatusUpdated:
			return "已更新此群考勤推送范围。"
		case WriteStatusNoOp:
			return "此群考勤推送未发生变化。"
		default:
			return "已为此群开启考勤推送"
		}
	case "subscription_cancelled":
		switch payload.Status {
		case WriteStatusNoOp:
			return "当前群还没有开启考勤自动推送，无需取消。"
		default:
			return "已取消此群的考勤自动推送"
		}
	default:
		return ""
	}
}

func buildAttendanceStatusReply(result *tools.AttendanceResult) string {
	if result == nil {
		return "未查询到相关考勤数据。"
	}

	notArrivedLabel := "未到"
	if result.ViewMode == "current" {
		notArrivedLabel = "当前未到"
	}

	return fmt.Sprintf("%s第%d节考勤状态：应到%d人，正常%d人，迟到%d人，请假%d人，%s%d人。",
		result.Date,
		result.Section,
		result.ShouldAttend,
		result.OnTimeCount,
		result.LateCount,
		result.LeaveCount,
		notArrivedLabel,
		result.AbsentCount,
	)
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

func buildSubscriptionStatusReply(info *tools.GroupSubInfo) string {
	if info == nil || !info.Subscribed {
		return "当前群还没有订阅考勤推送。"
	}
	if !info.PushEnabled {
		return "当前群已订阅考勤推送，但后台已暂停自动推送。请联系管理员在后台恢复。"
	}

	reply := "当前群已订阅考勤推送。"
	if len(info.DeptIDs) > 0 {
		reply += "目前是按指定部门范围推送。"
	}
	return reply
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

func renderMissingFieldsClarify(operation string, fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	if operation == "attendance.query_status" && containsField(fields, "date") && containsField(fields, "section") {
		return "请补充要查询哪一天和第几节。"
	}
	if operation == "subscription.start" && containsField(fields, "scope") {
		return "请选择订阅范围：全部人员 / 指定部门。"
	}
	if operation == "attendance.query_status" && containsField(fields, "section") {
		return "请补充要查询第几节。"
	}
	if operation == "attendance.query_status" && containsField(fields, "date") {
		return "请补充要查询哪一天。"
	}
	return ""
}

func containsField(fields []string, target string) bool {
	for _, field := range fields {
		if field == target {
			return true
		}
	}
	return false
}

func safeProtocolText(value string) string {
	text := strings.TrimSpace(value)
	if text == "" || looksLikeInternalPayload(text) {
		return ""
	}
	return text
}

func looksLikeInternalPayload(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "error_code") || strings.Contains(lower, "internalerror") {
		return true
	}
	if (strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}")) ||
		(strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]")) {
		return true
	}
	return false
}

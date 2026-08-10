package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

func (p protocolLivePipeline) attendanceRequest(ctx context.Context, message string, draft ProtocolDraft, tenantID uint) (OperationRequest, ResponseModel, bool) { //nolint:gocyclo // Attendance request assembly keeps slot resolution order explicit.
	manifest, ok := lookupOperation(draft.Operation)
	if !ok {
		return OperationRequest{}, unsupportedOperationResponse(), false
	}
	resolveCtx := EntityResolveContext{TenantID: tenantID}
	trusted := trustedEntities{UserRole: 0, TenantID: tenantID, TrustedParams: map[string]TrustedParam{}}
	if raw := draftRawSlotForTarget(manifest, draft, "query_shape"); raw != "" {
		trusted.QueryShape = raw
		trusted.TrustedParams["query_shape"] = trustedParamFromContext(resolveCtx.RawSlot("query_shape", raw), "query_shape", raw, "query_shape_slot")
	}
	now := p.now()
	dateRaw := draftRawSlotForTarget(manifest, draft, "date")
	dateInput := resolveCtx.RawSlot("date", dateRaw)
	if dateRaw == "" {
		dateInput = resolveCtx.Default("date")
	}
	date := resolveDateParam(dateInput, SlotDefaultToday, func() time.Time { return now })
	if date.Status == ResolveResolved {
		trusted.Date = fmt.Sprint(date.Value)
		trusted.TrustedParams["date"] = date.Param
	}

	sectionRaw := draftRawSlotForTarget(manifest, draft, "section")
	section := resolveSectionParam(resolveCtx.RawSlot("section", sectionRaw), p.schedulePeriods(ctx), func() time.Time { return now })
	if section.Status == ResolveResolved {
		if value, ok := section.Value.(int); ok {
			trusted.Section = value
			trusted.TrustedParams["section"] = section.Param
		}
	}

	userRaw := draftRawSlotForTarget(manifest, draft, "user_id")
	if userRaw != "" && p.deps.User != nil {
		users, err := p.deps.User.SearchByName(ctx, userRaw)
		if err == nil {
			resolved := resolveUserParam(resolveCtx.RawSlot("user_id", userRaw), users)
			switch resolved.Status {
			case ResolveResolved:
				if userID, ok := resolved.Value.(uint); ok {
					trusted.UserID = userID
					trusted.QueryShape = "user_day_status"
					trusted.TrustedParams["user_id"] = resolved.Param
					trusted.TrustedParams["query_shape"] = trustedParam("query_shape", "user_day_status", tenantID, TrustedParamSource{
						Kind:     TrustedParamSourceDerived,
						Resolver: "attendance_user_resolver",
					})
				}
			case ResolveAmbiguous:
				return OperationRequest{}, ResponseModel{Kind: ResponseSelectOptions, Options: responseOptionsFromEntityCandidates(resolved.Candidates)}, false
			}
		}
	}

	req, blocked := buildOperationRequest(draft, trusted)
	if blocked {
		return OperationRequest{}, ResponseModel{Kind: ResponseClarify, ClarifyReason: "missing_attendance_fields"}, false
	}
	if _, ok := req.TrustedParams["week"]; !ok && p.deps.Semester != nil {
		if week, _, err := p.deps.Semester.GetCurrentWeek(ctx); err == nil && week > 0 {
			req.TrustedParams["week"] = trustedParam("week", week, tenantID, TrustedParamSource{
				Kind:     TrustedParamSourceDefault,
				Resolver: "semester_default",
			})
		}
	}
	return req, ResponseModel{}, true
}

func (p protocolLivePipeline) schedulePeriods(ctx context.Context) []tools.PeriodInfo {
	if p.deps.SchedulePeriod == nil {
		return nil
	}
	periods, _, err := p.deps.SchedulePeriod.GetScheduleInfo(ctx)
	if err != nil {
		return nil
	}
	return periods
}

func (p protocolLivePipeline) scheduleRequest(ctx context.Context, message string, draft ProtocolDraft, tenantID uint) (OperationRequest, ResponseModel, bool) {
	manifest, ok := lookupOperation(draft.Operation)
	if !ok {
		return OperationRequest{}, unsupportedOperationResponse(), false
	}
	resolveCtx := EntityResolveContext{TenantID: tenantID}
	trusted := trustedEntities{UserRole: 0, TenantID: tenantID, TrustedParams: map[string]TrustedParam{}}
	weekRaw := draftRawSlotForTarget(manifest, draft, "week")
	weekInput := resolveCtx.RawSlot("week", weekRaw)
	if weekRaw == "" {
		weekInput = resolveCtx.Default("week")
	}
	week := resolveWeekParam(ctx, weekInput, SlotDefaultCurrentWeek, p.deps.Semester)
	if week.Status == ResolveResolved {
		if value, ok := week.Value.(int); ok {
			trusted.Week = value
			trusted.TrustedParams["week"] = week.Param
		}
	}

	if operationRequiresTrustedParam(manifest, "user_id") {
		userRaw := draftRawSlotForTarget(manifest, draft, "user_id")
		if userRaw == "" || p.deps.User == nil {
			return OperationRequest{}, missingOperationParamsResponse(draft.Operation, []string{"user_id"}), false
		}
		users, err := p.deps.User.SearchByName(ctx, userRaw)
		if err != nil {
			return OperationRequest{}, operationErrorResponse(), false
		}
		resolved := resolveUserParam(resolveCtx.RawSlot("user_id", userRaw), users)
		switch resolved.Status {
		case ResolveResolved:
			if userID, ok := resolved.Value.(uint); ok {
				trusted.UserID = userID
				trusted.TrustedParams["user_id"] = resolved.Param
			}
		case ResolveAmbiguous:
			return OperationRequest{}, ResponseModel{Kind: ResponseSelectOptions, Options: responseOptionsFromEntityCandidates(resolved.Candidates)}, false
		default:
			return OperationRequest{}, missingOperationParamsResponse(draft.Operation, []string{"user_id"}), false
		}
	}

	req, blocked := buildOperationRequest(draft, trusted)
	if blocked {
		missing := missingRequiredTrustedParams(manifest, trusted)
		if len(missing) == 0 {
			missing = paramNames(manifest.RequiredTrustedParams)
		}
		return OperationRequest{}, missingOperationParamsResponse(draft.Operation, missing), false
	}
	return req, ResponseModel{}, true
}

func operationRequiresTrustedParam(manifest OperationManifest, field string) bool {
	return paramSpecListContains(manifest.RequiredTrustedParams, field) ||
		queryShapesRequireTrustedParam(manifest.QueryShapes, field)
}

func queryShapesRequireTrustedParam(shapes []QueryShapeMetadata, field string) bool {
	for _, shape := range shapes {
		if paramSpecListContains(shape.RequiredTrustedParams, field) {
			return true
		}
	}
	return false
}

func paramSpecListContains(params []ParamSpec, field string) bool {
	for _, param := range params {
		if param.Name == field {
			return true
		}
	}
	return false
}

func missingRequiredTrustedParams(manifest OperationManifest, trusted trustedEntities) []string {
	required := manifest.RequiredTrustedParams
	if len(manifest.QueryShapes) > 0 {
		if shape, ok := selectQueryShape(manifest, trusted); ok {
			required = shape.RequiredTrustedParams
		}
	}

	missing := make([]string, 0, len(required))
	for _, param := range required {
		if _, ok := trustedParamValue(trusted, param.Name); !ok {
			missing = append(missing, param.Name)
		}
	}
	return missing
}

func protocolRuleTopic(message string, draft ProtocolDraft) string {
	if manifest, ok := lookupOperation(draft.Operation); ok {
		if raw := draftRawSlotForTarget(manifest, draft, "rule_topic"); raw != "" {
			return raw
		}
	}
	return strings.TrimSpace(message)
}

func draftSlotRaw(draft ProtocolDraft, field string) string {
	if draft.Slots == nil {
		return ""
	}
	slot, ok := draft.Slots[field]
	if !ok {
		return ""
	}
	return strings.TrimSpace(slot.Raw)
}

func draftRawSlotForTarget(manifest OperationManifest, draft ProtocolDraft, targetParam string) string {
	for _, spec := range manifest.Recognition.RawSlots {
		if spec.TargetParam != targetParam {
			continue
		}
		if raw := draftSlotRaw(draft, spec.RawName); raw != "" {
			return raw
		}
	}
	return ""
}

func messageDateSignal(message string) string {
	for _, candidate := range []string{"今天", "昨天", "明天"} {
		if strings.Contains(message, candidate) {
			return candidate
		}
	}
	return ""
}

func extractSectionToken(message string) string {
	for i := 1; i <= 12; i++ {
		token := fmt.Sprintf("第%d节", i)
		if strings.Contains(message, token) {
			return token
		}
	}
	for _, token := range []string{"第一节", "第二节", "第三节", "第四节", "第五节", "第六节", "第七节", "第八节", "第九节", "第十节"} {
		if strings.Contains(message, token) {
			return token
		}
	}
	return ""
}

func extractWeekToken(message string) string {
	normalized := normalizeQuery(message)
	for i := 1; i <= 30; i++ {
		token := fmt.Sprintf("第%d周", i)
		if strings.Contains(normalized, token) {
			return token
		}
	}
	for _, token := range []string{"本周", "这周", "下周"} {
		if strings.Contains(normalized, token) {
			return token
		}
	}
	return ""
}

type scheduleSubjectKind uint8

const (
	scheduleSubjectUnspecified scheduleSubjectKind = iota
	scheduleSubjectSelf
	scheduleSubjectNamed
	scheduleSubjectUnresolved
)

func parseScheduleSubject(message string) (scheduleSubjectKind, string) {
	value := normalizeQuery(message)
	if value == "" {
		return scheduleSubjectUnspecified, ""
	}
	explicitSelf := containsAny(value, []string{"我的", "我这周", "我本周", "给我查", "帮我查下自己"})
	for _, week := range []string{extractWeekToken(value), "本周", "这周", "下周"} {
		if week != "" {
			value = strings.ReplaceAll(value, week, "")
		}
	}
	cut := len(value)
	for _, token := range []string{"课程信息", "课程安排", "上什么课", "有哪些课", "课表", "课程"} {
		if index := strings.Index(value, token); index >= 0 && index < cut {
			cut = index
		}
	}
	value = strings.TrimSuffix(value[:cut], "的")
	lastActionEnd := -1
	for _, action := range []string{"查询", "查看", "看看", "想知道", "知道", "了解", "查", "看"} {
		if index := strings.LastIndex(value, action); index >= 0 && index+len(action) > lastActionEnd {
			lastActionEnd = index + len(action)
		}
	}
	if lastActionEnd >= 0 {
		value = value[lastActionEnd:]
	}
	for _, filler := range []string{"一下", "下", "一查", "一看"} {
		value = strings.TrimPrefix(value, filler)
	}
	value = strings.Trim(value, "，。！？,.!?：: ")
	if value == "" {
		if explicitSelf {
			return scheduleSubjectSelf, ""
		}
		return scheduleSubjectUnspecified, ""
	}
	if value == "我" || value == "本人" || value == "自己" {
		return scheduleSubjectSelf, ""
	}
	if containsAny(value, []string{"你", "他", "她", "他们", "她们", "对方", "谁", "某人"}) ||
		containsAny(value, []string{"查询", "查看", "看看", "课程", "课表", "规则", "什么", "如何", "怎么"}) || len([]rune(value)) > 32 {
		return scheduleSubjectUnresolved, ""
	}
	return scheduleSubjectNamed, value

}

func extractScheduleUserName(message string) string {
	kind, name := parseScheduleSubject(message)
	if kind != scheduleSubjectNamed {
		return ""
	}
	return name
}

func responseOptionsFromEntityCandidates(candidates []EntityCandidate) []ResponseOption {
	options := make([]ResponseOption, 0, len(candidates))
	for _, candidate := range candidates {
		options = append(options, ResponseOption{
			Label: candidate.Label,
			Value: candidate.ID,
		})
	}
	return options
}

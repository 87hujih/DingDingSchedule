package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

func (p protocolLivePipeline) attendanceRequest(ctx context.Context, message string, draft ProtocolDraft, tenantID uint) (OperationRequest, ResponseModel, bool) {
	resolveCtx := EntityResolveContext{TenantID: tenantID}
	trusted := trustedEntities{UserRole: 0, TenantID: tenantID, TrustedParams: map[string]TrustedParam{}}
	if raw := draftSlotRaw(draft, "query_shape"); raw != "" {
		trusted.QueryShape = raw
		trusted.TrustedParams["query_shape"] = trustedParamFromContext(resolveCtx.RawSlot("query_shape", raw), "query_shape", raw, "query_shape_slot")
	}
	now := p.now()
	dateRaw := firstNonEmpty(draftSlotRaw(draft, "date"), extractDateToken(message))
	if dateRaw == "" && hasDateSignal(message) {
		dateRaw = messageDateSignal(message)
	}
	dateInput := resolveCtx.RawSlot("date", dateRaw)
	if dateRaw == "" {
		dateInput = resolveCtx.Default("date")
	}
	date := resolveDateParam(dateInput, SlotDefaultToday, func() time.Time { return now })
	if date.Status == ResolveResolved {
		trusted.Date = fmt.Sprint(date.Value)
		trusted.TrustedParams["date"] = date.Param
	}

	sectionRaw := firstNonEmpty(draftSlotRaw(draft, "section"), extractSectionToken(message))
	section := resolveSectionParam(resolveCtx.RawSlot("section", sectionRaw), p.schedulePeriods(ctx), func() time.Time { return now })
	if section.Status == ResolveResolved {
		if value, ok := section.Value.(int); ok {
			trusted.Section = value
			trusted.TrustedParams["section"] = section.Param
		}
	}

	userRaw := firstNonEmpty(draftSlotRaw(draft, "user"), draftSlotRaw(draft, "user_name"))
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
	weekRaw := firstNonEmpty(draftSlotRaw(draft, "week"), extractWeekToken(message))
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
		userRaw := firstNonEmpty(draftSlotRaw(draft, "user"), draftSlotRaw(draft, "user_name"), extractScheduleUserName(message))
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
	if raw := draftSlotRaw(draft, "rule_topic"); raw != "" {
		return raw
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
	if strings.Contains(normalized, "本周") {
		return "本周"
	}
	return ""
}

func extractScheduleUserName(message string) string {
	value := strings.TrimSpace(message)
	value = strings.TrimPrefix(value, "查")
	value = strings.TrimPrefix(value, "查询")
	if idx := strings.Index(value, "第"); idx > 0 {
		value = value[:idx]
	}
	value = strings.ReplaceAll(value, "课表", "")
	value = strings.ReplaceAll(value, "的", "")
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "我") {
		return ""
	}
	return value
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

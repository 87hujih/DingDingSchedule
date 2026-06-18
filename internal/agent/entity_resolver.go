package agent

import (
	"context"
	"strconv"
	"strings"
	"time"

	agenttools "schedule_server/internal/agent/tools"
)

type ResolveStatus string

const (
	ResolveResolved     ResolveStatus = "resolved"
	ResolveAmbiguous    ResolveStatus = "ambiguous"
	ResolveNotFound     ResolveStatus = "not_found"
	ResolveInvalidShape ResolveStatus = "invalid_shape"
)

type ResolveResult struct {
	Field      string
	Status     ResolveStatus
	Value      any
	Values     map[string]any
	Param      TrustedParam
	Candidates []EntityCandidate
	Reason     string
}

type EntityCandidate struct {
	ID       string
	Label    string
	Value    any
	TenantID uint
	Source   TrustedParamSource
}

type entityContext struct {
	TenantID    uint
	Raw         string
	Departments []agenttools.DeptItem
	Users       []agenttools.UserInfo
}

type entityResolution struct {
	Status     ResolveStatus
	Department *agenttools.DeptItem
	User       *agenttools.UserInfo
	Candidates []string
}

type EntityResolveContext struct {
	TenantID uint
	Field    string
	Raw      string
	Source   TrustedParamSourceKind
}

func (ctx EntityResolveContext) RawSlot(field string, raw string) EntityResolveContext {
	ctx.Field = field
	ctx.Raw = strings.TrimSpace(raw)
	ctx.Source = TrustedParamSourceRawSlot
	return ctx
}

func (ctx EntityResolveContext) Default(field string) EntityResolveContext {
	ctx.Field = field
	ctx.Raw = ""
	ctx.Source = TrustedParamSourceDefault
	return ctx
}

func (ctx EntityResolveContext) Runtime(field string, value string) EntityResolveContext {
	ctx.Field = field
	ctx.Raw = strings.TrimSpace(value)
	ctx.Source = TrustedParamSourceRuntime
	return ctx
}

func (ctx EntityResolveContext) Candidate(field string, value string) EntityResolveContext {
	ctx.Field = field
	ctx.Raw = strings.TrimSpace(value)
	ctx.Source = TrustedParamSourceCandidate
	return ctx
}

func trustedParam(field string, value any, tenantID uint, source TrustedParamSource) TrustedParam {
	return TrustedParam{
		Field:    field,
		Value:    value,
		Source:   source,
		TenantID: tenantID,
	}
}

func trustedParamFromContext(ctx EntityResolveContext, field string, value any, resolver string) TrustedParam {
	source := ctx.Source
	if source == "" {
		source = TrustedParamSourceRawSlot
	}
	return trustedParam(field, value, ctx.TenantID, TrustedParamSource{
		Kind:     source,
		Raw:      rawForTrustedSource(source, ctx.Raw),
		Resolver: resolver,
	})
}

func rawForTrustedSource(source TrustedParamSourceKind, raw string) string {
	if source == TrustedParamSourceRawSlot || source == TrustedParamSourceCandidate {
		return raw
	}
	return ""
}

func resolveDateParam(ctx EntityResolveContext, defaultValue SlotDefault, clock func() time.Time) ResolveResult {
	result := resolveDateSlot(ctx.Raw, defaultValue, clock)
	if result.Status == ResolveResolved {
		result.Param = trustedParamFromContext(ctx, "date", result.Value, "date_slot")
	}
	return result
}

func resolveWeekParam(ctx context.Context, input EntityResolveContext, defaultValue SlotDefault, semester weekProvider) ResolveResult {
	result := resolveWeekSlot(ctx, input.Raw, defaultValue, semester)
	if result.Status == ResolveResolved {
		result.Param = trustedParamFromContext(input, "week", result.Value, "week_slot")
	}
	return result
}

func resolveSectionParam(ctx EntityResolveContext, periods []agenttools.PeriodInfo, clock func() time.Time) ResolveResult {
	result := resolveSectionSlot(ctx.Raw, periods, clock)
	if result.Status == ResolveResolved {
		result.Param = trustedParamFromContext(ctx, "section", result.Value, "section_slot")
	}
	return result
}

func resolveConversationParam(ctx EntityResolveContext) ResolveResult {
	value := strings.TrimSpace(ctx.Raw)
	if value == "" {
		return ResolveResult{Field: ctx.Field, Status: ResolveNotFound, Reason: "conversation_not_found"}
	}
	result := ResolveResult{Field: ctx.Field, Status: ResolveResolved, Value: value}
	result.Param = trustedParamFromContext(ctx, ctx.Field, value, "conversation_runtime")
	return result
}

func resolveUserParam(ctx EntityResolveContext, users []agenttools.UserInfo) ResolveResult {
	return resolveUserSlotWithContext(ctx, users)
}

func resolveDepartmentParam(ctx EntityResolveContext, departments []agenttools.DeptItem) ResolveResult {
	return resolveDepartmentSlotWithContext(ctx, departments)
}

// resolveDepartment resolves department.
func resolveDepartment(ctx entityContext) entityResolution {
	raw := strings.TrimSpace(ctx.Raw)
	if !looksLikeEntityInput(raw) {
		return entityResolution{Status: ResolveInvalidShape}
	}
	departments := filterDepartmentsByTenant(ctx.Departments, ctx.TenantID)

	exactMatches := exactDepartmentMatches(raw, departments)
	if len(exactMatches) > 0 {
		return departmentResolution(exactMatches)
	}
	normalizedMatches := normalizedDepartmentMatches(raw, departments)
	if len(normalizedMatches) > 0 {
		return departmentResolution(normalizedMatches)
	}

	return departmentResolution(departmentCandidates(raw, departments))
}

func departmentResolution(matches []agenttools.DeptItem) entityResolution {
	switch len(matches) {
	case 0:
		return entityResolution{Status: ResolveNotFound}
	case 1:
		dept := matches[0]
		return entityResolution{Status: ResolveResolved, Department: &dept}
	default:
		names := make([]string, 0, len(matches))
		for _, candidate := range matches {
			names = append(names, candidate.Name)
		}
		return entityResolution{Status: ResolveAmbiguous, Candidates: names}
	}
}

// resolveUser resolves user.
func resolveUser(ctx entityContext) entityResolution {
	raw := strings.TrimSpace(ctx.Raw)
	if !looksLikeEntityInput(raw) {
		return entityResolution{Status: ResolveInvalidShape}
	}
	users := filterUsersByTenant(ctx.Users, ctx.TenantID)

	exactMatches := make([]agenttools.UserInfo, 0)
	for _, user := range users {
		if strings.TrimSpace(user.Name) == raw {
			exactMatches = append(exactMatches, user)
		}
	}
	if len(exactMatches) > 0 {
		return userResolution(exactMatches)
	}

	matches := make([]agenttools.UserInfo, 0)
	for _, candidate := range users {
		name := normalizeEntityName(candidate.Name)
		if containsEntityVariant(name, raw) {
			matches = append(matches, candidate)
		}
	}

	return userResolution(matches)
}

func userResolution(matches []agenttools.UserInfo) entityResolution {
	switch len(matches) {
	case 0:
		return entityResolution{Status: ResolveNotFound}
	case 1:
		user := matches[0]
		return entityResolution{Status: ResolveResolved, User: &user}
	default:
		names := make([]string, 0, len(matches))
		for _, candidate := range matches {
			names = append(names, candidate.Name)
		}
		return entityResolution{Status: ResolveAmbiguous, Candidates: names}
	}
}

// resolveDate resolves date.
func resolveDate(raw string) (string, bool) {
	return resolveDateWithClock(raw, time.Now)
}

// resolveDateSlot resolves date slot with catalog defaults.
func resolveDateSlot(raw string, defaultValue SlotDefault, clock func() time.Time) ResolveResult {
	if clock == nil {
		clock = time.Now
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		if defaultValue == SlotDefaultToday {
			return ResolveResult{Field: "date", Status: ResolveResolved, Value: clock().Format("2006-01-02")}
		}
		return ResolveResult{Field: "date", Status: ResolveNotFound, Reason: "missing_date"}
	}
	dateValue, ok := resolveDateWithClock(value, clock)
	if !ok {
		return ResolveResult{Field: "date", Status: ResolveInvalidShape, Reason: "invalid_date"}
	}
	return ResolveResult{Field: "date", Status: ResolveResolved, Value: dateValue}
}

type weekProvider interface {
	GetCurrentWeek(ctx context.Context) (week int, totalWeeks int, err error)
}

// resolveWeekSlot resolves teaching week slot with catalog defaults.
func resolveWeekSlot(ctx context.Context, raw string, defaultValue SlotDefault, semester weekProvider) ResolveResult {
	value := strings.TrimSpace(raw)
	if value == "" || value == "本周" {
		if defaultValue != SlotDefaultCurrentWeek {
			if value == "" {
				return ResolveResult{Field: "week", Status: ResolveNotFound, Reason: "missing_week"}
			}
		}
		if semester == nil {
			return ResolveResult{Field: "week", Status: ResolveNotFound, Reason: "missing_semester_provider"}
		}
		week, _, err := semester.GetCurrentWeek(ctx)
		if err != nil || week <= 0 {
			return ResolveResult{Field: "week", Status: ResolveNotFound, Reason: "current_week_unavailable"}
		}
		return ResolveResult{Field: "week", Status: ResolveResolved, Value: week}
	}
	week, ok := parseWeekNumber(value)
	if !ok {
		return ResolveResult{Field: "week", Status: ResolveInvalidShape, Reason: "invalid_week"}
	}
	return ResolveResult{Field: "week", Status: ResolveResolved, Value: week}
}

// resolveSectionSlot resolves section slot, including "本节" from active periods.
func resolveSectionSlot(raw string, periods []agenttools.PeriodInfo, clock func() time.Time) ResolveResult {
	if clock == nil {
		clock = time.Now
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return ResolveResult{Field: "section", Status: ResolveNotFound, Reason: "missing_section"}
	}
	if value == "本节" {
		section, ok := currentSection(periods, clock())
		if !ok {
			return ResolveResult{Field: "section", Status: ResolveNotFound, Reason: "current_section_unavailable"}
		}
		return ResolveResult{Field: "section", Status: ResolveResolved, Value: section}
	}
	section, ok := parseSectionNumber(value)
	if !ok {
		return ResolveResult{Field: "section", Status: ResolveInvalidShape, Reason: "invalid_section"}
	}
	return ResolveResult{Field: "section", Status: ResolveResolved, Value: section}
}

// resolveUserSlot resolves user name to canonical user id.
func resolveUserSlot(raw string, users []agenttools.UserInfo) ResolveResult {
	return resolveUserSlotWithContext(EntityResolveContext{}.RawSlot("user_id", raw), users)
}

func resolveUserSlotWithContext(ctx EntityResolveContext, users []agenttools.UserInfo) ResolveResult {
	value := strings.TrimSpace(ctx.Raw)
	if !looksLikeEntityInput(value) {
		return ResolveResult{Field: ctx.Field, Status: ResolveInvalidShape, Reason: "invalid_user_shape"}
	}
	users = filterUsersByTenant(users, ctx.TenantID)

	exactMatches := make([]agenttools.UserInfo, 0)
	for _, user := range users {
		if strings.TrimSpace(user.Name) == value {
			exactMatches = append(exactMatches, user)
		}
	}
	if len(exactMatches) > 0 {
		return userSlotResolution(ctx, exactMatches)
	}

	normalized := normalizeEntityName(value)
	matches := make([]agenttools.UserInfo, 0)
	for _, candidate := range users {
		if strings.Contains(normalizeEntityName(candidate.Name), normalized) {
			matches = append(matches, candidate)
		}
	}
	return userSlotResolution(ctx, matches)
}

// resolveDepartmentSlot resolves department name to canonical department id.
func resolveDepartmentSlot(raw string, departments []agenttools.DeptItem) ResolveResult {
	return resolveDepartmentSlotWithContext(EntityResolveContext{}.RawSlot("dept_ids", raw), departments)
}

func resolveDepartmentSlotWithContext(ctx EntityResolveContext, departments []agenttools.DeptItem) ResolveResult {
	value := strings.TrimSpace(ctx.Raw)
	if !looksLikeEntityInput(value) {
		return ResolveResult{Field: ctx.Field, Status: ResolveInvalidShape, Reason: "invalid_department_shape"}
	}
	departments = filterDepartmentsByTenant(departments, ctx.TenantID)

	exactMatches := exactDepartmentMatches(value, departments)
	if len(exactMatches) > 0 {
		return departmentSlotResolution(ctx, exactMatches)
	}

	normalizedMatches := normalizedDepartmentMatches(value, departments)
	if len(normalizedMatches) > 0 {
		return departmentSlotResolution(ctx, normalizedMatches)
	}

	return departmentSlotResolution(ctx, departmentCandidates(value, departments))
}

func userSlotResolution(ctx EntityResolveContext, matches []agenttools.UserInfo) ResolveResult {
	field := firstNonEmpty(ctx.Field, "user_id")
	switch len(matches) {
	case 0:
		return ResolveResult{Field: field, Status: ResolveNotFound, Reason: "user_not_found"}
	case 1:
		value := matches[0].ID
		result := ResolveResult{
			Field:  field,
			Status: ResolveResolved,
			Value:  value,
			Values: map[string]any{
				"user_id":   value,
				"user_name": matches[0].Name,
			},
		}
		result.Param = trustedParamFromContext(ctx, field, value, "user_resolver")
		return result
	default:
		candidates := make([]EntityCandidate, 0, len(matches))
		for _, candidate := range matches {
			candidates = append(candidates, EntityCandidate{
				ID:       strconv.FormatUint(uint64(candidate.ID), 10),
				Label:    candidate.Name,
				Value:    candidate.ID,
				TenantID: candidate.TenantID,
				Source: TrustedParamSource{
					Kind:     TrustedParamSourceRawSlot,
					Raw:      ctx.Raw,
					Resolver: "user_resolver",
				},
			})
		}
		return ResolveResult{Field: field, Status: ResolveAmbiguous, Candidates: candidates, Reason: "user_ambiguous"}
	}
}

func departmentSlotResolution(ctx EntityResolveContext, matches []agenttools.DeptItem) ResolveResult {
	field := firstNonEmpty(ctx.Field, "dept_ids")
	switch len(matches) {
	case 0:
		return ResolveResult{Field: field, Status: ResolveNotFound, Reason: "department_not_found"}
	case 1:
		deptIDs := []int64{matches[0].DeptID}
		result := ResolveResult{
			Field:  field,
			Status: ResolveResolved,
			Value:  deptIDs,
			Values: map[string]any{
				"dept_ids": deptIDs,
			},
		}
		result.Param = trustedParamFromContext(ctx, field, deptIDs, "department_resolver")
		return result
	default:
		candidates := make([]EntityCandidate, 0, len(matches))
		for _, candidate := range matches {
			candidates = append(candidates, EntityCandidate{
				ID:       strconv.FormatInt(candidate.DeptID, 10),
				Label:    candidate.Name,
				Value:    candidate.DeptID,
				TenantID: candidate.TenantID,
				Source: TrustedParamSource{
					Kind:     TrustedParamSourceRawSlot,
					Raw:      ctx.Raw,
					Resolver: "department_resolver",
				},
			})
		}
		return ResolveResult{Field: field, Status: ResolveAmbiguous, Candidates: candidates, Reason: "department_ambiguous"}
	}
}

func filterUsersByTenant(users []agenttools.UserInfo, tenantID uint) []agenttools.UserInfo {
	if tenantID == 0 {
		return users
	}
	filtered := make([]agenttools.UserInfo, 0, len(users))
	for _, user := range users {
		if user.TenantID == tenantID {
			filtered = append(filtered, user)
		}
	}
	return filtered
}

func filterDepartmentsByTenant(departments []agenttools.DeptItem, tenantID uint) []agenttools.DeptItem {
	if tenantID == 0 {
		return departments
	}
	filtered := make([]agenttools.DeptItem, 0, len(departments))
	for _, department := range departments {
		if department.TenantID == tenantID {
			filtered = append(filtered, department)
		}
	}
	return filtered
}

func resolveDateWithClock(raw string, clock func() time.Time) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	switch value {
	case "今天":
		return clock().Format("2006-01-02"), true
	case "昨天":
		return clock().AddDate(0, 0, -1).Format("2006-01-02"), true
	case "明天":
		return clock().AddDate(0, 0, 1).Format("2006-01-02"), true
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return "", false
	}
	return value, true
}

func parseWeekNumber(raw string) (int, bool) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "第")
	value = strings.TrimSuffix(value, "周")
	week, err := strconv.Atoi(value)
	if err != nil || week <= 0 {
		return 0, false
	}
	return week, true
}

func parseSectionNumber(raw string) (int, bool) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "第")
	value = strings.TrimSuffix(value, "节")
	if section, err := strconv.Atoi(value); err == nil && section > 0 {
		return section, true
	}
	return parseChinesePositiveInt(value)
}

func parseChinesePositiveInt(value string) (int, bool) {
	digits := map[rune]int{
		'一': 1,
		'二': 2,
		'三': 3,
		'四': 4,
		'五': 5,
		'六': 6,
		'七': 7,
		'八': 8,
		'九': 9,
	}
	if value == "十" {
		return 10, true
	}
	if len([]rune(value)) == 1 {
		number, ok := digits[[]rune(value)[0]]
		return number, ok
	}
	parts := strings.Split(value, "十")
	if len(parts) != 2 {
		return 0, false
	}
	tens := 1
	if parts[0] != "" {
		value, ok := digits[[]rune(parts[0])[0]]
		if !ok {
			return 0, false
		}
		tens = value
	}
	ones := 0
	if parts[1] != "" {
		value, ok := digits[[]rune(parts[1])[0]]
		if !ok {
			return 0, false
		}
		ones = value
	}
	number := tens*10 + ones
	return number, number > 0
}

func currentSection(periods []agenttools.PeriodInfo, now time.Time) (int, bool) {
	for idx, period := range periods {
		start, ok := clockTimeOnDate(now, period.Start)
		if !ok {
			continue
		}
		end, ok := clockTimeOnDate(now, period.End)
		if !ok {
			continue
		}
		if !now.Before(start) && !now.After(end) {
			return idx + 1, true
		}
	}
	return 0, false
}

func clockTimeOnDate(base time.Time, value string) (time.Time, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(base.Year(), base.Month(), base.Day(), parsed.Hour(), parsed.Minute(), 0, 0, base.Location()), true
}

// looksLikeEntityInput reports whether it looks like entity input.
func looksLikeEntityInput(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	disallowed := []string{"功能", "吗", "查询", "执行", "可以", "能否", "能不能"}
	for _, marker := range disallowed {
		if strings.Contains(raw, marker) {
			return false
		}
	}
	return true
}

// exactDepartmentMatch handles exact department match.
func exactDepartmentMatch(raw string, departments []agenttools.DeptItem) (*agenttools.DeptItem, bool) {
	matches := exactDepartmentMatches(raw, departments)
	if len(matches) != 1 {
		return nil, false
	}
	dept := matches[0]
	return &dept, true
}

// normalizedDepartmentMatch normalizes d department match.
func normalizedDepartmentMatch(raw string, departments []agenttools.DeptItem) (*agenttools.DeptItem, bool) {
	matches := normalizedDepartmentMatches(raw, departments)
	if len(matches) != 1 {
		return nil, false
	}
	dept := matches[0]
	return &dept, true
}

func exactDepartmentMatches(raw string, departments []agenttools.DeptItem) []agenttools.DeptItem {
	matches := make([]agenttools.DeptItem, 0)
	for _, department := range departments {
		if strings.TrimSpace(department.Name) == raw {
			matches = append(matches, department)
		}
	}
	return matches
}

func normalizedDepartmentMatches(raw string, departments []agenttools.DeptItem) []agenttools.DeptItem {
	matches := make([]agenttools.DeptItem, 0)
	for _, department := range departments {
		name := normalizeEntityName(department.Name)
		for _, variant := range entityNameVariants(raw) {
			if name == variant {
				matches = append(matches, department)
				break
			}
		}
	}
	return matches
}

func containsEntityVariant(normalizedName, raw string) bool {
	for _, variant := range entityNameVariants(raw) {
		if variant != "" && strings.Contains(normalizedName, variant) {
			return true
		}
	}
	return false
}

func entityNameVariants(value string) []string {
	base := normalizeEntityName(value)
	if base == "" {
		return nil
	}
	variants := []string{base}
	for _, prefix := range []string{"就选", "选择", "选", "就", "要"} {
		if strings.HasPrefix(base, prefix) && len(base) > len(prefix) {
			variant := strings.TrimPrefix(base, prefix)
			if variant != "" && !stringSliceContains(variants, variant) {
				variants = append(variants, variant)
			}
		}
	}
	return variants
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// departmentCandidates handles department candidates.
func departmentCandidates(raw string, departments []agenttools.DeptItem) []agenttools.DeptItem {
	matches := make([]agenttools.DeptItem, 0)
	for _, candidate := range departments {
		if containsEntityVariant(normalizeEntityName(candidate.Name), raw) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

// normalizeEntityName normalizes entity name.
func normalizeEntityName(value string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "-", "", "_", "")
	return replacer.Replace(strings.TrimSpace(value))
}

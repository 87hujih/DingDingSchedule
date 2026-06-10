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
	ResolveMissing      ResolveStatus = "missing"
	ResolveAmbiguous    ResolveStatus = "ambiguous"
	ResolveNotFound     ResolveStatus = "not_found"
	ResolveInvalidShape ResolveStatus = "invalid_shape"
)

type ResolveResult struct {
	Field      string
	Status     ResolveStatus
	Value      any
	Values     map[string]any
	Candidates []EntityCandidate
	Reason     string
}

type EntityCandidate struct {
	ID    string
	Label string
	Value any
}

type entityContext struct {
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

// resolveDepartment resolves department.
func resolveDepartment(ctx entityContext) entityResolution {
	raw := strings.TrimSpace(ctx.Raw)
	if !looksLikeEntityInput(raw) {
		return entityResolution{Status: ResolveInvalidShape}
	}

	exactMatches := exactDepartmentMatches(raw, ctx.Departments)
	if len(exactMatches) > 0 {
		return departmentResolution(exactMatches)
	}
	normalizedMatches := normalizedDepartmentMatches(raw, ctx.Departments)
	if len(normalizedMatches) > 0 {
		return departmentResolution(normalizedMatches)
	}

	return departmentResolution(departmentCandidates(raw, ctx.Departments))
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

	exactMatches := make([]agenttools.UserInfo, 0)
	for _, user := range ctx.Users {
		if strings.TrimSpace(user.Name) == raw {
			exactMatches = append(exactMatches, user)
		}
	}
	if len(exactMatches) > 0 {
		return userResolution(exactMatches)
	}

	normalized := normalizeEntityName(raw)
	matches := make([]agenttools.UserInfo, 0)
	for _, candidate := range ctx.Users {
		name := normalizeEntityName(candidate.Name)
		if strings.Contains(name, normalized) {
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
		return ResolveResult{Field: "date", Status: ResolveMissing, Reason: "missing_date"}
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
				return ResolveResult{Field: "week", Status: ResolveMissing, Reason: "missing_week"}
			}
		}
		if semester == nil {
			return ResolveResult{Field: "week", Status: ResolveMissing, Reason: "missing_semester_provider"}
		}
		week, _, err := semester.GetCurrentWeek(ctx)
		if err != nil || week <= 0 {
			return ResolveResult{Field: "week", Status: ResolveMissing, Reason: "current_week_unavailable"}
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
		return ResolveResult{Field: "section", Status: ResolveMissing, Reason: "missing_section"}
	}
	if value == "本节" {
		section, ok := currentSection(periods, clock())
		if !ok {
			return ResolveResult{Field: "section", Status: ResolveMissing, Reason: "current_section_unavailable"}
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
	value := strings.TrimSpace(raw)
	if !looksLikeEntityInput(value) {
		return ResolveResult{Field: "user_id", Status: ResolveInvalidShape, Reason: "invalid_user_shape"}
	}

	exactMatches := make([]agenttools.UserInfo, 0)
	for _, user := range users {
		if strings.TrimSpace(user.Name) == value {
			exactMatches = append(exactMatches, user)
		}
	}
	if len(exactMatches) > 0 {
		return userSlotResolution(exactMatches)
	}

	normalized := normalizeEntityName(value)
	matches := make([]agenttools.UserInfo, 0)
	for _, candidate := range users {
		if strings.Contains(normalizeEntityName(candidate.Name), normalized) {
			matches = append(matches, candidate)
		}
	}
	return userSlotResolution(matches)
}

// resolveDepartmentSlot resolves department name to canonical department id.
func resolveDepartmentSlot(raw string, departments []agenttools.DeptItem) ResolveResult {
	value := strings.TrimSpace(raw)
	if !looksLikeEntityInput(value) {
		return ResolveResult{Field: "dept_ids", Status: ResolveInvalidShape, Reason: "invalid_department_shape"}
	}

	exactMatches := exactDepartmentMatches(value, departments)
	if len(exactMatches) > 0 {
		return departmentSlotResolution(exactMatches)
	}

	normalizedMatches := normalizedDepartmentMatches(value, departments)
	if len(normalizedMatches) > 0 {
		return departmentSlotResolution(normalizedMatches)
	}

	return departmentSlotResolution(departmentCandidates(value, departments))
}

func userSlotResolution(matches []agenttools.UserInfo) ResolveResult {
	switch len(matches) {
	case 0:
		return ResolveResult{Field: "user_id", Status: ResolveNotFound, Reason: "user_not_found"}
	case 1:
		return ResolveResult{
			Field:  "user_id",
			Status: ResolveResolved,
			Value:  matches[0].ID,
			Values: map[string]any{
				"user_id":   matches[0].ID,
				"user_name": matches[0].Name,
			},
		}
	default:
		candidates := make([]EntityCandidate, 0, len(matches))
		for _, candidate := range matches {
			candidates = append(candidates, EntityCandidate{
				ID:    strconv.FormatUint(uint64(candidate.ID), 10),
				Label: candidate.Name,
				Value: candidate.ID,
			})
		}
		return ResolveResult{Field: "user_id", Status: ResolveAmbiguous, Candidates: candidates, Reason: "user_ambiguous"}
	}
}

func departmentSlotResolution(matches []agenttools.DeptItem) ResolveResult {
	switch len(matches) {
	case 0:
		return ResolveResult{Field: "dept_ids", Status: ResolveNotFound, Reason: "department_not_found"}
	case 1:
		deptIDs := []int64{matches[0].DeptID}
		return ResolveResult{
			Field:  "dept_ids",
			Status: ResolveResolved,
			Value:  deptIDs,
			Values: map[string]any{
				"dept_ids": deptIDs,
			},
		}
	default:
		candidates := make([]EntityCandidate, 0, len(matches))
		for _, candidate := range matches {
			candidates = append(candidates, EntityCandidate{
				ID:    strconv.FormatInt(candidate.DeptID, 10),
				Label: candidate.Name,
				Value: candidate.DeptID,
			})
		}
		return ResolveResult{Field: "dept_ids", Status: ResolveAmbiguous, Candidates: candidates, Reason: "department_ambiguous"}
	}
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
	normalized := normalizeEntityName(raw)
	matches := make([]agenttools.DeptItem, 0)
	for _, department := range departments {
		if normalizeEntityName(department.Name) == normalized {
			matches = append(matches, department)
		}
	}
	return matches
}

// departmentCandidates handles department candidates.
func departmentCandidates(raw string, departments []agenttools.DeptItem) []agenttools.DeptItem {
	normalized := normalizeEntityName(raw)
	matches := make([]agenttools.DeptItem, 0)
	for _, candidate := range departments {
		if strings.Contains(normalizeEntityName(candidate.Name), normalized) {
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

package agent

import (
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

func resolveDepartment(ctx entityContext) entityResolution {
	raw := strings.TrimSpace(ctx.Raw)
	if !looksLikeEntityInput(raw) {
		return entityResolution{Status: ResolveInvalidShape}
	}

	if dept, ok := exactDepartmentMatch(raw, ctx.Departments); ok {
		return entityResolution{Status: ResolveResolved, Department: dept}
	}
	if dept, ok := normalizedDepartmentMatch(raw, ctx.Departments); ok {
		return entityResolution{Status: ResolveResolved, Department: dept}
	}

	candidates := departmentCandidates(raw, ctx.Departments)
	switch len(candidates) {
	case 0:
		return entityResolution{Status: ResolveNotFound}
	case 1:
		dept := candidates[0]
		return entityResolution{Status: ResolveResolved, Department: &dept}
	default:
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.Name)
		}
		return entityResolution{Status: ResolveAmbiguous, Candidates: names}
	}
}

func resolveUser(ctx entityContext) entityResolution {
	raw := strings.TrimSpace(ctx.Raw)
	if !looksLikeEntityInput(raw) {
		return entityResolution{Status: ResolveInvalidShape}
	}

	for i := range ctx.Users {
		if strings.TrimSpace(ctx.Users[i].Name) == raw {
			user := ctx.Users[i]
			return entityResolution{Status: ResolveResolved, User: &user}
		}
	}

	normalized := normalizeEntityName(raw)
	matches := make([]agenttools.UserInfo, 0)
	for _, candidate := range ctx.Users {
		name := normalizeEntityName(candidate.Name)
		if strings.Contains(name, normalized) {
			matches = append(matches, candidate)
		}
	}

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

func resolveDate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	switch value {
	case "今天":
		return time.Now().Format("2006-01-02"), true
	case "昨天":
		return time.Now().AddDate(0, 0, -1).Format("2006-01-02"), true
	case "明天":
		return time.Now().AddDate(0, 0, 1).Format("2006-01-02"), true
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return "", false
	}
	return value, true
}

func resolveSection(raw string) (int, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}
	if strings.HasPrefix(value, "第") && strings.HasSuffix(value, "节") {
		switch strings.TrimSuffix(strings.TrimPrefix(value, "第"), "节") {
		case "一":
			return 1, true
		case "二":
			return 2, true
		case "三":
			return 3, true
		case "四":
			return 4, true
		case "五":
			return 5, true
		}
	}
	switch value {
	case "1":
		return 1, true
	case "2":
		return 2, true
	case "3":
		return 3, true
	case "4":
		return 4, true
	case "5":
		return 5, true
	}
	return 0, false
}

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

func exactDepartmentMatch(raw string, departments []agenttools.DeptItem) (*agenttools.DeptItem, bool) {
	for i := range departments {
		if strings.TrimSpace(departments[i].Name) == raw {
			dept := departments[i]
			return &dept, true
		}
	}
	return nil, false
}

func normalizedDepartmentMatch(raw string, departments []agenttools.DeptItem) (*agenttools.DeptItem, bool) {
	normalized := normalizeEntityName(raw)
	var matched *agenttools.DeptItem
	for i := range departments {
		if normalizeEntityName(departments[i].Name) != normalized {
			continue
		}
		if matched != nil {
			return nil, false
		}
		dept := departments[i]
		matched = &dept
	}
	if matched == nil {
		return nil, false
	}
	return matched, true
}

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

func normalizeEntityName(value string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "-", "", "_", "")
	return replacer.Replace(strings.TrimSpace(value))
}

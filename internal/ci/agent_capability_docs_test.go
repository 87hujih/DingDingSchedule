package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	agentCapabilitiesStart = "<!-- agent-capabilities:start -->"
	agentCapabilitiesEnd   = "<!-- agent-capabilities:end -->"
)

func extractMarkedSection(document, startMarker, endMarker string) (string, string) {
	if strings.Count(document, startMarker) != 1 {
		return "", "README must contain exactly one marker " + startMarker
	}
	if strings.Count(document, endMarker) != 1 {
		return "", "README must contain exactly one marker " + endMarker
	}

	start := strings.Index(document, startMarker)
	end := strings.Index(document, endMarker)
	contentStart := start + len(startMarker)
	if end < contentStart {
		return "", "README Agent capability markers are out of order"
	}

	section := strings.TrimSpace(document[contentStart:end])
	if section == "" {
		return "", "README Agent capability section must not be empty"
	}
	return section, ""
}

func containsAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func isNegativeCapabilityStatement(line string) bool {
	return containsAny(
		line,
		"尚未开放",
		"不能",
		"不支持",
		"不执行",
		"不可",
		"不会",
		"仅提供",
		"仅解释",
		"仅说明",
	)
}

func hasConditionalGroupSubscriptionBoundary(active string) bool {
	for _, line := range strings.Split(active, "\n") {
		var hasGroupPermission, hasPrivateDenial bool
		clauses := strings.FieldsFunc(line, func(r rune) bool {
			return strings.ContainsRune("；;。，,", r)
		})
		for _, clause := range clauses {
			if !strings.Contains(clause, "订阅") {
				continue
			}
			if strings.Contains(clause, "群聊") &&
				containsAny(clause, "支持", "允许", "可订阅") &&
				!isNegativeCapabilityStatement(clause) {
				hasGroupPermission = true
			}
			if strings.Contains(clause, "私聊") &&
				containsAny(clause, "不执行", "不支持", "不能", "不可", "不会") {
				hasPrivateDenial = true
			}
		}
		if hasGroupPermission && hasPrivateDenial {
			return true
		}
	}
	return false
}

func readmeAgentCapabilityProblems(document string) []string {
	active, markerProblem := extractMarkedSection(
		document,
		agentCapabilitiesStart,
		agentCapabilitiesEnd,
	)
	if markerProblem != "" {
		return []string{markerProblem}
	}

	var problems []string
	requiredBoundaries := []string{
		"课表查询",
		"考勤查询",
		"规则查询",
		"仅回答",
	}
	for _, boundary := range requiredBoundaries {
		if !strings.Contains(active, boundary) {
			problems = append(problems, "active Agent capability section missing boundary "+boundary)
		}
	}

	if !hasConditionalGroupSubscriptionBoundary(active) {
		problems = append(
			problems,
			"active Agent capability section must allow subscription in group chat and deny it in private chat",
		)
	}

	for _, rawLine := range strings.Split(active, "\n") {
		line := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rawLine), "-*"))
		if line == "" || isNegativeCapabilityStatement(line) {
			continue
		}
		if (strings.Contains(line, "请假") && strings.Contains(line, "查询")) ||
			containsAny(line, "统计分析", "交叉分析") ||
			containsAny(line, "人工补签", "手动补签", "管理员补签") {
			problems = append(problems, "active Agent capability section advertises unavailable execution")
		}
	}
	return problems
}

func TestReadmeAgentCapabilities(t *testing.T) {
	readmePath := filepath.Join("..", "..", "README.md")
	content, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	for _, problem := range readmeAgentCapabilityProblems(string(content)) {
		t.Error(problem)
	}
}

func TestReadmeAgentCapabilityContractExamples(t *testing.T) {
	validActive := strings.Join([]string{
		"- 课表查询：仅回答个人课表。",
		"- 考勤查询：仅回答当前考勤。",
		"- 规则查询：仅回答考勤规则。",
		"- 群考勤订阅：群聊中允许订阅，私聊不执行订阅操作。",
		"- 人工补签：仅解释操作路径，聊天不会发起补签操作。",
	}, "\n")
	marked := func(active string) string {
		return agentCapabilitiesStart + "\n" + active + "\n" + agentCapabilitiesEnd
	}

	tests := []struct {
		name      string
		document  string
		wantValid bool
	}{
		{
			name:      "accepts current boundaries and explanation-only manual sign",
			document:  marked(validActive),
			wantValid: true,
		},
		{
			name: "accepts negative leave and analytics disclaimer",
			document: marked(validActive + "\n" +
				"- 请假查询、统计分析和交叉分析尚未开放，当前不能执行。"),
			wantValid: true,
		},
		{
			name: "rejects direct manual sign with capability phrase after subject",
			document: marked(strings.Replace(
				validActive,
				"人工补签：仅解释操作路径，聊天不会发起补签操作。",
				"人工补签：聊天可直接发起补签操作。",
				1,
			)),
			wantValid: false,
		},
		{
			name: "rejects unrelated group and subscription tokens",
			document: marked(strings.Replace(
				validActive,
				"群考勤订阅：群聊中允许订阅，私聊不执行订阅操作。",
				"群聊中仅回答规则；私聊支持订阅。",
				1,
			)),
			wantValid: false,
		},
		{
			name:      "rejects active leave query",
			document:  marked(validActive + "\n- 当前支持个人请假查询。"),
			wantValid: false,
		},
		{
			name:      "rejects active analytics",
			document:  marked(validActive + "\n- 当前可以执行统计分析和交叉分析。"),
			wantValid: false,
		},
		{
			name:      "rejects duplicate start marker",
			document:  agentCapabilitiesStart + "\n" + marked(validActive),
			wantValid: false,
		},
		{
			name:      "rejects duplicate end marker",
			document:  marked(validActive) + "\n" + agentCapabilitiesEnd,
			wantValid: false,
		},
		{
			name: "rejects reversed markers",
			document: agentCapabilitiesEnd + "\n" + validActive + "\n" +
				agentCapabilitiesStart,
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := readmeAgentCapabilityProblems(tt.document)
			if tt.wantValid && len(problems) != 0 {
				t.Fatalf("expected valid document, got problems: %v", problems)
			}
			if !tt.wantValid && len(problems) == 0 {
				t.Fatal("expected contract violation, got none")
			}
		})
	}
}

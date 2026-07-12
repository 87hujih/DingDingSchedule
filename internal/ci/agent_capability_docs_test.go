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

	expectedAgentCapabilities = `- 课表查询：仅回答个人课表和指定时间段空闲人员等查询。
- 考勤查询：仅回答当前节次、指定节次、考勤文本和周排行等查询。
- 规则查询：仅回答作息、休息日及考勤规则说明。
- 群考勤推送订阅：仅在群聊中支持订阅、取消订阅和状态查询；私聊不执行群订阅操作。
- 人工补签：仅提供规则与操作路径说明，聊天不会发起补签操作。`
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

func normalizeCapabilitySection(section string) string {
	lines := strings.Split(strings.ReplaceAll(section, "\r\n", "\n"), "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return strings.Join(normalized, "\n")
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

	if normalizeCapabilitySection(active) != normalizeCapabilitySection(expectedAgentCapabilities) {
		return []string{"README active Agent capability section drifted from the reviewed snapshot"}
	}
	return nil
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

func TestAgentCapabilityMarkers(t *testing.T) {
	valid := agentCapabilitiesStart + "\nactive\n" + agentCapabilitiesEnd
	tests := []struct {
		name      string
		document  string
		wantValid bool
	}{
		{name: "valid", document: valid, wantValid: true},
		{name: "missing start", document: "active\n" + agentCapabilitiesEnd},
		{name: "missing end", document: agentCapabilitiesStart + "\nactive"},
		{name: "duplicate start", document: agentCapabilitiesStart + "\n" + valid},
		{name: "duplicate end", document: valid + "\n" + agentCapabilitiesEnd},
		{name: "reversed", document: agentCapabilitiesEnd + "\nactive\n" + agentCapabilitiesStart},
		{name: "empty", document: agentCapabilitiesStart + "\n\n" + agentCapabilitiesEnd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, problem := extractMarkedSection(
				tt.document,
				agentCapabilitiesStart,
				agentCapabilitiesEnd,
			)
			if tt.wantValid && problem != "" {
				t.Fatalf("expected valid markers, got %q", problem)
			}
			if !tt.wantValid && problem == "" {
				t.Fatal("expected marker contract violation, got none")
			}
		})
	}
}

func TestReadmeAgentCapabilitySnapshotRejectsDrift(t *testing.T) {
	drifted := strings.Replace(
		expectedAgentCapabilities,
		"私聊不执行群订阅操作",
		"私聊支持订阅操作",
		1,
	)
	document := agentCapabilitiesStart + "\n" + drifted + "\n" + agentCapabilitiesEnd

	if problems := readmeAgentCapabilityProblems(document); len(problems) == 0 {
		t.Fatal("expected snapshot drift to require review")
	}
}

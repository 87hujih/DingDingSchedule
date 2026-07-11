package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	agentCapabilitiesStart = "<!-- agent-capabilities:start -->"
	agentCapabilitiesEnd   = "<!-- agent-capabilities:end -->"
)

func extractMarkedSection(t *testing.T, document, startMarker, endMarker string) string {
	t.Helper()

	start := strings.Index(document, startMarker)
	if start == -1 {
		t.Fatalf("README missing marker %q", startMarker)
	}

	contentStart := start + len(startMarker)
	endOffset := strings.Index(document[contentStart:], endMarker)
	if endOffset == -1 {
		t.Fatalf("README missing marker %q", endMarker)
	}

	section := strings.TrimSpace(document[contentStart : contentStart+endOffset])
	if section == "" {
		t.Fatal("README Agent capability section must not be empty")
	}
	return section
}

func TestReadmeAgentCapabilities(t *testing.T) {
	readmePath := filepath.Join("..", "..", "README.md")
	content, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	active := extractMarkedSection(
		t,
		string(content),
		agentCapabilitiesStart,
		agentCapabilitiesEnd,
	)

	requiredBoundaries := []string{
		"课表查询",
		"考勤查询",
		"规则查询",
		"仅回答",
		"群聊中",
		"订阅",
	}
	for _, boundary := range requiredBoundaries {
		if !strings.Contains(active, boundary) {
			t.Errorf("active Agent capability section missing boundary %q", boundary)
		}
	}

	forbiddenClaims := []*regexp.Regexp{
		regexp.MustCompile(`(?m)^\s*[-*]\s*.*请假.*查询.*$`),
		regexp.MustCompile(`(?m)^\s*[-*]\s*.*(统计分析|交叉分析).*$`),
		regexp.MustCompile(`(?m)^\s*[-*]\s*(管理员补签|人工补签|手动补签)\s*$`),
		regexp.MustCompile(`(支持|可以).*(执行|创建|进行).*(人工补签|手动补签)`),
	}
	for _, claim := range forbiddenClaims {
		if claim.MatchString(active) {
			t.Errorf("active Agent capability section advertises unavailable execution: %q", claim.String())
		}
	}
}

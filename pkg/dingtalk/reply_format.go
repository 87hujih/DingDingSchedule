package dingtalk

import (
	"regexp"
	"strings"
)

var (
	markdownHeadingPattern = regexp.MustCompile(`^\s{0,3}#{1,6}\s*(.*?)\s*#*\s*$`)
	markdownListPattern    = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	markdownLinkPattern    = regexp.MustCompile(`\[(.*?)\]\((https?://[^\s)]+)\)`)
	markdownBoldPattern    = regexp.MustCompile(`\*\*(.*?)\*\*|__(.*?)__`)
	markdownCodePattern    = regexp.MustCompile("`([^`]+)`")
	markdownItalicPattern  = regexp.MustCompile(`\*(.*?)\*|_(.*?)_`)
)

// renderPlainTextReply 将常见 Markdown 样式降级为适合钉钉纯文本消息的内容。
func renderPlainTextReply(reply string) string {
	normalized := strings.ReplaceAll(reply, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = markdownLinkPattern.ReplaceAllString(normalized, "$1：$2")
	normalized = markdownCodePattern.ReplaceAllString(normalized, "$1")
	normalized = markdownBoldPattern.ReplaceAllStringFunc(normalized, func(s string) string {
		return strings.Trim(s, "*_")
	})
	normalized = markdownItalicPattern.ReplaceAllStringFunc(normalized, func(s string) string {
		return strings.Trim(s, "*_")
	})

	lines := strings.Split(normalized, "\n")
	rendered := make([]string, 0, len(lines))
	blankPending := false
	for _, line := range lines {
		trimmedRight := strings.TrimRight(line, " \t")
		trimmed := strings.TrimSpace(trimmedRight)
		if trimmed == "" {
			if len(rendered) > 0 {
				blankPending = true
			}
			continue
		}

		if blankPending && len(rendered) > 0 {
			rendered = append(rendered, "")
			blankPending = false
		}

		if match := markdownHeadingPattern.FindStringSubmatch(trimmedRight); len(match) == 2 {
			trimmedRight = strings.TrimSpace(match[1])
		}
		if match := markdownListPattern.FindStringSubmatch(trimmedRight); len(match) == 2 {
			trimmedRight = "- " + strings.TrimSpace(match[1])
		}

		rendered = append(rendered, strings.TrimSpace(trimmedRight))
	}

	return strings.TrimSpace(strings.Join(rendered, "\n"))
}

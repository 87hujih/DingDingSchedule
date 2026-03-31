package agent

import (
	"fmt"
	"strings"
)

// buildKnowledgeOnlyPrompt 构建纯知识问答场景下的系统提示词。
func buildKnowledgeOnlyPrompt(result RetrievalResult) string {
	summary := buildKnowledgeSummary(result)
	if summary == "" {
		return ""
	}

	return "以下是与当前问题相关的规则知识。规则说明只能来自下面的知识片段，不要把实时数据或工具结果当成规则说明；如果知识不足，请明确说明。\n" +
		summary +
		"回答时优先引用来源字段。"
}

// buildMixedAnswerPrompt 构建 mixed 场景下的系统提示词，固定实时结果与规则说明的顺序。
func buildMixedAnswerPrompt(result RetrievalResult) string {
	summary := buildKnowledgeSummary(result)
	if summary == "" {
		return ""
	}

	return "以下规则知识只用于解释实时查询结果。先回答实时查询结果，再补充规则说明，最后列出来源。实时数据只能来自工具，规则说明只能来自下面的知识片段，不要编造。\n" +
		summary
}

// buildKnowledgeSummary 将检索命中的知识片段整理成可注入模型的来源摘要。
func buildKnowledgeSummary(result RetrievalResult) string {
	if len(result.Hits) == 0 {
		return ""
	}

	var b strings.Builder
	for i, hit := range result.Hits {
		sourceRef := strings.TrimSpace(hit.SourceRef)
		if sourceRef == "" {
			sourceRef = strings.TrimSpace(hit.Title)
		}
		fmt.Fprintf(&b, "%d. 来源：%s\n", i+1, sourceRef)
		if strings.TrimSpace(hit.Title) != "" {
			fmt.Fprintf(&b, "标题：%s\n", hit.Title)
		}
		if strings.TrimSpace(hit.Heading) != "" {
			fmt.Fprintf(&b, "小节：%s\n", hit.Heading)
		}
		fmt.Fprintf(&b, "内容：%s\n", strings.TrimSpace(hit.Body))
	}
	return b.String()
}

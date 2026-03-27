package agent

import (
	"context"
	"fmt"
	"strings"
)

const defaultKnowledgeTopK = 4

// retrieveKnowledge 按租户和问题执行首版规则知识检索。
func (a *Agent) retrieveKnowledge(ctx context.Context, tenantID uint, question string) ([]KnowledgeHit, error) {
	if a.deps.Knowledge == nil || tenantID == 0 {
		return nil, nil
	}
	return a.deps.Knowledge.Search(ctx, tenantID, question, defaultKnowledgeTopK)
}

// buildKnowledgePrompt 将检索命中的知识片段拼成可注入给模型的上下文。
func buildKnowledgePrompt(hits []KnowledgeHit) string {
	if len(hits) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("以下是与当前问题相关的规则知识，只能基于这些片段回答规则说明；如果知识不足，请明确说明。\n")
	for i, hit := range hits {
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
	b.WriteString("回答规则类内容时，优先引用上面的来源字段。")
	return b.String()
}

// collectKnowledgeSourceRefs 提取去重后的来源引用，用于日志和评测。
func collectKnowledgeSourceRefs(hits []KnowledgeHit) []string {
	sources := make([]string, 0, len(hits))
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		source := strings.TrimSpace(hit.SourceRef)
		if source == "" {
			source = strings.TrimSpace(hit.Title)
		}
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		sources = append(sources, source)
	}
	return sources
}

package agent

import "strings"

type queryKind string

const (
	queryKindTool  queryKind = "tool"
	queryKindRAG   queryKind = "rag"
	queryKindMixed queryKind = "mixed"
)

type queryRoute struct {
	Kind queryKind
}

type queryRouter struct{}

// newQueryRouter 创建轻量问题分流器。
func newQueryRouter() *queryRouter {
	return &queryRouter{}
}

// Route 根据问题内容选择 tool、rag 或 mixed 路径。
func (r *queryRouter) Route(question string) queryRoute {
	normalized := normalizeQuery(question)
	hasRule := hasRuleSignal(normalized)
	if !hasRule {
		return queryRoute{Kind: queryKindTool}
	}
	if hasLiveSignal(normalized) {
		return queryRoute{Kind: queryKindMixed}
	}
	return queryRoute{Kind: queryKindRAG}
}

// normalizeQuery 统一移除常见空白和标点，便于后续关键词判断。
func normalizeQuery(question string) string {
	replacer := strings.NewReplacer(
		" ", "",
		"\t", "",
		"\n", "",
		"\r", "",
		"，", "",
		"。", "",
		"？", "",
		"！", "",
		",", "",
		".", "",
		"?", "",
		"!", "",
	)
	return strings.ToLower(replacer.Replace(strings.TrimSpace(question)))
}

// hasRuleSignal 判断问题是否显式包含规则说明类意图。
func hasRuleSignal(question string) bool {
	keywords := []string{
		"规则",
		"判定",
		"说明",
		"流程",
		"口径",
		"依据",
		"配置",
		"手册",
		"文档",
		"怎么判",
		"如何判",
		"为什么",
		"区别",
		"影响",
		"优先级",
		"生效",
		"顺延",
		"实时视图",
		"最终结算",
	}
	return containsAny(question, keywords)
}

// hasLiveSignal 判断问题是否需要实时业务数据参与回答。
func hasLiveSignal(question string) bool {
	timeKeywords := []string{
		"今天",
		"昨天",
		"昨日",
		"明天",
		"本周",
		"这周",
		"下周",
		"周一",
		"周二",
		"周三",
		"周四",
		"周五",
		"周六",
		"周日",
		"星期一",
		"星期二",
		"星期三",
		"星期四",
		"星期五",
		"星期六",
		"星期日",
	}
	queryKeywords := []string{
		"谁",
		"哪些",
		"多少",
		"名单",
		"有没有",
		"帮我看",
		"查一下",
		"查",
		"看一下",
		"看看",
		"统计",
		"排行",
	}

	if containsAny(question, timeKeywords) || containsAny(question, queryKeywords) {
		return true
	}

	return strings.Contains(question, "第") && strings.Contains(question, "节")
}

// containsAny 判断问题中是否命中任一关键词。
func containsAny(question string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(question, keyword) {
			return true
		}
	}
	return false
}

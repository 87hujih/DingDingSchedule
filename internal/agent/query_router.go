package agent

import "strings"

type queryKind string

const (
	queryKindTool  queryKind = "tool"
	queryKindRAG   queryKind = "rag"
	queryKindMixed queryKind = "mixed"
)

type answerMode string

const (
	answerModeKnowledgeOnly answerMode = "knowledge-only"
	answerModeToolFirst     answerMode = "tool-first"
	answerModeMixed         answerMode = "mixed"
	answerModeReject        answerMode = "reject"
)

const retrievalStrongScoreThreshold = 8

type intentType string

const (
	intentHelp      intentType = "help"
	intentAction    intentType = "action"
	intentLiveQuery intentType = "live-query"
	intentRule      intentType = "rule"
	intentMixed     intentType = "mixed"
	intentClarify   intentType = "clarify"
)

type intentDecision struct {
	Intent     intentType
	AnswerMode answerMode
}

type queryRoute struct {
	Kind queryKind
}

type routeInputs struct {
	DomainResult      domainResult
	HasLiveSignal     bool
	RetrievalHitCount int
	TopScore          int
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

// Decide 根据领域判定、实时信号和检索强度选择回答模式。
func (r *queryRouter) Decide(inputs routeInputs) answerMode {
	if inputs.DomainResult != domainIn {
		return answerModeReject
	}

	knowledgeStrong := inputs.RetrievalHitCount > 0 && inputs.TopScore >= retrievalStrongScoreThreshold
	if inputs.HasLiveSignal {
		if knowledgeStrong {
			return answerModeMixed
		}
		return answerModeToolFirst
	}
	if knowledgeStrong {
		return answerModeKnowledgeOnly
	}
	return answerModeReject
}

func (r *queryRouter) DecideIntent(question string, domainResult domainResult, retrievalResult RetrievalResult) intentDecision {
	return classifyIntent(question, domainResult, retrievalResult)
}

// DecideForQuestion 在基础模式决策上补一层“是否真的在问规则”的保护，避免纯实时问题被误抬成 mixed。
func (r *queryRouter) DecideForQuestion(question string, domainResult domainResult, retrievalResult RetrievalResult) answerMode {
	return r.DecideIntent(question, domainResult, retrievalResult).AnswerMode
}

func classifyIntent(question string, domainResult domainResult, retrievalResult RetrievalResult) intentDecision {
	if domainResult != domainIn {
		return intentDecision{AnswerMode: answerModeReject}
	}

	normalized := normalizeQuery(question)
	if hasHelpIntent(normalized) {
		return intentDecision{Intent: intentHelp, AnswerMode: answerModeToolFirst}
	}
	if hasClarifyIntent(normalized) {
		return intentDecision{Intent: intentClarify, AnswerMode: answerModeToolFirst}
	}
	if hasActionIntent(normalized) {
		return intentDecision{Intent: intentAction, AnswerMode: answerModeToolFirst}
	}

	hasRule := hasRuleSignal(normalized)
	hasLive := hasLiveSignal(normalized)
	mode := newQueryRouter().Decide(routeInputs{
		DomainResult:      domainResult,
		HasLiveSignal:     hasLive,
		RetrievalHitCount: len(retrievalResult.Hits),
		TopScore:          topKnowledgeScore(retrievalResult),
	})

	if hasLive && hasRule {
		if mode == answerModeReject {
			mode = answerModeToolFirst
		}
		return intentDecision{Intent: intentMixed, AnswerMode: mode}
	}
	if hasRule {
		return intentDecision{Intent: intentRule, AnswerMode: mode}
	}
	if hasLive {
		if mode == answerModeReject {
			mode = answerModeToolFirst
		}
		return intentDecision{Intent: intentLiveQuery, AnswerMode: mode}
	}

	return intentDecision{Intent: intentRule, AnswerMode: mode}
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

func hasHelpIntent(question string) bool {
	return containsAny(question, []string{
		"你有什么功能",
		"有什么功能",
		"你能做什么",
		"能做什么",
		"怎么用你",
		"如何使用你",
		"你会什么",
	})
}

// hasLiveSignal 判断问题是否需要实时业务数据参与回答。
func hasLiveSignal(question string) bool {
	if containsAny(question, []string{"按谁优先", "谁优先", "什么优先"}) {
		return false
	}

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
	if strings.Contains(question, "谁") {
		liveContextKeywords := []string{
			"今天",
			"昨天",
			"本周",
			"这周",
			"下周",
			"第",
			"节",
			"未到",
			"有课",
			"没课",
			"无课",
			"请假",
			"缺勤",
			"出勤",
			"名单",
		}
		if containsAny(question, liveContextKeywords) {
			return true
		}
	}

	return strings.Contains(question, "第") && strings.Contains(question, "节")
}

func hasActionIntent(question string) bool {
	if hasExplanatoryIntent(question) {
		return false
	}
	return hasSubscriptionActionSignal(question) || hasManualSignActionSignal(question)
}

func hasClarifyIntent(question string) bool {
	if containsAny(question, []string{"订阅状态", "有没有订阅", "是否订阅", "有开", "开了没", "开没开"}) &&
		containsAny(question, []string{"考勤", "推送"}) {
		return true
	}
	if hasSubscriptionScopeIntent(question) && !containsQuotedOrEnumeratedDeptHints(question) {
		return true
	}
	if hasManualSignActionSignal(question) && !containsAny(question, []string{"今天", "昨天", "明天", "第", "节", "-"}) {
		return true
	}
	return false
}

func hasExplanatoryIntent(question string) bool {
	return containsAny(question, []string{
		"为什么",
		"原因",
		"失败",
		"报错",
		"异常",
		"说明",
		"流程",
		"如何",
		"怎么",
		"是什么",
		"含义",
		"作用",
		"介绍",
	})
}

func hasSubscriptionActionSignal(question string) bool {
	if containsAny(question, []string{"订阅状态", "有没有订阅", "是否订阅"}) &&
		containsAny(question, []string{"考勤", "推送"}) {
		return true
	}
	if containsAny(question, []string{"有开", "开了没", "开没开"}) &&
		containsAny(question, []string{"考勤推送", "考勤订阅"}) {
		return true
	}
	if containsAny(question, []string{"订阅", "开启", "开通", "打开", "取消", "关闭", "查询"}) &&
		containsAny(question, []string{"考勤", "推送"}) {
		return true
	}
	return false
}

func hasSubscriptionScopeIntent(question string) bool {
	return containsAny(question, []string{"指定部门", "部分部门", "某些部门", "几个部门"}) &&
		containsAny(question, []string{"订阅", "推送", "考勤"})
}

func containsQuotedOrEnumeratedDeptHints(question string) bool {
	return containsAny(question, []string{"、", ",", "，", "和", "以及", "\"", "“", "教务处", "学工处"})
}

func hasManualSignActionSignal(question string) bool {
	if containsAny(question, []string{"帮我", "给我", "请", "麻烦"}) &&
		containsAny(question, []string{"补签", "代签"}) {
		return true
	}
	if (strings.HasPrefix(question, "补签") || strings.HasPrefix(question, "代签") ||
		strings.HasPrefix(question, "给")) && containsAny(question, []string{"补签", "代签"}) {
		return true
	}
	return false
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

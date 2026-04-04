package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

// EvalCase 表示一条离线评测样本。
type EvalCase struct {
	Name             string   `json:"name"`
	Category         string   `json:"category"`
	Question         string   `json:"question"`
	ExpectedDomain   string   `json:"expected_domain,omitempty"`
	ExpectedPlanKind string   `json:"expected_plan_kind,omitempty"`
	ExpectedIntent   string   `json:"expected_intent,omitempty"`
	ExpectedExecutor string   `json:"expected_executor,omitempty"`
	ExpectedMode     string   `json:"expected_mode,omitempty"`
	ExpectedRoute    string   `json:"expected_route,omitempty"`
	ExpectedTools    []string `json:"expected_tools,omitempty"`
	ExpectedSources  []string `json:"expected_sources,omitempty"`
	ExpectedKeywords []string `json:"expected_keywords,omitempty"`
}

// EvalObservation 表示一次端到端问答观测结果。
type EvalObservation struct {
	Reply string
	Tools []string
}

// EvalObserver 执行真实问答并返回回复与工具调用信息。
type EvalObserver func(ctx context.Context, question string) (EvalObservation, error)

// EvalCaseResult 表示一条样本的评测结果。
type EvalCaseResult struct {
	Name              string
	Category          string
	Question          string
	DomainHint        string
	DomainResult      string
	DomainMatched     bool
	PlanKind          string
	PlanChecked       bool
	PlanMatched       bool
	KnowledgeStrength string
	PlannerReason     string
	Intent            string
	IntentChecked     bool
	IntentMatched     bool
	Executor          string
	ExecutorChecked   bool
	ExecutorMatched   bool
	AnswerMode        string
	ModeMatched       bool
	Route             string
	RouteMatched      bool
	RetrievalChecked  bool
	RetrievalMatched  bool
	RetrievedSources  []string
	ToolsChecked      bool
	ToolsMatched      bool
	ActualTools       []string
	KeywordsChecked   bool
	KeywordsMatched   bool
	Reply             string
	DurationMs        int64
	Error             string
}

// EvalSummary 表示整批样本的评测摘要。
type EvalSummary struct {
	TotalCases        int
	DomainPassed      int
	DomainAccuracy    float64
	PlanCases         int
	PlanPassed        int
	PlanAccuracy      float64
	IntentCases       int
	IntentPassed      int
	IntentAccuracy    float64
	ExecutorCases     int
	ExecutorPassed    int
	ExecutorAccuracy  float64
	ModePassed        int
	ModeAccuracy      float64
	RoutePassed       int
	RouteAccuracy     float64
	RetrievalCases    int
	RetrievalPassed   int
	RetrievalAccuracy float64
	ToolCases         int
	ToolPassed        int
	ToolAccuracy      float64
	KeywordCases      int
	KeywordPassed     int
	KeywordAccuracy   float64
	AverageLatencyMs  int64
}

// LoadEvalCases 从 JSON 文件加载评测样本。
func LoadEvalCases(path string) ([]EvalCase, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read eval cases: %w", err)
	}

	var cases []EvalCase
	if err := json.Unmarshal(content, &cases); err != nil {
		return nil, fmt.Errorf("decode eval cases: %w", err)
	}
	return cases, nil
}

// EvaluateCases 评估 unified planner、知识检索以及可选的端到端问答结果。
func EvaluateCases(ctx context.Context, knowledge KnowledgePort, tenantID uint, cases []EvalCase, observer EvalObserver) (EvalSummary, []EvalCaseResult, error) {
	domainGate := newDomainGate()
	results := make([]EvalCaseResult, 0, len(cases))
	summary := EvalSummary{TotalCases: len(cases)}

	var totalLatency int64

	for _, tc := range cases {
		start := time.Now()
		result := EvalCaseResult{
			Name:     tc.Name,
			Category: tc.Category,
			Question: tc.Question,
		}

		normalized := normalizeQuery(tc.Question)
		conversationDecision := interpretConversation(tc.Question, nil)
		domainHint := domainGate.Hint(tc.Question)
		result.DomainHint = string(domainHint)

		domainResult := evalDomainResultForHint(domainHint)
		result.DomainResult = string(domainResult)
		expectedDomain := strings.TrimSpace(tc.ExpectedDomain)
		if expectedDomain == "" {
			expectedDomain = result.DomainResult
		}
		result.DomainMatched = strings.EqualFold(result.DomainResult, expectedDomain)
		if result.DomainMatched {
			summary.DomainPassed++
		}

		userCtx := evalUserContext()
		taskCandidate := buildTaskFromRequest(tc.Question, userCtx)

		retrievalResult := RetrievalResult{}
		var retrievalErr error
		if domainHint != domainHintObviousOut && taskCandidate == nil {
			retrievalResult, retrievalErr = searchEvalKnowledge(ctx, knowledge, tenantID, tc.Question)
			if retrievalErr != nil {
				result.Error = retrievalErr.Error()
			}
		}

		planDecision := evalPlanDecision(normalized, conversationDecision, domainHint, retrievalResult, taskCandidate, userCtx, tc.Question)
		result.PlanKind = string(planDecision.Kind)
		result.KnowledgeStrength = string(planDecision.KnowledgeStrength)
		result.PlannerReason = planDecision.ClarifyReason

		expectedPlanKind := defaultExpectedPlanKind(tc)
		if expectedPlanKind != "" {
			result.PlanChecked = true
			summary.PlanCases++
			result.PlanMatched = strings.EqualFold(result.PlanKind, expectedPlanKind)
			if result.PlanMatched {
				summary.PlanPassed++
			}
		}

		compat := evalCompatForPlan(normalized, taskCandidate, planDecision)
		result.Intent = compat.Intent
		expectedIntent := defaultExpectedIntent(tc)
		if expectedIntent != "" {
			result.IntentChecked = true
			summary.IntentCases++
			result.IntentMatched = strings.EqualFold(result.Intent, expectedIntent)
			if result.IntentMatched {
				summary.IntentPassed++
			}
		}

		result.Executor = compat.Executor
		expectedExecutor := defaultExpectedExecutor(tc)
		if expectedExecutor != "" {
			result.ExecutorChecked = true
			summary.ExecutorCases++
			result.ExecutorMatched = strings.EqualFold(result.Executor, expectedExecutor)
			if result.ExecutorMatched {
				summary.ExecutorPassed++
			}
		}

		result.AnswerMode = string(compat.AnswerMode)
		expectedMode := strings.TrimSpace(tc.ExpectedMode)
		if expectedMode == "" {
			expectedMode = defaultExpectedMode(tc)
		}
		result.ModeMatched = expectedMode == "" || strings.EqualFold(result.AnswerMode, expectedMode)
		if result.ModeMatched {
			summary.ModePassed++
		}

		result.Route = string(compat.Route)
		expectedRoute := strings.TrimSpace(tc.ExpectedRoute)
		if expectedRoute == "" {
			expectedRoute = string(modeToQueryKind(answerModeForExpectedMode(expectedMode)))
			if expectedRoute == "" {
				expectedRoute = strings.TrimSpace(tc.Category)
			}
		}
		result.RouteMatched = expectedRoute == "" || strings.EqualFold(result.Route, expectedRoute)
		if result.RouteMatched {
			summary.RoutePassed++
		}

		if len(tc.ExpectedSources) > 0 {
			result.RetrievalChecked = true
			summary.RetrievalCases++
			if result.Error == "" {
				result.RetrievedSources = collectSourceRefs(retrievalResult.Hits)
				result.RetrievalMatched = matchAnyNormalized(result.RetrievedSources, tc.ExpectedSources)
				if result.RetrievalMatched {
					summary.RetrievalPassed++
				}
			}
		}

		if observer != nil {
			observation, err := observer(ctx, tc.Question)
			if err != nil {
				if result.Error == "" {
					result.Error = err.Error()
				}
			} else {
				result.Reply = observation.Reply
				result.ActualTools = observation.Tools
			}

			if len(tc.ExpectedTools) > 0 {
				result.ToolsChecked = true
				summary.ToolCases++
				result.ToolsMatched = containsAllNormalized(result.ActualTools, tc.ExpectedTools)
				if result.ToolsMatched {
					summary.ToolPassed++
				}
			}

			if len(tc.ExpectedKeywords) > 0 {
				result.KeywordsChecked = true
				summary.KeywordCases++
				result.KeywordsMatched = containsAllKeywords(result.Reply, tc.ExpectedKeywords)
				if result.KeywordsMatched {
					summary.KeywordPassed++
				}
			}
		}

		result.DurationMs = time.Since(start).Milliseconds()
		totalLatency += result.DurationMs
		results = append(results, result)
	}

	summary.DomainAccuracy = percent(summary.DomainPassed, summary.TotalCases)
	summary.PlanAccuracy = percent(summary.PlanPassed, summary.PlanCases)
	summary.IntentAccuracy = percent(summary.IntentPassed, summary.IntentCases)
	summary.ExecutorAccuracy = percent(summary.ExecutorPassed, summary.ExecutorCases)
	summary.ModeAccuracy = percent(summary.ModePassed, summary.TotalCases)
	summary.RouteAccuracy = percent(summary.RoutePassed, summary.TotalCases)
	summary.RetrievalAccuracy = percent(summary.RetrievalPassed, summary.RetrievalCases)
	summary.ToolAccuracy = percent(summary.ToolPassed, summary.ToolCases)
	summary.KeywordAccuracy = percent(summary.KeywordPassed, summary.KeywordCases)
	if summary.TotalCases > 0 {
		summary.AverageLatencyMs = totalLatency / int64(summary.TotalCases)
	}

	return summary, results, nil
}

func evalUserContext() *tools.UserContext {
	return &tools.UserContext{
		TenantID:          1,
		UserID:            1,
		UserRole:          1,
		DingUserID:        "eval-user",
		Name:              "EvalUser",
		ConversationType:  "2",
		ConversationID:    "eval-conversation",
		ConversationTitle: "评测群",
	}
}

func evalDomainResultForHint(hint DomainHint) domainResult {
	if hint == domainHintObviousOut {
		return domainOut
	}
	return domainIn
}

func evalPlanDecision(
	normalized string,
	conversationDecision conversationDecision,
	domainHint DomainHint,
	retrievalResult RetrievalResult,
	taskCandidate *ActiveTask,
	userCtx *tools.UserContext,
	question string,
) PlanDecision {
	if conversationDecision.Event == eventGreeting {
		return PlanDecision{
			Kind:              planKindTool,
			ClarifyReason:     conversationDecision.Reason,
			KnowledgeStrength: knowledgeStrengthNone,
		}
	}
	if hasHelpIntent(normalized) {
		return PlanDecision{
			Kind:              planKindTool,
			ClarifyReason:     "help_intent",
			KnowledgeStrength: knowledgeStrengthNone,
		}
	}
	return plan(PlanInput{
		Question:          question,
		UserContext:       userCtx,
		History:           nil,
		ActiveTask:        nil,
		ConversationEvent: conversationDecision,
		DomainHint:        domainHint,
		Retrieval:         retrievalResult,
		TaskCandidate:     taskCandidate,
		HasLiveSignal:     hasLiveSignal(normalized),
		HasRuleSignal:     hasRuleSignal(normalized),
		HasActionIntent:   hasActionIntent(normalized),
		HasClarifyIntent:  hasClarifyIntent(normalized),
		HasHelpIntent:     hasHelpIntent(normalized),
	})
}

type evalCompatDecision struct {
	Intent     string
	Executor   string
	AnswerMode answerMode
	Route      queryKind
}

func evalCompatForPlan(normalized string, taskCandidate *ActiveTask, decision PlanDecision) evalCompatDecision {
	if hasHelpIntent(normalized) {
		return evalCompatDecision{
			Intent:     string(intentHelp),
			Executor:   "help",
			AnswerMode: answerModeToolFirst,
			Route:      queryKindTool,
		}
	}

	switch decision.Kind {
	case planKindObviousOut:
		return evalCompatDecision{
			Executor:   "reject",
			AnswerMode: answerModeReject,
			Route:      queryKindTool,
		}
	case planKindClarify:
		return evalCompatDecision{
			Intent:     string(intentClarify),
			Executor:   "clarify",
			AnswerMode: answerModeToolFirst,
			Route:      queryKindTool,
		}
	case planKindRAG:
		return evalCompatDecision{
			Intent:     string(intentRule),
			Executor:   "knowledge",
			AnswerMode: answerModeKnowledgeOnly,
			Route:      queryKindRAG,
		}
	case planKindMixed:
		return evalCompatDecision{
			Intent:     string(intentMixed),
			Executor:   "mixed",
			AnswerMode: answerModeMixed,
			Route:      queryKindMixed,
		}
	case planKindContinueTask:
		return evalCompatDecision{
			Intent:     string(intentAction),
			Executor:   "tool",
			AnswerMode: answerModeToolFirst,
			Route:      queryKindTool,
		}
	case planKindTool:
		intent := string(intentAction)
		if taskCandidate == nil {
			switch {
			case hasLiveSignal(normalized):
				intent = string(intentLiveQuery)
			case hasActionIntent(normalized):
				intent = string(intentAction)
			case hasClarifyIntent(normalized):
				intent = string(intentClarify)
			case hasRuleSignal(normalized):
				intent = string(intentRule)
			}
		}
		return evalCompatDecision{
			Intent:     intent,
			Executor:   "tool",
			AnswerMode: answerModeToolFirst,
			Route:      queryKindTool,
		}
	default:
		return evalCompatDecision{}
	}
}

func searchEvalKnowledge(ctx context.Context, knowledge KnowledgePort, tenantID uint, question string) (RetrievalResult, error) {
	if knowledge == nil || tenantID == 0 {
		return RetrievalResult{}, nil
	}

	hits, err := knowledge.Search(ctx, tenantID, question, defaultKnowledgeTopK)
	if err != nil {
		return RetrievalResult{}, err
	}

	result := RetrievalResult{
		Hits:              hits,
		CandidateCount:    len(hits),
		TopRefs:           collectSourceRefs(hits),
		TopScores:         collectKnowledgeScores(hits),
		KnowledgeDocTypes: collectKnowledgeDocTypes(hits),
	}
	if len(hits) == 0 {
		result.FilteredReason = retrievalFilteredReasonNoHits
	}
	return result, nil
}

func defaultExpectedPlanKind(tc EvalCase) string {
	if planKind := strings.TrimSpace(tc.ExpectedPlanKind); planKind != "" {
		return planKind
	}

	if executor := strings.TrimSpace(tc.ExpectedExecutor); executor != "" {
		switch executor {
		case "help":
			return string(planKindTool)
		case "clarify":
			return string(planKindClarify)
		case "knowledge":
			return string(planKindRAG)
		case "mixed":
			return string(planKindMixed)
		case "reject":
			return string(planKindObviousOut)
		case "tool":
			return string(planKindTool)
		}
	}

	switch strings.TrimSpace(tc.ExpectedMode) {
	case string(answerModeKnowledgeOnly):
		return string(planKindRAG)
	case string(answerModeMixed):
		return string(planKindMixed)
	case string(answerModeReject):
		return string(planKindObviousOut)
	case string(answerModeToolFirst):
		return string(planKindTool)
	}

	switch strings.TrimSpace(tc.Category) {
	case "rag":
		return string(planKindRAG)
	case "mixed":
		return string(planKindMixed)
	case "reject":
		return string(planKindObviousOut)
	case "tool":
		return string(planKindTool)
	default:
		return ""
	}
}

func defaultExpectedIntent(tc EvalCase) string {
	if intent := strings.TrimSpace(tc.ExpectedIntent); intent != "" {
		return intent
	}

	switch defaultExpectedPlanKind(tc) {
	case string(planKindClarify):
		return string(intentClarify)
	case string(planKindRAG):
		return string(intentRule)
	case string(planKindMixed):
		return string(intentMixed)
	default:
		return ""
	}
}

func defaultExpectedExecutor(tc EvalCase) string {
	if executor := strings.TrimSpace(tc.ExpectedExecutor); executor != "" {
		return executor
	}

	switch defaultExpectedPlanKind(tc) {
	case string(planKindClarify):
		return "clarify"
	case string(planKindRAG):
		return "knowledge"
	case string(planKindMixed):
		return "mixed"
	case string(planKindTool), string(planKindContinueTask):
		return "tool"
	case string(planKindObviousOut):
		return "reject"
	default:
		return ""
	}
}

func defaultExpectedMode(tc EvalCase) string {
	if mode := strings.TrimSpace(tc.ExpectedMode); mode != "" {
		return mode
	}

	switch defaultExpectedPlanKind(tc) {
	case string(planKindRAG):
		return string(answerModeKnowledgeOnly)
	case string(planKindMixed):
		return string(answerModeMixed)
	case string(planKindObviousOut):
		return string(answerModeReject)
	case string(planKindTool), string(planKindContinueTask), string(planKindClarify):
		return string(answerModeToolFirst)
	default:
		return ""
	}
}

func answerModeForExpectedMode(mode string) answerMode {
	switch strings.TrimSpace(mode) {
	case string(answerModeKnowledgeOnly):
		return answerModeKnowledgeOnly
	case string(answerModeMixed):
		return answerModeMixed
	case string(answerModeReject):
		return answerModeReject
	case string(answerModeToolFirst):
		return answerModeToolFirst
	default:
		return ""
	}
}

// collectSourceRefs 提取评测结果中的来源引用列表。
func collectSourceRefs(hits []KnowledgeHit) []string {
	sources := make([]string, 0, len(hits))
	for _, hit := range hits {
		source := strings.TrimSpace(hit.SourceRef)
		if source == "" {
			source = strings.TrimSpace(hit.Title)
		}
		if source == "" {
			continue
		}
		sources = append(sources, source)
	}
	return sources
}

// matchAnyNormalized 判断实际结果是否至少命中一个期望值。
func matchAnyNormalized(actual []string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, item := range actual {
		actualSet[normalizeEvalText(item)] = struct{}{}
	}
	for _, item := range expected {
		if _, ok := actualSet[normalizeEvalText(item)]; ok {
			return true
		}
	}
	return false
}

// containsAllNormalized 判断实际结果是否覆盖全部期望值。
func containsAllNormalized(actual []string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, item := range actual {
		actualSet[normalizeEvalText(item)] = struct{}{}
	}
	for _, item := range expected {
		if _, ok := actualSet[normalizeEvalText(item)]; !ok {
			return false
		}
	}
	return true
}

// containsAllKeywords 判断回复中是否包含全部期望关键词。
func containsAllKeywords(reply string, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	normalizedReply := normalizeEvalText(reply)
	for _, keyword := range keywords {
		if !strings.Contains(normalizedReply, normalizeEvalText(keyword)) {
			return false
		}
	}
	return true
}

// normalizeEvalText 统一清理文本中的空白和常见标点。
func normalizeEvalText(text string) string {
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
	return strings.ToLower(replacer.Replace(strings.TrimSpace(text)))
}

// percent 计算通过数在总数中的百分比。
func percent(passed int, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(passed) * 100 / float64(total)
}

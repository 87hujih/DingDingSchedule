package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// EvalCase 表示一条离线评测样本。
type EvalCase struct {
	Name             string   `json:"name"`
	Category         string   `json:"category"`
	Question         string   `json:"question"`
	ExpectedDomain   string   `json:"expected_domain,omitempty"`
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
	Name             string
	Category         string
	Question         string
	DomainResult     string
	DomainMatched    bool
	AnswerMode       string
	ModeMatched      bool
	Route            string
	RouteMatched     bool
	RetrievalChecked bool
	RetrievalMatched bool
	RetrievedSources []string
	ToolsChecked     bool
	ToolsMatched     bool
	ActualTools      []string
	KeywordsChecked  bool
	KeywordsMatched  bool
	Reply            string
	DurationMs       int64
	Error            string
}

// EvalSummary 表示整批样本的评测摘要。
type EvalSummary struct {
	TotalCases        int
	DomainPassed      int
	DomainAccuracy    float64
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

// EvaluateCases 评估 query router、知识检索以及可选的端到端问答结果。
func EvaluateCases(ctx context.Context, knowledge KnowledgePort, tenantID uint, cases []EvalCase, observer EvalObserver) (EvalSummary, []EvalCaseResult, error) {
	domainGate := newDomainGate()
	router := newQueryRouter()
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

		domainResult := domainGate.Check(tc.Question)
		result.DomainResult = string(domainResult)
		expectedDomain := strings.TrimSpace(tc.ExpectedDomain)
		if expectedDomain == "" {
			expectedDomain = result.DomainResult
		}
		result.DomainMatched = strings.EqualFold(result.DomainResult, expectedDomain)
		if result.DomainMatched {
			summary.DomainPassed++
		}

		var hits []KnowledgeHit
		var retrievalErr error
		if domainResult == domainIn {
			hits, retrievalErr = searchEvalKnowledge(ctx, knowledge, tenantID, domainResult, tc.Question)
			if retrievalErr != nil {
				result.Error = retrievalErr.Error()
			}
		}

		answerMode := router.DecideForQuestion(tc.Question, domainResult, RetrievalResult{
			Hits:      hits,
			TopScores: collectKnowledgeScores(hits),
		})
		result.AnswerMode = string(answerMode)
		expectedMode := strings.TrimSpace(tc.ExpectedMode)
		if expectedMode == "" {
			expectedMode = defaultExpectedMode(tc)
		}
		result.ModeMatched = expectedMode == "" || strings.EqualFold(result.AnswerMode, expectedMode)
		if result.ModeMatched {
			summary.ModePassed++
		}

		result.Route = string(modeToQueryKind(answerMode))
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
				result.RetrievedSources = collectSourceRefs(hits)
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

// searchEvalKnowledge 仅在站内问题上执行知识检索。
func searchEvalKnowledge(ctx context.Context, knowledge KnowledgePort, tenantID uint, domainResult domainResult, question string) ([]KnowledgeHit, error) {
	if knowledge == nil || tenantID == 0 {
		return nil, nil
	}
	if domainResult != domainIn {
		return nil, nil
	}
	return knowledge.Search(ctx, tenantID, question, defaultKnowledgeTopK)
}

func defaultExpectedMode(tc EvalCase) string {
	if mode := strings.TrimSpace(tc.ExpectedMode); mode != "" {
		return mode
	}

	switch strings.TrimSpace(tc.ExpectedRoute) {
	case string(queryKindRAG):
		return string(answerModeKnowledgeOnly)
	case string(queryKindMixed):
		return string(answerModeMixed)
	case string(queryKindTool):
		if strings.EqualFold(strings.TrimSpace(tc.ExpectedDomain), string(domainOut)) {
			return string(answerModeReject)
		}
		return string(answerModeToolFirst)
	}

	switch strings.TrimSpace(tc.Category) {
	case "rag":
		return string(answerModeKnowledgeOnly)
	case "mixed":
		return string(answerModeMixed)
	case "tool":
		if strings.EqualFold(strings.TrimSpace(tc.ExpectedDomain), string(domainOut)) {
			return string(answerModeReject)
		}
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

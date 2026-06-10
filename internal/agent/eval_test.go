package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	agenttools "schedule_server/internal/agent/tools"
)

type evalKnowledgePort struct {
	hitsByQuery map[string][]agenttools.KnowledgeHit
}

// Search 按问题返回预置的评测知识命中结果。
func (p evalKnowledgePort) Search(_ context.Context, _ uint, query string, _ int) ([]agenttools.KnowledgeHit, error) {
	return p.hitsByQuery[query], nil
}

type tenantRecordingEvalKnowledgePort struct {
	mu      sync.Mutex
	tenants []uint
	hits    []agenttools.KnowledgeHit
}

func (p *tenantRecordingEvalKnowledgePort) Search(_ context.Context, tenantID uint, _ string, _ int) ([]agenttools.KnowledgeHit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tenants = append(p.tenants, tenantID)
	return append([]agenttools.KnowledgeHit(nil), p.hits...), nil
}

func (p *tenantRecordingEvalKnowledgePort) seenTenants() []uint {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]uint(nil), p.tenants...)
}

// TestEvaluateCasesAggregatesRouteRetrievalToolAndKeywordMatches 验证评测摘要会汇总多种命中结果。
func TestEvaluateCasesAggregatesRouteRetrievalToolAndKeywordMatches(t *testing.T) {
	t.Parallel()

	cases := []EvalCase{
		{
			Name:             "rule-question",
			Category:         "rag",
			Question:         "如果请假信息没能同步到位，会出现什么情况",
			ExpectedDomain:   "in_domain",
			ExpectedMode:     "knowledge-only",
			ExpectedSources:  []string{"请假同步说明#3"},
			ExpectedKeywords: []string{"不会直接覆盖", "重试"},
		},
		{
			Name:           "mixed-question",
			Category:       "mixed",
			Question:       "今天第一节谁未到，并说明迟到规则",
			ExpectedDomain: "in_domain",
			ExpectedMode:   "mixed",
			ExpectedTools:  []string{"query_attendance_status"},
		},
	}

	knowledge := evalKnowledgePort{
		hitsByQuery: map[string][]agenttools.KnowledgeHit{
			"如果请假信息没能同步到位，会出现什么情况": {
				{SourceRef: "请假同步说明#3", Body: "同步失败不会直接覆盖已经生成的考勤快照；排障后应重试同步。", Score: 18},
			},
			"今天第一节谁未到，并说明迟到规则": {
				{SourceRef: "考勤规则说明#1", Body: "上课开始后超过 10 分钟打卡视为迟到。", Score: 18},
			},
		},
	}

	observer := func(_ context.Context, tc EvalCase) (EvalObservation, error) {
		if tc.Question == "如果请假信息没能同步到位，会出现什么情况" {
			return EvalObservation{
				Reply: "同步失败不会直接覆盖已生成的考勤快照，排障后应重试同步。",
			}, nil
		}
		return EvalObservation{
			Reply: "今天第一节未到人员已查询，同时迟到按 10 分钟判定。",
			Tools: []string{"query_attendance_status"},
		}, nil
	}

	summary, results, err := EvaluateCases(context.Background(), knowledge, 42, cases, observer)
	if err != nil {
		t.Fatalf("EvaluateCases() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}

	if summary.TotalCases != 2 {
		t.Fatalf("TotalCases = %d, want 2", summary.TotalCases)
	}
	if summary.DomainPassed != 2 || summary.DomainAccuracy != 100 {
		t.Fatalf("domain summary = %+v", summary)
	}
	if summary.ModePassed != 2 || summary.ModeAccuracy != 100 {
		t.Fatalf("mode summary = %+v", summary)
	}
	if summary.RoutePassed != 2 || summary.RouteAccuracy != 100 {
		t.Fatalf("route summary = %+v", summary)
	}
	if summary.RetrievalCases != 1 || summary.RetrievalPassed != 1 || summary.RetrievalAccuracy != 100 {
		t.Fatalf("retrieval summary = %+v", summary)
	}
	if summary.ToolCases != 1 || summary.ToolPassed != 1 || summary.ToolAccuracy != 100 {
		t.Fatalf("tool summary = %+v", summary)
	}
	if summary.KeywordCases != 1 || summary.KeywordPassed != 1 || summary.KeywordAccuracy != 100 {
		t.Fatalf("keyword summary = %+v", summary)
	}
	if !results[0].RetrievalMatched {
		t.Fatalf("first case retrieval should match: %+v", results[0])
	}
	if !results[0].DomainMatched || !results[0].ModeMatched {
		t.Fatalf("first case domain/mode should match: %+v", results[0])
	}
	if !results[1].ToolsMatched {
		t.Fatalf("second case tools should match: %+v", results[1])
	}
	if !results[1].DomainMatched || !results[1].ModeMatched {
		t.Fatalf("second case domain/mode should match: %+v", results[1])
	}
}

func TestEvaluateCasesAggregatesIntentAndExecutorMatches(t *testing.T) {
	t.Parallel()

	cases := []EvalCase{
		{
			Name:             "help-question",
			Category:         "tool",
			Question:         "你有什么功能",
			ExpectedDomain:   "in_domain",
			ExpectedIntent:   "help",
			ExpectedExecutor: "help",
			ExpectedMode:     "tool-first",
			ExpectedRoute:    "tool",
		},
		{
			Name:             "clarify-question",
			Category:         "tool",
			Question:         "订阅指定部门考勤",
			ExpectedDomain:   "in_domain",
			ExpectedIntent:   "clarify",
			ExpectedExecutor: "clarify",
			ExpectedMode:     "tool-first",
			ExpectedRoute:    "tool",
		},
		{
			Name:             "rule-question",
			Category:         "rag",
			Question:         "如果请假信息没能同步到位，会出现什么情况",
			ExpectedDomain:   "in_domain",
			ExpectedIntent:   "rule",
			ExpectedExecutor: "knowledge",
			ExpectedMode:     "knowledge-only",
			ExpectedRoute:    "rag",
			ExpectedSources:  []string{"请假同步说明#3"},
		},
		{
			Name:             "mixed-question",
			Category:         "mixed",
			Question:         "今天第一节谁未到，并说明迟到规则",
			ExpectedDomain:   "in_domain",
			ExpectedIntent:   "mixed",
			ExpectedExecutor: "mixed",
			ExpectedMode:     "mixed",
			ExpectedRoute:    "mixed",
			ExpectedSources:  []string{"考勤规则说明#1"},
		},
	}

	knowledge := evalKnowledgePort{
		hitsByQuery: map[string][]agenttools.KnowledgeHit{
			"如果请假信息没能同步到位，会出现什么情况": {
				{SourceRef: "请假同步说明#3", Body: "同步失败不会直接覆盖已经生成的考勤快照；排障后应重试同步。", Score: 18},
			},
			"今天第一节谁未到，并说明迟到规则": {
				{SourceRef: "考勤规则说明#1", Body: "上课开始后超过 10 分钟打卡视为迟到。", Score: 18},
			},
		},
	}

	summary, results, err := EvaluateCases(context.Background(), knowledge, 42, cases, nil)
	if err != nil {
		t.Fatalf("EvaluateCases() error = %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("result count = %d, want 4", len(results))
	}
	if summary.IntentPassed != 4 || summary.IntentAccuracy != 100 {
		t.Fatalf("intent summary = %+v", summary)
	}
	if summary.ExecutorPassed != 4 || summary.ExecutorAccuracy != 100 {
		t.Fatalf("executor summary = %+v", summary)
	}
	if summary.ModePassed != 4 || summary.ModeAccuracy != 100 {
		t.Fatalf("mode summary = %+v", summary)
	}
	if summary.RoutePassed != 4 || summary.RouteAccuracy != 100 {
		t.Fatalf("route summary = %+v", summary)
	}
	if results[0].Intent != "help" || !results[0].IntentMatched {
		t.Fatalf("first case intent should match: %+v", results[0])
	}
	if results[1].Executor != "clarify" || !results[1].ExecutorMatched {
		t.Fatalf("second case executor should match: %+v", results[1])
	}
	if results[2].Intent != "rule" || results[2].Executor != "knowledge" {
		t.Fatalf("third case should be rule/knowledge: %+v", results[2])
	}
	if results[3].Intent != "mixed" || results[3].Executor != "mixed" {
		t.Fatalf("fourth case should be mixed/mixed: %+v", results[3])
	}
}

func TestEvaluateCasesAggregatesPlannerDecisionMatches(t *testing.T) {
	t.Parallel()

	cases := []EvalCase{
		{
			Name:             "clarify-question",
			Category:         "tool",
			Question:         "订阅指定部门考勤",
			ExpectedDomain:   "in_domain",
			ExpectedPlanKind: "clarify",
			ExpectedMode:     "tool-first",
		},
		{
			Name:             "rule-question",
			Category:         "rag",
			Question:         "如果请假信息没能同步到位，会出现什么情况",
			ExpectedDomain:   "in_domain",
			ExpectedPlanKind: "rag",
			ExpectedMode:     "knowledge-only",
			ExpectedSources:  []string{"请假同步说明#3"},
		},
		{
			Name:             "reject-question",
			Category:         "reject",
			Question:         "今天上海天气怎么样？",
			ExpectedDomain:   "out_of_domain",
			ExpectedPlanKind: "obvious_out",
			ExpectedMode:     "reject",
		},
	}

	knowledge := evalKnowledgePort{
		hitsByQuery: map[string][]agenttools.KnowledgeHit{
			"如果请假信息没能同步到位，会出现什么情况": {
				{SourceRef: "请假同步说明#3", Body: "同步失败不会直接覆盖已经生成的考勤快照；排障后应重试同步。", Score: 18},
			},
		},
	}

	summary, results, err := EvaluateCases(context.Background(), knowledge, 42, cases, nil)
	if err != nil {
		t.Fatalf("EvaluateCases() error = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("result count = %d, want 3", len(results))
	}
	if summary.PlanCases != 3 || summary.PlanPassed < 2 {
		t.Fatalf("plan summary = %+v", summary)
	}
	if results[0].PlanKind != "clarify" || !results[0].PlanMatched {
		t.Fatalf("first case should be clarify: %+v", results[0])
	}
	if results[1].PlanKind != "rag" || !results[1].PlanMatched {
		t.Fatalf("second case should be rag: %+v", results[1])
	}
	if results[2].PlanKind != "obvious_out" || !results[2].PlanMatched {
		t.Fatalf("third case should be obvious_out: %+v", results[2])
	}
}

func TestEvaluateCasesRejectsObviousOutOfDomainCases(t *testing.T) {
	t.Parallel()

	cases := []EvalCase{
		{
			Name:             "reject-weather",
			Category:         "reject",
			Question:         "今天上海天气怎么样？",
			ExpectedDomain:   "out_of_domain",
			ExpectedPlanKind: "obvious_out",
			ExpectedMode:     "reject",
			ExpectedRoute:    "tool",
		},
		{
			Name:             "reject-code",
			Category:         "reject",
			Question:         "帮我写一个二分查找",
			ExpectedDomain:   "out_of_domain",
			ExpectedPlanKind: "obvious_out",
			ExpectedMode:     "reject",
			ExpectedRoute:    "tool",
		},
	}

	summary, results, err := EvaluateCases(context.Background(), evalKnowledgePort{hitsByQuery: map[string][]agenttools.KnowledgeHit{}}, 42, cases, nil)
	if err != nil {
		t.Fatalf("EvaluateCases() error = %v", err)
	}
	if summary.DomainPassed != len(cases) || summary.PlanPassed != len(cases) || summary.ModePassed != len(cases) {
		t.Fatalf("reject summary = %+v results=%+v", summary, results)
	}
	for _, result := range results {
		if result.DomainResult != "out_of_domain" || result.PlanKind != "obvious_out" || result.AnswerMode != "reject" {
			t.Fatalf("reject case result = %+v", result)
		}
	}
}

func TestEvaluateCasesDoesNotRequireLegacyRouteForProtocolOnlyCases(t *testing.T) {
	t.Parallel()

	cases := []EvalCase{
		{
			Name:                      "protocol-rule",
			Category:                  "protocol",
			Question:                  "迟到规则是什么",
			ExpectedProtocolAct:       "rule_question",
			ExpectedProtocolDomain:    "attendance",
			ExpectedProtocolOperation: "attendance.rule_explain",
			ExpectedResponseKind:      "answer",
		},
	}

	knowledge := evalKnowledgePort{
		hitsByQuery: map[string][]agenttools.KnowledgeHit{
			"迟到规则是什么": {
				{SourceRef: "考勤规则说明#1", Body: "上课开始后超过 10 分钟打卡视为迟到。", Score: 18},
			},
		},
	}

	summary, results, err := EvaluateCases(context.Background(), knowledge, 42, cases, nil)
	if err != nil {
		t.Fatalf("EvaluateCases() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("result count = %d, want 1", len(results))
	}
	if !results[0].ProtocolMatched {
		t.Fatalf("protocol expectation should match: %+v", results[0])
	}
	if !results[0].RouteMatched || summary.RoutePassed != 1 {
		t.Fatalf("protocol-only case should not fail legacy route: summary=%+v result=%+v", summary, results[0])
	}
}

func TestEvaluateCasesAggregatesProtocolMatches(t *testing.T) {
	t.Parallel()

	cases := []EvalCase{
		{
			Name:                      "protocol-help-overview",
			Category:                  "protocol",
			Question:                  "你有什么功能",
			ExpectedProtocolAct:       "help",
			ExpectedProtocolDomain:    "system",
			ExpectedProtocolOperation: "system.describe_capability",
			ExpectedResponseKind:      "answer",
			ExpectedTools:             []string{},
		},
		{
			Name:                      "protocol-subscription-missing-scope",
			Category:                  "protocol",
			Question:                  "开启本群考勤订阅",
			ExpectedProtocolAct:       "write_request",
			ExpectedProtocolDomain:    "subscription",
			ExpectedProtocolOperation: "subscription.start",
			ExpectedResponseKind:      "clarify",
			ExpectedBlockedReason:     "missing_scope",
			ExpectedTools:             []string{},
		},
	}

	summary, results, err := EvaluateCases(context.Background(), nil, 42, cases, nil)
	if err != nil {
		t.Fatalf("EvaluateCases() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}
	if summary.ProtocolCases != 2 || summary.ProtocolPassed != 2 || summary.ProtocolAccuracy != 100 {
		t.Fatalf("protocol summary = %+v results=%+v", summary, results)
	}
	if results[0].ProtocolAct != "help" || !results[0].ProtocolMatched {
		t.Fatalf("first protocol result = %+v", results[0])
	}
	if results[1].ProtocolBlockedReason != "missing_scope" || !results[1].ProtocolMatched {
		t.Fatalf("second protocol result = %+v", results[1])
	}
	if !results[0].NoWriteToolsMatched || !results[1].NoWriteToolsMatched {
		t.Fatalf("no-write-tool expectations should pass: %+v %+v", results[0], results[1])
	}
}

func TestEvaluateCasesProtocolUsesTargetTenantForKnowledge(t *testing.T) {
	t.Parallel()

	knowledge := &tenantRecordingEvalKnowledgePort{
		hits: []agenttools.KnowledgeHit{{Heading: "迟到规则", Body: "迟到规则说明", SourceRef: "attendance#late"}},
	}
	cases := []EvalCase{{
		Name:                      "protocol-rule-tenant",
		Category:                  "protocol",
		Question:                  "迟到规则是什么",
		ExpectedProtocolAct:       "rule_question",
		ExpectedProtocolDomain:    "attendance",
		ExpectedProtocolOperation: "attendance.rule_explain",
		ExpectedResponseKind:      "answer",
	}}

	_, results, err := EvaluateCases(context.Background(), knowledge, 77, cases, nil)
	if err != nil {
		t.Fatalf("EvaluateCases() error = %v", err)
	}
	if len(results) != 1 || !results[0].ProtocolMatched {
		t.Fatalf("protocol result = %+v", results)
	}
	for _, tenantID := range knowledge.seenTenants() {
		if tenantID != 77 {
			t.Fatalf("knowledge tenantID = %d, want 77; all calls=%v", tenantID, knowledge.seenTenants())
		}
	}
}

func TestEvaluateCasesProtocolFixturePasses(t *testing.T) {
	t.Parallel()

	allCases, err := LoadEvalCases(filepath.Join("testdata", "eval_cases.json"))
	if err != nil {
		t.Fatalf("LoadEvalCases() error = %v", err)
	}
	protocolCases := make([]EvalCase, 0)
	for _, tc := range allCases {
		if protocolExpectationPresent(tc) {
			protocolCases = append(protocolCases, tc)
		}
	}
	if len(protocolCases) == 0 {
		t.Fatalf("protocol fixture cases = 0, want at least one")
	}

	summary, results, err := EvaluateCases(context.Background(), evalKnowledgePort{hitsByQuery: map[string][]agenttools.KnowledgeHit{}}, 42, protocolCases, nil)
	if err != nil {
		t.Fatalf("EvaluateCases() error = %v", err)
	}
	if summary.ProtocolCases != len(protocolCases) || summary.ProtocolPassed != summary.ProtocolCases {
		t.Fatalf("protocol fixture summary = %+v results = %+v", summary, results)
	}
}

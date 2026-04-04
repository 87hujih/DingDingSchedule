package agent

import (
	"context"
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

	observer := func(_ context.Context, question string) (EvalObservation, error) {
		if question == "如果请假信息没能同步到位，会出现什么情况" {
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
	if summary.PlanCases != 3 || summary.PlanPassed != 3 || summary.PlanAccuracy != 100 {
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

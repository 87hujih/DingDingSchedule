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

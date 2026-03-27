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
			Question:         "考勤迟到怎么判定？",
			ExpectedSources:  []string{"考勤规则说明#1"},
			ExpectedKeywords: []string{"10分钟", "迟到"},
		},
		{
			Name:          "mixed-question",
			Category:      "mixed",
			Question:      "今天第一节谁未到，并说明迟到判定规则",
			ExpectedTools: []string{"query_attendance_status"},
		},
	}

	knowledge := evalKnowledgePort{
		hitsByQuery: map[string][]agenttools.KnowledgeHit{
			"考勤迟到怎么判定？": {
				{SourceRef: "考勤规则说明#1", Body: "上课开始后超过 10 分钟打卡视为迟到。"},
			},
		},
	}

	observer := func(_ context.Context, question string) (EvalObservation, error) {
		if question == "考勤迟到怎么判定？" {
			return EvalObservation{
				Reply: "根据考勤规则，开课后 10 分钟打卡算迟到。",
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
	if !results[1].ToolsMatched {
		t.Fatalf("second case tools should match: %+v", results[1])
	}
}

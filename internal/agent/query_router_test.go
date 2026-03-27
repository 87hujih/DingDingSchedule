package agent

import "testing"

// TestQueryRouterClassifiesRuleQuestionAsRAG 验证规则问句会走 RAG 路径。
func TestQueryRouterClassifiesRuleQuestionAsRAG(t *testing.T) {
	t.Parallel()

	router := newQueryRouter()
	route := router.Route("考勤迟到怎么判定？")
	if route.Kind != queryKindRAG {
		t.Fatalf("Route kind = %q, want %q", route.Kind, queryKindRAG)
	}
}

// TestQueryRouterClassifiesAttendanceQueryAsTool 验证实时查询问句会走工具路径。
func TestQueryRouterClassifiesAttendanceQueryAsTool(t *testing.T) {
	t.Parallel()

	router := newQueryRouter()
	route := router.Route("今天第一节谁未到？")
	if route.Kind != queryKindTool {
		t.Fatalf("Route kind = %q, want %q", route.Kind, queryKindTool)
	}
}

// TestQueryRouterClassifiesHybridQuestionAsMixed 验证混合问题会同时走检索和工具路径。
func TestQueryRouterClassifiesHybridQuestionAsMixed(t *testing.T) {
	t.Parallel()

	router := newQueryRouter()
	route := router.Route("今天第一节谁未到，并说明迟到判定规则")
	if route.Kind != queryKindMixed {
		t.Fatalf("Route kind = %q, want %q", route.Kind, queryKindMixed)
	}
}

// TestQueryRouterClassifiesRuleOutcomeQuestionsAsRAG 验证“区别/影响/优先级”类规则问句也会走 RAG 路径。
func TestQueryRouterClassifiesRuleOutcomeQuestionsAsRAG(t *testing.T) {
	t.Parallel()

	router := newQueryRouter()
	cases := []struct {
		name     string
		question string
	}{
		{name: "view-mode-diff", question: "实时视图和最终结算有什么区别？"},
		{name: "leave-impact", question: "请假审批同步后会怎么影响考勤？"},
		{name: "priority", question: "休息日和有课冲突时按什么优先级处理？"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			route := router.Route(tc.question)
			if route.Kind != queryKindRAG {
				t.Fatalf("Route(%q) kind = %q, want %q", tc.question, route.Kind, queryKindRAG)
			}
		})
	}
}

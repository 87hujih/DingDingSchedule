package agent

import (
	"testing"
	"time"
)

func TestPlannerReturnsObviousOutForClearlyIrrelevantRequest(t *testing.T) {
	t.Parallel()

	decision := plan(PlanInput{
		Question:          "今天上海天气怎么样",
		ConversationEvent: conversationDecision{Event: eventNewRequest},
		DomainHint:        domainHintObviousOut,
	})
	if decision.Kind != planKindObviousOut {
		t.Fatalf("Kind = %q, want %q", decision.Kind, planKindObviousOut)
	}
}

func TestPlannerPrefersContinueTaskOverDomainHint(t *testing.T) {
	t.Parallel()

	task := &ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"dept_names"},
		FilledSlots:   map[string]string{"scope": "department"},
		ExpiresAt:     time.Now().Add(time.Minute),
	}

	decision := plan(PlanInput{
		Question:          "信工24级",
		ActiveTask:        task,
		ConversationEvent: conversationDecision{Event: eventTaskFollowUp},
		DomainHint:        domainHintObviousOut,
	})
	if decision.Kind != planKindContinueTask {
		t.Fatalf("Kind = %q, want %q", decision.Kind, planKindContinueTask)
	}
}

func TestPlannerReturnsClarifyForWeakInDomainRequest(t *testing.T) {
	t.Parallel()

	decision := plan(PlanInput{
		Question:          "这个怎么处理",
		ConversationEvent: conversationDecision{Event: eventNewRequest},
		DomainHint:        domainHintUnknown,
		Retrieval: RetrievalResult{
			Hits:      []KnowledgeHit{{Title: "系统说明", Score: 3}},
			TopScores: []int{3},
		},
	})
	if decision.Kind != planKindClarify {
		t.Fatalf("Kind = %q, want %q", decision.Kind, planKindClarify)
	}
}

func TestPlannerReturnsRAGForStrongKnowledgeWithoutLiveSignal(t *testing.T) {
	t.Parallel()

	decision := plan(PlanInput{
		Question:          "请假同步失败会出现什么情况",
		ConversationEvent: conversationDecision{Event: eventNewRequest},
		DomainHint:        domainHintLikelyIn,
		Retrieval: RetrievalResult{
			Hits:      []KnowledgeHit{{Title: "请假同步说明", Score: 18}},
			TopScores: []int{18},
		},
		HasRuleSignal: true,
	})
	if decision.Kind != planKindRAG {
		t.Fatalf("Kind = %q, want %q", decision.Kind, planKindRAG)
	}
}

func TestPlannerReturnsMixedForStrongKnowledgeWithLiveSignal(t *testing.T) {
	t.Parallel()

	decision := plan(PlanInput{
		Question:          "今天第一节谁未到，并说明迟到规则",
		ConversationEvent: conversationDecision{Event: eventNewRequest},
		DomainHint:        domainHintLikelyIn,
		Retrieval: RetrievalResult{
			Hits:      []KnowledgeHit{{Title: "考勤规则", Score: 18}},
			TopScores: []int{18},
		},
		HasRuleSignal: true,
		HasLiveSignal: true,
	})
	if decision.Kind != planKindMixed {
		t.Fatalf("Kind = %q, want %q", decision.Kind, planKindMixed)
	}
}

func TestPlannerReturnsToolForActionWithoutStrongKnowledge(t *testing.T) {
	t.Parallel()

	decision := plan(PlanInput{
		Question:          "帮我查今天第一节谁未到",
		ConversationEvent: conversationDecision{Event: eventNewRequest},
		DomainHint:        domainHintLikelyIn,
		Retrieval:         RetrievalResult{},
		HasActionIntent:   true,
		HasLiveSignal:     true,
	})
	if decision.Kind != planKindTool {
		t.Fatalf("Kind = %q, want %q", decision.Kind, planKindTool)
	}
}

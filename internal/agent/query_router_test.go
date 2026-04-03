package agent

import "testing"

func TestModeSelectorChoosesKnowledgeOnlyForRuleQuestion(t *testing.T) {
	t.Parallel()

	router := newQueryRouter()
	mode := router.Decide(routeInputs{
		DomainResult:      domainIn,
		HasLiveSignal:     false,
		RetrievalHitCount: 1,
		TopScore:          12,
	})
	if mode != answerModeKnowledgeOnly {
		t.Fatalf("Decide() = %q, want %q", mode, answerModeKnowledgeOnly)
	}
}

func TestModeSelectorChoosesMixedForRealtimePlusRuleQuestion(t *testing.T) {
	t.Parallel()

	router := newQueryRouter()
	mode := router.Decide(routeInputs{
		DomainResult:      domainIn,
		HasLiveSignal:     true,
		RetrievalHitCount: 2,
		TopScore:          16,
	})
	if mode != answerModeMixed {
		t.Fatalf("Decide() = %q, want %q", mode, answerModeMixed)
	}
}

func TestModeSelectorChoosesToolFirstWhenLiveSignalExistsButKnowledgeWeak(t *testing.T) {
	t.Parallel()

	router := newQueryRouter()
	mode := router.Decide(routeInputs{
		DomainResult:      domainIn,
		HasLiveSignal:     true,
		RetrievalHitCount: 0,
		TopScore:          0,
	})
	if mode != answerModeToolFirst {
		t.Fatalf("Decide() = %q, want %q", mode, answerModeToolFirst)
	}
}

func TestModeSelectorChoosesRejectForOutOfDomainQuestion(t *testing.T) {
	t.Parallel()

	router := newQueryRouter()
	mode := router.Decide(routeInputs{
		DomainResult:      domainOut,
		HasLiveSignal:     false,
		RetrievalHitCount: 0,
		TopScore:          0,
	})
	if mode != answerModeReject {
		t.Fatalf("Decide() = %q, want %q", mode, answerModeReject)
	}
}

func TestHasLiveSignalDetectsRealtimeQuestion(t *testing.T) {
	t.Parallel()

	if !hasLiveSignal(normalizeQuery("今天第一节谁未到，并说明迟到规则")) {
		t.Fatalf("hasLiveSignal() = false, want true")
	}
}

func TestHasLiveSignalIgnoresPureRuleQuestion(t *testing.T) {
	t.Parallel()

	if hasLiveSignal(normalizeQuery("如果请假信息没能同步到位，会出现什么情况")) {
		t.Fatalf("hasLiveSignal() = true, want false")
	}
}

func TestHasLiveSignalIgnoresRulePriorityQuestionWithWho(t *testing.T) {
	t.Parallel()

	if hasLiveSignal(normalizeQuery("如果休息日和有课撞上了，系统按谁优先")) {
		t.Fatalf("hasLiveSignal() = true, want false")
	}
}

func TestIntentClassifierReturnsHelpForCapabilityQuestion(t *testing.T) {
	t.Parallel()

	decision := classifyIntent("你有什么功能", domainIn, RetrievalResult{})
	if decision.Intent != intentHelp {
		t.Fatalf("Intent = %q, want %q", decision.Intent, intentHelp)
	}
}

func TestIntentClassifierReturnsActionForSubscriptionCommand(t *testing.T) {
	t.Parallel()

	decision := classifyIntent("添加考勤订阅", domainIn, RetrievalResult{})
	if decision.Intent != intentAction {
		t.Fatalf("Intent = %q, want %q", decision.Intent, intentAction)
	}
}

func TestIntentClassifierReturnsLiveQueryForRealtimeAttendanceQuestion(t *testing.T) {
	t.Parallel()

	decision := classifyIntent("今天第一节谁未到", domainIn, RetrievalResult{})
	if decision.Intent != intentLiveQuery {
		t.Fatalf("Intent = %q, want %q", decision.Intent, intentLiveQuery)
	}
}

func TestIntentClassifierReturnsRuleForRuleQuestion(t *testing.T) {
	t.Parallel()

	decision := classifyIntent("迟到怎么判", domainIn, RetrievalResult{
		Hits: []KnowledgeHit{{Score: 18}},
	})
	if decision.Intent != intentRule {
		t.Fatalf("Intent = %q, want %q", decision.Intent, intentRule)
	}
}

func TestIntentClassifierReturnsMixedForRealtimePlusRuleQuestion(t *testing.T) {
	t.Parallel()

	decision := classifyIntent("今天第一节谁未到，并说明迟到规则", domainIn, RetrievalResult{
		Hits: []KnowledgeHit{{Score: 18}},
	})
	if decision.Intent != intentMixed {
		t.Fatalf("Intent = %q, want %q", decision.Intent, intentMixed)
	}
}

func TestIntentClassifierReturnsClarifyForDepartmentScopedSubscriptionWithoutDeptNames(t *testing.T) {
	t.Parallel()

	decision := classifyIntent("订阅指定部门考勤", domainIn, RetrievalResult{})
	if decision.Intent != intentClarify {
		t.Fatalf("Intent = %q, want %q", decision.Intent, intentClarify)
	}
}

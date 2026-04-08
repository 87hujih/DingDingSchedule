package agent

import "testing"

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

func TestSignalHelpersTreatPriorityRuleQuestionAsRuleButNotLive(t *testing.T) {
	t.Parallel()

	normalized := normalizeQuery("如果休息日和有课撞上了，系统按谁优先")
	if !hasRuleSignal(normalized) {
		t.Fatalf("hasRuleSignal() = false, want true")
	}
	if hasLiveSignal(normalized) {
		t.Fatalf("hasLiveSignal() = true, want false")
	}
}

func TestSignalHelpersTreatSubscriptionCommandAsActionIntent(t *testing.T) {
	t.Parallel()

	if !hasActionIntent(normalizeQuery("添加考勤订阅")) {
		t.Fatalf("hasActionIntent() = false, want true")
	}
}

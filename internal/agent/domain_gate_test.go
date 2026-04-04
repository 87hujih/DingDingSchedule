package agent

import "testing"

func TestDomainHintReturnsLikelyInForAttendanceRuleQuestion(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Hint("如果请假信息没能同步到位，会出现什么情况")
	if result != domainHintLikelyIn {
		t.Fatalf("Hint() = %q, want %q", result, domainHintLikelyIn)
	}
}

func TestDomainHintReturnsObviousOutForWeatherQuestion(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Hint("今天上海天气怎么样")
	if result != domainHintObviousOut {
		t.Fatalf("Hint() = %q, want %q", result, domainHintObviousOut)
	}
}

func TestDomainHintReturnsObviousOutForCodingQuestion(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Hint("帮我写一个二分查找")
	if result != domainHintObviousOut {
		t.Fatalf("Hint() = %q, want %q", result, domainHintObviousOut)
	}
}

func TestDomainHintReturnsUnknownForShortAmbiguousBusinessLikeMessage(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Hint("信工24级")
	if result != domainHintUnknown {
		t.Fatalf("Hint() = %q, want %q", result, domainHintUnknown)
	}
}

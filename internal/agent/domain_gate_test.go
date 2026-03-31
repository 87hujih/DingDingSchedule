package agent

import "testing"

func TestDomainGateAcceptsLeaveSyncFailureQuestion(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Check("如果请假信息没能同步到位，会出现什么情况")
	if result != domainIn {
		t.Fatalf("Check() = %q, want %q", result, domainIn)
	}
}

func TestDomainGateAcceptsRuleQuestionWithoutExplicitRuleKeyword(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Check("当前的考勤规则是什么")
	if result != domainIn {
		t.Fatalf("Check() = %q, want %q", result, domainIn)
	}
}

func TestDomainGateRejectsWeatherQuestion(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Check("今天上海天气怎么样")
	if result != domainOut {
		t.Fatalf("Check() = %q, want %q", result, domainOut)
	}
}

func TestDomainGateRejectsCodingQuestion(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Check("帮我写一个二分查找")
	if result != domainOut {
		t.Fatalf("Check() = %q, want %q", result, domainOut)
	}
}

func TestDomainGateAcceptsFreeUsersQuestionWithMeiKe(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Check("帮我查周三第1到2节哪些人没课")
	if result != domainIn {
		t.Fatalf("Check() = %q, want %q", result, domainIn)
	}
}

func TestDomainGateAcceptsRealtimeViewQuestion(t *testing.T) {
	t.Parallel()

	gate := newDomainGate()
	result := gate.Check("实时视图什么时候才会变成最终结算？")
	if result != domainIn {
		t.Fatalf("Check() = %q, want %q", result, domainIn)
	}
}

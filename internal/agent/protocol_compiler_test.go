package agent

import "testing"

func TestProtocolCompilerClassifiesAttendanceReadQuery(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{
		Message: "查询一下今天第二节的考勤状态",
	})
	if draft.Act != ActReadQuery {
		t.Fatalf("Act = %q, want %q", draft.Act, ActReadQuery)
	}
	if draft.Domain != DomainAttendance {
		t.Fatalf("Domain = %q, want %q", draft.Domain, DomainAttendance)
	}
	if draft.Operation != "attendance.query_status" {
		t.Fatalf("Operation = %q, want attendance.query_status", draft.Operation)
	}
}

func TestProtocolCompilerClassifiesManualSignCapabilityQuestion(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{
		Message: "可以执行代签功能吗",
	})
	if draft.Act != ActCapabilityQuestion {
		t.Fatalf("Act = %q, want %q", draft.Act, ActCapabilityQuestion)
	}
	if draft.Domain != DomainManualSign {
		t.Fatalf("Domain = %q, want %q", draft.Domain, DomainManualSign)
	}
	if draft.Operation != "manual_sign.describe_capability" {
		t.Fatalf("Operation = %q, want manual_sign.describe_capability", draft.Operation)
	}
}

func TestProtocolCompilerClassifiesSubscriptionWriteRequest(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{
		Message: "帮我开启本群考勤订阅",
	})
	if draft.Act != ActWriteRequest {
		t.Fatalf("Act = %q, want %q", draft.Act, ActWriteRequest)
	}
	if draft.Domain != DomainSubscription {
		t.Fatalf("Domain = %q, want %q", draft.Domain, DomainSubscription)
	}
	if draft.Operation != "subscription.start" {
		t.Fatalf("Operation = %q, want subscription.start", draft.Operation)
	}
}

func TestProtocolCompilerTreatsDepartmentHelpAsWorkflowContinueOnlyWhenDeptNamesMissing(t *testing.T) {
	t.Parallel()

	message := "现在都有哪些部门"
	withWorkflow := compileProtocol(protocolInput{
		Message: message,
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"dept_names"},
		},
	})
	if withWorkflow.Act != ActWorkflowContinue {
		t.Fatalf("Act = %q, want %q when dept_names missing", withWorkflow.Act, ActWorkflowContinue)
	}
	if withWorkflow.Operation != "subscription.list_departments" {
		t.Fatalf("Operation = %q, want subscription.list_departments", withWorkflow.Operation)
	}

	withoutWorkflow := compileProtocol(protocolInput{Message: message})
	if withoutWorkflow.Act != ActUnknown {
		t.Fatalf("Act without workflow = %q, want %q", withoutWorkflow.Act, ActUnknown)
	}
}

func TestProtocolCompilerTreatsManualSignNameReplyAsWorkflowContinue(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{
		Message: "张三",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "manual_sign.create",
			MissingFields: []string{"user_id"},
		},
	})
	if draft.Act != ActWorkflowContinue {
		t.Fatalf("Act = %q, want %q", draft.Act, ActWorkflowContinue)
	}
	if draft.Domain != DomainManualSign {
		t.Fatalf("Domain = %q, want %q", draft.Domain, DomainManualSign)
	}
	if draft.Operation != "manual_sign.create" {
		t.Fatalf("Operation = %q, want manual_sign.create", draft.Operation)
	}
}

func TestProtocolCompilerReturnsUnknownForAmbiguousSmallTalk(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{
		Message: "最近怎么样",
	})
	if draft.Act != ActUnknown {
		t.Fatalf("Act = %q, want %q", draft.Act, ActUnknown)
	}
}

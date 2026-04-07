package agent

import "testing"

func TestPolicyGateRejectsExecutionForCapabilityQuestion(t *testing.T) {
	t.Parallel()

	result := validateProtocol(ProtocolDraft{
		Act:       ActCapabilityQuestion,
		Domain:    DomainManualSign,
		Operation: "manual_sign.describe_capability",
	}, nil)
	if result.AllowExecution {
		t.Fatalf("AllowExecution = true, want false")
	}
	if result.ValidationCode != "capability_non_executable" {
		t.Fatalf("ValidationCode = %q, want capability_non_executable", result.ValidationCode)
	}
}

func TestPolicyGateAllowsReadQueryWithoutWorkflowHijack(t *testing.T) {
	t.Parallel()

	result := validateProtocol(ProtocolDraft{
		Act:       ActReadQuery,
		Domain:    DomainAttendance,
		Operation: "attendance.query_status",
	}, &protocolWorkflowContext{
		Type:          "subscription.start",
		MissingFields: []string{"dept_names"},
	})
	if !result.AllowExecution {
		t.Fatalf("AllowExecution = false, want true")
	}
	if result.ValidationCode != "allowed_read_query" {
		t.Fatalf("ValidationCode = %q, want allowed_read_query", result.ValidationCode)
	}
	if result.UseActiveWorkflow {
		t.Fatalf("UseActiveWorkflow = true, want false")
	}
}

func TestPolicyGateRequiresActiveWorkflowForWorkflowContinue(t *testing.T) {
	t.Parallel()

	result := validateProtocol(ProtocolDraft{
		Act:       ActWorkflowContinue,
		Domain:    DomainSubscription,
		Operation: "subscription.list_departments",
	}, nil)
	if result.AllowExecution {
		t.Fatalf("AllowExecution = true, want false")
	}
	if result.ValidationCode != "workflow_missing" {
		t.Fatalf("ValidationCode = %q, want workflow_missing", result.ValidationCode)
	}
}

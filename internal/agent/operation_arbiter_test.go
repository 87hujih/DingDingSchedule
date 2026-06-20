package agent

import "testing"

func TestOperationArbiterPrefersExplicitReadOverWorkflowSlotValue(t *testing.T) {
	t.Parallel()

	decision := newOperationArbiter().Decide(OperationArbiterInput{
		Message: "查询今天第二节考勤",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"dept_names"},
		},
		Candidates: []OperationCandidate{
			{
				Draft: ProtocolDraft{
					Act:       ActWorkflowContinue,
					Domain:    DomainSubscription,
					Operation: "subscription.start",
				},
				Source:     OperationCandidateSourceWorkflowSlot,
				Confidence: 0.8,
			},
			{
				Draft: ProtocolDraft{
					Act:       ActReadQuery,
					Domain:    DomainAttendance,
					Operation: "attendance.query_status",
				},
				Source:     OperationCandidateSourceCatalogAlias,
				Confidence: 0.95,
			},
		},
	})

	if decision.Draft.Operation != "attendance.query_status" {
		t.Fatalf("Draft = %+v, want attendance read query", decision.Draft)
	}
	if decision.Kind != OperationArbiterDecisionNewOperation {
		t.Fatalf("Kind = %q, want new operation", decision.Kind)
	}
	if !decision.InterruptWorkflow {
		t.Fatalf("InterruptWorkflow = false, want true for explicit read query")
	}
}

func TestOperationArbiterAllowsAuxiliaryOperationToContinueWorkflow(t *testing.T) {
	t.Parallel()

	decision := newOperationArbiter().Decide(OperationArbiterInput{
		Message: "都有哪些部门",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"scope"},
		},
		Candidates: []OperationCandidate{
			{
				Draft: ProtocolDraft{
					Act:       ActReadQuery,
					Domain:    DomainSubscription,
					Operation: "subscription.list_departments",
				},
				Source:     OperationCandidateSourceCatalogAlias,
				Confidence: 0.95,
			},
		},
	})

	if decision.Draft.Act != ActWorkflowContinue || decision.Draft.Operation != "subscription.list_departments" {
		t.Fatalf("Draft = %+v, want auxiliary workflow continue", decision.Draft)
	}
	if decision.Kind != OperationArbiterDecisionWorkflowAuxiliary {
		t.Fatalf("Kind = %q, want workflow auxiliary", decision.Kind)
	}
	if decision.InterruptWorkflow {
		t.Fatalf("InterruptWorkflow = true, want false for auxiliary operation")
	}
}

func TestOperationArbiterPrefersPureWorkflowCancelOverSlotValue(t *testing.T) {
	t.Parallel()

	decision := newOperationArbiter().Decide(OperationArbiterInput{
		Message: "算了",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"scope"},
		},
		Candidates: []OperationCandidate{
			{
				Draft: ProtocolDraft{
					Act:       ActWorkflowContinue,
					Domain:    DomainSubscription,
					Operation: "subscription.start",
				},
				Source:     OperationCandidateSourceWorkflowSlot,
				Confidence: 1,
			},
			{
				Draft: ProtocolDraft{
					Act:       ActWorkflowCancel,
					Domain:    DomainSubscription,
					Operation: "subscription.start",
				},
				Source:     OperationCandidateSourceWorkflowCtrl,
				Confidence: 1,
			},
		},
	})

	if decision.Draft.Act != ActWorkflowCancel {
		t.Fatalf("Draft = %+v, want workflow cancel", decision.Draft)
	}
	if decision.Kind != OperationArbiterDecisionWorkflowCancel {
		t.Fatalf("Kind = %q, want workflow cancel", decision.Kind)
	}
}

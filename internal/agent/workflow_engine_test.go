package agent

import "testing"

func TestWorkflowEngineAdvancesSubscriptionStartToReady(t *testing.T) {
	t.Parallel()

	wf, ok := startWorkflow(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "subscription.start",
	})
	if !ok {
		t.Fatalf("startWorkflow() ok = false, want true")
	}
	if wf.State != WorkflowCollectScope {
		t.Fatalf("State = %q, want %q", wf.State, WorkflowCollectScope)
	}

	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "subscription.start",
	}, trustedEntities{
		Scope: "all",
	})
	if result.Decision != WorkflowReadyToExecute {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowReadyToExecute)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowReady {
		t.Fatalf("Workflow state = %+v, want ready", result.Workflow)
	}
}

func TestWorkflowEngineSuspendsSubscriptionForNewReadQuery(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectDepartments,
		MissingSlots: []string{"dept_names"},
	}
	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActReadQuery,
		Domain:    DomainAttendance,
		Operation: "attendance.query_status",
	}, trustedEntities{})
	if result.Decision != WorkflowSuspendForNewRequest {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowSuspendForNewRequest)
	}
}

func TestWorkflowEngineRejectsInvalidContinuationShape(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectDepartments,
		MissingSlots: []string{"dept_names"},
	}
	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActWorkflowContinue,
		Domain:    DomainSubscription,
		Operation: "subscription.list_departments",
	}, trustedEntities{})
	if result.Decision != WorkflowRejectInvalidShape {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowRejectInvalidShape)
	}
}

func TestWorkflowEngineAdvancesManualSignToReady(t *testing.T) {
	t.Parallel()

	wf, ok := startWorkflow(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "manual_sign.create",
	})
	if !ok {
		t.Fatalf("startWorkflow() ok = false, want true")
	}

	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "manual_sign.create",
	}, trustedEntities{
		UserID:  7,
		Date:    "2026-04-07",
		Section: 2,
	})
	if result.Decision != WorkflowReadyToExecute {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowReadyToExecute)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowReady {
		t.Fatalf("Workflow state = %+v, want ready", result.Workflow)
	}
}

func TestWorkflowEngineCancelsActiveWorkflow(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		Type:  WorkflowSubscriptionStart,
		State: WorkflowCollectDepartments,
	}
	result := cancelWorkflow(wf)
	if result.Decision != WorkflowCanceled {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowCanceled)
	}
	if result.Workflow != nil {
		t.Fatalf("Workflow = %+v, want nil", result.Workflow)
	}
}

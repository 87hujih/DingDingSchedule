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
		Act:       ActWorkflowContinue,
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

func TestWorkflowEngineCollectsDepartmentScopeBeforeReady(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectScope,
		MissingSlots: []string{"scope"},
	}
	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActWorkflowContinue,
		Operation: "subscription.start",
	}, trustedEntities{
		Scope: "department",
	})
	if result.Decision != WorkflowContinueDecision {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowContinueDecision)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowCollectDepartments {
		t.Fatalf("Workflow = %+v, want collect departments", result.Workflow)
	}
}

func TestWorkflowEngineAdvancesDepartmentSubscriptionToReady(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectDepartments,
		MissingSlots: []string{"dept_ids"},
		Trusted: trustedEntities{
			Scope: "department",
		},
	}
	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActWorkflowContinue,
		Operation: "subscription.start",
	}, trustedEntities{
		DeptIDs: []int64{101},
	})
	if result.Decision != WorkflowReadyToExecute {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowReadyToExecute)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowReady {
		t.Fatalf("Workflow = %+v, want ready", result.Workflow)
	}
	if len(result.Workflow.Trusted.DeptIDs) != 1 || result.Workflow.Trusted.DeptIDs[0] != 101 {
		t.Fatalf("Trusted.DeptIDs = %v, want [101]", result.Workflow.Trusted.DeptIDs)
	}
}

func TestContinueSubscriptionWorkflowSwitchesDepartmentSelectionToAllScope(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		Type:          WorkflowSubscriptionStart,
		State:         WorkflowCollectDepartments,
		MissingFields: []string{"dept_names"},
		Trusted: trustedEntities{
			TenantID:     42,
			Scope:        "department",
			DepartmentID: 101,
			DeptIDs:      []int64{101},
			TrustedParams: map[string]TrustedParam{
				"conversation_id": {Field: "conversation_id", Value: "conv-1", TenantID: 42},
				"dept_ids":        {Field: "dept_ids", Value: []int64{101}, TenantID: 42},
			},
		},
	}
	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActWorkflowContinue,
		Operation: "subscription.start",
	}, trustedEntities{
		TenantID: 42,
		Scope:    "all",
	})

	if result.Decision != WorkflowReadyToExecute {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowReadyToExecute)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowReady {
		t.Fatalf("Workflow = %+v, want ready", result.Workflow)
	}
	if result.Workflow.Trusted.Scope != "all" {
		t.Fatalf("Trusted.Scope = %q, want all", result.Workflow.Trusted.Scope)
	}
	if result.Workflow.Trusted.DepartmentID != 0 || len(result.Workflow.Trusted.DeptIDs) != 0 {
		t.Fatalf("department IDs = %d/%v, want cleared", result.Workflow.Trusted.DepartmentID, result.Workflow.Trusted.DeptIDs)
	}
	if _, ok := result.Workflow.Trusted.TrustedParams["dept_ids"]; ok {
		t.Fatalf("TrustedParams[dept_ids] = %+v, want removed", result.Workflow.Trusted.TrustedParams["dept_ids"])
	}
	if _, ok := result.Workflow.Trusted.TrustedParams["conversation_id"]; !ok {
		t.Fatalf("TrustedParams = %+v, want tenant-scoped conversation_id preserved", result.Workflow.Trusted.TrustedParams)
	}
	if len(workflowMissingFields(result.Workflow)) != 0 {
		t.Fatalf("missing fields = %v, want none", workflowMissingFields(result.Workflow))
	}
}

func TestWorkflowEngineReturnsMetaForDepartmentList(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectDepartments,
		MissingSlots: []string{"dept_ids"},
	}
	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActWorkflowContinue,
		Domain:    DomainSubscription,
		Operation: "subscription.list_departments",
	}, trustedEntities{})
	if result.Decision != WorkflowMetaResult {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowMetaResult)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowCollectDepartments {
		t.Fatalf("Workflow = %+v, want collect departments kept active", result.Workflow)
	}
}

func TestWorkflowInterruptsOnExplicitNewRequest(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		ID:           "wf-1",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectDepartments,
		MissingSlots: []string{"dept_names"},
	}
	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActCapabilityQuestion,
		Domain:    DomainManualSign,
		Operation: "manual_sign.describe_capability",
	}, trustedEntities{})
	if result.Decision != WorkflowInterrupted {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowInterrupted)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowInterruptedState {
		t.Fatalf("Workflow = %+v, want interrupted state", result.Workflow)
	}
}

func TestWorkflowInterruptsOnExplicitWriteRequest(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		ID:           "wf-1",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectScope,
		MissingSlots: []string{"scope"},
	}
	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActWriteRequest,
		Domain:    DomainSubscription,
		Operation: "subscription.cancel",
	}, trustedEntities{})
	if result.Decision != WorkflowInterrupted {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowInterrupted)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowInterruptedState {
		t.Fatalf("Workflow = %+v, want interrupted state", result.Workflow)
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
		Operation: "subscription.start",
	}, trustedEntities{})
	if result.Decision != WorkflowRejectInvalidShape {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowRejectInvalidShape)
	}
}

func TestWorkflowEngineCancelsActiveWorkflow(t *testing.T) {
	t.Parallel()

	wf := WorkflowSnapshot{
		ID:           "wf-1",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectScope,
		MissingSlots: []string{"scope"},
	}
	result := continueWorkflow(wf, ProtocolDraft{
		Act:       ActWorkflowCancel,
		Operation: "subscription.start",
	}, trustedEntities{})
	if result.Decision != WorkflowCanceled {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowCanceled)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowCancelled {
		t.Fatalf("Workflow = %+v, want cancelled state", result.Workflow)
	}
}

func TestWorkflowEngineDoesNotStartManualSignInProtocolLive(t *testing.T) {
	t.Parallel()

	_, ok := startWorkflow(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "manual_sign.create",
	})
	if ok {
		t.Fatalf("startWorkflow(manual_sign.create) ok = true, want false")
	}
}

func TestWorkflowEngineMarksReadyWorkflowCompleted(t *testing.T) {
	t.Parallel()

	result := completeWorkflow(WorkflowSnapshot{
		ID:    "wf-1",
		Type:  WorkflowSubscriptionStart,
		State: WorkflowReady,
	})
	if result.Decision != WorkflowCompletedDecision {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowCompletedDecision)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowCompleted {
		t.Fatalf("Workflow = %+v, want completed state", result.Workflow)
	}
}

func TestSubscriptionWorkflowUsesAllResolvedDepartmentIDs(t *testing.T) {
	t.Parallel()

	got := subscriptionDeptIDsFromTrusted(trustedEntities{
		DepartmentID: 101,
		DeptIDs:      []int64{101, 102},
	})
	if len(got) != 2 || got[0] != 101 || got[1] != 102 {
		t.Fatalf("subscriptionDeptIDsFromTrusted() = %v, want [101 102]", got)
	}
}

func TestSessionClearsTerminalWorkflowResults(t *testing.T) {
	t.Parallel()

	sm := newSessionManager()
	key := "tenant:conv:user"
	sm.setWorkflowState(key, &WorkflowSnapshot{
		ID:    "wf-1",
		Type:  WorkflowSubscriptionStart,
		State: WorkflowCollectScope,
	})
	sm.applyWorkflowResult(key, WorkflowResult{
		Decision: WorkflowInterrupted,
		Workflow: &WorkflowSnapshot{
			ID:    "wf-1",
			Type:  WorkflowSubscriptionStart,
			State: WorkflowInterruptedState,
		},
	})
	_, workflow := sm.getWorkflowState(key)
	if workflow != nil {
		t.Fatalf("workflow = %+v, want cleared for terminal result", workflow)
	}

	sm.setWorkflowState(key, &WorkflowSnapshot{
		ID:    "wf-2",
		Type:  WorkflowSubscriptionStart,
		State: WorkflowReady,
	})
	sm.applyWorkflowResult(key, WorkflowResult{
		Decision: WorkflowCompletedDecision,
		Workflow: &WorkflowSnapshot{
			ID:    "wf-2",
			Type:  WorkflowSubscriptionStart,
			State: WorkflowCompleted,
		},
	})
	_, workflow = sm.getWorkflowState(key)
	if workflow != nil {
		t.Fatalf("workflow = %+v, want cleared for completed result", workflow)
	}
}

func TestInterruptActiveWorkflowAppliesTerminalLifecycle(t *testing.T) {
	t.Parallel()

	sm := newSessionManager()
	key := "tenant:conv:user"
	workflow := &WorkflowSnapshot{
		ID:           "wf-1",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectScope,
		MissingSlots: []string{"scope"},
	}
	sm.setWorkflowState(key, workflow)

	result := interruptActiveWorkflow(sm, key, workflow, ProtocolDraft{
		Act:       ActReadQuery,
		Domain:    DomainAttendance,
		Operation: "attendance.query_status",
	})
	if result.Decision != WorkflowInterrupted {
		t.Fatalf("Decision = %q, want %q", result.Decision, WorkflowInterrupted)
	}
	if result.Workflow == nil || result.Workflow.State != WorkflowInterruptedState {
		t.Fatalf("Workflow = %+v, want interrupted state", result.Workflow)
	}
	_, active := sm.getWorkflowState(key)
	if active != nil {
		t.Fatalf("workflow = %+v, want cleared after interrupted lifecycle", active)
	}
}

package agent

import (
	"fmt"
	"time"
)

// startWorkflow starts a workflow snapshot from a protocol draft.
func startWorkflow(draft ProtocolDraft) (WorkflowSnapshot, bool) {
	switch draft.Operation {
	case workflowPrimaryOperationName(WorkflowSubscriptionStart):
		return WorkflowSnapshot{
			ID:            fmt.Sprintf("wf-%d", time.Now().UnixNano()),
			Type:          WorkflowSubscriptionStart,
			State:         WorkflowCollectScope,
			MissingFields: []string{"scope"},
			MissingSlots:  []string{"scope"},
		}, true
	default:
		return WorkflowSnapshot{}, false
	}
}

// continueWorkflow continues a workflow snapshot with trusted entities.
func continueWorkflow(workflow WorkflowSnapshot, draft ProtocolDraft, trusted trustedEntities) WorkflowResult {
	if draft.Act == ActWorkflowCancel {
		workflow.State = WorkflowCancelled
		setWorkflowMissingFields(&workflow, nil)
		return WorkflowResult{Decision: WorkflowCanceled, Workflow: &workflow}
	}

	if isExplicitNewRequest(draft.Act) {
		workflow.State = WorkflowInterruptedState
		setWorkflowMissingFields(&workflow, nil)
		return WorkflowResult{Decision: WorkflowInterrupted, Workflow: &workflow}
	}

	next := workflow
	switch workflow.Type {
	case WorkflowSubscriptionStart:
		return continueSubscriptionWorkflow(next, draft, trusted)
	case WorkflowManualSignCreate:
		return continueManualSignWorkflow(next, trusted)
	default:
		return WorkflowResult{Decision: WorkflowRejectInvalidShape, Workflow: &workflow}
	}
}

// completeWorkflow marks an executed workflow as completed.
func completeWorkflow(workflow WorkflowSnapshot) WorkflowResult {
	workflow.State = WorkflowCompleted
	setWorkflowMissingFields(&workflow, nil)
	return WorkflowResult{Decision: WorkflowCompletedDecision, Workflow: &workflow}
}

// interruptActiveWorkflow applies interrupted lifecycle to an active workflow.
func interruptActiveWorkflow(sessions *sessionManager, sessionKey string, workflow *WorkflowSnapshot, draft ProtocolDraft) WorkflowResult {
	if workflow == nil {
		return WorkflowResult{Decision: WorkflowInterrupted}
	}
	result := continueWorkflow(*workflow, draft, trustedEntities{})
	if result.Decision != WorkflowInterrupted {
		next := cloneWorkflowSnapshot(workflow)
		next.State = WorkflowInterruptedState
		setWorkflowMissingFields(next, nil)
		result = WorkflowResult{Decision: WorkflowInterrupted, Workflow: next}
	}
	if sessions != nil {
		sessions.applyWorkflowResult(sessionKey, result)
	}
	return result
}

// continueSubscriptionWorkflow continues subscription workflow.
func continueSubscriptionWorkflow(workflow WorkflowSnapshot, draft ProtocolDraft, trusted trustedEntities) WorkflowResult {
	if draft.Operation == workflowAuxiliaryOperationName(WorkflowSubscriptionStart, ExecutorBindingSubscriptionListDepartments) && workflow.State == WorkflowCollectDepartments {
		return WorkflowResult{Decision: WorkflowMetaResult, Workflow: &workflow}
	}
	if draft.Operation != workflowPrimaryOperationName(WorkflowSubscriptionStart) {
		return WorkflowResult{Decision: WorkflowRejectInvalidShape, Workflow: &workflow}
	}

	switch workflow.State {
	case WorkflowCollectScope:
		switch trusted.Scope {
		case "all":
			workflow.Trusted.Scope = trusted.Scope
			mergeTrustedParams(&workflow.Trusted, trusted.TrustedParams)
			workflow.State = WorkflowReady
			setWorkflowMissingFields(&workflow, nil)
			return WorkflowResult{Decision: WorkflowReadyToExecute, Workflow: &workflow}
		case "department":
			workflow.Trusted.Scope = trusted.Scope
			mergeTrustedParams(&workflow.Trusted, trusted.TrustedParams)
			deptIDs := trusted.DeptIDs
			if len(deptIDs) == 0 && trusted.DepartmentID != 0 {
				deptIDs = []int64{trusted.DepartmentID}
			}
			if len(deptIDs) > 0 {
				workflow.Trusted.DepartmentID = deptIDs[0]
				workflow.Trusted.DeptIDs = append([]int64(nil), deptIDs...)
				workflow.State = WorkflowReady
				setWorkflowMissingFields(&workflow, nil)
				return WorkflowResult{Decision: WorkflowReadyToExecute, Workflow: &workflow}
			}
			workflow.State = WorkflowCollectDepartments
			setWorkflowMissingFields(&workflow, []string{"dept_names"})
			return WorkflowResult{Decision: WorkflowContinueDecision, Workflow: &workflow}
		default:
			return WorkflowResult{Decision: WorkflowRejectInvalidShape, Workflow: &workflow}
		}
	case WorkflowCollectDepartments:
		if trusted.Scope == "all" {
			workflow.Trusted.Scope = trusted.Scope
			mergeTrustedParams(&workflow.Trusted, trusted.TrustedParams)
			workflow.Trusted.DepartmentID = 0
			workflow.Trusted.DeptIDs = nil
			delete(workflow.Trusted.TrustedParams, "dept_ids")
			workflow.State = WorkflowReady
			setWorkflowMissingFields(&workflow, nil)
			return WorkflowResult{Decision: WorkflowReadyToExecute, Workflow: &workflow}
		}
		deptIDs := trusted.DeptIDs
		if len(deptIDs) == 0 && trusted.DepartmentID != 0 {
			deptIDs = []int64{trusted.DepartmentID}
		}
		if len(deptIDs) == 0 {
			return WorkflowResult{Decision: WorkflowRejectInvalidShape, Workflow: &workflow}
		}
		workflow.Trusted.DepartmentID = deptIDs[0]
		workflow.Trusted.DeptIDs = append([]int64(nil), deptIDs...)
		mergeTrustedParams(&workflow.Trusted, trusted.TrustedParams)
		workflow.State = WorkflowReady
		setWorkflowMissingFields(&workflow, nil)
		return WorkflowResult{Decision: WorkflowReadyToExecute, Workflow: &workflow}
	default:
		return WorkflowResult{Decision: WorkflowRejectInvalidShape, Workflow: &workflow}
	}
}

// continueManualSignWorkflow continues manual sign workflow.
func continueManualSignWorkflow(workflow WorkflowSnapshot, trusted trustedEntities) WorkflowResult {
	if trusted.UserID != 0 {
		workflow.Trusted.UserID = trusted.UserID
	}
	if trusted.UserName != "" {
		workflow.Trusted.UserName = trusted.UserName
	}
	mergeTrustedParams(&workflow.Trusted, trusted.TrustedParams)
	if trusted.Date != "" {
		workflow.Trusted.Date = trusted.Date
	}
	if trusted.Section != 0 {
		workflow.Trusted.Section = trusted.Section
	}

	setWorkflowMissingFields(&workflow, workflowMissingSlots(workflow.Trusted))
	if len(workflow.MissingSlots) == 0 {
		workflow.State = WorkflowReady
		return WorkflowResult{Decision: WorkflowReadyToExecute, Workflow: &workflow}
	}

	workflow.State = nextManualSignState(workflow.MissingSlots[0])
	return WorkflowResult{Decision: WorkflowContinueDecision, Workflow: &workflow}
}

// workflowMissingSlots handles workflow missing slots.
func workflowMissingSlots(trusted trustedEntities) []string {
	missing := make([]string, 0, 3)
	if trusted.UserID == 0 {
		missing = append(missing, "user_id")
	}
	if trusted.Date == "" {
		missing = append(missing, "date")
	}
	if trusted.Section == 0 {
		missing = append(missing, "section")
	}
	return missing
}

// nextManualSignState handles next manual sign state.
func nextManualSignState(slot string) WorkflowState {
	switch slot {
	case "user_id":
		return WorkflowCollectUser
	case "date":
		return WorkflowCollectDate
	case "section":
		return WorkflowCollectSection
	default:
		return WorkflowCollectUser
	}
}

func mergeTrustedParams(dst *trustedEntities, params map[string]TrustedParam) {
	if dst == nil || len(params) == 0 {
		return
	}
	if dst.TrustedParams == nil {
		dst.TrustedParams = make(map[string]TrustedParam, len(params))
	}
	for field, param := range params {
		dst.TrustedParams[field] = param
	}
}

// isExplicitNewRequest reports whether it is explicit new request.
func isExplicitNewRequest(act UserAct) bool {
	switch act {
	case ActCapabilityQuestion, ActRuleQuestion, ActReadQuery, ActWriteRequest, ActHelp:
		return true
	default:
		return false
	}
}

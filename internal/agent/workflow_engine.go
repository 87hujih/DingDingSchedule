package agent

import (
	"fmt"
	"time"
)

func startWorkflow(draft ProtocolDraft) (WorkflowSnapshot, bool) {
	switch draft.Operation {
	case "subscription.start":
		return WorkflowSnapshot{
			ID:           fmt.Sprintf("wf-%d", time.Now().UnixNano()),
			Type:         WorkflowSubscriptionStart,
			State:        WorkflowCollectScope,
			MissingSlots: []string{"scope"},
		}, true
	case "manual_sign.create":
		return WorkflowSnapshot{
			ID:           fmt.Sprintf("wf-%d", time.Now().UnixNano()),
			Type:         WorkflowManualSignCreate,
			State:        WorkflowCollectUser,
			MissingSlots: []string{"user_id", "date", "section"},
		}, true
	default:
		return WorkflowSnapshot{}, false
	}
}

func continueWorkflow(workflow WorkflowSnapshot, draft ProtocolDraft, trusted trustedEntities) WorkflowResult {
	if isExplicitNewRequest(draft.Act) {
		return WorkflowResult{Decision: WorkflowSuspendForNewRequest, Workflow: &workflow}
	}

	next := workflow
	switch workflow.Type {
	case WorkflowSubscriptionStart:
		return continueSubscriptionWorkflow(next, trusted)
	case WorkflowManualSignCreate:
		return continueManualSignWorkflow(next, trusted)
	default:
		return WorkflowResult{Decision: WorkflowRejectInvalidShape, Workflow: &workflow}
	}
}

func cancelWorkflow(WorkflowSnapshot) WorkflowResult {
	return WorkflowResult{Decision: WorkflowCanceled, Workflow: nil}
}

func continueSubscriptionWorkflow(workflow WorkflowSnapshot, trusted trustedEntities) WorkflowResult {
	switch workflow.State {
	case WorkflowCollectScope:
		switch trusted.Scope {
		case "all":
			workflow.Trusted.Scope = trusted.Scope
			workflow.State = WorkflowReady
			workflow.MissingSlots = nil
			return WorkflowResult{Decision: WorkflowReadyToExecute, Workflow: &workflow}
		case "department":
			workflow.Trusted.Scope = trusted.Scope
			workflow.State = WorkflowCollectDepartments
			workflow.MissingSlots = []string{"dept_names"}
			return WorkflowResult{Decision: WorkflowContinueDecision, Workflow: &workflow}
		default:
			return WorkflowResult{Decision: WorkflowRejectInvalidShape, Workflow: &workflow}
		}
	case WorkflowCollectDepartments:
		if trusted.DepartmentID == 0 {
			return WorkflowResult{Decision: WorkflowRejectInvalidShape, Workflow: &workflow}
		}
		workflow.Trusted.DepartmentID = trusted.DepartmentID
		workflow.State = WorkflowReady
		workflow.MissingSlots = nil
		return WorkflowResult{Decision: WorkflowReadyToExecute, Workflow: &workflow}
	default:
		return WorkflowResult{Decision: WorkflowRejectInvalidShape, Workflow: &workflow}
	}
}

func continueManualSignWorkflow(workflow WorkflowSnapshot, trusted trustedEntities) WorkflowResult {
	if trusted.UserID != 0 {
		workflow.Trusted.UserID = trusted.UserID
	}
	if trusted.UserName != "" {
		workflow.Trusted.UserName = trusted.UserName
	}
	if trusted.Date != "" {
		workflow.Trusted.Date = trusted.Date
	}
	if trusted.Section != 0 {
		workflow.Trusted.Section = trusted.Section
	}

	workflow.MissingSlots = workflowMissingSlots(workflow.Trusted)
	if len(workflow.MissingSlots) == 0 {
		workflow.State = WorkflowReady
		return WorkflowResult{Decision: WorkflowReadyToExecute, Workflow: &workflow}
	}

	workflow.State = nextManualSignState(workflow.MissingSlots[0])
	return WorkflowResult{Decision: WorkflowContinueDecision, Workflow: &workflow}
}

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

func isExplicitNewRequest(act UserAct) bool {
	switch act {
	case ActCapabilityQuestion, ActRuleQuestion, ActReadQuery, ActHelp:
		return true
	default:
		return false
	}
}

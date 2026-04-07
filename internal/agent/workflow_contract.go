package agent

type WorkflowType string
type WorkflowState string
type WorkflowDecision string

const (
	WorkflowSubscriptionStart WorkflowType = "subscription.start"
	WorkflowManualSignCreate  WorkflowType = "manual_sign.create"
)

const (
	WorkflowCollectScope       WorkflowState = "collect_scope"
	WorkflowCollectDepartments WorkflowState = "collect_departments"
	WorkflowCollectUser        WorkflowState = "collect_user"
	WorkflowCollectDate        WorkflowState = "collect_date"
	WorkflowCollectSection     WorkflowState = "collect_section"
	WorkflowReady              WorkflowState = "ready"
)

const (
	WorkflowContinueDecision     WorkflowDecision = "continue"
	WorkflowReadyToExecute       WorkflowDecision = "ready_to_execute"
	WorkflowSuspendForNewRequest WorkflowDecision = "suspend_for_new_request"
	WorkflowRejectInvalidShape   WorkflowDecision = "reject_invalid_shape"
	WorkflowCanceled             WorkflowDecision = "canceled"
)

type WorkflowSnapshot struct {
	ID           string
	Type         WorkflowType
	State        WorkflowState
	MissingSlots []string
	Trusted      trustedEntities
}

type WorkflowResult struct {
	Decision WorkflowDecision
	Workflow *WorkflowSnapshot
}

func cloneWorkflowSnapshot(workflow *WorkflowSnapshot) *WorkflowSnapshot {
	if workflow == nil {
		return nil
	}

	cloned := *workflow
	if len(workflow.MissingSlots) > 0 {
		cloned.MissingSlots = append([]string(nil), workflow.MissingSlots...)
	}
	return &cloned
}

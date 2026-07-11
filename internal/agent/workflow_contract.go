package agent

import "time"

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
	WorkflowCompleted          WorkflowState = "completed"
	WorkflowCancelled          WorkflowState = "cancelled"
	WorkflowInterruptedState   WorkflowState = "interrupted"
)

const (
	WorkflowContinueDecision     WorkflowDecision = "continue"
	WorkflowReadyToExecute       WorkflowDecision = "ready_to_execute"
	WorkflowSuspendForNewRequest WorkflowDecision = "suspend_for_new_request"
	WorkflowRejectInvalidShape   WorkflowDecision = "reject_invalid_shape"
	WorkflowMetaResult           WorkflowDecision = "meta_result"
	WorkflowCompletedDecision    WorkflowDecision = "completed"
	WorkflowCanceled             WorkflowDecision = "cancel"
	WorkflowInterrupted          WorkflowDecision = "interrupt"
	WorkflowStartNew             WorkflowDecision = "start_new"
	WorkflowSingleTurn           WorkflowDecision = "single_turn"
)

type TrustedEntity struct {
	ID       string
	Type     string
	Label    string
	Value    any
	TenantID uint
}

type Candidate struct {
	ID       string
	Label    string
	Value    any
	TenantID uint
}

type WorkflowSnapshot struct {
	ID             string
	TenantID       uint
	ActorUserID    uint
	ConversationID string

	Type  WorkflowType
	State WorkflowState

	MissingFields   []string
	TrustedEntities map[string]TrustedEntity
	Candidates      map[string][]Candidate

	LastUserMessage string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       time.Time
	Version         int64

	MissingSlots []string
	Trusted      trustedEntities
}

type WorkflowResult struct {
	Decision WorkflowDecision
	Workflow *WorkflowSnapshot
}

type VersionedWorkflow struct {
	Snapshot *WorkflowSnapshot
	Version  uint64
}

// cloneWorkflowSnapshot clones workflow snapshot.
func cloneWorkflowSnapshot(workflow *WorkflowSnapshot) *WorkflowSnapshot {
	if workflow == nil {
		return nil
	}

	cloned := *workflow
	cloned.MissingFields = cloneStringSlice(workflowMissingFields(workflow))
	if len(workflow.MissingSlots) > 0 {
		cloned.MissingSlots = append([]string(nil), workflow.MissingSlots...)
	} else {
		cloned.MissingSlots = cloneStringSlice(cloned.MissingFields)
	}
	cloned.TrustedEntities = cloneTrustedEntities(workflow.TrustedEntities)
	cloned.Candidates = cloneWorkflowCandidates(workflow.Candidates)
	cloned.Trusted.DeptIDs = append([]int64(nil), workflow.Trusted.DeptIDs...)
	if cloned.Trusted.TrustedParams != nil {
		cloned.Trusted.TrustedParams = cloneTrustedParamMap(workflow.Trusted.TrustedParams)
	}
	return &cloned
}

func workflowMissingFields(workflow *WorkflowSnapshot) []string {
	if workflow == nil {
		return nil
	}
	if len(workflow.MissingFields) > 0 {
		return workflow.MissingFields
	}
	return workflow.MissingSlots
}

func setWorkflowMissingFields(workflow *WorkflowSnapshot, fields []string) {
	if workflow == nil {
		return
	}
	workflow.MissingFields = cloneStringSlice(fields)
	workflow.MissingSlots = cloneStringSlice(fields)
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneTrustedEntities(values map[string]TrustedEntity) map[string]TrustedEntity {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]TrustedEntity, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneWorkflowCandidates(values map[string][]Candidate) map[string][]Candidate {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string][]Candidate, len(values))
	for key, candidates := range values {
		if len(candidates) == 0 {
			continue
		}
		cloned[key] = append([]Candidate(nil), candidates...)
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneTrustedParamMap(values map[string]TrustedParam) map[string]TrustedParam {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]TrustedParam, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

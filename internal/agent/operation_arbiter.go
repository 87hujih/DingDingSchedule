package agent

type OperationCandidateSource string

const (
	OperationCandidateSourceLLM          OperationCandidateSource = "llm"
	OperationCandidateSourceCatalogAlias OperationCandidateSource = "catalog_alias"
	OperationCandidateSourceWorkflowSlot OperationCandidateSource = "workflow_slot"
	OperationCandidateSourceWorkflowCtrl OperationCandidateSource = "workflow_control"
	OperationCandidateSourceLegacy       OperationCandidateSource = "legacy_deterministic"
)

type OperationCandidate struct {
	Draft      ProtocolDraft
	Source     OperationCandidateSource
	Confidence float64
	Evidence   string
}

type OperationArbiterDecisionKind string

const (
	OperationArbiterDecisionUnknown           OperationArbiterDecisionKind = "unknown"
	OperationArbiterDecisionNewOperation      OperationArbiterDecisionKind = "new_operation"
	OperationArbiterDecisionWorkflowContinue  OperationArbiterDecisionKind = "workflow_continue"
	OperationArbiterDecisionWorkflowAuxiliary OperationArbiterDecisionKind = "workflow_auxiliary"
	OperationArbiterDecisionWorkflowCancel    OperationArbiterDecisionKind = "workflow_cancel"
)

type OperationArbiterInput struct {
	Message        string
	ActiveWorkflow *protocolWorkflowContext
	Candidates     []OperationCandidate
}

type OperationArbiterDecision struct {
	Kind              OperationArbiterDecisionKind
	Draft             ProtocolDraft
	Source            OperationCandidateSource
	InterruptWorkflow bool
	Reason            string
}

type operationArbiter struct{}

func newOperationArbiter() operationArbiter {
	return operationArbiter{}
}

func deterministicOperationDecision(input OperationArbiterInput) (OperationArbiterDecision, bool) {
	if len(input.Candidates) != 1 {
		return OperationArbiterDecision{}, false
	}

	candidate := input.Candidates[0]
	if candidate.Confidence != 1 || candidate.Draft.Operation == "" {
		return OperationArbiterDecision{}, false
	}
	switch candidate.Source {
	case OperationCandidateSourceCatalogAlias, OperationCandidateSourceWorkflowCtrl:
	case OperationCandidateSourceWorkflowSlot:
		if !isCandidateSelectionShape(input.Message) {
			return OperationArbiterDecision{}, false
		}
	default:
		return OperationArbiterDecision{}, false
	}

	decision := newOperationArbiter().Decide(input)
	if decision.Kind == OperationArbiterDecisionUnknown || decision.Draft.Operation == "" {
		return OperationArbiterDecision{}, false
	}
	return decision, true
}

func (operationArbiter) Decide(input OperationArbiterInput) OperationArbiterDecision {
	if len(input.Candidates) == 0 {
		return OperationArbiterDecision{
			Kind:   OperationArbiterDecisionUnknown,
			Draft:  ProtocolDraft{Act: ActUnknown, Domain: DomainUnknown, ClarifyReason: "unknown_intent"},
			Reason: "no_candidates",
		}
	}

	if candidate, ok := bestCandidate(input.Candidates, func(candidate OperationCandidate) bool {
		return candidate.Draft.Act == ActWriteRequest || candidate.Draft.Act == ActReadQuery ||
			candidate.Draft.Act == ActCapabilityQuestion || candidate.Draft.Act == ActRuleQuestion ||
			candidate.Draft.Act == ActHelp
	}); ok {
		if operationIsAuxiliaryForActiveWorkflow(candidate.Draft.Operation, input.ActiveWorkflow) {
			draft := candidate.Draft
			draft.Act = ActWorkflowContinue
			return OperationArbiterDecision{
				Kind:   OperationArbiterDecisionWorkflowAuxiliary,
				Draft:  draft,
				Source: candidate.Source,
				Reason: "active_workflow_auxiliary_operation",
			}
		}
		return OperationArbiterDecision{
			Kind:              OperationArbiterDecisionNewOperation,
			Draft:             candidate.Draft,
			Source:            candidate.Source,
			InterruptWorkflow: input.ActiveWorkflow != nil && policyExplicitNewRequest(candidate.Draft.Act),
			Reason:            "explicit_operation",
		}
	}

	if candidate, ok := bestCandidate(input.Candidates, func(candidate OperationCandidate) bool {
		return candidate.Draft.Act == ActWorkflowCancel
	}); ok {
		return OperationArbiterDecision{
			Kind:   OperationArbiterDecisionWorkflowCancel,
			Draft:  candidate.Draft,
			Source: candidate.Source,
			Reason: "workflow_control",
		}
	}

	if candidate, ok := bestCandidate(input.Candidates, func(candidate OperationCandidate) bool {
		return candidate.Draft.Act == ActWorkflowContinue
	}); ok {
		return OperationArbiterDecision{
			Kind:   OperationArbiterDecisionWorkflowContinue,
			Draft:  candidate.Draft,
			Source: candidate.Source,
			Reason: "workflow_slot_value",
		}
	}

	candidate := input.Candidates[0]
	if candidate.Draft.Act == ActUnknown {
		return OperationArbiterDecision{
			Kind:   OperationArbiterDecisionUnknown,
			Draft:  candidate.Draft,
			Source: candidate.Source,
			Reason: "unknown_candidate",
		}
	}
	return OperationArbiterDecision{
		Kind:   OperationArbiterDecisionUnknown,
		Draft:  ProtocolDraft{Act: ActUnknown, Domain: DomainUnknown, ClarifyReason: "unknown_intent"},
		Source: candidate.Source,
		Reason: "unsupported_candidate",
	}
}

func bestCandidate(candidates []OperationCandidate, match func(OperationCandidate) bool) (OperationCandidate, bool) {
	var best OperationCandidate
	found := false
	for _, candidate := range candidates {
		if !match(candidate) {
			continue
		}
		if !found || operationCandidateRank(candidate) > operationCandidateRank(best) ||
			(operationCandidateRank(candidate) == operationCandidateRank(best) && candidate.Confidence > best.Confidence) {
			best = candidate
			found = true
		}
	}
	return best, found
}

func operationCandidateRank(candidate OperationCandidate) int {
	switch candidate.Source {
	case OperationCandidateSourceCatalogAlias:
		return 50
	case OperationCandidateSourceLLM:
		return 40
	case OperationCandidateSourceWorkflowSlot:
		return 30
	case OperationCandidateSourceWorkflowCtrl:
		return 20
	case OperationCandidateSourceLegacy:
		return 10
	default:
		return 0
	}
}

func operationIsAuxiliaryForActiveWorkflow(operation string, workflow *protocolWorkflowContext) bool {
	if workflow == nil {
		return false
	}
	primary, ok := lookupOperation(workflow.Type)
	if !ok || primary.Workflow == nil {
		return false
	}
	for _, auxiliary := range primary.Workflow.AuxiliaryOperations {
		if auxiliary == operation {
			return true
		}
	}
	return false
}

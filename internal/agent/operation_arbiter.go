package agent

type OperationCandidateSource string

const (
	OperationCandidateSourceLLM          OperationCandidateSource = "llm"
	OperationCandidateSourceCatalogAlias OperationCandidateSource = "catalog_alias"
	OperationCandidateSourceWorkflowSlot OperationCandidateSource = "workflow_slot"
	OperationCandidateSourceWorkflowCtrl OperationCandidateSource = "workflow_control"
	OperationCandidateSourceLegacy       OperationCandidateSource = "legacy_deterministic"
)

type CandidateMatchKind string

const (
	MatchExactAlias           CandidateMatchKind = "exact_alias"
	MatchContainedAlias       CandidateMatchKind = "contained_alias"
	MatchExactWorkflowControl CandidateMatchKind = "exact_workflow_control"
	MatchExactCandidate       CandidateMatchKind = "exact_candidate"
	MatchStructuredSlot       CandidateMatchKind = "structured_slot"
	MatchLegacyRule           CandidateMatchKind = "legacy_rule"
	MatchLLM                  CandidateMatchKind = "llm"
)

type OperationCandidate struct {
	Draft        ProtocolDraft
	Source       OperationCandidateSource
	MatchKind    CandidateMatchKind
	ShortCircuit bool
	Confidence   float64
	Evidence     string
}

type OperationArbiterDecisionKind string

const (
	OperationArbiterDecisionUnknown           OperationArbiterDecisionKind = "unknown"
	OperationArbiterDecisionNewOperation      OperationArbiterDecisionKind = "new_operation"
	OperationArbiterDecisionWorkflowContinue  OperationArbiterDecisionKind = "workflow_continue"
	OperationArbiterDecisionWorkflowAuxiliary OperationArbiterDecisionKind = "workflow_auxiliary"
	OperationArbiterDecisionWorkflowCancel    OperationArbiterDecisionKind = "workflow_cancel"
	OperationArbiterDecisionAmbiguous         OperationArbiterDecisionKind = "ambiguous"
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

func (operationArbiter) Decide(input OperationArbiterInput) OperationArbiterDecision {
	if len(input.Candidates) == 0 {
		return OperationArbiterDecision{
			Kind:   OperationArbiterDecisionUnknown,
			Draft:  ProtocolDraft{Act: ActUnknown, Domain: DomainUnknown, ClarifyReason: "unknown_intent"},
			Reason: "no_candidates",
		}
	}

	if ambiguousOperationCandidates(input.Candidates) {
		return OperationArbiterDecision{
			Kind:   OperationArbiterDecisionAmbiguous,
			Draft:  unknownIntentDraft("ambiguous_intent"),
			Reason: "ambiguous_candidates",
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
	switch candidate.MatchKind {
	case MatchExactAlias, MatchExactWorkflowControl, MatchExactCandidate:
		return 100
	case MatchLLM:
		return 80
	case MatchStructuredSlot:
		return 70
	case MatchContainedAlias:
		return 20
	case MatchLegacyRule:
		return 10
	}
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

func ambiguousOperationCandidates(candidates []OperationCandidate) bool {
	topRank := -1
	operations := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.Draft.Act == ActUnknown {
			continue
		}
		rank := operationCandidateRank(candidate)
		if rank > topRank {
			topRank = rank
			operations = map[string]struct{}{candidateIdentity(candidate): {}}
			continue
		}
		if rank == topRank {
			operations[candidateIdentity(candidate)] = struct{}{}
		}
	}
	return len(operations) > 1
}

func candidateIdentity(candidate OperationCandidate) string {
	return string(candidate.Draft.Act) + "\x00" + candidate.Draft.Operation
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

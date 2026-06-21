package agent

type CandidateSelectionInput struct {
	Field      string
	Message    string
	TenantID   uint
	Candidates []Candidate
}

type CandidateSelectionResult struct {
	Handled   bool
	OK        bool
	Candidate Candidate
	Reason    string
}

func resolveCandidateSelection(input CandidateSelectionInput) CandidateSelectionResult {
	if ordinal, ok := parseCandidateOrdinal(input.Message); ok {
		if ordinal > len(input.Candidates) {
			return CandidateSelectionResult{Handled: true, Reason: "candidate_ordinal_out_of_range"}
		}
		return validateCandidateTenant(input.Candidates[ordinal-1], input.TenantID)
	}

	for _, variant := range entityNameVariants(input.Message) {
		for _, candidate := range input.Candidates {
			if normalizeEntityName(candidate.Label) == variant {
				return validateCandidateTenant(candidate, input.TenantID)
			}
		}
	}
	return CandidateSelectionResult{Reason: "candidate_not_found"}
}

func validateCandidateTenant(candidate Candidate, tenantID uint) CandidateSelectionResult {
	if tenantID != 0 && candidate.TenantID != tenantID {
		return CandidateSelectionResult{Handled: true, Candidate: candidate, Reason: "candidate_tenant_mismatch"}
	}
	return CandidateSelectionResult{Handled: true, OK: true, Candidate: candidate}
}

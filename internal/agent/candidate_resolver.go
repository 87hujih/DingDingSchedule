package agent

import "strings"

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
	if ordinal, label, ok := parseRenderedCandidateSelection(input.Message); ok {
		if ordinal > len(input.Candidates) {
			return CandidateSelectionResult{Handled: true, Reason: "candidate_ordinal_out_of_range"}
		}
		candidate := input.Candidates[ordinal-1]
		if label != "" {
			labelMatches := false
			for _, variant := range entityNameVariants(label) {
				if normalizeEntityName(candidate.Label) == variant {
					labelMatches = true
					break
				}
			}
			if !labelMatches {
				return CandidateSelectionResult{
					Handled:   true,
					Candidate: candidate,
					Reason:    "candidate_ordinal_label_mismatch",
				}
			}
		}
		return validateCandidateTenant(candidate, input.TenantID)
	}

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

func parseRenderedCandidateSelection(message string) (int, string, bool) {
	message = strings.TrimSpace(message)
	for index, separator := range message {
		switch separator {
		case '.', '、', ':', '：', ')':
			ordinal, ok := parseCandidateOrdinal(strings.TrimSpace(message[:index]))
			if !ok {
				return 0, "", false
			}
			return ordinal, strings.TrimSpace(message[index+len(string(separator)):]), true
		}
	}
	return 0, "", false
}

func validateCandidateTenant(candidate Candidate, tenantID uint) CandidateSelectionResult {
	if tenantID != 0 && candidate.TenantID != tenantID {
		return CandidateSelectionResult{Handled: true, Candidate: candidate, Reason: "candidate_tenant_mismatch"}
	}
	return CandidateSelectionResult{Handled: true, OK: true, Candidate: candidate}
}

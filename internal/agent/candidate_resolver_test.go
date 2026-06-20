package agent

import "testing"

func TestResolveCandidateSelectionMatchesOrdinalAndLabel(t *testing.T) {
	t.Parallel()

	candidates := []Candidate{
		{ID: "101", Label: "家族7期", Value: int64(101), TenantID: 42},
		{ID: "125", Label: "乐知全栈一期", Value: int64(125), TenantID: 42},
	}

	ordinal := resolveCandidateSelection(CandidateSelectionInput{
		Field:      "dept_ids",
		Message:    "第 2 个",
		TenantID:   42,
		Candidates: candidates,
	})
	if !ordinal.Handled || !ordinal.OK || ordinal.Candidate.ID != "125" {
		t.Fatalf("ordinal selection = %+v, want candidate 125", ordinal)
	}

	label := resolveCandidateSelection(CandidateSelectionInput{
		Field:      "dept_ids",
		Message:    "家族七期",
		TenantID:   42,
		Candidates: candidates,
	})
	if !label.Handled || !label.OK || label.Candidate.ID != "101" {
		t.Fatalf("label selection = %+v, want candidate 101", label)
	}
}

func TestResolveCandidateSelectionRejectsCrossTenantCandidate(t *testing.T) {
	t.Parallel()

	result := resolveCandidateSelection(CandidateSelectionInput{
		Field:    "dept_ids",
		Message:  "第一个",
		TenantID: 42,
		Candidates: []Candidate{
			{ID: "201", Label: "其他租户部门", Value: int64(201), TenantID: 99},
		},
	})
	if !result.Handled {
		t.Fatalf("Handled = false, want ordinal input handled")
	}
	if result.OK {
		t.Fatalf("OK = true, want cross-tenant candidate rejected: %+v", result)
	}
}

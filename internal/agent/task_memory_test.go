package agent

import "testing"

func TestTaskMemoryPreservesRetryStateAndCandidateCache(t *testing.T) {
	t.Parallel()

	original := &TaskInstance{
		ID:            "task-1",
		Type:          "subscribe_attendance_push",
		Status:        "waiting_slots",
		LastErrorCode: "department_name_not_found",
		CandidateCache: map[string]any{
			"departments": []string{"家族7期", "乐知全栈一期"},
		},
	}

	cloned := cloneTaskInstance(original)
	if cloned == nil {
		t.Fatalf("cloneTaskInstance() = nil, want cloned task")
	}
	if cloned.LastErrorCode != "department_name_not_found" {
		t.Fatalf("LastErrorCode = %q, want department_name_not_found", cloned.LastErrorCode)
	}

	got, ok := cloned.CandidateCache["departments"].([]string)
	if !ok {
		t.Fatalf("CandidateCache[departments] type = %T, want []string", cloned.CandidateCache["departments"])
	}
	if len(got) != 2 || got[0] != "家族7期" || got[1] != "乐知全栈一期" {
		t.Fatalf("CandidateCache[departments] = %v, want [家族7期 乐知全栈一期]", got)
	}

	cloned.CandidateCache["departments"] = []string{"改后的候选"}
	after, ok := original.CandidateCache["departments"].([]string)
	if !ok {
		t.Fatalf("original CandidateCache[departments] type = %T, want []string", original.CandidateCache["departments"])
	}
	if len(after) != 2 || after[0] != "家族7期" || after[1] != "乐知全栈一期" {
		t.Fatalf("original CandidateCache[departments] = %v, want original values preserved", after)
	}
}

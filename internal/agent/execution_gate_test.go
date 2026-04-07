package agent

import "testing"

func TestExecutionGateBlocksManualSignWithoutTrustedUserID(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "manual_sign.create",
	}, trustedEntities{
		Date:    "2026-04-07",
		Section: 2,
	})
	if !blocked {
		t.Fatalf("blocked = false, want true")
	}
}

func TestExecutionGateBuildsAttendanceReadRequestFromTrustedValues(t *testing.T) {
	t.Parallel()

	req, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActReadQuery,
		Operation: "attendance.query_status",
	}, trustedEntities{
		Date:    "2026-04-07",
		Section: 2,
	})
	if blocked {
		t.Fatalf("blocked = true, want false")
	}
	if req.Operation != "attendance.query_status" {
		t.Fatalf("Operation = %q, want attendance.query_status", req.Operation)
	}
	if req.TrustedParams["date"] != "2026-04-07" {
		t.Fatalf("date = %v, want 2026-04-07", req.TrustedParams["date"])
	}
	if req.TrustedParams["section"] != 2 {
		t.Fatalf("section = %v, want 2", req.TrustedParams["section"])
	}
}

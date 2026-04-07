package agent

import (
	"strings"
	"testing"
)

func TestResponseRendererClarifyForUnknownIntent(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind:          ResponseClarify,
		ClarifyReason: "unknown_intent",
	})
	if !strings.Contains(reply, "请再明确") {
		t.Fatalf("reply = %q, want clarify guidance", reply)
	}
}

func TestResponseRendererRefuseDoesNotEchoInternalError(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind:          ResponseRefuse,
		InternalError: "department_name_not_found",
	})
	if strings.Contains(reply, "department_name_not_found") {
		t.Fatalf("reply = %q, should not expose internal error", reply)
	}
	if strings.TrimSpace(reply) == "" {
		t.Fatalf("reply = %q, want non-empty refusal", reply)
	}
}

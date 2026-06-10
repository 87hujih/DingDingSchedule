package agent

import (
	"reflect"
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
	if strings.Contains(reply, "没完全理解") {
		t.Fatalf("reply = %q, should avoid generic legacy wording", reply)
	}
}

func TestRenderProtocolResponseClarifiesMissingSubscriptionScope(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind:          ResponseClarify,
		Operation:     "subscription.start",
		MissingFields: []string{"scope"},
	})
	if !strings.Contains(reply, "请选择订阅范围") ||
		!strings.Contains(reply, "全部人员") ||
		!strings.Contains(reply, "指定部门") {
		t.Fatalf("reply = %q, want subscription scope guidance", reply)
	}
}

func TestRenderProtocolResponseClarifiesMissingAttendanceSection(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind:          ResponseClarify,
		Operation:     "attendance.query_status",
		MissingFields: []string{"section"},
	})
	if !strings.Contains(reply, "请补充要查询第几节") {
		t.Fatalf("reply = %q, want attendance section guidance", reply)
	}
}

func TestRenderProtocolResponseClarifiesAllMissingAttendanceFields(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind:          ResponseClarify,
		Operation:     "attendance.query_status",
		MissingFields: []string{"date", "section"},
	})
	if !strings.Contains(reply, "哪一天") || !strings.Contains(reply, "第几节") {
		t.Fatalf("reply = %q, want date and section guidance", reply)
	}
}

func TestRenderProtocolResponseSelectOptionsListsCandidates(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind: ResponseSelectOptions,
		Options: []ResponseOption{
			{Label: "张三", Value: "7"},
			{Label: "张四", Value: "8"},
		},
	})
	if !strings.Contains(reply, "张三") || !strings.Contains(reply, "张四") {
		t.Fatalf("reply = %q, want candidate names", reply)
	}
}

func TestRenderProtocolResponseNoKnowledgeHitIsTenantScoped(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind:          ResponseAnswer,
		Operation:     "attendance.rule_explain",
		BusinessError: "no_knowledge_hit",
	})
	if !strings.Contains(reply, "当前租户") || !strings.Contains(reply, "规则说明") {
		t.Fatalf("reply = %q, want tenant-scoped no knowledge answer", reply)
	}
}

func TestResponseRendererRefuseDoesNotEchoInternalError(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind:          ResponseRefuse,
		BusinessError: `{"error_code":"department_name_not_found"}`,
	})
	if strings.Contains(reply, "department_name_not_found") {
		t.Fatalf("reply = %q, should not expose internal error", reply)
	}
	if strings.Contains(reply, "error_code") || strings.Contains(reply, "{") {
		t.Fatalf("reply = %q, should not expose JSON or error_code", reply)
	}
	if strings.TrimSpace(reply) == "" {
		t.Fatalf("reply = %q, want non-empty refusal", reply)
	}
}

func TestResponseRendererSanitizesAllFreeTextChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model ResponseModel
	}{
		{
			name:  "message",
			model: ResponseModel{Kind: ResponseAnswer, Message: `{"error_code":"tool_failed"}`},
		},
		{
			name:  "answer",
			model: ResponseModel{Kind: ResponseAnswer, Answer: `{"error_code":"tool_failed"}`},
		},
		{
			name:  "result text",
			model: ResponseModel{Kind: ResponseResult, ResultText: `{"error_code":"tool_failed"}`},
		},
		{
			name:  "refusal reason",
			model: ResponseModel{Kind: ResponseRefuse, RefusalReason: `{"error_code":"tool_failed"}`},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reply := renderProtocolResponse(tt.model)
			if strings.Contains(reply, "error_code") || strings.Contains(reply, "{") || strings.Contains(reply, "}") {
				t.Fatalf("reply = %q, should sanitize raw internal text", reply)
			}
		})
	}
}

func TestResponseModelHasNoConfirmOrInternalErrorFields(t *testing.T) {
	t.Parallel()

	modelType := reflect.TypeOf(ResponseModel{})
	if _, ok := modelType.FieldByName("ConfirmText"); ok {
		t.Fatalf("ResponseModel should not expose ConfirmText")
	}
	if _, ok := modelType.FieldByName("InternalError"); ok {
		t.Fatalf("ResponseModel should not expose InternalError")
	}
}

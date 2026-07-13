package agent

import (
	"reflect"
	"strings"
	"testing"

	"schedule_server/internal/agent/tools"
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

func TestRenderProtocolResponseClarifiesMissingSubscriptionDepartments(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind:          ResponseClarify,
		Operation:     "subscription.start",
		MissingFields: []string{"dept_names"},
	})
	if !strings.Contains(reply, "部门选项") || !strings.Contains(reply, "准确部门名") {
		t.Fatalf("reply = %q, want department selection guidance", reply)
	}
	if strings.Contains(reply, "选择订阅范围") {
		t.Fatalf("reply = %q, should not ask for subscription scope", reply)
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
	if !strings.Contains(reply, "1. 张三") || !strings.Contains(reply, "2. 张四") {
		t.Fatalf("reply = %q, want stable candidate numbering", reply)
	}
}

func TestRenderProtocolResponseResultUsesStructuredAttendancePayload(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind: ResponseResult,
		Payload: AttendanceStatusPayload{Result: &tools.AttendanceResult{
			Date:         "2026-06-06",
			Section:      2,
			ShouldAttend: 3,
			OnTimeCount:  2,
			LateCount:    1,
		}},
	})
	if !strings.Contains(reply, "2026-06-06第2节考勤状态") {
		t.Fatalf("reply = %q, want attendance payload rendered by renderer", reply)
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

func TestRenderProtocolResponseConfirmUsesPlainText(t *testing.T) {
	t.Parallel()

	reply := renderProtocolResponse(ResponseModel{
		Kind:    ResponseConfirm,
		Message: "请确认是否执行该写操作。",
	})
	if !strings.Contains(reply, "请确认") {
		t.Fatalf("reply = %q, want confirmation prompt", reply)
	}
	if strings.Contains(reply, "**") || strings.Contains(reply, "|") {
		t.Fatalf("reply = %q, should stay plain text", reply)
	}
}

func TestResponseModelHasNoInternalErrorField(t *testing.T) {
	t.Parallel()

	modelType := reflect.TypeOf(ResponseModel{})
	if _, ok := modelType.FieldByName("InternalError"); ok {
		t.Fatalf("ResponseModel should not expose InternalError")
	}
}

func TestRenderSubscriptionWriteEffectsGolden(t *testing.T) {
	tests := []struct {
		code   string
		status WriteStatus
		want   string
	}{
		{"subscription_started", WriteStatusCreated, "已为此群开启考勤推送。"},
		{"subscription_started", WriteStatusUpdated, "已更新此群考勤推送范围。"},
		{"subscription_started", WriteStatusNoOp, "此群考勤推送未发生变化。"},
		{"subscription_cancelled", WriteStatusCancelled, "已取消此群的考勤自动推送。"},
		{"subscription_cancelled", WriteStatusNoOp, "当前群还没有开启考勤自动推送，无需取消。"},
	}
	for _, tt := range tests {
		if got := renderProtocolResponse(ResponseModel{Kind: ResponseResult, Payload: OperationStatusPayload{Code: tt.code, Status: tt.status}}); got != tt.want {
			t.Errorf("%s/%s = %q, want %q", tt.code, tt.status, got, tt.want)
		}
	}
}

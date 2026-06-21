package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPolicyGateRejectsExecutionForCapabilityQuestion(t *testing.T) {
	t.Parallel()

	result := validateProtocol(ProtocolDraft{
		Act:       ActCapabilityQuestion,
		Domain:    DomainManualSign,
		Operation: "manual_sign.describe_capability",
	}, nil)
	if result.AllowExecution {
		t.Fatalf("AllowExecution = true, want false")
	}
	if result.ValidationCode != "capability_non_executable" {
		t.Fatalf("ValidationCode = %q, want capability_non_executable", result.ValidationCode)
	}
}

func TestPolicyGateWorkflowContinueUsesCatalogBindings(t *testing.T) {
	t.Parallel()

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	sourcePath := filepath.Join(filepath.Dir(testFile), "policy_gate.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", sourcePath, err)
	}
	for _, token := range []string{`"subscription.start"`, `"subscription.list_departments"`} {
		if strings.Contains(string(source), token) {
			t.Fatalf("policy_gate.go still hard-codes %s; workflow continue policy must use OperationCatalog bindings", token)
		}
	}
}

func TestPolicyGateBlocksUnknownIntent(t *testing.T) {
	t.Parallel()

	decision := validateProtocol(IntentDraft{Act: ActUnknown}, nil)
	if decision.AllowExecution {
		t.Fatalf("unknown intent must not execute: %+v", decision)
	}
	if decision.ValidationCode != "unknown_intent" {
		t.Fatalf("ValidationCode = %q, want unknown_intent", decision.ValidationCode)
	}
	if decision.ResponseKind != ResponseClarify {
		t.Fatalf("ResponseKind = %q, want %q", decision.ResponseKind, ResponseClarify)
	}
}

func TestCatalogValidatorRejectsOperationOutsideCatalog(t *testing.T) {
	t.Parallel()

	result := newCatalogValidator().Validate(IntentDraft{
		Act:        ActWriteRequest,
		Domain:     DomainManualSign,
		Operation:  "manual_sign.create",
		Confidence: 0.96,
	}, nil)

	if result.AllowExecution {
		t.Fatalf("AllowExecution = true, want false")
	}
	if result.ValidationCode != "operation_not_allowed" {
		t.Fatalf("ValidationCode = %q, want operation_not_allowed", result.ValidationCode)
	}
	if result.ResponseKind != ResponseRefuse {
		t.Fatalf("ResponseKind = %q, want %q", result.ResponseKind, ResponseRefuse)
	}
}

func TestPrePolicyGateEnforcesConversationScopeBeforeResolution(t *testing.T) {
	t.Parallel()

	result := newPrePolicyGate().Validate(PrePolicyGateInput{
		Draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.start",
			Confidence: 0.96,
		},
		ConversationType: "1",
		UserRole:         1,
		HasUserContext:   true,
	})

	if result.AllowExecution {
		t.Fatalf("AllowExecution = true, want false")
	}
	if result.ValidationCode != "conversation_scope_denied" {
		t.Fatalf("ValidationCode = %q, want conversation_scope_denied", result.ValidationCode)
	}
	if result.ResponseKind != ResponseRefuse {
		t.Fatalf("ResponseKind = %q, want %q", result.ResponseKind, ResponseRefuse)
	}
}

func TestPrePolicyGateEnforcesMinRoleBeforeResolution(t *testing.T) {
	t.Parallel()

	result := newPrePolicyGate().Validate(PrePolicyGateInput{
		Draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.cancel",
			Confidence: 0.96,
		},
		ConversationType: "2",
		UserRole:         0,
		HasUserContext:   true,
	})

	if result.AllowExecution {
		t.Fatalf("AllowExecution = true, want false")
	}
	if result.ValidationCode != "role_denied" {
		t.Fatalf("ValidationCode = %q, want role_denied", result.ValidationCode)
	}
	if result.ResponseKind != ResponseRefuse {
		t.Fatalf("ResponseKind = %q, want %q", result.ResponseKind, ResponseRefuse)
	}
}

func TestPrePolicyGateAllowsGroupAdminSubscriptionWrite(t *testing.T) {
	t.Parallel()

	result := newPrePolicyGate().Validate(PrePolicyGateInput{
		Draft: ProtocolDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.cancel",
			Confidence: 0.96,
		},
		ConversationType: "2",
		UserRole:         1,
		HasUserContext:   true,
	})

	if !result.AllowExecution {
		t.Fatalf("AllowExecution = false, want true: %+v", result)
	}
	if result.ValidationCode != "allowed_write_request" {
		t.Fatalf("ValidationCode = %q, want allowed_write_request", result.ValidationCode)
	}
}

func TestCatalogValidatorUsesManifestRendererKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		draft    IntentDraft
		workflow *protocolWorkflowContext
		wantKind ResponseKind
	}{
		{
			name: "read result",
			draft: IntentDraft{
				Act:       ActReadQuery,
				Domain:    DomainAttendance,
				Operation: "attendance.query_status",
			},
			wantKind: ResponseResult,
		},
		{
			name: "read select options",
			draft: IntentDraft{
				Act:       ActReadQuery,
				Domain:    DomainSubscription,
				Operation: "subscription.list_departments",
			},
			wantKind: ResponseSelectOptions,
		},
		{
			name: "workflow continue select options",
			draft: IntentDraft{
				Act:       ActWorkflowContinue,
				Domain:    DomainSubscription,
				Operation: "subscription.list_departments",
			},
			workflow: &protocolWorkflowContext{Type: "subscription.start", MissingFields: []string{"scope"}},
			wantKind: ResponseSelectOptions,
		},
		{
			name: "capability answer",
			draft: IntentDraft{
				Act:       ActCapabilityQuestion,
				Domain:    DomainManualSign,
				Operation: "manual_sign.describe_capability",
			},
			wantKind: ResponseAnswer,
		},
		{
			name: "write result",
			draft: IntentDraft{
				Act:        ActWriteRequest,
				Domain:     DomainSubscription,
				Operation:  "subscription.start",
				Confidence: 0.96,
			},
			wantKind: ResponseResult,
		},
	}

	validator := newCatalogValidator()
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := validator.Validate(tt.draft, tt.workflow)
			if !result.AllowExecution && result.ResponseKind != ResponseAnswer {
				t.Fatalf("Validate() = %+v, want allowed execution or answer", result)
			}
			if result.ResponseKind != tt.wantKind {
				t.Fatalf("ResponseKind = %q, want manifest renderer kind %q", result.ResponseKind, tt.wantKind)
			}
		})
	}
}

func TestPolicyGateAllowsReadQueryWithoutWorkflowHijack(t *testing.T) {
	t.Parallel()

	result := validateProtocol(ProtocolDraft{
		Act:       ActReadQuery,
		Domain:    DomainAttendance,
		Operation: "attendance.query_status",
	}, &protocolWorkflowContext{
		Type:          "subscription.start",
		MissingFields: []string{"dept_names"},
	})
	if !result.AllowExecution {
		t.Fatalf("AllowExecution = false, want true")
	}
	if result.ValidationCode != "allowed_read_query" {
		t.Fatalf("ValidationCode = %q, want allowed_read_query", result.ValidationCode)
	}
	if result.UseActiveWorkflow {
		t.Fatalf("UseActiveWorkflow = true, want false")
	}
	if !result.InterruptActiveWorkflow {
		t.Fatalf("InterruptActiveWorkflow = false, want true")
	}
}

func TestPolicyGateRequiresActiveWorkflowForWorkflowContinue(t *testing.T) {
	t.Parallel()

	result := validateProtocol(ProtocolDraft{
		Act:       ActWorkflowContinue,
		Domain:    DomainSubscription,
		Operation: "subscription.list_departments",
	}, nil)
	if result.AllowExecution {
		t.Fatalf("AllowExecution = true, want false")
	}
	if result.ValidationCode != "workflow_missing" {
		t.Fatalf("ValidationCode = %q, want workflow_missing", result.ValidationCode)
	}
}

func TestPolicyGateDoesNotTreatWorkflowContinueAsGenericWrite(t *testing.T) {
	t.Parallel()

	decision := validateProtocol(IntentDraft{
		Act:       ActWorkflowContinue,
		Domain:    DomainManualSign,
		Operation: "manual_sign.create",
	}, &protocolWorkflowContext{Type: "subscription.start", MissingFields: []string{"dept_ids"}})

	if decision.AllowExecution {
		t.Fatalf("cross-workflow continue must not execute: %+v", decision)
	}
	if decision.ValidationCode != "workflow_operation_mismatch" {
		t.Fatalf("ValidationCode = %q, want workflow_operation_mismatch", decision.ValidationCode)
	}
}

func TestPolicyGateAllowsSubscriptionDepartmentListWorkflowContinue(t *testing.T) {
	t.Parallel()

	decision := validateProtocol(IntentDraft{
		Act:       ActWorkflowContinue,
		Domain:    DomainSubscription,
		Operation: "subscription.list_departments",
	}, &protocolWorkflowContext{Type: "subscription.start", MissingFields: []string{"dept_names"}})

	if !decision.AllowExecution {
		t.Fatalf("AllowExecution = false, want true: %+v", decision)
	}
	if !decision.UseActiveWorkflow {
		t.Fatalf("UseActiveWorkflow = false, want true")
	}
	if decision.ValidationCode != "workflow_continue_allowed" {
		t.Fatalf("ValidationCode = %q, want workflow_continue_allowed", decision.ValidationCode)
	}
}

func TestPolicyGateRejectsDepartmentListWorkflowContinueForOtherWorkflowTypes(t *testing.T) {
	t.Parallel()

	decision := validateProtocol(IntentDraft{
		Act:       ActWorkflowContinue,
		Domain:    DomainSubscription,
		Operation: "subscription.list_departments",
	}, &protocolWorkflowContext{Type: "manual_sign.create", MissingFields: []string{"reason"}})

	if decision.AllowExecution {
		t.Fatalf("AllowExecution = true, want false: %+v", decision)
	}
	if decision.ValidationCode != "workflow_operation_mismatch" {
		t.Fatalf("ValidationCode = %q, want workflow_operation_mismatch", decision.ValidationCode)
	}
}

func TestPolicyGateBlocksManualSignCreateWorkflowContinueBecauseCatalogMissing(t *testing.T) {
	t.Parallel()

	decision := validateProtocol(IntentDraft{
		Act:       ActWorkflowContinue,
		Domain:    DomainManualSign,
		Operation: "manual_sign.create",
	}, &protocolWorkflowContext{Type: "manual_sign.create", MissingFields: []string{"user_id"}})

	if decision.AllowExecution {
		t.Fatalf("manual_sign.create workflow continue must not execute: %+v", decision)
	}
	if decision.ValidationCode != "operation_not_allowed" {
		t.Fatalf("ValidationCode = %q, want operation_not_allowed", decision.ValidationCode)
	}
}

func TestPolicyGateAnswerActsOnlyAllowMatchingAnswerOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		draft     IntentDraft
		wantCode  string
		wantKind  ResponseKind
		wantAllow bool
	}{
		{
			name: "help allows system describe capability as answer",
			draft: IntentDraft{
				Act:       ActHelp,
				Domain:    DomainSystem,
				Operation: "system.describe_capability",
			},
			wantCode: "help_non_executable",
			wantKind: ResponseAnswer,
		},
		{
			name: "help rejects non system capability",
			draft: IntentDraft{
				Act:       ActHelp,
				Domain:    DomainAttendance,
				Operation: "attendance.describe_capability",
			},
			wantCode: "act_operation_mismatch",
			wantKind: ResponseRefuse,
		},
		{
			name: "capability question allows describe capability as answer",
			draft: IntentDraft{
				Act:       ActCapabilityQuestion,
				Domain:    DomainManualSign,
				Operation: "manual_sign.describe_capability",
			},
			wantCode: "capability_non_executable",
			wantKind: ResponseAnswer,
		},
		{
			name: "capability question rejects rule operation",
			draft: IntentDraft{
				Act:       ActCapabilityQuestion,
				Domain:    DomainAttendance,
				Operation: "attendance.rule_explain",
			},
			wantCode: "act_operation_mismatch",
			wantKind: ResponseRefuse,
		},
		{
			name: "rule question allows rule explain as answer",
			draft: IntentDraft{
				Act:       ActRuleQuestion,
				Domain:    DomainAttendance,
				Operation: "attendance.rule_explain",
			},
			wantCode: "rule_non_executable",
			wantKind: ResponseAnswer,
		},
		{
			name: "rule question rejects capability operation",
			draft: IntentDraft{
				Act:       ActRuleQuestion,
				Domain:    DomainAttendance,
				Operation: "attendance.describe_capability",
			},
			wantCode: "act_operation_mismatch",
			wantKind: ResponseRefuse,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision := validateProtocol(tt.draft, nil)
			if decision.AllowExecution != tt.wantAllow {
				t.Fatalf("AllowExecution = %v, want %v: %+v", decision.AllowExecution, tt.wantAllow, decision)
			}
			if decision.ValidationCode != tt.wantCode {
				t.Fatalf("ValidationCode = %q, want %q", decision.ValidationCode, tt.wantCode)
			}
			if decision.ResponseKind != tt.wantKind {
				t.Fatalf("ResponseKind = %q, want %q", decision.ResponseKind, tt.wantKind)
			}
		})
	}
}

func TestPolicyGateBlocksReadWriteOperationMismatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		draft    IntentDraft
		wantCode string
	}{
		{
			name: "read query cannot target write operation",
			draft: IntentDraft{
				Act:       ActReadQuery,
				Domain:    DomainSubscription,
				Operation: "subscription.start",
			},
			wantCode: "read_query_cannot_write",
		},
		{
			name: "write request cannot target read operation",
			draft: IntentDraft{
				Act:        ActWriteRequest,
				Domain:     DomainSchedule,
				Operation:  "schedule.query_my_schedule",
				Confidence: 0.91,
			},
			wantCode: "write_request_cannot_read",
		},
		{
			name: "operation domain must match draft domain",
			draft: IntentDraft{
				Act:       ActReadQuery,
				Domain:    DomainManualSign,
				Operation: "attendance.query_status",
			},
			wantCode: "domain_operation_mismatch",
		},
		{
			name: "manual sign create is absent from first-version catalog",
			draft: IntentDraft{
				Act:        ActWriteRequest,
				Domain:     DomainManualSign,
				Operation:  "manual_sign.create",
				Confidence: 0.92,
			},
			wantCode: "operation_not_allowed",
		},
		{
			name: "low confidence write blocks execution",
			draft: IntentDraft{
				Act:        ActWriteRequest,
				Domain:     DomainSubscription,
				Operation:  "subscription.start",
				Confidence: 0.74,
			},
			wantCode: "low_confidence_write",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision := validateProtocol(tt.draft, nil)
			if decision.AllowExecution {
				t.Fatalf("AllowExecution = true, want false: %+v", decision)
			}
			if decision.ValidationCode != tt.wantCode {
				t.Fatalf("ValidationCode = %q, want %q", decision.ValidationCode, tt.wantCode)
			}
		})
	}
}

func TestPolicyGateBlocksZeroConfidenceWriteRequest(t *testing.T) {
	t.Parallel()

	decision := validateProtocol(IntentDraft{
		Act:       ActWriteRequest,
		Domain:    DomainSubscription,
		Operation: "subscription.start",
	}, nil)

	if decision.AllowExecution {
		t.Fatalf("AllowExecution = true, want false: %+v", decision)
	}
	if decision.ValidationCode != "low_confidence_write" {
		t.Fatalf("ValidationCode = %q, want low_confidence_write", decision.ValidationCode)
	}
}

func TestPolicyGateAllowsDeterministicProtocolWriteRequest(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{Message: "开启本群考勤订阅"})
	decision := validateProtocol(draft, nil)

	if !decision.AllowExecution {
		t.Fatalf("AllowExecution = false, want true: %+v", decision)
	}
	if decision.ValidationCode != "allowed_write_request" {
		t.Fatalf("ValidationCode = %q, want allowed_write_request", decision.ValidationCode)
	}
}

func TestPolicyGateExplicitNewRequestActsInterruptActiveWorkflow(t *testing.T) {
	t.Parallel()

	workflow := &protocolWorkflowContext{Type: "subscription.start", MissingFields: []string{"scope"}}
	tests := []struct {
		name  string
		draft IntentDraft
	}{
		{
			name: "help interrupts",
			draft: IntentDraft{
				Act:       ActHelp,
				Domain:    DomainSystem,
				Operation: "system.describe_capability",
			},
		},
		{
			name: "capability question interrupts",
			draft: IntentDraft{
				Act:       ActCapabilityQuestion,
				Domain:    DomainManualSign,
				Operation: "manual_sign.describe_capability",
			},
		},
		{
			name: "rule question interrupts",
			draft: IntentDraft{
				Act:       ActRuleQuestion,
				Domain:    DomainAttendance,
				Operation: "attendance.rule_explain",
			},
		},
		{
			name: "read query interrupts",
			draft: IntentDraft{
				Act:       ActReadQuery,
				Domain:    DomainAttendance,
				Operation: "attendance.query_status",
			},
		},
		{
			name: "write request interrupts",
			draft: IntentDraft{
				Act:        ActWriteRequest,
				Domain:     DomainSubscription,
				Operation:  "subscription.cancel",
				Confidence: 0.9,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision := validateProtocol(tt.draft, workflow)
			if !decision.InterruptActiveWorkflow {
				t.Fatalf("InterruptActiveWorkflow = false, want true: %+v", decision)
			}
			if decision.UseActiveWorkflow {
				t.Fatalf("UseActiveWorkflow = true, want false: %+v", decision)
			}
		})
	}
}

func TestPolicyGateDoesNotInterruptWorkflowForRejectedNewRequest(t *testing.T) {
	t.Parallel()

	decision := validateProtocol(IntentDraft{
		Act:        ActWriteRequest,
		Domain:     DomainSubscription,
		Operation:  "subscription.cancel",
		Confidence: 0.2,
	}, &protocolWorkflowContext{Type: "subscription.start", MissingFields: []string{"scope"}})

	if decision.AllowExecution {
		t.Fatalf("AllowExecution = true, want false: %+v", decision)
	}
	if decision.ValidationCode != "low_confidence_write" {
		t.Fatalf("ValidationCode = %q, want low_confidence_write", decision.ValidationCode)
	}
	if decision.InterruptActiveWorkflow {
		t.Fatalf("InterruptActiveWorkflow = true, want false for rejected new request")
	}
}

func TestPolicyGateUnsupportedExplicitWriteInterruptsActiveWorkflow(t *testing.T) {
	t.Parallel()

	decision := validateProtocol(IntentDraft{
		Act:        ActWriteRequest,
		Domain:     DomainManualSign,
		Operation:  "manual_sign.create",
		Confidence: 0.96,
	}, &protocolWorkflowContext{Type: "subscription.start", MissingFields: []string{"scope"}})

	if decision.AllowExecution {
		t.Fatalf("AllowExecution = true, want false: %+v", decision)
	}
	if decision.ValidationCode != "operation_not_allowed" {
		t.Fatalf("ValidationCode = %q, want operation_not_allowed", decision.ValidationCode)
	}
	if !decision.InterruptActiveWorkflow {
		t.Fatalf("InterruptActiveWorkflow = false, want true for clear unsupported write")
	}
}

func TestPolicyGateAllowsDepartmentListMetaInAnySubscriptionWorkflowState(t *testing.T) {
	t.Parallel()

	decision := validateProtocol(IntentDraft{
		Act:        ActWorkflowContinue,
		Domain:     DomainSubscription,
		Operation:  "subscription.list_departments",
		Confidence: 0.9,
	}, &protocolWorkflowContext{Type: "subscription.start", MissingFields: []string{"scope"}})

	if !decision.AllowExecution {
		t.Fatalf("AllowExecution = false, want true: %+v", decision)
	}
	if decision.ValidationCode != "workflow_continue_allowed" {
		t.Fatalf("ValidationCode = %q, want workflow_continue_allowed", decision.ValidationCode)
	}
	if !decision.UseActiveWorkflow {
		t.Fatalf("UseActiveWorkflow = false, want true")
	}
}

func TestPolicyGateAllowsCompiledWorkflowCancelWithActiveWorkflow(t *testing.T) {
	t.Parallel()

	workflow := &protocolWorkflowContext{
		Type:          "subscription.start",
		MissingFields: []string{"scope"},
	}
	draft, err := compileProtocolWithCompiler(context.Background(), protocolInput{
		Message:        "取消",
		ActiveWorkflow: workflow,
	}, &fakeProtocolIntentCompiler{})
	if err != nil {
		t.Fatalf("compileProtocolWithCompiler() error = %v", err)
	}
	if draft.Act != ActWorkflowCancel {
		t.Fatalf("draft = %+v, want workflow_cancel", draft)
	}

	result := validateProtocol(draft, workflow)
	if result.ValidationCode != "workflow_cancel_non_executable" {
		t.Fatalf("ValidationCode = %q, want workflow_cancel_non_executable", result.ValidationCode)
	}
	if !result.UseActiveWorkflow {
		t.Fatalf("UseActiveWorkflow = false, want true")
	}
	if result.ResponseKind != ResponseResult {
		t.Fatalf("ResponseKind = %q, want %q", result.ResponseKind, ResponseResult)
	}
}

func TestPolicyGateRequiresActiveWorkflowForWorkflowCancel(t *testing.T) {
	t.Parallel()

	result := validateProtocol(IntentDraft{
		Act:       ActWorkflowCancel,
		Domain:    DomainSubscription,
		Operation: "subscription.start",
	}, nil)

	if result.AllowExecution {
		t.Fatalf("AllowExecution = true, want false")
	}
	if result.UseActiveWorkflow {
		t.Fatalf("UseActiveWorkflow = true, want false")
	}
	if result.ValidationCode != "workflow_missing" {
		t.Fatalf("ValidationCode = %q, want workflow_missing", result.ValidationCode)
	}
}

func TestProtocolPrimaryDispatchHonorsPolicyGateDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		draft      ProtocolDraft
		validation ProtocolValidationResult
		want       bool
	}{
		{
			name: "blocks rejected workflow continue before handler dispatch",
			draft: ProtocolDraft{
				Act:       ActWorkflowContinue,
				Domain:    DomainSubscription,
				Operation: "subscription.list_departments",
			},
			validation: ProtocolValidationResult{
				AllowExecution: false,
				ValidationCode: "workflow_operation_mismatch",
				ResponseKind:   ResponseRefuse,
			},
			want: false,
		},
		{
			name: "allows executable write request",
			draft: ProtocolDraft{
				Act:       ActWriteRequest,
				Domain:    DomainSubscription,
				Operation: "subscription.start",
			},
			validation: ProtocolValidationResult{
				AllowExecution: true,
				ValidationCode: "allowed_write_request",
				ResponseKind:   ResponseResult,
			},
			want: true,
		},
		{
			name: "allows non executable answer rendering",
			draft: ProtocolDraft{
				Act:       ActCapabilityQuestion,
				Domain:    DomainManualSign,
				Operation: "manual_sign.describe_capability",
			},
			validation: ProtocolValidationResult{
				AllowExecution: false,
				ValidationCode: "capability_non_executable",
				ResponseKind:   ResponseAnswer,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := protocolPrimaryDispatchAllowed(tt.draft, tt.validation)
			if got != tt.want {
				t.Fatalf("protocolPrimaryDispatchAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

package agent

import (
	"context"
	"testing"
)

func TestProtocolCompilerUsesInjectedIntentCompilerDraftContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		message    string
		wantAct    UserAct
		wantDomain BusinessDomain
		wantOp     string
		wantShape  string
	}{
		{
			name:       "system help",
			message:    "你有什么功能",
			wantAct:    ActHelp,
			wantDomain: DomainSystem,
			wantOp:     "system.describe_capability",
		},
		{
			name:       "manual sign capability",
			message:    "可以补签吗",
			wantAct:    ActCapabilityQuestion,
			wantDomain: DomainManualSign,
			wantOp:     "manual_sign.describe_capability",
		},
		{
			name:       "absence rule question",
			message:    "为什么判我缺勤",
			wantAct:    ActRuleQuestion,
			wantDomain: DomainAttendance,
			wantOp:     "attendance.rule_explain",
		},
		{
			name:       "check in mechanism rule question",
			message:    "打卡机制是什么",
			wantAct:    ActRuleQuestion,
			wantDomain: DomainAttendance,
			wantOp:     "attendance.rule_explain",
		},
		{
			name:       "slot attendance status",
			message:    "查询今天第二节考勤状态",
			wantAct:    ActReadQuery,
			wantDomain: DomainAttendance,
			wantOp:     "attendance.query_status",
			wantShape:  "slot_status",
		},
		{
			name:       "user day attendance status",
			message:    "王志伟今天迟到没有",
			wantAct:    ActReadQuery,
			wantDomain: DomainAttendance,
			wantOp:     "attendance.query_status",
			wantShape:  "user_day_status",
		},
		{
			name:       "start group subscription",
			message:    "开启本群考勤订阅",
			wantAct:    ActWriteRequest,
			wantDomain: DomainSubscription,
			wantOp:     "subscription.start",
		},
		{
			name:       "cancel group subscription",
			message:    "取消本群考勤推送",
			wantAct:    ActWriteRequest,
			wantDomain: DomainSubscription,
			wantOp:     "subscription.cancel",
		},
		{
			name:       "query subscription status",
			message:    "查本群订阅状态",
			wantAct:    ActReadQuery,
			wantDomain: DomainSubscription,
			wantOp:     "subscription.query_status",
		},
		{
			name:       "query my schedule",
			message:    "查我的课表",
			wantAct:    ActReadQuery,
			wantDomain: DomainSchedule,
			wantOp:     "schedule.query_my_schedule",
		},
		{
			name:       "query user schedule",
			message:    "查张三第10周课表",
			wantAct:    ActReadQuery,
			wantDomain: DomainSchedule,
			wantOp:     "schedule.query_user_schedule",
		},
	}

	fake := &fakeProtocolIntentCompiler{
		draftsByMessage: make(map[string]IntentDraft, len(tests)),
	}
	for _, tt := range tests {
		slots := map[string]SlotDraft{}
		if tt.wantShape != "" {
			slots["query_shape"] = SlotDraft{Field: "query_shape", Raw: tt.wantShape}
		}
		fake.draftsByMessage[tt.message] = IntentDraft{
			Act:        tt.wantAct,
			Domain:     tt.wantDomain,
			Operation:  tt.wantOp,
			Confidence: 0.9,
			Slots:      slots,
			Reason:     "golden case",
		}
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			draft, err := compileProtocolWithCompiler(context.Background(), protocolInput{
				Message: tt.message,
			}, fake)
			if err != nil {
				t.Fatalf("compileProtocolWithCompiler() error = %v", err)
			}
			if draft.Act != tt.wantAct || draft.Domain != tt.wantDomain || draft.Operation != tt.wantOp {
				t.Fatalf("draft = %+v", draft)
			}
			if tt.wantShape != "" && draft.Slots["query_shape"].Raw != tt.wantShape {
				t.Fatalf("query_shape = %q, want %q", draft.Slots["query_shape"].Raw, tt.wantShape)
			}
		})
	}

	if len(fake.requests) != len(tests) {
		t.Fatalf("compiler requests = %d, want %d", len(fake.requests), len(tests))
	}
	for i, req := range fake.requests {
		if req.Message != tests[i].message {
			t.Fatalf("request %d Message = %q, want %q", i, req.Message, tests[i].message)
		}
	}
}

func TestProtocolCompilerPassesWorkflowContextToInjectedCompiler(t *testing.T) {
	t.Parallel()

	fake := &fakeProtocolIntentCompiler{
		draftsByMessage: map[string]IntentDraft{
			"全部人员": {
				Act:       ActWorkflowContinue,
				Domain:    DomainSubscription,
				Operation: "subscription.start",
			},
		},
	}

	_, err := compileProtocolWithCompiler(context.Background(), protocolInput{
		Message: "全部人员",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"scope"},
		},
	}, fake)
	if err != nil {
		t.Fatalf("compileProtocolWithCompiler() error = %v", err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("compiler requests = %d, want 1", len(fake.requests))
	}
	req := fake.requests[0]
	if req.ActiveWorkflow == nil {
		t.Fatalf("ActiveWorkflow = nil, want subscription workflow context")
	}
	if req.ActiveWorkflow.Type != "subscription.start" {
		t.Fatalf("ActiveWorkflow.Type = %q, want subscription.start", req.ActiveWorkflow.Type)
	}
	if len(req.ActiveWorkflow.MissingFields) != 1 || req.ActiveWorkflow.MissingFields[0] != "scope" {
		t.Fatalf("ActiveWorkflow.MissingFields = %v, want [scope]", req.ActiveWorkflow.MissingFields)
	}
}

func TestProtocolCompilerFallsBackToDeterministicWorkflowContinueWhenInjectedCompilerReturnsUnknown(t *testing.T) {
	t.Parallel()

	fake := &fakeProtocolIntentCompiler{
		draftsByMessage: map[string]IntentDraft{
			"1": {
				Act:           ActUnknown,
				Domain:        DomainUnknown,
				ClarifyReason: "intent_parse_failed",
			},
		},
	}

	draft, err := compileProtocolWithCompiler(context.Background(), protocolInput{
		Message: "1",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"dept_names"},
		},
	}, fake)
	if err != nil {
		t.Fatalf("compileProtocolWithCompiler() error = %v", err)
	}
	if draft.Act != ActWorkflowContinue {
		t.Fatalf("Act = %q, want %q", draft.Act, ActWorkflowContinue)
	}
	if draft.Domain != DomainSubscription {
		t.Fatalf("Domain = %q, want %q", draft.Domain, DomainSubscription)
	}
	if draft.Operation != "subscription.start" {
		t.Fatalf("Operation = %q, want subscription.start", draft.Operation)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("compiler requests = %d, want 1", len(fake.requests))
	}
}

func TestProtocolCompilerFallsBackToDepartmentWorkflowContinueDuringScopeCollection(t *testing.T) {
	t.Parallel()

	fake := &fakeProtocolIntentCompiler{
		draftsByMessage: map[string]IntentDraft{
			"家族七期": {
				Act:           ActUnknown,
				Domain:        DomainUnknown,
				ClarifyReason: "intent_parse_failed",
			},
		},
	}

	draft, err := compileProtocolWithCompiler(context.Background(), protocolInput{
		Message: "家族七期",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"scope"},
		},
	}, fake)
	if err != nil {
		t.Fatalf("compileProtocolWithCompiler() error = %v", err)
	}
	if draft.Act != ActWorkflowContinue {
		t.Fatalf("Act = %q, want %q", draft.Act, ActWorkflowContinue)
	}
	if draft.Domain != DomainSubscription {
		t.Fatalf("Domain = %q, want %q", draft.Domain, DomainSubscription)
	}
	if draft.Operation != "subscription.start" {
		t.Fatalf("Operation = %q, want subscription.start", draft.Operation)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("compiler requests = %d, want 1", len(fake.requests))
	}
}

func TestProtocolCompilerClassifiesHelpMeCancelSubscriptionAsCancel(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{Message: "帮我取消本群考勤订阅"})
	if draft.Act != ActWriteRequest {
		t.Fatalf("Act = %q, want %q", draft.Act, ActWriteRequest)
	}
	if draft.Domain != DomainSubscription {
		t.Fatalf("Domain = %q, want %q", draft.Domain, DomainSubscription)
	}
	if draft.Operation != "subscription.cancel" {
		t.Fatalf("Operation = %q, want subscription.cancel", draft.Operation)
	}
	if draft.Confidence < lowConfidenceWriteThreshold {
		t.Fatalf("Confidence = %v, want high-confidence deterministic cancel", draft.Confidence)
	}
}

func TestProtocolCompilerClassifiesSubscriptionStatusQuery(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{Message: "查本群订阅状态"})
	if draft.Act != ActReadQuery {
		t.Fatalf("Act = %q, want %q", draft.Act, ActReadQuery)
	}
	if draft.Domain != DomainSubscription {
		t.Fatalf("Domain = %q, want %q", draft.Domain, DomainSubscription)
	}
	if draft.Operation != "subscription.query_status" {
		t.Fatalf("Operation = %q, want subscription.query_status", draft.Operation)
	}
}

func TestProtocolCompilerClassifiesDeterministicCatalogIntents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		message    string
		workflow   *protocolWorkflowContext
		wantAct    UserAct
		wantDomain BusinessDomain
		wantOp     string
	}{
		{
			name:       "manual sign capability with buqian wording",
			message:    "可以补签吗",
			wantAct:    ActCapabilityQuestion,
			wantDomain: DomainManualSign,
			wantOp:     "manual_sign.describe_capability",
		},
		{
			name:       "attendance rule question",
			message:    "迟到规则是什么",
			wantAct:    ActRuleQuestion,
			wantDomain: DomainAttendance,
			wantOp:     "attendance.rule_explain",
		},
		{
			name:       "my schedule query",
			message:    "查我的本周课表",
			wantAct:    ActReadQuery,
			wantDomain: DomainSchedule,
			wantOp:     "schedule.query_my_schedule",
		},
		{
			name:    "department list meta question while scope missing",
			message: "有哪些部门",
			workflow: &protocolWorkflowContext{
				Type:          "subscription.start",
				MissingFields: []string{"scope"},
			},
			wantAct:    ActWorkflowContinue,
			wantDomain: DomainSubscription,
			wantOp:     "subscription.list_departments",
		},
		{
			name:    "manual sign capability interrupts subscription department collection",
			message: "补签规则是什么",
			workflow: &protocolWorkflowContext{
				Type:          "subscription.start",
				MissingFields: []string{"dept_names"},
			},
			wantAct:    ActCapabilityQuestion,
			wantDomain: DomainManualSign,
			wantOp:     "manual_sign.describe_capability",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			draft := compileProtocol(protocolInput{
				Message:        tt.message,
				ActiveWorkflow: tt.workflow,
			})
			if draft.Act != tt.wantAct || draft.Domain != tt.wantDomain || draft.Operation != tt.wantOp {
				t.Fatalf("draft = %+v, want act=%q domain=%q op=%q", draft, tt.wantAct, tt.wantDomain, tt.wantOp)
			}
		})
	}
}

func TestProtocolCompilerTreatsDepartmentHelpAsWorkflowMetaOnlyDuringSubscriptionWorkflow(t *testing.T) {
	t.Parallel()

	message := "现在都有哪些部门"
	withWorkflow := compileProtocol(protocolInput{
		Message: message,
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"dept_names"},
		},
	})
	if withWorkflow.Act != ActWorkflowContinue {
		t.Fatalf("Act = %q, want %q when dept_names missing", withWorkflow.Act, ActWorkflowContinue)
	}
	if withWorkflow.Operation != "subscription.list_departments" {
		t.Fatalf("Operation = %q, want subscription.list_departments", withWorkflow.Operation)
	}

	withScopeMissing := compileProtocol(protocolInput{
		Message: message,
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"scope"},
		},
	})
	if withScopeMissing.Act != ActWorkflowContinue {
		t.Fatalf("Act with scope missing = %q, want %q", withScopeMissing.Act, ActWorkflowContinue)
	}
	if withScopeMissing.Operation != "subscription.list_departments" {
		t.Fatalf("Operation with scope missing = %q, want subscription.list_departments", withScopeMissing.Operation)
	}

	withoutWorkflow := compileProtocol(protocolInput{Message: message})
	if withoutWorkflow.Act != ActUnknown {
		t.Fatalf("Act without workflow = %q, want %q", withoutWorkflow.Act, ActUnknown)
	}
}

func TestProtocolCompilerTreatsManualSignNameReplyAsWorkflowContinue(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{
		Message: "张三",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "manual_sign.create",
			MissingFields: []string{"user_id"},
		},
	})
	if draft.Act != ActWorkflowContinue {
		t.Fatalf("Act = %q, want %q", draft.Act, ActWorkflowContinue)
	}
	if draft.Domain != DomainManualSign {
		t.Fatalf("Domain = %q, want %q", draft.Domain, DomainManualSign)
	}
	if draft.Operation != "manual_sign.create" {
		t.Fatalf("Operation = %q, want manual_sign.create", draft.Operation)
	}
}

func TestProtocolCompilerReturnsUnknownForAmbiguousSmallTalk(t *testing.T) {
	t.Parallel()

	draft := compileProtocol(protocolInput{
		Message: "最近怎么样",
	})
	if draft.Act != ActUnknown {
		t.Fatalf("Act = %q, want %q", draft.Act, ActUnknown)
	}
}

type fakeProtocolIntentCompiler struct {
	draftsByMessage map[string]IntentDraft
	requests        []IntentCompileRequest
}

func (c *fakeProtocolIntentCompiler) Compile(ctx context.Context, req IntentCompileRequest) (IntentDraft, error) {
	c.requests = append(c.requests, req)
	if draft, ok := c.draftsByMessage[req.Message]; ok {
		return draft, nil
	}
	return IntentDraft{Act: ActUnknown, Domain: DomainUnknown, Reason: "missing_fake_draft"}, nil
}

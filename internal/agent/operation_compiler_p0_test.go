package agent

import (
	"context"
	"errors"
	"testing"
)

func TestOperationCompilerUsesSemanticCompilerForExactBusinessPhrase(t *testing.T) {
	t.Parallel()

	intent := &countingIntentCompiler{result: IntentCompileResult{
		Draft:  IntentDraft{Act: ActReadQuery, Domain: DomainSubscription, Operation: "subscription.query_status", Confidence: 0.95},
		Status: IntentCompileOK,
	}}
	result, err := newOperationCompiler(intent).Compile(context.Background(), protocolInput{Message: "订阅状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if intent.calls != 1 {
		t.Fatalf("LLM calls = %d, want 1", intent.calls)
	}
	if result.Source != CompilerSourceLLM || result.LLMStatus != IntentCompileOK {
		t.Fatalf("result = %+v, want semantic compiler result", result)
	}
	if result.Draft.Operation != "subscription.query_status" {
		t.Fatalf("operation = %q, want subscription.query_status", result.Draft.Operation)
	}
}

func TestOperationCompilerUsesSemanticCompilerForNaturalUserScheduleQuery(t *testing.T) {
	t.Parallel()

	intent := &countingIntentCompiler{result: IntentCompileResult{
		Draft: IntentDraft{
			Act:        ActReadQuery,
			Domain:     DomainSchedule,
			Operation:  "schedule.query_user_schedule",
			Confidence: 0.97,
			Slots: map[string]SlotDraft{
				"user_name": {Field: "user_name", Raw: "杨思见"},
			},
		},
		Status: IntentCompileOK,
	}}
	result, err := newOperationCompiler(intent).Compile(context.Background(), protocolInput{
		Message: "查询一下杨思见的课程信息",
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if intent.calls != 1 {
		t.Fatalf("LLM calls = %d, want 1", intent.calls)
	}
	if result.Source != CompilerSourceLLM || result.LLMStatus != IntentCompileOK {
		t.Fatalf("result = %+v, want semantic compiler result", result)
	}
	if result.Draft.Operation != "schedule.query_user_schedule" {
		t.Fatalf("operation = %q, want schedule.query_user_schedule", result.Draft.Operation)
	}
	if got := draftSlotRaw(result.Draft, "user_name"); got != "杨思见" {
		t.Fatalf("user_name = %q, want 杨思见", got)
	}
}

func TestOperationCompilerSemanticScheduleClassificationDistinguishesRuleQuestion(t *testing.T) {
	t.Parallel()

	intent := &countingIntentCompiler{result: IntentCompileResult{
		Draft: IntentDraft{
			Act:        ActRuleQuestion,
			Domain:     DomainSchedule,
			Operation:  "schedule.rule_explain",
			Confidence: 0.94,
		},
		Status:   IntentCompileOK,
		Attempts: 1,
	}}
	result, err := newOperationCompiler(intent).Compile(context.Background(), protocolInput{
		Message: "课表规则是什么",
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Draft.Operation != "schedule.rule_explain" {
		t.Fatalf("result = %+v, want schedule.rule_explain", result)
	}
}

func TestOperationCompilerSemanticScheduleParaphraseMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		message  string
		wantOp   string
		wantRaw  string
		wantWeek string
		draft    IntentDraft
	}{
		{
			message: "我接下来几天都要上哪些课",
			wantOp:  "schedule.query_my_schedule",
			draft:   IntentDraft{Act: ActReadQuery, Domain: DomainSchedule, Operation: "schedule.query_my_schedule", Confidence: 0.95},
		},
		{
			message:  "帮我看看杨思见最近的教学安排",
			wantOp:   "schedule.query_user_schedule",
			wantRaw:  "杨思见",
			wantWeek: "这周",
			draft: IntentDraft{Act: ActReadQuery, Domain: DomainSchedule, Operation: "schedule.query_user_schedule", Confidence: 0.96, Slots: map[string]SlotDraft{
				"user_name": {Field: "user_name", Raw: "杨思见"},
				"week":      {Field: "week", Raw: "这周"},
			}},
		},
		{
			message: "先别处理杨思见的教学安排",
			draft:   unknownIntentDraft("unknown_intent"),
		},
		{
			message: "看看他最近都上些什么",
			wantOp:  "schedule.query_user_schedule",
			draft:   IntentDraft{Act: ActReadQuery, Domain: DomainSchedule, Operation: "schedule.query_user_schedule", Confidence: 0.8},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.message, func(t *testing.T) {
			t.Parallel()
			status := IntentCompileOK
			if tt.draft.Act == ActUnknown {
				status = IntentCompileUnknown
			}
			intent := &countingIntentCompiler{result: IntentCompileResult{Draft: tt.draft, Status: status}}
			result, err := newOperationCompiler(intent).Compile(context.Background(), protocolInput{Message: tt.message})
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if intent.calls != 1 {
				t.Fatalf("LLM calls = %d, want 1", intent.calls)
			}
			if result.Draft.Operation != tt.wantOp {
				t.Fatalf("operation = %q, want %q; result=%+v", result.Draft.Operation, tt.wantOp, result)
			}
			if got := draftSlotRaw(result.Draft, "user_name"); got != tt.wantRaw {
				t.Fatalf("user_name = %q, want %q", got, tt.wantRaw)
			}
			if got := draftSlotRaw(result.Draft, "week"); got != tt.wantWeek {
				t.Fatalf("week = %q, want %q", got, tt.wantWeek)
			}
		})
	}
}

func TestContainedAliasDoesNotShortCircuitLLM(t *testing.T) {
	t.Parallel()

	intent := &countingIntentCompiler{result: IntentCompileResult{
		Draft: IntentDraft{
			Act:        ActRuleQuestion,
			Domain:     DomainAttendance,
			Operation:  "attendance.explain_rule",
			Confidence: 0.9,
		},
		Status:   IntentCompileOK,
		Attempts: 1,
	}}
	result, err := newOperationCompiler(intent).Compile(context.Background(), protocolInput{
		Message: "不要查询考勤状态，我想了解规则",
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if intent.calls != 1 || !result.LLMInvoked {
		t.Fatalf("result = %+v calls=%d, want LLM invoked", result, intent.calls)
	}
	if result.Draft.Operation != "attendance.explain_rule" || result.Source != CompilerSourceLLM {
		t.Fatalf("result = %+v, contained alias must not override LLM", result)
	}
}

func TestOperationCompilerDoesNotGuessBusinessIntentOnLLMTimeout(t *testing.T) {
	t.Parallel()

	intent := &countingIntentCompiler{result: IntentCompileResult{
		Draft:  unknownIntentDraft("intent_timeout"),
		Status: IntentCompileTimeout,
	}}
	result, err := newOperationCompiler(intent).Compile(context.Background(), protocolInput{Message: "订阅状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Draft.Act != ActUnknown || result.Draft.ClarifyReason != "intent_timeout" {
		t.Fatalf("result = %+v, want explicit timeout without business fallback", result)
	}
	if !result.LLMInvoked || result.LLMStatus != IntentCompileTimeout {
		t.Fatalf("result = %+v, want semantic compiler timeout", result)
	}
}

func TestOperationCompilerPreservesExactCandidateOnInvalidOutput(t *testing.T) {
	t.Parallel()

	intent := &countingIntentCompiler{result: IntentCompileResult{
		Draft:  unknownIntentDraft("intent_parse_failed"),
		Status: IntentCompileInvalidOutput,
	}}
	result, err := newOperationCompiler(intent).Compile(context.Background(), protocolInput{
		Message: "请查询考勤状态",
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.LLMStatus != IntentCompileInvalidOutput || result.FallbackReason == "" {
		t.Fatalf("result = %+v, want typed invalid-output fallback", result)
	}
	if result.Draft.Act != ActUnknown {
		t.Fatalf("result = %+v, contained alias must fail closed", result)
	}
}

func TestOperationCompilerParentCancellationNeverFallsBack(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	intent := &countingIntentCompiler{err: context.Canceled}

	_, err := newOperationCompiler(intent).Compile(ctx, protocolInput{Message: "请查询考勤状态"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Compile() error = %v, want context.Canceled", err)
	}
}

func TestOperationCompilerObserveAndFallbackPreserveSuccessfulLLMDecision(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"observe", "fallback"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			intent := &countingIntentCompiler{result: IntentCompileResult{
				Draft: IntentDraft{
					Act:        ActRuleQuestion,
					Domain:     DomainAttendance,
					Operation:  "attendance.explain_rule",
					Confidence: 0.9,
				},
				Status: IntentCompileOK,
			}}
			result, err := newOperationCompilerWithMode(intent, mode).Compile(context.Background(), protocolInput{
				Message: "不要订阅状态，我想了解考勤规则",
			})
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if result.Draft.Operation != "attendance.explain_rule" || result.Source != CompilerSourceLLM {
				t.Fatalf("result = %+v, successful LLM decision must remain authoritative in %s mode", result, mode)
			}
		})
	}
}

type countingIntentCompiler struct {
	result IntentCompileResult
	err    error
	calls  int
}

func (c *countingIntentCompiler) Compile(context.Context, IntentCompileRequest) (IntentCompileResult, error) {
	c.calls++
	return c.result, c.err
}

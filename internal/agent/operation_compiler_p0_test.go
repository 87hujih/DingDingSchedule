package agent

import (
	"context"
	"errors"
	"testing"
)

func TestOperationCompilerSkipsLLMForUniqueExactAlias(t *testing.T) {
	t.Parallel()

	intent := &countingIntentCompiler{}
	result, err := newOperationCompiler(intent).Compile(context.Background(), protocolInput{Message: "订阅状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if intent.calls != 0 {
		t.Fatalf("LLM calls = %d, want 0", intent.calls)
	}
	if result.Source != CompilerSourceDeterministic || result.LLMStatus != IntentCompileSkipped {
		t.Fatalf("result = %+v, want deterministic skipped", result)
	}
	if result.Draft.Operation != "subscription.query_status" {
		t.Fatalf("operation = %q, want subscription.query_status", result.Draft.Operation)
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

func TestOperationCompilerFallsBackToExactReadOnLLMTimeout(t *testing.T) {
	t.Parallel()

	intent := &countingIntentCompiler{result: IntentCompileResult{
		Draft:  unknownIntentDraft("intent_timeout"),
		Status: IntentCompileTimeout,
	}}
	result, err := newOperationCompiler(intent).Compile(context.Background(), protocolInput{Message: "订阅状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Draft.Operation != "subscription.query_status" {
		t.Fatalf("result = %+v, want safe deterministic fallback", result)
	}
	if result.Source != CompilerSourceDeterministic || result.LLMStatus != IntentCompileSkipped {
		t.Fatalf("result = %+v, exact alias should skip LLM before timeout", result)
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

package agent

import (
	"context"
	"errors"
	"testing"
)

type recordingIntentCompiler struct {
	draft    IntentDraft
	err      error
	requests []IntentCompileRequest
}

func (c *recordingIntentCompiler) Compile(_ context.Context, req IntentCompileRequest) (IntentDraft, error) {
	c.requests = append(c.requests, req)
	return c.draft, c.err
}

func TestOperationCompilerExactAliasSkipsLLM(t *testing.T) {
	t.Parallel()

	intent := &recordingIntentCompiler{
		draft: IntentDraft{Act: ActUnknown, Domain: DomainUnknown, Reason: "should_not_be_called"},
	}
	compiler := newOperationCompiler(intent)

	result, err := compiler.Compile(context.Background(), protocolInput{Message: "取消考勤推送"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(intent.requests) != 0 {
		t.Fatalf("LLM Compile() calls = %d, want 0", len(intent.requests))
	}
	if result.Draft.Act != ActWriteRequest || result.Draft.Operation != "subscription.cancel" {
		t.Fatalf("Draft = %+v, want subscription.cancel write request", result.Draft)
	}
	if result.Source != CompilerSourceDeterministic {
		t.Fatalf("Source = %q, want deterministic", result.Source)
	}
	if result.LLMInvoked {
		t.Fatal("LLMInvoked = true, want false")
	}
}

func TestOperationCompilerAmbiguousAliasTextStillInvokesLLM(t *testing.T) {
	t.Parallel()

	intent := &recordingIntentCompiler{
		draft: IntentDraft{
			Act:        ActReadQuery,
			Domain:     DomainSubscription,
			Operation:  "subscription.query_status",
			Confidence: 0.9,
			Reason:     "ambiguous_request_resolved_by_llm",
		},
	}
	compiler := newOperationCompiler(intent)

	result, err := compiler.Compile(context.Background(), protocolInput{Message: "取消考勤推送还是查询状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(intent.requests) != 1 {
		t.Fatalf("LLM Compile() calls = %d, want 1", len(intent.requests))
	}
	if result.Source != CompilerSourceLLM || !result.LLMInvoked {
		t.Fatalf("Source = %q, LLMInvoked = %v; want llm, true", result.Source, result.LLMInvoked)
	}
}

func TestOperationCompilerInferredWriteStillInvokesLLM(t *testing.T) {
	t.Parallel()

	intent := &recordingIntentCompiler{
		draft: IntentDraft{
			Act:        ActWriteRequest,
			Domain:     DomainSubscription,
			Operation:  "subscription.cancel",
			Confidence: 0.9,
			Reason:     "inferred_write_resolved_by_llm",
		},
	}
	compiler := newOperationCompiler(intent)

	result, err := compiler.Compile(context.Background(), protocolInput{Message: "把这个群的考勤推送关掉"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(intent.requests) != 1 {
		t.Fatalf("LLM Compile() calls = %d, want 1", len(intent.requests))
	}
	if result.Source != CompilerSourceLLM || !result.LLMInvoked {
		t.Fatalf("Source = %q, LLMInvoked = %v; want llm, true", result.Source, result.LLMInvoked)
	}
}

func TestOperationCompilerProducesCatalogAliasCandidateWhenLLMReturnsUnknown(t *testing.T) {
	t.Parallel()

	compiler := newOperationCompiler(&fakeProtocolIntentCompiler{
		draftsByMessage: map[string]IntentDraft{
			"取消考勤推送": {
				Act:           ActUnknown,
				Domain:        DomainUnknown,
				ClarifyReason: "intent_parse_failed",
			},
		},
	})

	result, err := compiler.Compile(context.Background(), protocolInput{Message: "取消考勤推送"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Draft.Act != ActWriteRequest || result.Draft.Operation != "subscription.cancel" {
		t.Fatalf("Draft = %+v, want subscription.cancel write request", result.Draft)
	}
	if !operationCandidatesContain(result.Candidates, "subscription.cancel", OperationCandidateSourceCatalogAlias) {
		t.Fatalf("Candidates = %+v, want catalog alias candidate for subscription.cancel", result.Candidates)
	}
	if result.Decision.Source != OperationCandidateSourceCatalogAlias {
		t.Fatalf("Decision.Source = %q, want catalog alias", result.Decision.Source)
	}
}

func TestOperationCompilerBusinessCancelBeatsWorkflowCancel(t *testing.T) {
	t.Parallel()

	compiler := newOperationCompiler(&fakeProtocolIntentCompiler{
		draftsByMessage: map[string]IntentDraft{
			"取消考勤推送": {
				Act:       ActWorkflowCancel,
				Domain:    DomainSubscription,
				Operation: "subscription.start",
				Reason:    "llm_confused_business_cancel_with_workflow_cancel",
			},
		},
	})

	result, err := compiler.Compile(context.Background(), protocolInput{
		Message: "取消考勤推送",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"scope"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Draft.Act != ActWriteRequest || result.Draft.Operation != "subscription.cancel" {
		t.Fatalf("Draft = %+v, want explicit business cancel to beat workflow cancel", result.Draft)
	}
	if result.Decision.Kind != OperationArbiterDecisionNewOperation {
		t.Fatalf("Decision.Kind = %q, want new operation", result.Decision.Kind)
	}
}

func TestOperationCompilerProducesWorkflowSlotCandidateForActiveWorkflowInput(t *testing.T) {
	t.Parallel()

	compiler := newOperationCompiler(&fakeProtocolIntentCompiler{
		draftsByMessage: map[string]IntentDraft{
			"家族七期": {
				Act:           ActUnknown,
				Domain:        DomainUnknown,
				ClarifyReason: "intent_parse_failed",
			},
		},
	})

	result, err := compiler.Compile(context.Background(), protocolInput{
		Message: "家族七期",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"scope"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Draft.Act != ActWorkflowContinue || result.Draft.Operation != "subscription.start" {
		t.Fatalf("Draft = %+v, want subscription.start workflow continue", result.Draft)
	}
	candidate, ok := findOperationCandidate(result.Candidates, "subscription.start", OperationCandidateSourceWorkflowSlot)
	if !ok {
		t.Fatalf("Candidates = %+v, want workflow slot candidate", result.Candidates)
	}
	if candidate.Evidence != "workflow_slot_shape" {
		t.Fatalf("workflow slot candidate evidence = %q, want workflow_slot_shape", candidate.Evidence)
	}
	if result.Decision.Kind != OperationArbiterDecisionWorkflowContinue {
		t.Fatalf("Decision.Kind = %q, want workflow continue", result.Decision.Kind)
	}
}

func TestOperationCompilerFallsBackToExactReadOnLLMTimeout(t *testing.T) {
	t.Parallel()

	compiler := newOperationCompiler(&recordingIntentCompiler{err: context.DeadlineExceeded})
	result, err := compiler.Compile(context.Background(), protocolInput{Message: "当前都有哪些部门"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Draft.Act != ActReadQuery || result.Draft.Operation != "subscription.list_departments" {
		t.Fatalf("Draft = %+v, want subscription.list_departments read", result.Draft)
	}
	if result.Source != CompilerSourceFallback || !result.LLMInvoked {
		t.Fatalf("Source=%q LLMInvoked=%v, want fallback/true", result.Source, result.LLMInvoked)
	}
	if result.LLMStatus != "timeout" || result.FallbackReason != "llm_timeout" {
		t.Fatalf("LLMStatus=%q FallbackReason=%q, want timeout/llm_timeout", result.LLMStatus, result.FallbackReason)
	}
}

func TestOperationCompilerInferredWriteFailsClosedOnLLMTimeout(t *testing.T) {
	t.Parallel()

	compiler := newOperationCompiler(&recordingIntentCompiler{err: context.DeadlineExceeded})
	result, err := compiler.Compile(context.Background(), protocolInput{Message: "帮我弄一下群推送"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Draft.Act != ActUnknown || result.Draft.Operation != "" {
		t.Fatalf("Draft = %+v, want non-executable unknown", result.Draft)
	}
	if result.Source != CompilerSourceFallback || result.LLMStatus != "timeout" || result.FallbackReason != "llm_timeout" {
		t.Fatalf("result metadata = %+v, want closed timeout fallback", result)
	}
}

func TestOperationCompilerFuzzyReadFailsClosedOnLLMTimeout(t *testing.T) {
	t.Parallel()

	compiler := newOperationCompiler(&recordingIntentCompiler{err: context.DeadlineExceeded})
	result, err := compiler.Compile(context.Background(), protocolInput{Message: "查询今天第二节考勤状态"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Draft.Act != ActUnknown || result.Draft.Operation != "" {
		t.Fatalf("Draft = %+v, want fuzzy read to fail closed", result.Draft)
	}
	for _, candidate := range result.Candidates {
		if candidate.Source == OperationCandidateSourceLegacy {
			t.Fatalf("Candidates = %+v, must not add legacy fallback after LLM error", result.Candidates)
		}
	}
}

func TestOperationCompilerNonTimeoutErrorUsesLowCardinalityFallbackMetadata(t *testing.T) {
	t.Parallel()

	const rawError = "provider secret response"
	compiler := newOperationCompiler(&recordingIntentCompiler{err: errors.New(rawError)})
	result, err := compiler.Compile(context.Background(), protocolInput{Message: "帮我弄一下群推送"})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.LLMStatus != "error" || result.FallbackReason != "llm_error" {
		t.Fatalf("LLMStatus=%q FallbackReason=%q, want error/llm_error", result.LLMStatus, result.FallbackReason)
	}
	if result.Draft.Reason == rawError || result.FallbackReason == rawError {
		t.Fatalf("raw provider error leaked into result: %+v", result)
	}
}

func TestSafeDeterministicFallbackAllowsExactLowRiskWrite(t *testing.T) {
	t.Parallel()

	candidate := OperationCandidate{
		Draft:  ProtocolDraft{Act: ActWriteRequest, Domain: DomainSubscription, Operation: "subscription.cancel"},
		Source: OperationCandidateSourceCatalogAlias, Confidence: 1,
	}
	decision, ok := safeDeterministicFallback("取消考勤推送", nil, []OperationCandidate{candidate})
	if !ok || decision.Draft.Operation != "subscription.cancel" {
		t.Fatalf("decision=%+v ok=%v, want exact low-risk write", decision, ok)
	}
}

func TestSafeDeterministicFallbackAllowsExactWorkflowControlAndSelection(t *testing.T) {
	t.Parallel()

	workflow := &protocolWorkflowContext{Type: "subscription.start", MissingFields: []string{"dept_ids"}}
	control := OperationCandidate{
		Draft:  ProtocolDraft{Act: ActWorkflowCancel, Domain: DomainSubscription, Operation: "subscription.start"},
		Source: OperationCandidateSourceWorkflowCtrl, Confidence: 1,
	}
	if decision, ok := safeDeterministicFallback("取消", workflow, []OperationCandidate{control}); !ok || decision.Kind != OperationArbiterDecisionWorkflowCancel {
		t.Fatalf("workflow control decision=%+v ok=%v", decision, ok)
	}

	selection := OperationCandidate{
		Draft:  ProtocolDraft{Act: ActWorkflowContinue, Domain: DomainSubscription, Operation: "subscription.start"},
		Source: OperationCandidateSourceWorkflowSlot, Confidence: 1,
	}
	if decision, ok := safeDeterministicFallback("第一个", workflow, []OperationCandidate{selection}); !ok || decision.Kind != OperationArbiterDecisionWorkflowContinue {
		t.Fatalf("workflow selection decision=%+v ok=%v", decision, ok)
	}
}

func TestSafeDeterministicFallbackRejectsFuzzyAndAmbiguousCandidates(t *testing.T) {
	t.Parallel()

	fuzzy := OperationCandidate{
		Draft:  ProtocolDraft{Act: ActReadQuery, Domain: DomainSubscription, Operation: "subscription.list_departments"},
		Source: OperationCandidateSourceCatalogAlias, Confidence: 0.8,
	}
	if _, ok := safeDeterministicFallback("大概有哪些部门", nil, []OperationCandidate{fuzzy}); ok {
		t.Fatal("fuzzy catalog candidate unexpectedly accepted")
	}
	ambiguous := []OperationCandidate{
		fuzzy,
		{Draft: ProtocolDraft{Act: ActReadQuery, Domain: DomainAttendance, Operation: "attendance.query_status"}, Source: OperationCandidateSourceCatalogAlias, Confidence: 1},
	}
	if _, ok := safeDeterministicFallback("查询状态", nil, ambiguous); ok {
		t.Fatal("ambiguous candidates unexpectedly accepted")
	}
}

func operationCandidatesContain(candidates []OperationCandidate, operation string, source OperationCandidateSource) bool {
	_, ok := findOperationCandidate(candidates, operation, source)
	return ok
}

func findOperationCandidate(candidates []OperationCandidate, operation string, source OperationCandidateSource) (OperationCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.Draft.Operation == operation && candidate.Source == source {
			return candidate, true
		}
	}
	return OperationCandidate{}, false
}

package agent

import (
	"context"
	"testing"
)

type recordingIntentCompiler struct {
	draft    IntentDraft
	requests []IntentCompileRequest
}

func (c *recordingIntentCompiler) Compile(_ context.Context, req IntentCompileRequest) (IntentDraft, error) {
	c.requests = append(c.requests, req)
	return c.draft, nil
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

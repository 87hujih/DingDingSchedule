package agent

import (
	"context"
	"testing"
)

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

func TestOperationCompilerRenderedCandidateSelectionSkipsLLM(t *testing.T) {
	t.Parallel()

	intent := &countingIntentCompiler{result: IntentCompileResult{
		Draft:  IntentDraft{Act: ActUnknown, Domain: DomainUnknown, Reason: "should_not_be_called"},
		Status: IntentCompileUnknown,
	}}
	compiler := newOperationCompiler(intent)

	result, err := compiler.Compile(context.Background(), protocolInput{
		Message: "3. 26暑期智能体开发训练营",
		ActiveWorkflow: &protocolWorkflowContext{
			Type:          "subscription.start",
			MissingFields: []string{"dept_names"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if intent.calls != 0 {
		t.Fatalf("LLM Compile() calls = %d, want 0", intent.calls)
	}
	if result.Source != CompilerSourceDeterministic || result.LLMInvoked {
		t.Fatalf("Source=%q LLMInvoked=%v, want deterministic/false", result.Source, result.LLMInvoked)
	}
	if result.Draft.Act != ActWorkflowContinue || result.Draft.Operation != "subscription.start" {
		t.Fatalf("Draft = %+v, want subscription.start workflow continue", result.Draft)
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

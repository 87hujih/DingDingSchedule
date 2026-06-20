package agent

import (
	"context"
	"strings"
)

type OperationCompileResult struct {
	Draft      ProtocolDraft
	Candidates []OperationCandidate
	Decision   OperationArbiterDecision
}

type operationCompiler struct {
	intent IntentCompiler
}

func newOperationCompiler(intent IntentCompiler) operationCompiler {
	return operationCompiler{intent: intent}
}

func (c operationCompiler) Compile(ctx context.Context, input protocolInput) (OperationCompileResult, error) {
	message := strings.TrimSpace(input.Message)
	if message == "" {
		draft := ProtocolDraft{Act: ActUnknown, Domain: DomainUnknown, Reason: "empty_message", ClarifyReason: "empty_message"}
		return OperationCompileResult{Draft: draft, Decision: OperationArbiterDecision{Kind: OperationArbiterDecisionUnknown, Draft: draft, Reason: "empty_message"}}, nil
	}

	candidates := catalogAliasCandidates(message)
	candidates = append(candidates, workflowControlCandidates(message, input.ActiveWorkflow)...)
	candidates = append(candidates, workflowSlotCandidates(message, input.ActiveWorkflow)...)

	var llmDraft ProtocolDraft
	if c.intent != nil {
		draft, err := c.intent.Compile(ctx, IntentCompileRequest{
			Message:        message,
			ActiveWorkflow: intentCompileWorkflowContext(input.ActiveWorkflow),
		})
		if err != nil {
			return OperationCompileResult{}, err
		}
		llmDraft = draft
		if draft.Act != ActUnknown {
			candidates = append(candidates, OperationCandidate{
				Draft:      draft,
				Source:     OperationCandidateSourceLLM,
				Confidence: draft.Confidence,
				Evidence:   strings.TrimSpace(draft.Reason),
			})
		}
	}

	if len(candidates) == 0 || llmDraft.Act == ActUnknown {
		if fallback := compileProtocol(input); fallback.Act != ActUnknown {
			candidates = append(candidates, OperationCandidate{
				Draft:      fallback,
				Source:     deterministicFallbackSource(fallback),
				Confidence: fallback.Confidence,
				Evidence:   "legacy_deterministic_fallback",
			})
		}
	}

	if len(candidates) == 0 {
		draft := llmDraft
		if draft.Act == "" {
			draft = unknownIntentDraft("unknown_intent")
		}
		decision := OperationArbiterDecision{Kind: OperationArbiterDecisionUnknown, Draft: draft, Reason: "no_candidate"}
		return OperationCompileResult{Draft: draft, Decision: decision}, nil
	}

	decision := newOperationArbiter().Decide(OperationArbiterInput{
		Message:        message,
		ActiveWorkflow: input.ActiveWorkflow,
		Candidates:     candidates,
	})
	return OperationCompileResult{
		Draft:      decision.Draft,
		Candidates: candidates,
		Decision:   decision,
	}, nil
}

func catalogAliasCandidates(message string) []OperationCandidate {
	candidates := []OperationCandidate{}
	for _, manifest := range operationManifests() {
		for _, alias := range manifest.Recognition.Aliases {
			if !recognitionAliasMatches(message, alias) {
				continue
			}
			act := primaryRecognitionAct(manifest)
			confidence := recognitionAliasConfidence(message, alias)
			candidates = append(candidates, OperationCandidate{
				Draft: ProtocolDraft{
					Act:        act,
					Domain:     manifest.Domain,
					Operation:  manifest.Name,
					Confidence: confidence,
					Reason:     "catalog_alias",
				},
				Source:     OperationCandidateSourceCatalogAlias,
				Confidence: confidence,
				Evidence:   alias,
			})
		}
	}
	return candidates
}

func workflowControlCandidates(message string, workflow *protocolWorkflowContext) []OperationCandidate {
	if draft, ok := compileWorkflowCancel(message, workflow); ok {
		return []OperationCandidate{{
			Draft:      draft,
			Source:     OperationCandidateSourceWorkflowCtrl,
			Confidence: 1,
			Evidence:   "workflow_cancel",
		}}
	}
	return nil
}

func workflowSlotCandidates(message string, workflow *protocolWorkflowContext) []OperationCandidate {
	if workflow == nil {
		return nil
	}
	manifest, ok := lookupOperation(workflow.Type)
	if !ok || manifest.Workflow == nil {
		return nil
	}
	if !messageMatchesWorkflowSlotShape(message, workflow, manifest) {
		return nil
	}
	return []OperationCandidate{{
		Draft: ProtocolDraft{
			Act:        ActWorkflowContinue,
			Domain:     manifest.Domain,
			Operation:  manifest.Name,
			Confidence: 1,
			Reason:     "workflow_slot_shape",
		},
		Source:     OperationCandidateSourceWorkflowSlot,
		Confidence: 1,
		Evidence:   "workflow_slot_shape",
	}}
}

func messageMatchesWorkflowSlotShape(message string, workflow *protocolWorkflowContext, manifest OperationManifest) bool {
	for _, hint := range manifest.Recognition.SlotHints {
		if !workflowHasMissingParam(workflow.MissingFields, hint.Field) {
			continue
		}
		switch hint.Shape {
		case "subscription_scope":
			if containsAny(normalizeQuery(message), []string{"全部人员", "全部", "指定部门", "部分部门"}) || looksLikeEntityInput(message) {
				return true
			}
		case "department_name_or_candidate":
			if looksLikeEntityInput(message) {
				return true
			}
			if _, ok := parseCandidateOrdinal(message); ok {
				return true
			}
		}
	}
	return false
}

func deterministicFallbackSource(draft ProtocolDraft) OperationCandidateSource {
	if draft.Act == ActWorkflowContinue {
		return OperationCandidateSourceWorkflowSlot
	}
	if draft.Act == ActWorkflowCancel {
		return OperationCandidateSourceWorkflowCtrl
	}
	return OperationCandidateSourceLegacy
}

func primaryRecognitionAct(manifest OperationManifest) UserAct {
	for _, act := range manifest.AllowedActs {
		if act != ActWorkflowContinue {
			return act
		}
	}
	if len(manifest.AllowedActs) == 0 {
		return ActUnknown
	}
	return manifest.AllowedActs[0]
}

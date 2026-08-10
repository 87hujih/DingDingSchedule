package agent

import (
	"context"
	"strings"
)

type OperationCompileResult struct {
	Draft          ProtocolDraft
	Candidates     []OperationCandidate
	Decision       OperationArbiterDecision
	Source         CompilerSource
	LLMInvoked     bool
	LLMStatus      IntentCompileStatus
	LLMAttempts    int
	FallbackReason string
}

type CompilerSource string

const (
	CompilerSourceDeterministic CompilerSource = "deterministic"
	CompilerSourceLLM           CompilerSource = "llm"
	CompilerSourceFallback      CompilerSource = "fallback"
)

type operationCompiler struct {
	intent IntentCompiler
	mode   string
}

func newOperationCompiler(intent IntentCompiler) operationCompiler {
	return newOperationCompilerWithMode(intent, "short_circuit")
}

func newOperationCompilerWithMode(intent IntentCompiler, mode string) operationCompiler {
	switch strings.TrimSpace(mode) {
	case "observe", "fallback", "short_circuit":
	default:
		mode = "short_circuit"
	}
	return operationCompiler{intent: intent, mode: mode}
}

func (c operationCompiler) Compile(ctx context.Context, input protocolInput) (OperationCompileResult, error) {
	message := strings.TrimSpace(input.Message)
	if message == "" {
		draft := ProtocolDraft{Act: ActUnknown, Domain: DomainUnknown, Reason: "empty_message", ClarifyReason: "empty_message"}
		return OperationCompileResult{
			Draft:     draft,
			Decision:  OperationArbiterDecision{Kind: OperationArbiterDecisionUnknown, Draft: draft, Reason: "empty_message"},
			Source:    CompilerSourceDeterministic,
			LLMStatus: IntentCompileSkipped,
		}, nil
	}
	if err := ctx.Err(); err != nil {
		return OperationCompileResult{}, err
	}

	candidates := workflowControlCandidates(message, input.ActiveWorkflow)
	if !hasExactOperationCandidate(candidates) {
		candidates = append(candidates, workflowSlotCandidates(message, input.ActiveWorkflow)...)
	}

	if c.mode == "short_circuit" {
		if exact := uniqueSafeDeterministicCandidates(candidates, true); len(exact) > 0 {
			decision := newOperationArbiter().Decide(OperationArbiterInput{
				Message:        message,
				ActiveWorkflow: input.ActiveWorkflow,
				Candidates:     exact,
			})
			return OperationCompileResult{
				Draft:      decision.Draft,
				Candidates: candidates,
				Decision:   decision,
				Source:     CompilerSourceDeterministic,
				LLMStatus:  IntentCompileSkipped,
			}, nil
		}
	}

	if c.intent == nil {
		draft := unknownIntentDraft("intent_compiler_unavailable")
		return OperationCompileResult{
			Draft:      draft,
			Candidates: candidates,
			Decision:   OperationArbiterDecision{Kind: OperationArbiterDecisionUnknown, Draft: draft, Reason: "compiler_unavailable"},
			LLMStatus:  IntentCompileSkipped,
		}, nil
	}

	compileResult, err := c.intent.Compile(ctx, IntentCompileRequest{
		Message:        message,
		RecentMessages: input.RecentMessages,
		ActiveWorkflow: intentCompileWorkflowContext(input.ActiveWorkflow),
	})
	if err != nil {
		return OperationCompileResult{}, err
	}
	if ctx.Err() != nil {
		return OperationCompileResult{}, ctx.Err()
	}

	if compileResult.Status == IntentCompileOK && compileResult.Draft.Act != ActUnknown {
		llmCandidate := OperationCandidate{
			Draft:      compileResult.Draft,
			Source:     OperationCandidateSourceLLM,
			MatchKind:  MatchLLM,
			Confidence: compileResult.Draft.Confidence,
			Evidence:   strings.TrimSpace(compileResult.Draft.Reason),
		}
		candidates = append(candidates, llmCandidate)
		decisionCandidates := candidates
		if c.mode == "observe" || c.mode == "fallback" {
			decisionCandidates = []OperationCandidate{llmCandidate}
		}
		decision := newOperationArbiter().Decide(OperationArbiterInput{
			Message:        message,
			ActiveWorkflow: input.ActiveWorkflow,
			Candidates:     decisionCandidates,
		})
		return OperationCompileResult{
			Draft:       decision.Draft,
			Candidates:  candidates,
			Decision:    decision,
			Source:      compilerSourceFromDecision(decision),
			LLMInvoked:  true,
			LLMStatus:   compileResult.Status,
			LLMAttempts: compileResult.Attempts,
		}, nil
	}

	if c.mode != "observe" {
		if safe := uniqueSafeDeterministicCandidates(candidates, false); len(safe) > 0 {
			decision := newOperationArbiter().Decide(OperationArbiterInput{
				Message:        message,
				ActiveWorkflow: input.ActiveWorkflow,
				Candidates:     safe,
			})
			return OperationCompileResult{
				Draft:          decision.Draft,
				Candidates:     candidates,
				Decision:       decision,
				Source:         CompilerSourceFallback,
				LLMInvoked:     true,
				LLMStatus:      compileResult.Status,
				LLMAttempts:    compileResult.Attempts,
				FallbackReason: fallbackReasonForStatus(compileResult.Status),
			}, nil
		}
	}

	draft := compileResult.Draft
	if draft.Act == "" {
		draft = unknownIntentDraft("unknown_intent")
	}
	if compileResult.Status == IntentCompileInvalidOutput ||
		compileResult.Status == IntentCompileTimeout ||
		compileResult.Status == IntentCompileTransportError {
		draft = unknownIntentDraft(fallbackReasonForStatus(compileResult.Status))
	}
	decision := OperationArbiterDecision{
		Kind:   OperationArbiterDecisionUnknown,
		Draft:  draft,
		Reason: "no_safe_candidate",
	}
	return OperationCompileResult{
		Draft:          draft,
		Candidates:     candidates,
		Decision:       decision,
		LLMInvoked:     true,
		LLMStatus:      compileResult.Status,
		LLMAttempts:    compileResult.Attempts,
		FallbackReason: fallbackReasonForStatus(compileResult.Status),
	}, nil
}

func hasExactOperationCandidate(candidates []OperationCandidate) bool {
	for _, candidate := range candidates {
		switch candidate.MatchKind {
		case MatchExactAlias, MatchExactWorkflowControl:
			return true
		}
	}
	return false
}

func uniqueSafeDeterministicCandidates(candidates []OperationCandidate, requireShortCircuit bool) []OperationCandidate {
	byIdentity := make(map[string]OperationCandidate)
	allIdentities := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.Draft.Act != ActUnknown {
			allIdentities[candidateIdentity(candidate)] = struct{}{}
		}
		if !safeDeterministicMatch(candidate.MatchKind) {
			continue
		}
		if requireShortCircuit && !candidate.ShortCircuit {
			continue
		}
		byIdentity[candidateIdentity(candidate)] = candidate
	}
	if len(byIdentity) != 1 || len(allIdentities) != 1 {
		return nil
	}
	result := make([]OperationCandidate, 0, 1)
	for _, candidate := range byIdentity {
		result = append(result, candidate)
	}
	return result
}

func safeDeterministicMatch(kind CandidateMatchKind) bool {
	switch kind {
	case MatchExactWorkflowControl, MatchExactCandidate:
		return true
	default:
		return false
	}
}

func fallbackReasonForStatus(status IntentCompileStatus) string {
	switch status {
	case IntentCompileTimeout:
		return "intent_timeout"
	case IntentCompileTransportError:
		return "intent_transport_error"
	case IntentCompileInvalidOutput:
		return "intent_parse_failed"
	case IntentCompileUnknown:
		return "unknown_intent"
	default:
		return ""
	}
}

func compilerSourceFromDecision(decision OperationArbiterDecision) CompilerSource {
	if decision.Source == OperationCandidateSourceLLM {
		return CompilerSourceLLM
	}
	if decision.Source != "" {
		return CompilerSourceDeterministic
	}
	return ""
}

func workflowControlCandidates(message string, workflow *protocolWorkflowContext) []OperationCandidate {
	if draft, ok := compileWorkflowCancel(message, workflow); ok {
		matchKind := workflowControlMatchKind(message)
		return []OperationCandidate{{
			Draft:        draft,
			Source:       OperationCandidateSourceWorkflowCtrl,
			MatchKind:    matchKind,
			ShortCircuit: matchKind == MatchExactWorkflowControl,
			Confidence:   1,
			Evidence:     "workflow_cancel",
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
	matchKind := workflowSlotMatchKind(message, workflow)
	return []OperationCandidate{{
		Draft: ProtocolDraft{
			Act:        ActWorkflowContinue,
			Domain:     manifest.Domain,
			Operation:  manifest.Name,
			Confidence: 1,
			Reason:     "workflow_slot_shape",
		},
		Source:       OperationCandidateSourceWorkflowSlot,
		MatchKind:    matchKind,
		ShortCircuit: workflowSlotCanShortCircuit(message, matchKind),
		Confidence:   1,
		Evidence:     "workflow_slot_shape",
	}}
}

func workflowSlotCanShortCircuit(message string, kind CandidateMatchKind) bool {
	return kind == MatchExactCandidate
}

func workflowSlotMatchKind(message string, workflow *protocolWorkflowContext) CandidateMatchKind {
	if isCandidateSelectionShape(message) {
		return MatchExactCandidate
	}
	normalized := normalizeQuery(message)
	for _, candidates := range workflow.Candidates {
		for _, candidate := range candidates {
			if normalizeQuery(candidate.Label) == normalized {
				return MatchExactCandidate
			}
		}
	}
	switch normalized {
	case "全部人员", "全部", "指定部门", "部分部门":
		return MatchStructuredSlot
	default:
		if plainWorkflowSlotValue(normalized) {
			return MatchStructuredSlot
		}
		return MatchLegacyRule
	}
}

func plainWorkflowSlotValue(normalized string) bool {
	if normalized == "" {
		return false
	}
	return !containsAny(normalized, []string{
		"什么", "怎么", "为什么", "能否", "能不能", "可以吗", "支持",
		"流程", "规则", "查询", "查看", "看看", "列表", "帮助", "功能",
		"不要", "不是", "取消", "停止",
	})
}

func workflowControlMatchKind(message string) CandidateMatchKind {
	switch normalizeQuery(message) {
	case "取消", "算了", "不用了", "停止", "退出":
		return MatchExactWorkflowControl
	default:
		return MatchContainedAlias
	}
}

func messageMatchesWorkflowSlotShape(message string, workflow *protocolWorkflowContext, manifest OperationManifest) bool {
	for _, slot := range manifest.Recognition.RawSlots {
		if slot.Shape == "" || !workflowHasMissingParam(workflow.MissingFields, slot.TargetParam) {
			continue
		}
		switch slot.Shape {
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

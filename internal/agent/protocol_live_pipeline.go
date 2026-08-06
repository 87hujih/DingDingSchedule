package agent

import (
	"context"
	"errors"
	"time"

	"schedule_server/internal/agent/tools"
)

type protocolLivePipelineDeps struct {
	Compiler       IntentCompiler
	CompilerMode   string
	Validator      CatalogValidator
	PrePolicy      PrePolicyGate
	ResourcePolicy ResourcePolicyGate
	WriteGuard     WriteGuard
	Executor       protocolOperationExecutor
	User           UserPort
	Dept           DeptPort
	Semester       SemesterPort
	SchedulePeriod SchedulePeriodPort
	Clock          func() time.Time
}

type protocolOperationExecutor interface {
	Execute(context.Context, OperationRequest) OperationExecutionResult
}

type protocolLivePipeline struct {
	deps protocolLivePipelineDeps
}

type protocolLiveInput struct {
	Message        string
	RecentMessages []tools.Message
	User           *tools.UserContext
	ActiveWorkflow *WorkflowSnapshot
}

type protocolLiveOutcome struct {
	RequestID               string
	Draft                   ProtocolDraft
	Validation              ProtocolValidationResult
	Response                ResponseModel
	ExecutionMetrics        OperationExecutionMetrics
	AnswerMode              answerMode
	BlockedReason           string
	CompilerStatus          string
	CompilerSource          string
	CompilerFallbackReason  string
	CompilerCandidateCount  int
	LLMInvoked              bool
	LLMAttempts             int
	CompilerLatencyMs       int64
	IntentDraftJSON         string
	CatalogValidationCode   string
	ResolvedSlots           map[string]any
	CandidateCount          int
	IdempotencyKey          string
	EntityResolutionStatus  string
	PrePolicyResult         string
	ResourcePolicyResult    string
	WriteGuardResult        string
	ExecutorStatus          string
	RendererName            string
	FailureLayer            FailureLayer
	LegacyCalled            bool
	WorkflowDecision        WorkflowDecision
	WorkflowInterruptReason string
	WorkflowAfter           *WorkflowSnapshot
	ClearWorkflow           bool
	PreparedWrite           *preparedWriteExecution
}

type preparedWriteExecution struct {
	Request                OperationRequest
	BusinessKey            string
	ClearWorkflowOnSuccess bool
}

func newProtocolLivePipeline(deps protocolLivePipelineDeps) protocolLivePipeline {
	return protocolLivePipeline{deps: deps}
}

func (p protocolLivePipeline) catalogValidator() CatalogValidator {
	if p.deps.Validator != nil {
		return p.deps.Validator
	}
	return newCatalogValidator()
}

func (p protocolLivePipeline) prePolicyGate() PrePolicyGate {
	if p.deps.PrePolicy != nil {
		return p.deps.PrePolicy
	}
	return newPrePolicyGate()
}

func (p protocolLivePipeline) resourcePolicyGate() ResourcePolicyGate {
	if p.deps.ResourcePolicy != nil {
		return p.deps.ResourcePolicy
	}
	return newResourcePolicyGate()
}

func (p protocolLivePipeline) writeGuard() WriteGuard {
	if p.deps.WriteGuard != nil {
		return p.deps.WriteGuard
	}
	return newWriteGuard()
}

func (p protocolLivePipeline) executor() protocolOperationExecutor {
	if p.deps.Executor != nil {
		return p.deps.Executor
	}
	return newOperationExecutor(operationExecutorDeps{})
}

func (p protocolLivePipeline) Handle(ctx context.Context, input protocolLiveInput) (outcome protocolLiveOutcome) { //nolint:funlen // Pipeline Handle is the top-level protocol orchestration boundary.
	outcome.RequestID = newProtocolLiveRequestID(time.Now())
	outcome.RendererName = "response_renderer"
	defer finalizeProtocolLiveOutcome(&outcome)

	receivedWorkflow := input.ActiveWorkflow
	activeWorkflow := receivedWorkflow
	if workflowExpired(activeWorkflow, p.now()) {
		activeWorkflow = nil
	}
	workflowCtx := protocolWorkflowContextFromWorkflowSnapshot(activeWorkflow)
	compileStart := time.Now()
	compileResult, err := compileProtocolWithCompilerMode(ctx, protocolInput{
		Message:        input.Message,
		RecentMessages: input.RecentMessages,
		ActiveWorkflow: workflowCtx,
	}, p.deps.Compiler, p.deps.CompilerMode)
	outcome.CompilerLatencyMs = elapsedMs(compileStart)
	outcome.CompilerStatus = string(compileResult.LLMStatus)
	outcome.CompilerSource = string(compileResult.Source)
	outcome.CompilerFallbackReason = compileResult.FallbackReason
	outcome.CompilerCandidateCount = len(compileResult.Candidates)
	outcome.LLMInvoked = compileResult.LLMInvoked
	outcome.LLMAttempts = compileResult.LLMAttempts
	if err != nil {
		outcome.CompilerStatus = string(IntentCompileSkipped)
		draft := unknownIntentDraft("intent_compile_failed")
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			draft = unknownIntentDraft("request_canceled")
			outcome.BlockedReason = "request_canceled"
		}
		outcome.Draft = draft
		outcome.IntentDraftJSON = compactIntentDraft(draft)
		outcome.Validation = ProtocolValidationResult{
			ValidationCode: "intent_compile_failed",
			ResponseKind:   ResponseRefuse,
		}
		outcome.FailureLayer = FailureIntent
		setProtocolOutcomeResponse(&outcome, ResponseModel{
			Kind:          ResponseRefuse,
			RefusalReason: "本次请求已终止，请稍后重试。",
		}, answerModeReject)
		return outcome
	}
	draft := compileResult.Draft
	outcome.IntentDraftJSON = compactIntentDraft(draft)

	validation := p.prePolicyGate().Validate(PrePolicyGateInput{
		Draft:            draft,
		ActiveWorkflow:   workflowCtx,
		ConversationType: userConversationType(input.User),
		UserRole:         inputUserRole(input.User),
		HasUserContext:   input.User != nil,
	})
	outcome.Draft = draft
	outcome.Validation = validation
	outcome.AnswerMode = answerModeReject
	outcome.CatalogValidationCode = validation.ValidationCode
	outcome.PrePolicyResult = protocolPrePolicyResult(validation)
	arbiterDecision := newWorkflowArbiter(p.now).Decide(WorkflowArbiterInput{
		Draft:          draft,
		ActiveWorkflow: receivedWorkflow,
	})
	if arbiterDecision.Expired {
		outcome.WorkflowDecision = arbiterDecision.Decision
		outcome.WorkflowInterruptReason = "expired"
		outcome.ClearWorkflow = true
	}

	if validation.InterruptActiveWorkflow {
		result := interruptActiveWorkflow(nil, "", activeWorkflow, draft)
		outcome.WorkflowDecision = result.Decision
		outcome.WorkflowInterruptReason = string(draft.Act)
		outcome.ClearWorkflow = workflowResultTerminal(result)
		activeWorkflow = nil
	}

	if blocked, response := protocolLiveRoleRefusal(input.User, draft); blocked {
		outcome.Validation.AllowExecution = false
		outcome.Validation.ResponseKind = ResponseRefuse
		setProtocolOutcomeResponse(&outcome, response, answerModeReject)
		outcome.BlockedReason = "role_denied"
		outcome.PrePolicyResult = "deny:role_denied"
		outcome.FailureLayer = FailurePrePolicyDenied
		return outcome
	}

	if !protocolPrimaryDispatchAllowed(draft, validation) {
		response, mode := protocolLiveGuardrailResponse(draft, validation, input.User)
		setProtocolOutcomeResponse(&outcome, response, mode)
		return outcome
	}

	if draft.Act == ActWorkflowCancel {
		if activeWorkflow == nil {
			response, mode := protocolLiveGuardrailResponse(draft, validation, input.User)
			setProtocolOutcomeResponse(&outcome, response, mode)
			return outcome
		}
		result := continueWorkflow(*activeWorkflow, draft, trustedEntities{})
		outcome.WorkflowDecision = result.Decision
		outcome.ClearWorkflow = workflowResultTerminal(result)
		setProtocolOutcomeResponse(&outcome, ResponseModel{Kind: ResponseResult, ResultText: "已取消当前任务。如需继续，请重新告诉我。"}, answerModeToolFirst)
		return outcome
	}

	if manifest, ok := lookupOperation(draft.Operation); ok {
		if ctx.Err() != nil {
			outcome.BlockedReason = "request_canceled"
			outcome.FailureLayer = FailureIntent
			setProtocolOutcomeResponse(&outcome, ResponseModel{
				Kind:          ResponseRefuse,
				RefusalReason: "本次请求已终止，请稍后重试。",
			}, answerModeReject)
			return outcome
		}
		if manifest.Renderer.Name != "" {
			outcome.RendererName = manifest.Renderer.Name
		}
		if dispatch, ok := lookupProtocolLiveDispatch(manifest.Dispatch.Name); ok {
			return dispatch.Handle(ctx, p, input, draft, manifest, activeWorkflow, outcome)
		}
	}

	response, mode := protocolLiveGuardrailResponse(draft, validation, input.User)
	setProtocolOutcomeResponse(&outcome, response, mode)
	return outcome
}

func (p protocolLivePipeline) now() time.Time {
	if p.deps.Clock != nil {
		return p.deps.Clock()
	}
	return time.Now()
}

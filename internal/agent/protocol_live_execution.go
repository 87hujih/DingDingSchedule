package agent

import (
	"context"
	"errors"

	"schedule_server/internal/agent/tools"
)

func (p protocolLivePipeline) execute(ctx context.Context, uctx *tools.UserContext, req OperationRequest, outcome protocolLiveOutcome) protocolLiveOutcome {
	prepared, outcome, ok := p.prepareExecution(ctx, uctx, req, outcome)
	if !ok {
		return outcome
	}
	if !prepared.Manifest.IsWrite {
		result := p.executor().Execute(ctx, prepared.Request)
		return applyOperationExecutionResult(outcome, prepared.Request, result)
	}
	outcome.WorkflowStoreApplied = true

	base := cloneWorkflowSnapshot(outcome.WorkflowAfter)
	if base == nil {
		base = cloneWorkflowSnapshot(outcome.WorkflowExecutionBase)
	}
	if base == nil {
		base = &WorkflowSnapshot{
			ID:    outcome.RequestID,
			Type:  WorkflowType(prepared.Request.Operation),
			State: WorkflowReady,
		}
	}
	key := workflowKeyFromUserContext(uctx)
	executed, err := newProtocolLiveExecutionCoordinator(p.deps.WorkflowStore, p.now).Execute(ctx, WorkflowExecutionRequest{
		Key:             key,
		ExpectedVersion: uint64(max(base.Version, 0)),
		Workflow:        base,
		Operation:       prepared.Request,
		BusinessKey:     prepared.Request.IdempotencyKey,
		RequestID:       outcome.RequestID,
	}, p.executor())
	if err != nil {
		outcome.FailureLayer = FailureExecutor
		outcome.BlockedReason = "workflow_execution_conflict"
		if errors.Is(err, ErrWorkflowConflict) {
			setProtocolOutcomeResponse(&outcome, ResponseModel{Kind: ResponseRefuse, RefusalReason: "操作正在处理中，请稍后再试。"}, answerModeReject)
		} else {
			setProtocolOutcomeResponse(&outcome, ResponseModel{Kind: ResponseRefuse, RefusalReason: "操作暂时无法执行，请稍后再试。"}, answerModeReject)
		}
		return outcome
	}
	outcome.WorkflowStoreApplied = true
	outcome.ClearWorkflow = executed.OperationResult.Response.Kind == ResponseResult
	if outcome.ClearWorkflow {
		outcome.WorkflowAfter = nil
	}
	return applyOperationExecutionResult(outcome, prepared.Request, executed.OperationResult)
}

type PreparedProtocolExecution struct {
	Request  OperationRequest
	Manifest OperationManifest
}

func (p protocolLivePipeline) prepareExecution(ctx context.Context, uctx *tools.UserContext, req OperationRequest, outcome protocolLiveOutcome) (PreparedProtocolExecution, protocolLiveOutcome, bool) {
	req = enrichOperationRequestFromUser(req, uctx)
	if resource := p.resourcePolicyGate().Validate(ctx, ResourcePolicyGateInput{
		User:    uctx,
		Request: req,
		Dept:    p.deps.Dept,
	}); !resource.Allow {
		outcome.ResourcePolicyResult = "deny:" + resource.BlockedReason
		outcome.FailureLayer = FailureResourcePolicyDenied
		kind := resource.ResponseKind
		if kind == "" {
			kind = ResponseRefuse
		}
		setProtocolOutcomeResponse(&outcome, ResponseModel{
			Kind:          kind,
			RefusalReason: resourceRefusalText(resource.BlockedReason),
		}, answerModeReject)
		outcome.BlockedReason = resource.BlockedReason
		return PreparedProtocolExecution{}, outcome, false
	}
	outcome.ResourcePolicyResult = "allow"
	manifest, manifestOK := lookupOperation(req.Operation)
	if manifestOK && manifest.IsWrite {
		guard := p.writeGuard().Check(WriteGuardInput{
			User:     uctx,
			Manifest: manifest,
			Request:  req,
			Workflow: outcome.WorkflowAfter,
		})
		outcome.IdempotencyKey = guard.IdempotencyKey
		req.IdempotencyKey = guard.IdempotencyKey
		if !guard.Allow {
			outcome.WriteGuardResult = "block:" + guard.BlockedReason
			outcome.FailureLayer = FailureWriteGuardBlocked
			kind := guard.ResponseKind
			if kind == "" {
				kind = ResponseRefuse
			}
			setProtocolOutcomeResponse(&outcome, ResponseModel{
				Kind:    kind,
				Message: writeGuardResponseText(guard.BlockedReason),
			}, answerModeReject)
			outcome.BlockedReason = guard.BlockedReason
			return PreparedProtocolExecution{}, outcome, false
		}
		outcome.WriteGuardResult = "allow"
	} else if outcome.WriteGuardResult == "" {
		outcome.WriteGuardResult = "not_required"
	}
	return PreparedProtocolExecution{Request: req, Manifest: manifest}, outcome, true
}

func applyOperationExecutionResult(outcome protocolLiveOutcome, req OperationRequest, result OperationExecutionResult) protocolLiveOutcome {
	setProtocolOutcomeResponse(&outcome, result.Response, result.Metrics.AnswerMode)
	if len(req.TrustedParams) > 0 {
		outcome.ResolvedSlots = mergeProtocolResolvedSlots(outcome.ResolvedSlots, protocolResolvedSlotsFromParams(req.TrustedParams))
	}
	outcome.ExecutionMetrics = result.Metrics
	outcome.ExecutorStatus = protocolExecutorStatus(result.Response)
	return outcome
}

func resourceRefusalText(reason string) string {
	switch reason {
	case "subscription_conversation_mismatch":
		return "只能操作当前群聊的考勤订阅。请在对应群聊里再告诉我。"
	case "schedule_user_visibility_denied":
		return "抱歉，你当前不能查看该用户的课表。"
	case "department_scope_denied":
		return "只能选择当前租户可访问的部门。请重新选择部门。"
	case "group_chat_required":
		return "该操作只能在群聊中使用。请在对应群聊里再告诉我。"
	default:
		return "抱歉，我当前不能直接执行这个请求。"
	}
}

func writeGuardResponseText(reason string) string {
	switch reason {
	case "write_confirmation_required":
		return "请确认是否执行该写操作。"
	default:
		return "抱歉，我当前不能直接执行这个请求。"
	}
}

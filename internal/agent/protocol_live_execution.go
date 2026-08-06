package agent

import (
	"context"

	"schedule_server/internal/agent/tools"
)

func (p protocolLivePipeline) execute(ctx context.Context, uctx *tools.UserContext, req OperationRequest, outcome protocolLiveOutcome) protocolLiveOutcome {
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
		return outcome
	}
	outcome.ResourcePolicyResult = "allow"
	if manifest, ok := lookupOperation(req.Operation); ok && manifest.IsWrite {
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
			return outcome
		}
		outcome.WriteGuardResult = "allow"
		businessKey, err := subscriptionBusinessKeyForRequest(req)
		if err != nil {
			outcome.FailureLayer = FailureWriteGuardBlocked
			outcome.WriteGuardResult = "block:invalid_business_key"
			outcome.BlockedReason = "invalid_business_key"
			setProtocolOutcomeResponse(&outcome, ResponseModel{
				Kind:    ResponseRefuse,
				Message: "写操作参数校验失败，请重新发起请求。",
			}, answerModeReject)
			return outcome
		}
		req.IdempotencyKey = businessKey
		outcome.IdempotencyKey = businessKey
		outcome.PreparedWrite = &preparedWriteExecution{
			Request:     req,
			BusinessKey: businessKey,
		}
		setProtocolOutcomeResponse(&outcome, preparedWriteResponse(req.Operation), answerModeToolFirst)
		outcome.ExecutorStatus = "prepared"
		if len(req.TrustedParams) > 0 {
			outcome.ResolvedSlots = mergeProtocolResolvedSlots(outcome.ResolvedSlots, protocolResolvedSlotsFromParams(req.TrustedParams))
		}
		return outcome
	} else if outcome.WriteGuardResult == "" {
		outcome.WriteGuardResult = "not_required"
	}
	result := p.executor().Execute(ctx, req)
	setProtocolOutcomeResponse(&outcome, result.Response, result.Metrics.AnswerMode)
	if len(req.TrustedParams) > 0 {
		outcome.ResolvedSlots = mergeProtocolResolvedSlots(outcome.ResolvedSlots, protocolResolvedSlotsFromParams(req.TrustedParams))
	}
	outcome.ExecutionMetrics = result.Metrics
	outcome.ExecutorStatus = protocolExecutorStatus(result.Response)
	return outcome
}

func preparedWriteResponse(operation string) ResponseModel {
	switch operation {
	case "subscription.start":
		return ResponseModel{
			Kind:    ResponseResult,
			Payload: OperationStatusPayload{Code: "subscription_started", Status: WriteStatusCreated},
		}
	case "subscription.cancel":
		return ResponseModel{
			Kind:    ResponseResult,
			Payload: OperationStatusPayload{Code: "subscription_cancelled", Status: WriteStatusUpdated},
		}
	default:
		return ResponseModel{Kind: ResponseRefuse, RefusalReason: "当前写操作暂不支持安全执行。"}
	}
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

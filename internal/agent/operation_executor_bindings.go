package agent

import "context"

type operationDomainBinding interface {
	Execute(ctx context.Context, deps operationExecutorDeps, req OperationRequest) (OperationExecutionResult, bool)
}

type operationDomainBindingFunc func(context.Context, operationExecutorDeps, OperationRequest) (OperationExecutionResult, bool)

func (fn operationDomainBindingFunc) Execute(ctx context.Context, deps operationExecutorDeps, req OperationRequest) (OperationExecutionResult, bool) {
	return fn(ctx, deps, req)
}

func operationDomainBindings() map[BusinessDomain]operationDomainBinding {
	return map[BusinessDomain]operationDomainBinding{
		DomainSystem:       operationDomainBindingFunc(executeSystemOperation),
		DomainAttendance:   operationDomainBindingFunc(executeAttendanceOperation),
		DomainSchedule:     operationDomainBindingFunc(executeScheduleOperation),
		DomainSubscription: operationDomainBindingFunc(executeSubscriptionOperation),
		DomainManualSign:   operationDomainBindingFunc(executeManualSignOperation),
	}
}

func executeSystemOperation(_ context.Context, _ operationExecutorDeps, req OperationRequest) (OperationExecutionResult, bool) {
	if req.Operation != "system.describe_capability" {
		return OperationExecutionResult{}, false
	}
	return operationExecutionResult(ResponseModel{
		Kind: ResponseAnswer,
		Payload: CapabilityAnswerPayload{
			Domain:           DomainSystem,
			UserRole:         operationRequestActorRole(req),
			ConversationType: operationRequestConversationType(req),
		},
	}, answerModeToolFirst), true
}

func executeManualSignOperation(_ context.Context, _ operationExecutorDeps, req OperationRequest) (OperationExecutionResult, bool) {
	if req.Operation != "manual_sign.describe_capability" {
		return OperationExecutionResult{}, false
	}
	return operationExecutionResult(ResponseModel{
		Kind: ResponseAnswer,
		Payload: CapabilityAnswerPayload{
			Domain:           DomainManualSign,
			UserRole:         operationRequestActorRole(req),
			ConversationType: operationRequestConversationType(req),
		},
	}, answerModeToolFirst), true
}

func executeAttendanceOperation(ctx context.Context, deps operationExecutorDeps, req OperationRequest) (OperationExecutionResult, bool) {
	executor := operationExecutor{deps: deps}
	switch req.Operation {
	case "attendance.query_status":
		return executor.executeAttendanceQuery(ctx, req), true
	case "attendance.describe_capability":
		return operationExecutionResult(ResponseModel{
			Kind: ResponseAnswer,
			Payload: CapabilityAnswerPayload{
				Domain:           DomainAttendance,
				UserRole:         operationRequestActorRole(req),
				ConversationType: operationRequestConversationType(req),
			},
		}, answerModeToolFirst), true
	case "attendance.rule_explain":
		return executor.executeRuleExplain(ctx, req), true
	default:
		return OperationExecutionResult{}, false
	}
}

func executeScheduleOperation(ctx context.Context, deps operationExecutorDeps, req OperationRequest) (OperationExecutionResult, bool) {
	executor := operationExecutor{deps: deps}
	switch req.Operation {
	case "schedule.query_my_schedule":
		return executor.executeMyScheduleQuery(ctx, req), true
	case "schedule.query_user_schedule":
		return executor.executeUserScheduleQuery(ctx, req), true
	case "schedule.describe_capability":
		return operationExecutionResult(ResponseModel{
			Kind: ResponseAnswer,
			Payload: CapabilityAnswerPayload{
				Domain:           DomainSchedule,
				UserRole:         operationRequestActorRole(req),
				ConversationType: operationRequestConversationType(req),
			},
		}, answerModeToolFirst), true
	case "schedule.rule_explain":
		return executor.executeRuleExplain(ctx, req), true
	default:
		return OperationExecutionResult{}, false
	}
}

func executeSubscriptionOperation(ctx context.Context, deps operationExecutorDeps, req OperationRequest) (OperationExecutionResult, bool) {
	executor := operationExecutor{deps: deps}
	switch req.Operation {
	case "subscription.start":
		return executor.executeSubscriptionStart(ctx, req), true
	case "subscription.cancel":
		return executor.executeSubscriptionCancel(ctx, req), true
	case "subscription.query_status":
		return executor.executeSubscriptionStatus(ctx, req), true
	case "subscription.list_departments":
		return executor.executeListDepartments(ctx), true
	case "subscription.describe_capability":
		return operationExecutionResult(ResponseModel{
			Kind: ResponseAnswer,
			Payload: CapabilityAnswerPayload{
				Domain:           DomainSubscription,
				UserRole:         operationRequestActorRole(req),
				ConversationType: operationRequestConversationType(req),
			},
		}, answerModeToolFirst), true
	case "subscription.rule_explain":
		return executor.executeRuleExplain(ctx, req), true
	default:
		return OperationExecutionResult{}, false
	}
}

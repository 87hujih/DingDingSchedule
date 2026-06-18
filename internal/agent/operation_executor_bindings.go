package agent

import "context"

const (
	ExecutorBindingSystemDescribeCapability       = "executor.system.describe_capability"
	ExecutorBindingAttendanceQueryStatus          = "executor.attendance.query_status"
	ExecutorBindingAttendanceDescribeCapability   = "executor.attendance.describe_capability"
	ExecutorBindingAttendanceRuleExplain          = "executor.attendance.rule_explain"
	ExecutorBindingScheduleQueryMySchedule        = "executor.schedule.query_my_schedule"
	ExecutorBindingScheduleQueryUserSchedule      = "executor.schedule.query_user_schedule"
	ExecutorBindingScheduleDescribeCapability     = "executor.schedule.describe_capability"
	ExecutorBindingScheduleRuleExplain            = "executor.schedule.rule_explain"
	ExecutorBindingSubscriptionStart              = "executor.subscription.start"
	ExecutorBindingSubscriptionCancel             = "executor.subscription.cancel"
	ExecutorBindingSubscriptionQueryStatus        = "executor.subscription.query_status"
	ExecutorBindingSubscriptionListDepartments    = "executor.subscription.list_departments"
	ExecutorBindingSubscriptionDescribeCapability = "executor.subscription.describe_capability"
	ExecutorBindingSubscriptionRuleExplain        = "executor.subscription.rule_explain"
	ExecutorBindingManualSignDescribeCapability   = "executor.manual_sign.describe_capability"
)

type operationExecutorBinding interface {
	Execute(ctx context.Context, deps operationExecutorDeps, manifest OperationManifest, req OperationRequest) OperationExecutionResult
}

type operationExecutorBindingFunc func(context.Context, operationExecutorDeps, OperationManifest, OperationRequest) OperationExecutionResult

func (fn operationExecutorBindingFunc) Execute(ctx context.Context, deps operationExecutorDeps, manifest OperationManifest, req OperationRequest) OperationExecutionResult {
	return fn(ctx, deps, manifest, req)
}

func lookupOperationExecutorBinding(name string) (operationExecutorBinding, bool) {
	binding, ok := operationExecutorBindings()[name]
	return binding, ok
}

func operationExecutorBindings() map[string]operationExecutorBinding {
	return map[string]operationExecutorBinding{
		ExecutorBindingSystemDescribeCapability: operationExecutorBindingFunc(executeCapabilityOperation),

		ExecutorBindingAttendanceQueryStatus:        operationExecutorBindingFunc(executeAttendanceQueryOperation),
		ExecutorBindingAttendanceDescribeCapability: operationExecutorBindingFunc(executeCapabilityOperation),
		ExecutorBindingAttendanceRuleExplain:        operationExecutorBindingFunc(executeRuleExplainOperation),

		ExecutorBindingScheduleQueryMySchedule:    operationExecutorBindingFunc(executeMyScheduleOperation),
		ExecutorBindingScheduleQueryUserSchedule:  operationExecutorBindingFunc(executeUserScheduleOperation),
		ExecutorBindingScheduleDescribeCapability: operationExecutorBindingFunc(executeCapabilityOperation),
		ExecutorBindingScheduleRuleExplain:        operationExecutorBindingFunc(executeRuleExplainOperation),

		ExecutorBindingSubscriptionStart:              operationExecutorBindingFunc(executeSubscriptionStartOperation),
		ExecutorBindingSubscriptionCancel:             operationExecutorBindingFunc(executeSubscriptionCancelOperation),
		ExecutorBindingSubscriptionQueryStatus:        operationExecutorBindingFunc(executeSubscriptionStatusOperation),
		ExecutorBindingSubscriptionListDepartments:    operationExecutorBindingFunc(executeListDepartmentsOperation),
		ExecutorBindingSubscriptionDescribeCapability: operationExecutorBindingFunc(executeCapabilityOperation),
		ExecutorBindingSubscriptionRuleExplain:        operationExecutorBindingFunc(executeRuleExplainOperation),

		ExecutorBindingManualSignDescribeCapability: operationExecutorBindingFunc(executeCapabilityOperation),
	}
}

func executeCapabilityOperation(_ context.Context, _ operationExecutorDeps, manifest OperationManifest, req OperationRequest) OperationExecutionResult {
	return operationExecutionResult(ResponseModel{
		Kind: ResponseAnswer,
		Payload: CapabilityAnswerPayload{
			Domain:           manifest.Domain,
			UserRole:         operationRequestActorRole(req),
			ConversationType: operationRequestConversationType(req),
		},
	}, answerModeToolFirst)
}

func executeAttendanceQueryOperation(ctx context.Context, deps operationExecutorDeps, _ OperationManifest, req OperationRequest) OperationExecutionResult {
	return operationExecutor{deps: deps}.executeAttendanceQuery(ctx, req)
}

func executeRuleExplainOperation(ctx context.Context, deps operationExecutorDeps, _ OperationManifest, req OperationRequest) OperationExecutionResult {
	return operationExecutor{deps: deps}.executeRuleExplain(ctx, req)
}

func executeMyScheduleOperation(ctx context.Context, deps operationExecutorDeps, _ OperationManifest, req OperationRequest) OperationExecutionResult {
	return operationExecutor{deps: deps}.executeMyScheduleQuery(ctx, req)
}

func executeUserScheduleOperation(ctx context.Context, deps operationExecutorDeps, _ OperationManifest, req OperationRequest) OperationExecutionResult {
	return operationExecutor{deps: deps}.executeUserScheduleQuery(ctx, req)
}

func executeSubscriptionStartOperation(ctx context.Context, deps operationExecutorDeps, _ OperationManifest, req OperationRequest) OperationExecutionResult {
	return operationExecutor{deps: deps}.executeSubscriptionStart(ctx, req)
}

func executeSubscriptionCancelOperation(ctx context.Context, deps operationExecutorDeps, _ OperationManifest, req OperationRequest) OperationExecutionResult {
	return operationExecutor{deps: deps}.executeSubscriptionCancel(ctx, req)
}

func executeSubscriptionStatusOperation(ctx context.Context, deps operationExecutorDeps, _ OperationManifest, req OperationRequest) OperationExecutionResult {
	return operationExecutor{deps: deps}.executeSubscriptionStatus(ctx, req)
}

func executeListDepartmentsOperation(ctx context.Context, deps operationExecutorDeps, _ OperationManifest, _ OperationRequest) OperationExecutionResult {
	return operationExecutor{deps: deps}.executeListDepartments(ctx)
}

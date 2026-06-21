package agent

import "context"

type protocolLiveDispatchBinding interface {
	Handle(ctx context.Context, p protocolLivePipeline, input protocolLiveInput, draft ProtocolDraft, manifest OperationManifest, activeWorkflow *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome
}

type protocolLiveDispatchBindingFunc func(context.Context, protocolLivePipeline, protocolLiveInput, ProtocolDraft, OperationManifest, *WorkflowSnapshot, protocolLiveOutcome) protocolLiveOutcome

func (fn protocolLiveDispatchBindingFunc) Handle(ctx context.Context, p protocolLivePipeline, input protocolLiveInput, draft ProtocolDraft, manifest OperationManifest, activeWorkflow *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome {
	return fn(ctx, p, input, draft, manifest, activeWorkflow, outcome)
}

func lookupProtocolLiveDispatch(name string) (protocolLiveDispatchBinding, bool) {
	binding, ok := protocolLiveDispatchBindings()[name]
	return binding, ok
}

func protocolLiveDispatchBindings() map[string]protocolLiveDispatchBinding {
	return map[string]protocolLiveDispatchBinding{
		ProtocolLiveDispatchAttendance:           protocolLiveDispatchBindingFunc(dispatchAttendanceOperation),
		ProtocolLiveDispatchSubscriptionWorkflow: protocolLiveDispatchBindingFunc(dispatchSubscriptionWorkflowOperation),
		ProtocolLiveDispatchRuntimeConversation:  protocolLiveDispatchBindingFunc(dispatchRuntimeConversationOperation),
		ProtocolLiveDispatchCapability:           protocolLiveDispatchBindingFunc(dispatchCapabilityOperation),
		ProtocolLiveDispatchRuleExplain:          protocolLiveDispatchBindingFunc(dispatchRuleExplainOperation),
		ProtocolLiveDispatchSchedule:             protocolLiveDispatchBindingFunc(dispatchScheduleOperation),
	}
}

func dispatchCapabilityOperation(ctx context.Context, p protocolLivePipeline, input protocolLiveInput, _ ProtocolDraft, manifest OperationManifest, _ *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome {
	return p.execute(ctx, input.User, OperationRequest{Operation: manifest.Name}, outcome)
}

func dispatchSubscriptionWorkflowOperation(ctx context.Context, p protocolLivePipeline, input protocolLiveInput, draft ProtocolDraft, _ OperationManifest, activeWorkflow *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome {
	return p.handleSubscription(ctx, input, draft, activeWorkflow, outcome)
}

func dispatchRuntimeConversationOperation(ctx context.Context, p protocolLivePipeline, input protocolLiveInput, _ ProtocolDraft, manifest OperationManifest, _ *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome {
	req := OperationRequest{
		Operation:      manifest.Name,
		TenantID:       userTenantID(input.User),
		ActorUserID:    userActorUserID(input.User),
		ConversationID: userConversationID(input.User),
		TrustedParams: trustedParamsFromValues(userTenantID(input.User), TrustedParamSource{
			Kind:     TrustedParamSourceRuntime,
			Resolver: "conversation_runtime",
		}, map[string]any{"conversation_id": userConversationID(input.User)}),
	}
	return p.execute(ctx, input.User, req, outcome)
}

func dispatchAttendanceOperation(ctx context.Context, p protocolLivePipeline, input protocolLiveInput, draft ProtocolDraft, _ OperationManifest, _ *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome {
	req, response, ok := p.attendanceRequest(ctx, input.Message, draft, userTenantID(input.User))
	if !ok {
		setProtocolOutcomeResponse(&outcome, response, answerModeToolFirst)
		return outcome
	}
	return p.execute(ctx, input.User, req, outcome)
}

func dispatchScheduleOperation(ctx context.Context, p protocolLivePipeline, input protocolLiveInput, draft ProtocolDraft, _ OperationManifest, _ *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome {
	req, response, ok := p.scheduleRequest(ctx, input.Message, draft, userTenantID(input.User))
	if !ok {
		setProtocolOutcomeResponse(&outcome, response, answerModeToolFirst)
		return outcome
	}
	return p.execute(ctx, input.User, req, outcome)
}

func dispatchRuleExplainOperation(ctx context.Context, p protocolLivePipeline, input protocolLiveInput, draft ProtocolDraft, _ OperationManifest, _ *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome {
	req, blocked := buildOperationRequest(draft, trustedEntities{
		UserRole: inputUserRole(input.User),
		TenantID: userTenantID(input.User),
		TrustedParams: trustedParamsFromValues(userTenantID(input.User), TrustedParamSource{
			Kind:     TrustedParamSourceRawSlot,
			Raw:      protocolRuleTopic(input.Message, draft),
			Resolver: "rule_topic_slot",
		}, map[string]any{"rule_topic": protocolRuleTopic(input.Message, draft)}),
	})
	if blocked {
		setProtocolOutcomeResponse(&outcome, missingOperationParamsResponse(draft.Operation, []string{"rule_topic"}), answerModeToolFirst)
		return outcome
	}
	return p.execute(ctx, input.User, req, outcome)
}

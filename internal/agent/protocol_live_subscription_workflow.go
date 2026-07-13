package agent

import (
	"context"
	"strconv"
	"strings"

	"schedule_server/internal/agent/tools"
)

func (p protocolLivePipeline) handleSubscription(ctx context.Context, input protocolLiveInput, draft ProtocolDraft, activeWorkflow *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome { //nolint:gocyclo,funlen // Subscription workflow orchestration is kept together so state transitions remain auditable.
	startOperation := workflowPrimaryOperationName(WorkflowSubscriptionStart)
	listDepartmentsOperation := workflowAuxiliaryOperationName(WorkflowSubscriptionStart, ExecutorBindingSubscriptionListDepartments)
	if draft.Operation == listDepartmentsOperation {
		if activeWorkflow != nil {
			if activeWorkflow.Type == WorkflowSubscriptionStart && activeWorkflow.State == WorkflowCollectScope {
				continueDraft := draft
				continueDraft.Operation = startOperation
				return p.continueSubscription(ctx, input, continueDraft, activeWorkflow, trustedEntities{Scope: "department"}, outcome)
			}
			outcome.WorkflowAfter = cloneWorkflowSnapshot(activeWorkflow)
			outcome.WorkflowDecision = WorkflowMetaResult
		}
		executed := p.execute(ctx, input.User, OperationRequest{Operation: listDepartmentsOperation}, outcome)
		persistWorkflowCandidatesFromResponse(executed.WorkflowAfter, "dept_ids", executed.Response.Options, userTenantID(input.User))
		return executed
	}

	if input.User == nil || input.User.ConversationType != "2" {
		setProtocolOutcomeResponse(&outcome, ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅只能在群聊中使用。请在对应群聊里再告诉我。"}, answerModeReject)
		outcome.BlockedReason = "group_chat_required"
		return outcome
	}

	if activeWorkflow == nil {
		if draft.Act != ActWriteRequest || draft.Operation != startOperation {
			response, mode := protocolLiveGuardrailResponse(draft, outcome.Validation, input.User)
			setProtocolOutcomeResponse(&outcome, response, mode)
			return outcome
		}
		workflow, ok := startWorkflow(draft)
		if !ok {
			response, mode := protocolLiveGuardrailResponse(draft, outcome.Validation, input.User)
			setProtocolOutcomeResponse(&outcome, response, mode)
			return outcome
		}
		if trusted, ok := p.resolveInitialSubscriptionTrustedEntities(ctx, input.Message, draft, userTenantID(input.User)); ok {
			continueDraft := draft
			continueDraft.Act = ActWorkflowContinue
			return p.continueSubscription(ctx, input, continueDraft, &workflow, trusted, outcome)
		}
		outcome.WorkflowDecision = WorkflowStartNew
		outcome.WorkflowAfter = &workflow
		setProtocolOutcomeResponse(&outcome, ResponseModel{Kind: ResponseClarify, ClarifyReason: "subscription_missing_fields"}, answerModeToolFirst)
		outcome.BlockedReason = "missing_scope"
		return outcome
	}

	if activeWorkflow.Type != WorkflowSubscriptionStart {
		response, mode := protocolLiveGuardrailResponse(draft, outcome.Validation, input.User)
		setProtocolOutcomeResponse(&outcome, response, mode)
		return outcome
	}

	if draft.Act != ActWorkflowContinue {
		response, mode := protocolLiveGuardrailResponse(draft, outcome.Validation, input.User)
		setProtocolOutcomeResponse(&outcome, response, mode)
		return outcome
	}

	trusted, resolved, ok := p.resolveSubscriptionTrustedEntities(ctx, input.Message, activeWorkflow, userTenantID(input.User))
	if !ok {
		outcome.WorkflowAfter = cloneWorkflowSnapshot(activeWorkflow)
		if resolved.Status == ResolveAmbiguous {
			persistWorkflowCandidatesFromEntityCandidates(outcome.WorkflowAfter, "dept_ids", resolved.Candidates)
			setProtocolOutcomeResponse(&outcome, ResponseModel{Kind: ResponseSelectOptions, Options: responseOptionsFromEntityCandidates(resolved.Candidates)}, answerModeToolFirst)
			return outcome
		}
		setProtocolOutcomeResponse(&outcome, ResponseModel{
			Kind:          ResponseClarify,
			Operation:     startOperation,
			ClarifyReason: "subscription_missing_fields",
			MissingFields: workflowMissingFields(activeWorkflow),
		}, answerModeToolFirst)
		if len(activeWorkflow.MissingSlots) > 0 {
			outcome.BlockedReason = "missing_" + activeWorkflow.MissingSlots[0]
		}
		return outcome
	}

	return p.continueSubscription(ctx, input, draft, activeWorkflow, trusted, outcome)
}

func (p protocolLivePipeline) continueSubscription(ctx context.Context, input protocolLiveInput, draft ProtocolDraft, activeWorkflow *WorkflowSnapshot, trusted trustedEntities, outcome protocolLiveOutcome) protocolLiveOutcome {
	result := continueWorkflow(*activeWorkflow, draft, trusted)
	outcome.WorkflowDecision = result.Decision
	switch result.Decision {
	case WorkflowContinueDecision:
		if result.Workflow == nil {
			response, mode := protocolLiveGuardrailResponse(draft, outcome.Validation, input.User)
			setProtocolOutcomeResponse(&outcome, response, mode)
			return outcome
		}
		outcome.WorkflowAfter = result.Workflow
		outcome.ResolvedSlots = protocolResolvedSlotsFromTrusted(result.Workflow.Trusted)
		executed := p.execute(ctx, input.User, OperationRequest{
			Operation: workflowAuxiliaryOperationName(WorkflowSubscriptionStart, ExecutorBindingSubscriptionListDepartments),
		}, outcome)
		persistWorkflowCandidatesFromResponse(executed.WorkflowAfter, "dept_ids", executed.Response.Options, userTenantID(input.User))
		return executed
	case WorkflowReadyToExecute:
		if result.Workflow == nil {
			response, mode := protocolLiveGuardrailResponse(draft, outcome.Validation, input.User)
			setProtocolOutcomeResponse(&outcome, response, mode)
			return outcome
		}
		deptIDs := subscriptionDeptIDsFromTrusted(result.Workflow.Trusted)
		outcome.WorkflowAfter = result.Workflow
		executed := p.execute(ctx, input.User, OperationRequest{
			Operation:      workflowPrimaryOperationName(WorkflowSubscriptionStart),
			TenantID:       userTenantID(input.User),
			ActorUserID:    userActorUserID(input.User),
			ConversationID: userConversationID(input.User),
			TrustedParams:  subscriptionStartTrustedParams(input.User, result.Workflow.Trusted, deptIDs),
		}, outcome)
		if executed.Response.Kind == ResponseResult {
			completed := completeWorkflow(*result.Workflow)
			executed.WorkflowDecision = completed.Decision
			executed.ClearWorkflow = true
			executed.WorkflowAfter = nil
		} else {
			executed.WorkflowAfter = result.Workflow
		}
		return executed
	default:
		outcome.WorkflowAfter = cloneWorkflowSnapshot(activeWorkflow)
		setProtocolOutcomeResponse(&outcome, ResponseModel{Kind: ResponseClarify, ClarifyReason: "subscription_invalid_shape"}, answerModeToolFirst)
		return outcome
	}
}

func subscriptionStartTrustedParams(uctx *tools.UserContext, trusted trustedEntities, deptIDs []int64) map[string]TrustedParam {
	tenantID := userTenantID(uctx)
	params := trustedParamsFromValues(tenantID, TrustedParamSource{Kind: TrustedParamSourceRuntime, Resolver: "conversation_runtime"}, map[string]any{
		"conversation_id": userConversationID(uctx),
	})
	for field, param := range trusted.TrustedParams {
		if param.TenantID == 0 {
			param.TenantID = tenantID
		}
		params[field] = param
	}
	if _, ok := params["scope"]; !ok && trusted.Scope != "" {
		params["scope"] = trustedParam("scope", trusted.Scope, tenantID, TrustedParamSource{
			Kind:     TrustedParamSourceWorkflow,
			Resolver: "subscription_workflow",
		})
	}
	if _, ok := params["dept_ids"]; !ok && len(deptIDs) > 0 {
		params["dept_ids"] = trustedParam("dept_ids", deptIDs, tenantID, TrustedParamSource{
			Kind:     TrustedParamSourceWorkflow,
			Resolver: "subscription_workflow",
		})
	}
	return params
}

func (p protocolLivePipeline) resolveSubscriptionTrustedEntities(ctx context.Context, message string, workflow *WorkflowSnapshot, tenantID uint) (trustedEntities, ResolveResult, bool) {
	if workflow == nil {
		return trustedEntities{}, ResolveResult{}, false
	}
	switch workflow.State {
	case WorkflowCollectScope:
		normalized := normalizeQuery(message)
		switch {
		case containsAny(normalized, []string{"全部人员", "全部"}):
			return trustedEntities{TenantID: tenantID, Scope: "all"}, ResolveResult{}, true
		case containsAny(normalized, []string{"指定部门", "部分部门"}):
			return trustedEntities{TenantID: tenantID, Scope: "department"}, ResolveResult{}, true
		default:
			return p.resolveSubscriptionDepartmentSelection(ctx, message, tenantID)
		}
	case WorkflowCollectDepartments:
		normalized := normalizeQuery(message)
		if normalized == "全部人员" || normalized == "全部" {
			return trustedEntities{TenantID: tenantID, Scope: "all"}, ResolveResult{}, true
		}
		if trusted, handled, ok := workflowDepartmentCandidateSelection(workflow, message, tenantID); handled {
			trusted.TenantID = tenantID
			return trusted, ResolveResult{}, ok
		}
		return p.resolveSubscriptionDepartmentSelection(ctx, message, tenantID)
	default:
		return trustedEntities{}, ResolveResult{}, false
	}
}

func persistWorkflowCandidatesFromResponse(workflow *WorkflowSnapshot, field string, options []ResponseOption, tenantID uint) {
	if workflow == nil || len(options) == 0 {
		return
	}
	candidates := make([]Candidate, 0, len(options))
	for _, option := range options {
		id := strings.TrimSpace(option.Value)
		label := strings.TrimSpace(option.Label)
		if id == "" && label == "" {
			continue
		}
		candidates = append(candidates, Candidate{
			ID:       id,
			Label:    firstNonEmpty(label, id),
			Value:    id,
			TenantID: tenantID,
		})
	}
	if len(candidates) == 0 {
		return
	}
	if workflow.Candidates == nil {
		workflow.Candidates = make(map[string][]Candidate)
	}
	workflow.Candidates[field] = candidates
}

func persistWorkflowCandidatesFromEntityCandidates(workflow *WorkflowSnapshot, field string, options []EntityCandidate) {
	if workflow == nil || len(options) == 0 {
		return
	}
	candidates := make([]Candidate, 0, len(options))
	for _, option := range options {
		id := strings.TrimSpace(option.ID)
		label := strings.TrimSpace(option.Label)
		if id == "" && label == "" {
			continue
		}
		candidates = append(candidates, Candidate{
			ID:       id,
			Label:    firstNonEmpty(label, id),
			Value:    option.Value,
			TenantID: option.TenantID,
		})
	}
	if len(candidates) == 0 {
		return
	}
	if workflow.Candidates == nil {
		workflow.Candidates = make(map[string][]Candidate)
	}
	workflow.Candidates[field] = candidates
}

func workflowDepartmentCandidateSelection(workflow *WorkflowSnapshot, message string, tenantID uint) (trustedEntities, bool, bool) {
	if workflow == nil {
		return trustedEntities{}, false, false
	}
	selection := resolveCandidateSelection(CandidateSelectionInput{
		Field:      "dept_ids",
		Message:    message,
		TenantID:   tenantID,
		Candidates: workflow.Candidates["dept_ids"],
	})
	if !selection.Handled {
		return trustedEntities{}, false, false
	}
	if !selection.OK {
		return trustedEntities{}, true, false
	}
	return trustedEntitiesFromDepartmentCandidate(selection.Candidate, message, tenantID)
}

func trustedEntitiesFromDepartmentCandidate(candidate Candidate, message string, tenantID uint) (trustedEntities, bool, bool) {
	if tenantID != 0 && candidate.TenantID != tenantID {
		return trustedEntities{}, true, false
	}
	deptID, ok := candidateInt64(candidate)
	if !ok || deptID == 0 {
		return trustedEntities{}, true, false
	}
	return trustedEntities{
		Scope:        "department",
		DepartmentID: deptID,
		DeptIDs:      []int64{deptID},
		TrustedParams: map[string]TrustedParam{
			"scope": trustedParam("scope", "department", candidate.TenantID, TrustedParamSource{
				Kind:     TrustedParamSourceCandidate,
				Raw:      message,
				Resolver: "workflow_candidate",
			}),
			"dept_ids": trustedParam("dept_ids", []int64{deptID}, candidate.TenantID, TrustedParamSource{
				Kind:     TrustedParamSourceCandidate,
				Raw:      message,
				Resolver: "workflow_candidate",
			}),
		},
	}, true, true
}

func parseCandidateOrdinal(message string) (int, bool) {
	normalized := normalizeQuery(message)
	normalized = strings.TrimPrefix(normalized, "选择")
	normalized = strings.TrimPrefix(normalized, "选")
	normalized = strings.TrimPrefix(normalized, "第")
	normalized = strings.TrimSuffix(normalized, "个")
	normalized = strings.TrimSuffix(normalized, "项")
	normalized = strings.TrimSuffix(normalized, "号")
	if normalized == "" {
		return 0, false
	}
	if value, err := strconv.Atoi(normalized); err == nil && value > 0 {
		return value, true
	}
	if value, ok := parseChinesePositiveInt(normalized); ok && value > 0 {
		return value, true
	}
	return 0, false
}

func candidateInt64(candidate Candidate) (int64, bool) {
	if id := strings.TrimSpace(candidate.ID); id != "" {
		if parsed, err := strconv.ParseInt(id, 10, 64); err == nil {
			return parsed, true
		}
	}
	switch value := candidate.Value.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case uint:
		return int64(value), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (p protocolLivePipeline) resolveSubscriptionDepartmentSelection(ctx context.Context, message string, tenantID uint) (trustedEntities, ResolveResult, bool) {
	if p.deps.Dept == nil {
		return trustedEntities{}, ResolveResult{}, false
	}
	depts, err := p.deps.Dept.ListDepts(ctx)
	if err != nil {
		return trustedEntities{}, ResolveResult{}, false
	}
	resolved := resolveDepartmentParam(EntityResolveContext{TenantID: tenantID}.RawSlot("dept_ids", message), depts)
	if resolved.Status != ResolveResolved {
		return trustedEntities{}, resolved, false
	}
	deptIDs, ok := resolved.Value.([]int64)
	if !ok || len(deptIDs) == 0 {
		return trustedEntities{}, resolved, false
	}
	return trustedEntities{
		TenantID:     tenantID,
		Scope:        "department",
		DepartmentID: deptIDs[0],
		DeptIDs:      deptIDs,
		TrustedParams: map[string]TrustedParam{
			"dept_ids": resolved.Param,
		},
	}, resolved, true
}

func (p protocolLivePipeline) resolveInitialSubscriptionTrustedEntities(ctx context.Context, message string, draft ProtocolDraft, tenantID uint) (trustedEntities, bool) {
	scope := normalizeSubscriptionScope(firstNonEmpty(draftSlotRaw(draft, "scope"), message))
	if scope == "" {
		return trustedEntities{}, false
	}
	trusted := trustedEntities{TenantID: tenantID, Scope: scope}
	if scope == "all" {
		return trusted, true
	}

	deptName := firstNonEmpty(
		draftSlotRaw(draft, "dept_names"),
		draftSlotRaw(draft, "dept_name"),
		draftSlotRaw(draft, "department"),
		draftSlotRaw(draft, "department_name"),
	)
	if deptName == "" || p.deps.Dept == nil {
		return trusted, true
	}
	depts, err := p.deps.Dept.ListDepts(ctx)
	if err != nil {
		return trusted, true
	}
	resolved := resolveDepartmentParam(EntityResolveContext{TenantID: tenantID}.RawSlot("dept_ids", deptName), depts)
	if resolved.Status != ResolveResolved {
		return trusted, true
	}
	deptIDs, ok := resolved.Value.([]int64)
	if !ok || len(deptIDs) == 0 {
		return trusted, true
	}
	trusted.DepartmentID = deptIDs[0]
	trusted.DeptIDs = deptIDs
	trusted.TrustedParams = map[string]TrustedParam{"dept_ids": resolved.Param}
	return trusted, true
}

func normalizeSubscriptionScope(raw string) string {
	normalized := normalizeQuery(raw)
	switch {
	case containsAny(normalized, []string{"全部人员", "全部"}):
		return "all"
	case containsAny(normalized, []string{"指定部门", "部分部门", "部门"}):
		return "department"
	default:
		return ""
	}
}

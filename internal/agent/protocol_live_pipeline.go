package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"schedule_server/internal/agent/tools"
)

type protocolLivePipelineDeps struct {
	Compiler       IntentCompiler
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
}

var protocolLiveRequestSeq uint64

func newProtocolLiveRequestID(now time.Time) string {
	seq := atomic.AddUint64(&protocolLiveRequestSeq, 1)
	if now.IsZero() {
		now = time.Now()
	}
	return fmt.Sprintf("plive-%d-%d", now.UnixNano(), seq)
}

func finalizeProtocolLiveOutcome(outcome *protocolLiveOutcome) {
	if outcome == nil {
		return
	}
	if outcome.RequestID == "" {
		outcome.RequestID = newProtocolLiveRequestID(time.Now())
	}
	if outcome.RendererName == "" {
		outcome.RendererName = "response_renderer"
	}
	if outcome.CatalogValidationCode == "" {
		outcome.CatalogValidationCode = outcome.Validation.ValidationCode
	}
	if outcome.PrePolicyResult == "" {
		outcome.PrePolicyResult = protocolPrePolicyResult(outcome.Validation)
	}
	if outcome.BlockedReason == "" {
		outcome.BlockedReason = protocolResponseBlockedReason(outcome.Response, outcome.Validation)
	}
	if outcome.EntityResolutionStatus == "" {
		outcome.EntityResolutionStatus = protocolEntityResolutionStatus(*outcome)
	}
	if outcome.WriteGuardResult == "" {
		outcome.WriteGuardResult = protocolWriteGuardResult(*outcome)
	}
	if outcome.ExecutorStatus == "" {
		outcome.ExecutorStatus = protocolExecutorStatus(outcome.Response)
	}
	if outcome.FailureLayer == "" {
		outcome.FailureLayer = inferFailureLayer(*outcome)
	}
}

func compactIntentDraft(draft ProtocolDraft) string {
	data, err := json.Marshal(draft)
	if err != nil {
		return ""
	}
	return string(data)
}

func protocolPrePolicyResult(validation ProtocolValidationResult) string {
	if validation.AllowExecution || validation.ResponseKind == ResponseAnswer || validation.UseActiveWorkflow {
		return "allow"
	}
	if strings.TrimSpace(validation.ValidationCode) == "" {
		return ""
	}
	return "deny:" + validation.ValidationCode
}

func protocolEntityResolutionStatus(outcome protocolLiveOutcome) string {
	if len(outcome.ResolvedSlots) > 0 {
		return string(ResolveResolved)
	}
	if outcome.Response.Kind == ResponseSelectOptions || outcome.CandidateCount > 0 {
		return string(ResolveAmbiguous)
	}
	if strings.HasPrefix(outcome.BlockedReason, "missing_") {
		return string(ResolveNotFound)
	}
	return ""
}

func protocolWriteGuardResult(outcome protocolLiveOutcome) string {
	manifest, ok := lookupOperation(outcome.Draft.Operation)
	if !ok {
		return ""
	}
	if !manifest.IsWrite {
		return "not_required"
	}
	if strings.TrimSpace(outcome.IdempotencyKey) != "" && outcome.FailureLayer != FailureWriteGuardBlocked {
		return "allow"
	}
	if outcome.FailureLayer == FailureWriteGuardBlocked {
		return "block:" + outcome.BlockedReason
	}
	return ""
}

func protocolExecutorStatus(response ResponseModel) string {
	switch response.Kind {
	case ResponseResult, ResponseAnswer, ResponseSelectOptions:
		return "success"
	case ResponseRefuse:
		return "failed"
	case ResponseClarify, ResponseConfirm:
		return "skipped"
	default:
		return ""
	}
}

func inferFailureLayer(outcome protocolLiveOutcome) FailureLayer {
	if outcome.Response.Kind == ResponseResult || outcome.Response.Kind == ResponseAnswer {
		return ""
	}
	reason := firstNonEmpty(outcome.BlockedReason, outcome.Validation.ValidationCode)
	switch reason {
	case "", "missing_scope", "subscription_missing_fields":
		return ""
	case "empty_message", "unknown_intent", "intent_parse_failed", "intent_timeout", "intent_compiler_unavailable":
		return FailureIntent
	case "operation_not_allowed", "act_operation_mismatch", "read_query_cannot_write", "write_request_cannot_read", "unsupported_act", "domain_operation_mismatch":
		return FailureCatalog
	case "workflow_missing", "workflow_operation_mismatch", "workflow_store_failed", "subscription_invalid_shape":
		return FailureWorkflow
	case "role_denied", "conversation_scope_denied":
		return FailurePrePolicyDenied
	case "subscription_conversation_mismatch", "schedule_user_visibility_denied", "department_scope_denied", "department_scope_unverified", "group_chat_required", "subscription_scope_invalid":
		return FailureResourcePolicyDenied
	case "write_confirmation_required", "idempotency_key_missing":
		return FailureWriteGuardBlocked
	default:
		if strings.Contains(reason, "ambiguous") {
			return FailureEntityAmbiguous
		}
		if strings.Contains(reason, "not_found") || strings.HasPrefix(reason, "missing_user") || strings.HasPrefix(reason, "missing_dept") {
			return FailureEntityNotFound
		}
	}
	if outcome.Response.Kind == ResponseRefuse {
		return FailureExecutor
	}
	return ""
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

func (p protocolLivePipeline) Handle(ctx context.Context, input protocolLiveInput) (outcome protocolLiveOutcome) {
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
	draft, err := compileProtocolWithCompiler(ctx, protocolInput{
		Message:        input.Message,
		ActiveWorkflow: workflowCtx,
	}, p.deps.Compiler)
	outcome.CompilerLatencyMs = elapsedMs(compileStart)
	outcome.CompilerStatus = "ok"
	if err != nil {
		reason := "intent_parse_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "intent_timeout"
			outcome.CompilerStatus = "timeout"
		} else {
			outcome.CompilerStatus = "error"
		}
		draft = unknownIntentDraft(reason)
	}
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

	if manifest, ok := lookupOperation(draft.Operation); ok && manifest.Capability != nil {
		return p.execute(ctx, input.User, OperationRequest{Operation: draft.Operation}, outcome)
	}

	switch draft.Operation {
	case "subscription.start", "subscription.list_departments":
		return p.handleSubscription(ctx, input, draft, activeWorkflow, outcome)
	case "subscription.query_status", "subscription.cancel":
		req := OperationRequest{
			Operation:      draft.Operation,
			TenantID:       userTenantID(input.User),
			ActorUserID:    userActorUserID(input.User),
			ConversationID: userConversationID(input.User),
			TrustedParams: trustedParamsFromValues(userTenantID(input.User), TrustedParamSource{
				Kind:     TrustedParamSourceRuntime,
				Resolver: "conversation_runtime",
			}, map[string]any{"conversation_id": userConversationID(input.User)}),
		}
		return p.execute(ctx, input.User, req, outcome)
	case "attendance.query_status":
		req, response, ok := p.attendanceRequest(ctx, input.Message, draft, userTenantID(input.User))
		if !ok {
			setProtocolOutcomeResponse(&outcome, response, answerModeToolFirst)
			return outcome
		}
		return p.execute(ctx, input.User, req, outcome)
	case "schedule.query_my_schedule", "schedule.query_user_schedule":
		req, response, ok := p.scheduleRequest(ctx, input.Message, draft, userTenantID(input.User))
		if !ok {
			setProtocolOutcomeResponse(&outcome, response, answerModeToolFirst)
			return outcome
		}
		return p.execute(ctx, input.User, req, outcome)
	case "attendance.rule_explain", "schedule.rule_explain", "subscription.rule_explain":
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
	default:
		response, mode := protocolLiveGuardrailResponse(draft, validation, input.User)
		setProtocolOutcomeResponse(&outcome, response, mode)
		return outcome
	}
}

func (p protocolLivePipeline) handleSubscription(ctx context.Context, input protocolLiveInput, draft ProtocolDraft, activeWorkflow *WorkflowSnapshot, outcome protocolLiveOutcome) protocolLiveOutcome {
	if draft.Operation == "subscription.list_departments" {
		if activeWorkflow != nil {
			if activeWorkflow.Type == WorkflowSubscriptionStart && activeWorkflow.State == WorkflowCollectScope {
				continueDraft := draft
				continueDraft.Operation = "subscription.start"
				return p.continueSubscription(ctx, input, continueDraft, activeWorkflow, trustedEntities{Scope: "department"}, outcome)
			}
			outcome.WorkflowAfter = cloneWorkflowSnapshot(activeWorkflow)
			outcome.WorkflowDecision = WorkflowMetaResult
		}
		executed := p.execute(ctx, input.User, OperationRequest{Operation: "subscription.list_departments"}, outcome)
		persistWorkflowCandidatesFromResponse(executed.WorkflowAfter, "dept_ids", executed.Response.Options, userTenantID(input.User))
		return executed
	}

	if input.User == nil || input.User.ConversationType != "2" {
		setProtocolOutcomeResponse(&outcome, ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅只能在群聊中使用。请在对应群聊里再告诉我。"}, answerModeReject)
		outcome.BlockedReason = "group_chat_required"
		return outcome
	}

	if activeWorkflow == nil {
		if draft.Act != ActWriteRequest || draft.Operation != "subscription.start" {
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
		setProtocolOutcomeResponse(&outcome, ResponseModel{Kind: ResponseClarify, ClarifyReason: "subscription_missing_fields"}, answerModeToolFirst)
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
		executed := p.execute(ctx, input.User, OperationRequest{Operation: "subscription.list_departments"}, outcome)
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
			Operation:      "subscription.start",
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

func setProtocolOutcomeResponse(outcome *protocolLiveOutcome, response ResponseModel, mode answerMode) {
	if outcome == nil {
		return
	}
	outcome.Response = response
	outcome.AnswerMode = mode
	if outcome.BlockedReason == "" {
		outcome.BlockedReason = protocolResponseBlockedReason(response, outcome.Validation)
	}
	if outcome.CandidateCount == 0 {
		outcome.CandidateCount = len(response.Options)
	}
}

func protocolResponseBlockedReason(response ResponseModel, validation ProtocolValidationResult) string {
	switch response.Kind {
	case ResponseClarify:
		if len(response.MissingFields) > 0 {
			return "missing_" + response.MissingFields[0]
		}
		if reason := strings.TrimSpace(response.ClarifyReason); reason != "" {
			return reason
		}
		if validation.ValidationCode != "" && !validation.AllowExecution {
			return validation.ValidationCode
		}
	case ResponseRefuse:
		if validation.ValidationCode != "" {
			return validation.ValidationCode
		}
		return "refused"
	}
	return ""
}

func mergeProtocolResolvedSlots(base map[string]any, next map[string]any) map[string]any {
	if len(next) == 0 {
		return base
	}
	merged := make(map[string]any, len(base)+len(next))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range next {
		merged[key] = value
	}
	return merged
}

func protocolResolvedSlotsFromParams(params map[string]TrustedParam) map[string]any {
	if len(params) == 0 {
		return nil
	}
	slots := make(map[string]any)
	for key, param := range params {
		if key == "conversation_id" {
			continue
		}
		switch key {
		case "date", "week", "section", "user_id", "scope", "dept_ids", "rule_topic", "query_shape":
			slots[key] = param.Value
		}
	}
	if len(slots) == 0 {
		return nil
	}
	return slots
}

func protocolResolvedSlotsFromTrusted(trusted trustedEntities) map[string]any {
	slots := make(map[string]any)
	if trusted.Date != "" {
		slots["date"] = trusted.Date
	}
	if trusted.Week != 0 {
		slots["week"] = trusted.Week
	}
	if trusted.Section != 0 {
		slots["section"] = trusted.Section
	}
	if trusted.UserID != 0 {
		slots["user_id"] = trusted.UserID
	}
	if trusted.Scope != "" {
		slots["scope"] = trusted.Scope
	}
	if len(trusted.DeptIDs) > 0 {
		slots["dept_ids"] = append([]int64(nil), trusted.DeptIDs...)
	} else if trusted.DepartmentID != 0 {
		slots["dept_ids"] = []int64{trusted.DepartmentID}
	}
	if trusted.QueryShape != "" {
		slots["query_shape"] = trusted.QueryShape
	}
	if len(slots) == 0 {
		return nil
	}
	return slots
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

func (p protocolLivePipeline) attendanceRequest(ctx context.Context, message string, draft ProtocolDraft, tenantID uint) (OperationRequest, ResponseModel, bool) {
	resolveCtx := EntityResolveContext{TenantID: tenantID}
	trusted := trustedEntities{UserRole: 0, TenantID: tenantID, TrustedParams: map[string]TrustedParam{}}
	if raw := draftSlotRaw(draft, "query_shape"); raw != "" {
		trusted.QueryShape = raw
		trusted.TrustedParams["query_shape"] = trustedParamFromContext(resolveCtx.RawSlot("query_shape", raw), "query_shape", raw, "query_shape_slot")
	}
	now := p.now()
	dateRaw := firstNonEmpty(draftSlotRaw(draft, "date"), extractDateToken(message))
	if dateRaw == "" && hasDateSignal(message) {
		dateRaw = messageDateSignal(message)
	}
	dateInput := resolveCtx.RawSlot("date", dateRaw)
	if dateRaw == "" {
		dateInput = resolveCtx.Default("date")
	}
	date := resolveDateParam(dateInput, SlotDefaultToday, func() time.Time { return now })
	if date.Status == ResolveResolved {
		trusted.Date = fmt.Sprint(date.Value)
		trusted.TrustedParams["date"] = date.Param
	}

	sectionRaw := firstNonEmpty(draftSlotRaw(draft, "section"), extractSectionToken(message))
	section := resolveSectionParam(resolveCtx.RawSlot("section", sectionRaw), p.schedulePeriods(ctx), func() time.Time { return now })
	if section.Status == ResolveResolved {
		if value, ok := section.Value.(int); ok {
			trusted.Section = value
			trusted.TrustedParams["section"] = section.Param
		}
	}

	userRaw := firstNonEmpty(draftSlotRaw(draft, "user"), draftSlotRaw(draft, "user_name"))
	if userRaw != "" && p.deps.User != nil {
		users, err := p.deps.User.SearchByName(ctx, userRaw)
		if err == nil {
			resolved := resolveUserParam(resolveCtx.RawSlot("user_id", userRaw), users)
			switch resolved.Status {
			case ResolveResolved:
				if userID, ok := resolved.Value.(uint); ok {
					trusted.UserID = userID
					trusted.QueryShape = "user_day_status"
					trusted.TrustedParams["user_id"] = resolved.Param
					trusted.TrustedParams["query_shape"] = trustedParam("query_shape", "user_day_status", tenantID, TrustedParamSource{
						Kind:     TrustedParamSourceDerived,
						Resolver: "attendance_user_resolver",
					})
				}
			case ResolveAmbiguous:
				return OperationRequest{}, ResponseModel{Kind: ResponseSelectOptions, Options: responseOptionsFromEntityCandidates(resolved.Candidates)}, false
			}
		}
	}

	req, blocked := buildOperationRequest(draft, trusted)
	if blocked {
		return OperationRequest{}, ResponseModel{Kind: ResponseClarify, ClarifyReason: "missing_attendance_fields"}, false
	}
	if _, ok := req.TrustedParams["week"]; !ok && p.deps.Semester != nil {
		if week, _, err := p.deps.Semester.GetCurrentWeek(ctx); err == nil && week > 0 {
			req.TrustedParams["week"] = trustedParam("week", week, tenantID, TrustedParamSource{
				Kind:     TrustedParamSourceDefault,
				Resolver: "semester_default",
			})
		}
	}
	return req, ResponseModel{}, true
}

func (p protocolLivePipeline) now() time.Time {
	if p.deps.Clock != nil {
		return p.deps.Clock()
	}
	return time.Now()
}

func (p protocolLivePipeline) schedulePeriods(ctx context.Context) []tools.PeriodInfo {
	if p.deps.SchedulePeriod == nil {
		return nil
	}
	periods, _, err := p.deps.SchedulePeriod.GetScheduleInfo(ctx)
	if err != nil {
		return nil
	}
	return periods
}

func (p protocolLivePipeline) scheduleRequest(ctx context.Context, message string, draft ProtocolDraft, tenantID uint) (OperationRequest, ResponseModel, bool) {
	resolveCtx := EntityResolveContext{TenantID: tenantID}
	trusted := trustedEntities{UserRole: 0, TenantID: tenantID, TrustedParams: map[string]TrustedParam{}}
	weekRaw := firstNonEmpty(draftSlotRaw(draft, "week"), extractWeekToken(message))
	weekInput := resolveCtx.RawSlot("week", weekRaw)
	if weekRaw == "" {
		weekInput = resolveCtx.Default("week")
	}
	week := resolveWeekParam(ctx, weekInput, SlotDefaultCurrentWeek, p.deps.Semester)
	if week.Status == ResolveResolved {
		if value, ok := week.Value.(int); ok {
			trusted.Week = value
			trusted.TrustedParams["week"] = week.Param
		}
	}

	if draft.Operation == "schedule.query_user_schedule" {
		userRaw := firstNonEmpty(draftSlotRaw(draft, "user"), draftSlotRaw(draft, "user_name"), extractScheduleUserName(message))
		if userRaw == "" || p.deps.User == nil {
			return OperationRequest{}, missingOperationParamsResponse(draft.Operation, []string{"user_id"}), false
		}
		users, err := p.deps.User.SearchByName(ctx, userRaw)
		if err != nil {
			return OperationRequest{}, operationErrorResponse(), false
		}
		resolved := resolveUserParam(resolveCtx.RawSlot("user_id", userRaw), users)
		switch resolved.Status {
		case ResolveResolved:
			if userID, ok := resolved.Value.(uint); ok {
				trusted.UserID = userID
				trusted.TrustedParams["user_id"] = resolved.Param
			}
		case ResolveAmbiguous:
			return OperationRequest{}, ResponseModel{Kind: ResponseSelectOptions, Options: responseOptionsFromEntityCandidates(resolved.Candidates)}, false
		default:
			return OperationRequest{}, missingOperationParamsResponse(draft.Operation, []string{"user_id"}), false
		}
	}

	req, blocked := buildOperationRequest(draft, trusted)
	if blocked {
		return OperationRequest{}, missingOperationParamsResponse(draft.Operation, []string{"week"}), false
	}
	return req, ResponseModel{}, true
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
	ordinal, ok := parseCandidateOrdinal(message)
	if !ok {
		return trustedEntities{}, false, false
	}
	if workflow == nil || len(workflow.Candidates["dept_ids"]) < ordinal {
		return trustedEntities{}, true, false
	}
	candidate := workflow.Candidates["dept_ids"][ordinal-1]
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
	deptIDs, _ := resolved.Value.([]int64)
	if len(deptIDs) == 0 {
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
	deptIDs, _ := resolved.Value.([]int64)
	if len(deptIDs) == 0 {
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

func protocolLiveRoleRefusal(uctx *tools.UserContext, draft ProtocolDraft) (bool, ResponseModel) {
	if draft.Act == ActWorkflowCancel {
		return false, ResponseModel{}
	}
	metadata, ok := lookupOperation(draft.Operation)
	if !ok || metadata.MinRole <= 0 {
		return false, ResponseModel{}
	}
	if uctx != nil && uctx.UserRole >= metadata.MinRole {
		return false, ResponseModel{}
	}
	return true, ResponseModel{
		Kind:          ResponseRefuse,
		RefusalReason: "该操作需要管理员权限。",
	}
}

func protocolLiveGuardrailResponse(draft ProtocolDraft, validation ProtocolValidationResult, uctx *tools.UserContext) (ResponseModel, answerMode) {
	switch draft.Act {
	case ActCapabilityQuestion:
		return ResponseModel{Kind: ResponseAnswer, Answer: buildProtocolCapabilityReply(draft.Domain, uctx)}, answerModeToolFirst
	case ActHelp:
		return ResponseModel{Kind: ResponseAnswer, Answer: buildHelpReply(uctx)}, answerModeToolFirst
	case ActUnknown:
		return ResponseModel{Kind: ResponseClarify, ClarifyReason: protocolUnknownIntentReasonCode(draft)}, answerModeReject
	default:
		switch validation.ValidationCode {
		case "conversation_scope_denied":
			if draft.Domain == DomainSubscription {
				return ResponseModel{Kind: ResponseRefuse, RefusalReason: "群考勤订阅只能在群聊中使用。请在对应群聊里再告诉我。"}, answerModeReject
			}
			return ResponseModel{Kind: ResponseRefuse, RefusalReason: "抱歉，我当前不能直接执行这个请求。"}, answerModeReject
		case "operation_not_allowed", "act_operation_mismatch", "read_query_cannot_write", "unsupported_act", "domain_operation_mismatch", "workflow_operation_mismatch", "role_denied":
			return ResponseModel{Kind: ResponseRefuse, RefusalReason: "抱歉，我当前不能直接执行这个请求。"}, answerModeReject
		case "low_confidence_write":
			return ResponseModel{Kind: ResponseClarify, ClarifyReason: "unknown_intent"}, answerModeReject
		default:
			return ResponseModel{Kind: ResponseClarify, ClarifyReason: "unknown_intent"}, answerModeReject
		}
	}
}

func protocolUnknownIntentReasonCode(draft ProtocolDraft) string {
	switch strings.TrimSpace(firstNonEmpty(draft.ClarifyReason, draft.Reason)) {
	case "empty_message":
		return "empty_message"
	case "intent_parse_failed":
		return "intent_parse_failed"
	case "intent_timeout":
		return "intent_timeout"
	case "intent_compiler_unavailable":
		return "intent_compiler_unavailable"
	case "operation_not_allowed":
		return "operation_not_allowed"
	default:
		return "unknown_intent"
	}
}

func protocolRuleTopic(message string, draft ProtocolDraft) string {
	if raw := draftSlotRaw(draft, "rule_topic"); raw != "" {
		return raw
	}
	return strings.TrimSpace(message)
}

func draftSlotRaw(draft ProtocolDraft, field string) string {
	if draft.Slots == nil {
		return ""
	}
	slot, ok := draft.Slots[field]
	if !ok {
		return ""
	}
	return strings.TrimSpace(slot.Raw)
}

func inputUserRole(uctx *tools.UserContext) int {
	if uctx == nil {
		return 0
	}
	return uctx.UserRole
}

func userTenantID(uctx *tools.UserContext) uint {
	if uctx == nil {
		return 0
	}
	return uctx.TenantID
}

func userConversationID(uctx *tools.UserContext) string {
	if uctx == nil {
		return ""
	}
	return uctx.ConversationID
}

func userConversationType(uctx *tools.UserContext) string {
	if uctx == nil {
		return ""
	}
	return uctx.ConversationType
}

func userActorUserID(uctx *tools.UserContext) uint {
	if uctx == nil {
		return 0
	}
	return uctx.UserID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func messageDateSignal(message string) string {
	for _, candidate := range []string{"今天", "昨天", "明天"} {
		if strings.Contains(message, candidate) {
			return candidate
		}
	}
	return ""
}

func extractSectionToken(message string) string {
	for i := 1; i <= 12; i++ {
		token := fmt.Sprintf("第%d节", i)
		if strings.Contains(message, token) {
			return token
		}
	}
	for _, token := range []string{"第一节", "第二节", "第三节", "第四节", "第五节", "第六节", "第七节", "第八节", "第九节", "第十节"} {
		if strings.Contains(message, token) {
			return token
		}
	}
	return ""
}

func extractWeekToken(message string) string {
	normalized := normalizeQuery(message)
	for i := 1; i <= 30; i++ {
		token := fmt.Sprintf("第%d周", i)
		if strings.Contains(normalized, token) {
			return token
		}
	}
	if strings.Contains(normalized, "本周") {
		return "本周"
	}
	return ""
}

func extractScheduleUserName(message string) string {
	value := strings.TrimSpace(message)
	value = strings.TrimPrefix(value, "查")
	value = strings.TrimPrefix(value, "查询")
	if idx := strings.Index(value, "第"); idx > 0 {
		value = value[:idx]
	}
	value = strings.ReplaceAll(value, "课表", "")
	value = strings.ReplaceAll(value, "的", "")
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "我") {
		return ""
	}
	return value
}

func responseOptionsFromEntityCandidates(candidates []EntityCandidate) []ResponseOption {
	options := make([]ResponseOption, 0, len(candidates))
	for _, candidate := range candidates {
		options = append(options, ResponseOption{
			Label: candidate.Label,
			Value: candidate.ID,
		})
	}
	return options
}

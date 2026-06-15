package agent

import (
	"context"
	"strings"

	"schedule_server/internal/agent/tools"
)

const lowConfidenceWriteThreshold = 0.75

type ProtocolValidationResult struct {
	AllowExecution          bool
	ValidationCode          string
	UseActiveWorkflow       bool
	InterruptActiveWorkflow bool
	ResponseKind            ResponseKind
}

type CatalogValidator interface {
	Validate(draft ProtocolDraft, activeWorkflow *protocolWorkflowContext) ProtocolValidationResult
}

type PrePolicyGate interface {
	Validate(input PrePolicyGateInput) ProtocolValidationResult
}

type PrePolicyGateInput struct {
	Draft            ProtocolDraft
	ActiveWorkflow   *protocolWorkflowContext
	ConversationType string
	UserRole         int
	HasUserContext   bool
}

type catalogValidator struct{}

type prePolicyGate struct{}

func newCatalogValidator() CatalogValidator {
	return catalogValidator{}
}

func newPrePolicyGate() PrePolicyGate {
	return prePolicyGate{}
}

func validateProtocol(draft ProtocolDraft, activeWorkflow *protocolWorkflowContext) ProtocolValidationResult {
	return newCatalogValidator().Validate(draft, activeWorkflow)
}

// Validate checks the untrusted intent draft against OperationCatalog protocol contracts.
func (catalogValidator) Validate(draft ProtocolDraft, activeWorkflow *protocolWorkflowContext) ProtocolValidationResult {
	return newPrePolicyGate().Validate(PrePolicyGateInput{
		Draft:          draft,
		ActiveWorkflow: activeWorkflow,
	})
}

// Validate checks the untrusted intent draft against pre-resolution protocol policy.
func (prePolicyGate) Validate(input PrePolicyGateInput) ProtocolValidationResult {
	draft := input.Draft
	activeWorkflow := input.ActiveWorkflow
	if draft.Act == ActUnknown {
		return ProtocolValidationResult{ValidationCode: "unknown_intent", ResponseKind: ResponseClarify}
	}

	if draft.Act == ActWorkflowContinue {
		return validateWorkflowContinue(input)
	}

	if draft.Act == ActWorkflowCancel {
		if activeWorkflow == nil {
			return ProtocolValidationResult{ValidationCode: "workflow_missing", ResponseKind: ResponseClarify}
		}
		return ProtocolValidationResult{
			ValidationCode:    "workflow_cancel_non_executable",
			UseActiveWorkflow: true,
			ResponseKind:      ResponseResult,
		}
	}

	metadata, ok := lookupOperation(draft.Operation)
	interrupt := activeWorkflow != nil && policyExplicitNewRequest(draft.Act)
	if !ok {
		if draft.Act == ActWriteRequest && draft.Confidence < lowConfidenceWriteThreshold {
			interrupt = false
		}
		return ProtocolValidationResult{
			ValidationCode:          "operation_not_allowed",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseRefuse,
		}
	}
	responseKind := catalogResponseKind(metadata, ResponseResult)

	if draft.Domain != metadata.Domain {
		return ProtocolValidationResult{
			ValidationCode:          "domain_operation_mismatch",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseRefuse,
		}
	}

	if draft.Act == ActReadQuery && metadata.IsWrite {
		return ProtocolValidationResult{
			ValidationCode:          "read_query_cannot_write",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseRefuse,
		}
	}
	if draft.Act == ActWriteRequest && !metadata.IsWrite {
		return ProtocolValidationResult{
			ValidationCode:          "write_request_cannot_read",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseRefuse,
		}
	}

	if !actAllowed(draft.Act, metadata.AllowedActs) {
		return ProtocolValidationResult{
			ValidationCode:          "act_operation_mismatch",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseRefuse,
		}
	}

	switch draft.Act {
	case ActCapabilityQuestion:
		if denied, result := enforcePreUserPolicy(input, metadata, interrupt); denied {
			return result
		}
		if metadata.Capability == nil {
			return ProtocolValidationResult{
				ValidationCode: "act_operation_mismatch",
				ResponseKind:   ResponseRefuse,
			}
		}
		return ProtocolValidationResult{
			ValidationCode:          "capability_non_executable",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            responseKind,
		}
	case ActRuleQuestion:
		if denied, result := enforcePreUserPolicy(input, metadata, interrupt); denied {
			return result
		}
		return ProtocolValidationResult{
			ValidationCode:          "rule_non_executable",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            responseKind,
		}
	case ActHelp:
		if denied, result := enforcePreUserPolicy(input, metadata, interrupt); denied {
			return result
		}
		if metadata.Capability == nil || metadata.Domain != DomainSystem {
			return ProtocolValidationResult{
				ValidationCode: "act_operation_mismatch",
				ResponseKind:   ResponseRefuse,
			}
		}
		return ProtocolValidationResult{
			ValidationCode:          "help_non_executable",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            responseKind,
		}
	case ActReadQuery:
		if denied, result := enforcePreUserPolicy(input, metadata, interrupt); denied {
			return result
		}
		return ProtocolValidationResult{
			AllowExecution:          true,
			ValidationCode:          "allowed_read_query",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            responseKind,
		}
	case ActWriteRequest:
		if draft.Confidence < lowConfidenceWriteThreshold {
			return ProtocolValidationResult{
				ValidationCode: "low_confidence_write",
				ResponseKind:   ResponseClarify,
			}
		}
		if denied, result := enforcePreUserPolicy(input, metadata, interrupt); denied {
			return result
		}
		return ProtocolValidationResult{
			AllowExecution:          true,
			ValidationCode:          "allowed_write_request",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            responseKind,
		}
	default:
		return ProtocolValidationResult{
			ValidationCode: "unsupported_act",
			ResponseKind:   ResponseRefuse,
		}
	}
}

// validateWorkflowContinue validates active workflow continuation without treating it as a generic write.
func validateWorkflowContinue(input PrePolicyGateInput) ProtocolValidationResult {
	draft := input.Draft
	activeWorkflow := input.ActiveWorkflow
	if activeWorkflow == nil {
		return ProtocolValidationResult{ValidationCode: "workflow_missing", ResponseKind: ResponseClarify}
	}
	if !workflowContinueTargetsActiveWorkflow(draft.Operation, activeWorkflow.Type) {
		return ProtocolValidationResult{
			ValidationCode:    "workflow_operation_mismatch",
			UseActiveWorkflow: true,
			ResponseKind:      ResponseRefuse,
		}
	}

	metadata, ok := lookupOperation(draft.Operation)
	if !ok {
		return ProtocolValidationResult{
			ValidationCode:    "operation_not_allowed",
			UseActiveWorkflow: true,
			ResponseKind:      ResponseRefuse,
		}
	}
	if draft.Domain != metadata.Domain {
		return ProtocolValidationResult{
			ValidationCode:    "domain_operation_mismatch",
			UseActiveWorkflow: true,
			ResponseKind:      ResponseRefuse,
		}
	}
	if !workflowContinueShapeAllowed(draft.Operation, activeWorkflow) {
		return ProtocolValidationResult{
			ValidationCode:    "workflow_operation_mismatch",
			UseActiveWorkflow: true,
			ResponseKind:      ResponseRefuse,
		}
	}
	if denied, result := enforcePreUserPolicy(input, metadata, false); denied {
		result.UseActiveWorkflow = true
		return result
	}
	return ProtocolValidationResult{
		AllowExecution:    true,
		ValidationCode:    "workflow_continue_allowed",
		UseActiveWorkflow: true,
		ResponseKind:      catalogResponseKind(metadata, ResponseResult),
	}
}

func catalogResponseKind(metadata OperationManifest, fallback ResponseKind) ResponseKind {
	if metadata.Renderer.Kind != "" {
		return metadata.Renderer.Kind
	}
	return fallback
}

// workflowContinueTargetsActiveWorkflow reports whether a continuation targets the active workflow.
func workflowContinueTargetsActiveWorkflow(operation string, workflowType string) bool {
	if operation == workflowType {
		return true
	}
	return workflowType == "subscription.start" && operation == "subscription.list_departments"
}

// workflowContinueShapeAllowed reports whether a continuation matches the active workflow slot shape.
func workflowContinueShapeAllowed(operation string, workflow *protocolWorkflowContext) bool {
	if workflow == nil {
		return false
	}
	switch {
	case workflow.Type == "subscription.start" && operation == "subscription.start":
		return hasMissingField(workflow.MissingFields, "scope") ||
			hasMissingField(workflow.MissingFields, "dept_names") ||
			hasMissingField(workflow.MissingFields, "dept_ids")
	case workflow.Type == "subscription.start" && operation == "subscription.list_departments":
		return true
	default:
		return false
	}
}

// policyExplicitNewRequest reports whether an act should interrupt any active workflow.
func policyExplicitNewRequest(act UserAct) bool {
	switch act {
	case ActHelp, ActCapabilityQuestion, ActRuleQuestion, ActReadQuery, ActWriteRequest:
		return true
	default:
		return false
	}
}

// actAllowed handles act allowed.
func actAllowed(act UserAct, allowed []UserAct) bool {
	for _, candidate := range allowed {
		if act == candidate {
			return true
		}
	}
	return false
}

func enforcePreUserPolicy(input PrePolicyGateInput, metadata OperationManifest, interrupt bool) (bool, ProtocolValidationResult) {
	if !input.HasUserContext {
		return false, ProtocolValidationResult{}
	}
	if !conversationScopeAllowed(metadata.Scope, input.ConversationType) {
		return true, ProtocolValidationResult{
			ValidationCode:          "conversation_scope_denied",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseRefuse,
		}
	}
	if input.UserRole < metadata.MinRole {
		return true, ProtocolValidationResult{
			ValidationCode:          "role_denied",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseRefuse,
		}
	}
	return false, ProtocolValidationResult{}
}

func conversationScopeAllowed(scope ConversationScope, conversationType string) bool {
	switch scope {
	case ConversationScopeBoth, "":
		return true
	case ConversationScopeGroup:
		return strings.TrimSpace(conversationType) == "2"
	case ConversationScopeDM:
		return strings.TrimSpace(conversationType) != "2"
	default:
		return false
	}
}

type ResourcePolicyGate interface {
	Validate(ctx context.Context, input ResourcePolicyGateInput) ResourcePolicyGateResult
}

type ResourcePolicyGateInput struct {
	User    *tools.UserContext
	Request OperationRequest
	Dept    DeptPort
}

type ResourcePolicyGateResult struct {
	Allow         bool
	BlockedReason string
	ResponseKind  ResponseKind
}

type resourcePolicyGate struct{}

func newResourcePolicyGate() ResourcePolicyGate {
	return resourcePolicyGate{}
}

func (resourcePolicyGate) Validate(ctx context.Context, input ResourcePolicyGateInput) ResourcePolicyGateResult {
	manifest, ok := lookupOperation(input.Request.Operation)
	if !ok {
		return denyResourcePolicy("operation_not_allowed")
	}
	for _, policy := range manifest.Policies {
		switch policy.Name {
		case "group_conversation":
			if input.User == nil || strings.TrimSpace(input.User.ConversationType) != "2" {
				return denyResourcePolicy("group_chat_required")
			}
		case "subscription_scope":
			if !subscriptionScopeValid(input.Request.Operation, input.Request.TrustedParams) {
				return denyResourcePolicy("subscription_scope_invalid")
			}
			if denied := validateDepartmentScope(ctx, input); denied.BlockedReason != "" {
				return denied
			}
		case "schedule_user_visibility":
			if !scheduleUserVisible(input.User, input.Request.TrustedParams) {
				return denyResourcePolicy("schedule_user_visibility_denied")
			}
		}
	}
	if subscriptionOperationRequiresCurrentConversation(input.Request.Operation) {
		if !requestConversationMatchesUser(input.User, input.Request) {
			return denyResourcePolicy("subscription_conversation_mismatch")
		}
	}
	return ResourcePolicyGateResult{Allow: true, ResponseKind: ResponseResult}
}

func denyResourcePolicy(reason string) ResourcePolicyGateResult {
	return ResourcePolicyGateResult{
		Allow:         false,
		BlockedReason: reason,
		ResponseKind:  ResponseRefuse,
	}
}

func subscriptionOperationRequiresCurrentConversation(operation string) bool {
	switch operation {
	case "subscription.start", "subscription.cancel", "subscription.query_status":
		return true
	default:
		return false
	}
}

func requestConversationMatchesUser(user *tools.UserContext, req OperationRequest) bool {
	if user == nil {
		return false
	}
	conversationID, ok := extractParamString(req.TrustedParams, "conversation_id")
	if !ok {
		conversationID = strings.TrimSpace(req.ConversationID)
	}
	return conversationID != "" && conversationID == strings.TrimSpace(user.ConversationID)
}

func scheduleUserVisible(user *tools.UserContext, params map[string]TrustedParam) bool {
	if user == nil {
		return false
	}
	targetUserID, ok := extractParamUint(params, "user_id")
	if !ok {
		return false
	}
	return targetUserID == user.UserID || user.UserRole >= 1
}

func validateDepartmentScope(ctx context.Context, input ResourcePolicyGateInput) ResourcePolicyGateResult {
	scope, ok := extractParamString(input.Request.TrustedParams, "scope")
	if !ok || scope != "department" || input.Dept == nil {
		return ResourcePolicyGateResult{}
	}
	deptIDs, ok := extractParamInt64Slice(input.Request.TrustedParams, "dept_ids")
	if !ok {
		return denyResourcePolicy("department_scope_denied")
	}
	if candidateDeptParamAllowed(input.User, input.Request.TrustedParams["dept_ids"]) {
		return ResourcePolicyGateResult{}
	}
	depts, err := input.Dept.ListDepts(ctx)
	if err != nil {
		return denyResourcePolicy("department_scope_unverified")
	}
	allowed := make(map[int64]struct{}, len(depts))
	for _, dept := range depts {
		if input.User != nil && input.User.TenantID != 0 && dept.TenantID != 0 && dept.TenantID != input.User.TenantID {
			continue
		}
		allowed[dept.DeptID] = struct{}{}
	}
	for _, deptID := range deptIDs {
		if _, ok := allowed[deptID]; !ok {
			return denyResourcePolicy("department_scope_denied")
		}
	}
	return ResourcePolicyGateResult{}
}

func candidateDeptParamAllowed(user *tools.UserContext, param TrustedParam) bool {
	if param.Source.Kind != TrustedParamSourceCandidate {
		return false
	}
	if user == nil || user.TenantID == 0 {
		return param.TenantID != 0
	}
	return param.TenantID == user.TenantID
}

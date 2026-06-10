package agent

import "strings"

const lowConfidenceWriteThreshold = 0.75

type ProtocolValidationResult struct {
	AllowExecution          bool
	ValidationCode          string
	UseActiveWorkflow       bool
	InterruptActiveWorkflow bool
	ResponseKind            ResponseKind
}

// validateProtocol validates whether a protocol draft is allowed to proceed.
func validateProtocol(draft ProtocolDraft, activeWorkflow *protocolWorkflowContext) ProtocolValidationResult {
	if draft.Act == ActUnknown {
		return ProtocolValidationResult{ValidationCode: "unknown_intent", ResponseKind: ResponseClarify}
	}

	if draft.Act == ActWorkflowContinue {
		return validateWorkflowContinue(draft, activeWorkflow)
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
		if !strings.HasSuffix(draft.Operation, ".describe_capability") {
			return ProtocolValidationResult{
				ValidationCode: "act_operation_mismatch",
				ResponseKind:   ResponseRefuse,
			}
		}
		return ProtocolValidationResult{
			ValidationCode:          "capability_non_executable",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseAnswer,
		}
	case ActRuleQuestion:
		if !strings.HasSuffix(draft.Operation, ".rule_explain") {
			return ProtocolValidationResult{
				ValidationCode: "act_operation_mismatch",
				ResponseKind:   ResponseRefuse,
			}
		}
		return ProtocolValidationResult{
			ValidationCode:          "rule_non_executable",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseAnswer,
		}
	case ActHelp:
		if draft.Operation != "system.describe_capability" {
			return ProtocolValidationResult{
				ValidationCode: "act_operation_mismatch",
				ResponseKind:   ResponseRefuse,
			}
		}
		return ProtocolValidationResult{
			ValidationCode:          "help_non_executable",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseAnswer,
		}
	case ActReadQuery:
		return ProtocolValidationResult{
			AllowExecution:          true,
			ValidationCode:          "allowed_read_query",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseResult,
		}
	case ActWriteRequest:
		if draft.Confidence < lowConfidenceWriteThreshold {
			return ProtocolValidationResult{
				ValidationCode: "low_confidence_write",
				ResponseKind:   ResponseClarify,
			}
		}
		return ProtocolValidationResult{
			AllowExecution:          true,
			ValidationCode:          "allowed_write_request",
			InterruptActiveWorkflow: interrupt,
			ResponseKind:            ResponseResult,
		}
	default:
		return ProtocolValidationResult{
			ValidationCode: "unsupported_act",
			ResponseKind:   ResponseRefuse,
		}
	}
}

// validateWorkflowContinue validates active workflow continuation without treating it as a generic write.
func validateWorkflowContinue(draft ProtocolDraft, activeWorkflow *protocolWorkflowContext) ProtocolValidationResult {
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
	return ProtocolValidationResult{
		AllowExecution:    true,
		ValidationCode:    "workflow_continue_allowed",
		UseActiveWorkflow: true,
		ResponseKind:      ResponseResult,
	}
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

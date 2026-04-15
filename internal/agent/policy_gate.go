package agent

type ProtocolValidationResult struct {
	AllowExecution    bool
	ValidationCode    string
	UseActiveWorkflow bool
}

// validateProtocol validates whether a protocol draft is allowed to proceed.
func validateProtocol(draft ProtocolDraft, activeWorkflow *protocolWorkflowContext) ProtocolValidationResult {
	if draft.Act == ActUnknown {
		return ProtocolValidationResult{ValidationCode: "unknown_intent"}
	}

	if draft.Act == ActWorkflowContinue {
		if activeWorkflow == nil {
			return ProtocolValidationResult{ValidationCode: "workflow_missing"}
		}
		return ProtocolValidationResult{
			AllowExecution:    true,
			ValidationCode:    "workflow_continue_allowed",
			UseActiveWorkflow: true,
		}
	}

	metadata, ok := lookupOperation(draft.Operation)
	if !ok {
		return ProtocolValidationResult{ValidationCode: "operation_not_allowed"}
	}

	if !actAllowed(draft.Act, metadata.AllowedActs) {
		return ProtocolValidationResult{ValidationCode: "act_operation_mismatch"}
	}

	switch draft.Act {
	case ActCapabilityQuestion:
		return ProtocolValidationResult{ValidationCode: "capability_non_executable"}
	case ActRuleQuestion:
		return ProtocolValidationResult{ValidationCode: "rule_non_executable"}
	case ActHelp:
		return ProtocolValidationResult{ValidationCode: "help_non_executable"}
	case ActReadQuery:
		if metadata.IsWrite {
			return ProtocolValidationResult{ValidationCode: "read_query_cannot_write"}
		}
		return ProtocolValidationResult{AllowExecution: true, ValidationCode: "allowed_read_query"}
	case ActWriteRequest:
		return ProtocolValidationResult{AllowExecution: true, ValidationCode: "allowed_write_request"}
	case ActWorkflowCancel:
		if activeWorkflow == nil {
			return ProtocolValidationResult{ValidationCode: "workflow_missing"}
		}
		return ProtocolValidationResult{ValidationCode: "workflow_cancel_non_executable", UseActiveWorkflow: true}
	default:
		return ProtocolValidationResult{ValidationCode: "unsupported_act"}
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

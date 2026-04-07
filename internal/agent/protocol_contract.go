package agent

type UserAct string
type BusinessDomain string
type ProtocolMode string
type ResponseKind string

const (
	ActCapabilityQuestion UserAct = "capability_question"
	ActRuleQuestion       UserAct = "rule_question"
	ActReadQuery          UserAct = "read_query"
	ActWriteRequest       UserAct = "write_request"
	ActWorkflowContinue   UserAct = "workflow_continue"
	ActWorkflowCancel     UserAct = "workflow_cancel"
	ActHelp               UserAct = "help"
	ActUnknown            UserAct = "unknown"
)

const (
	DomainAttendance   BusinessDomain = "attendance"
	DomainSubscription BusinessDomain = "subscription"
	DomainManualSign   BusinessDomain = "manual_sign"
	DomainSchedule     BusinessDomain = "schedule"
	DomainLeave        BusinessDomain = "leave"
	DomainAnalytics    BusinessDomain = "analytics"
	DomainUnknown      BusinessDomain = "unknown"
)

const (
	ProtocolModeLegacy ProtocolMode = "legacy"
	ProtocolModeShadow ProtocolMode = "protocol_shadow"
	ProtocolModeLive   ProtocolMode = "protocol_live"
)

const (
	ResponseAnswer        ResponseKind = "answer"
	ResponseClarify       ResponseKind = "clarify"
	ResponseSelectOptions ResponseKind = "select_options"
	ResponseConfirm       ResponseKind = "confirm"
	ResponseResult        ResponseKind = "result"
	ResponseRefuse        ResponseKind = "refuse"
)

type ProtocolDraft struct {
	Act           UserAct
	Domain        BusinessDomain
	Operation     string
	Confidence    float64
	Params        map[string]any
	MissingFields []string
	ClarifyReason string
}

type OperationRequest struct {
	Operation     string
	TrustedParams map[string]any
}

func userActs() []UserAct {
	return []UserAct{
		ActCapabilityQuestion,
		ActRuleQuestion,
		ActReadQuery,
		ActWriteRequest,
		ActWorkflowContinue,
		ActWorkflowCancel,
		ActHelp,
		ActUnknown,
	}
}

func businessDomains() []BusinessDomain {
	return []BusinessDomain{
		DomainAttendance,
		DomainSubscription,
		DomainManualSign,
		DomainSchedule,
		DomainLeave,
		DomainAnalytics,
		DomainUnknown,
	}
}

func protocolModes() []ProtocolMode {
	return []ProtocolMode{
		ProtocolModeLegacy,
		ProtocolModeShadow,
		ProtocolModeLive,
	}
}

func normalizeProtocolMode(value string) ProtocolMode {
	mode := ProtocolMode(value)
	for _, allowed := range protocolModes() {
		if mode == allowed {
			return mode
		}
	}
	return ProtocolModeLegacy
}

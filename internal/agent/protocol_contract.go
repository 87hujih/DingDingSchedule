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
	DomainSystem       BusinessDomain = "system"
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
	ResponseResult        ResponseKind = "result"
	ResponseClarify       ResponseKind = "clarify"
	ResponseSelectOptions ResponseKind = "select_options"
	ResponseRefuse        ResponseKind = "refuse"
)

type SlotDraft struct {
	Field string
	Raw   string
}

type IntentDraft struct {
	Act           UserAct
	Domain        BusinessDomain
	Operation     string
	Confidence    float64
	Slots         map[string]SlotDraft
	Reason        string
	Params        map[string]any
	MissingFields []string
	ClarifyReason string
}

type ProtocolDraft = IntentDraft

type TrustedParamSourceKind string

const (
	TrustedParamSourceRawSlot   TrustedParamSourceKind = "raw_slot"
	TrustedParamSourceDefault   TrustedParamSourceKind = "default"
	TrustedParamSourceRuntime   TrustedParamSourceKind = "runtime"
	TrustedParamSourceWorkflow  TrustedParamSourceKind = "workflow"
	TrustedParamSourceCandidate TrustedParamSourceKind = "candidate"
	TrustedParamSourceDerived   TrustedParamSourceKind = "derived"
)

type TrustedParamSource struct {
	Kind     TrustedParamSourceKind
	Raw      string
	Resolver string
}

type TrustedParam struct {
	Field    string
	Value    any
	Source   TrustedParamSource
	TenantID uint
}

type OperationRequest struct {
	Operation     string
	TrustedParams map[string]TrustedParam
}

// protocolModes handles protocol modes.
func protocolModes() []ProtocolMode {
	return []ProtocolMode{
		ProtocolModeLegacy,
		ProtocolModeShadow,
		ProtocolModeLive,
	}
}

// normalizeProtocolMode normalizes protocol mode.
func normalizeProtocolMode(value string) ProtocolMode {
	mode := ProtocolMode(value)
	for _, allowed := range protocolModes() {
		if mode == allowed {
			return mode
		}
	}
	return ProtocolModeLegacy
}

// responseKinds returns response kinds approved for protocol replies.
func responseKinds() []ResponseKind {
	return []ResponseKind{
		ResponseAnswer,
		ResponseResult,
		ResponseClarify,
		ResponseSelectOptions,
		ResponseRefuse,
	}
}

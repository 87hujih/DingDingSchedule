package agent

import "schedule_server/internal/agent/tools"

type DomainHint string

const (
	domainHintObviousOut DomainHint = "obvious_out"
	domainHintLikelyIn   DomainHint = "likely_in"
	domainHintUnknown    DomainHint = "unknown"
)

type KnowledgeStrength string

const (
	knowledgeStrengthNone   KnowledgeStrength = "none"
	knowledgeStrengthWeak   KnowledgeStrength = "weak"
	knowledgeStrengthStrong KnowledgeStrength = "strong"
)

type PlanKind string

const (
	planKindObviousOut   PlanKind = "obvious_out"
	planKindContinueTask PlanKind = "continue_task"
	planKindClarify      PlanKind = "clarify"
	planKindTool         PlanKind = "tool"
	planKindRAG          PlanKind = "rag"
	planKindMixed        PlanKind = "mixed"
)

type PlanInput struct {
	Question          string
	UserContext       *tools.UserContext
	History           []tools.Message
	ActiveTask        *ActiveTask
	ConversationEvent conversationDecision
	Retrieval         RetrievalResult
	TaskCandidate     *ActiveTask
	HasLiveSignal     bool
	HasRuleSignal     bool
	HasActionIntent   bool
	HasClarifyIntent  bool
	HasHelpIntent     bool
}

type PlanDecision struct {
	Kind              PlanKind
	ActiveTask        *ActiveTask
	ClarifyReason     string
	ToolsAllowed      []string
	FollowUpMatched   []string
	KnowledgeStrength KnowledgeStrength
}

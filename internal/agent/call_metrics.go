package agent

type domainResult string

const (
	domainIn  domainResult = "in_domain"
	domainOut domainResult = "out_of_domain"
)

// callMetrics 记录一次对话调用的全部指标，按职责分组。
type callMetrics struct {
	// 核心路由决策
	QueryType         queryKind
	ConversationEvent conversationEvent
	AnswerMode        answerMode
	DomainResult      domainResult
	DomainHint        DomainHint
	PlanKind          PlanKind

	KnowledgeStrength KnowledgeStrength

	// 任务生命周期
	Task    taskMetrics
	Planner plannerMetrics
	Shadow  shadowMetrics
	Route   routeMetrics
	Proto   protocolMetrics
	Wf      workflowMetrics

	// 响应
	ResponseKind string

	// 知识检索
	Retrieval            retrievalMetrics
	SourceRefs           []string
	FollowUpMatchedSlots []string

	// LLM
	LLMDurationMs int64
}

type taskMetrics struct {
	ActiveTaskType   string
	TaskStatusBefore string
	TaskStatusAfter  string
	TaskID           string
	TaskKeepOpen     bool
	TaskSwitch       bool
	LastErrorCode    string
}

type plannerMetrics struct {
	Reason     string
	Action     string
	Confidence float64
}

type shadowMetrics struct {
	PlannerAction  string
	PlannerMatched bool
	RouteKind      string
	RouteMatched   bool
}

type routeMetrics struct {
	Kind              string
	Confidence        float64
	ReasonCode        string
	Source            string
	ClarifyCode       string
	SoftNoticeCode    string
	ExecutorName      string
	ToolPool          string
	RouterLatencyMs   int64
	ExecutorLatencyMs int64
}

type protocolMetrics struct {
	Mode                    string
	Act                     string
	Domain                  string
	Operation               string
	ValidationCode          string
	BlockedReason           string
	ResolvedSlots           string
	CandidateCount          int
	ExecutionAllowed        bool
	RequestID               string
	CompilerStatus          string
	CompilerLatencyMs       int64
	IntentDraftJSON         string
	CatalogValidationCode   string
	WorkflowDecision        string
	WorkflowInterruptReason string
	ResolvedSlotsJSON       string
	EntityResolutionStatus  string
	PrePolicyResult         string
	ResourcePolicyResult    string
	WriteGuardResult        string
	IdempotencyKey          string
	ExecutorStatus          string
	RendererName            string
	FailureLayer            string
	LegacyCalled            bool
}

type workflowMetrics struct {
	IDBefore    string
	IDAfter     string
	StateBefore string
	StateAfter  string
}

type retrievalMetrics struct {
	DurationMs     int64
	HitCount       int
	CandidateCount int
	TopRefs        []string
	TopScores      []int
	FilteredReason string
	DocTypes       []string
}

package agent

type RouteKind string

const (
	RouteOffTopicReject RouteKind = "off_topic_reject"
	RouteSocialRefuse   RouteKind = "social_refuse"
	RouteClarify        RouteKind = "clarify"
	RouteTaskStart      RouteKind = "task_start"
	RouteTaskContinue   RouteKind = "task_continue"
	RouteTaskMeta       RouteKind = "task_meta"
	RouteTaskCancel     RouteKind = "task_cancel"
	RouteRAGQuery       RouteKind = "rag_query"
	RouteToolQuery      RouteKind = "tool_query"
	RouteMixedQuery     RouteKind = "mixed_query"
)

type RouteSource string

const (
	RouteSourceShortCircuit   RouteSource = "short_circuit"
	RouteSourceSemanticRouter RouteSource = "semantic_router"
	RouteSourceFallback       RouteSource = "router_fallback"
)

type RouteMode string

const (
	RouteModeOff    RouteMode = "off"
	RouteModeShadow RouteMode = "shadow"
	RouteModeLive   RouteMode = "live"
)

type RouteDecision struct {
	Kind           RouteKind   `json:"kind"`
	Confidence     float64     `json:"confidence"`
	ReasonCode     string      `json:"reason_code"`
	UserIntent     string      `json:"user_intent,omitempty"`
	TargetTaskType string      `json:"target_task_type,omitempty"`
	TargetTaskID   string      `json:"target_task_id,omitempty"`
	SwitchTask     bool        `json:"switch_task,omitempty"`
	SoftNoticeCode string      `json:"soft_notice_code,omitempty"`
	ClarifyCode    string      `json:"clarify_code,omitempty"`
	RouteSource    RouteSource `json:"-"`

	ExtractedEntities *ExtractedEntities `json:"extracted_entities,omitempty"`
}

const routeTaskExecutionMinConfidence = 0.75

// guardLowConfidenceRouteDecision 将低置信度任务动作降级为澄清，避免模型猜测触发写操作或改变任务状态。
func guardLowConfidenceRouteDecision(decision RouteDecision) (RouteDecision, bool) {
	if !isTaskActionRoute(decision.Kind) || decision.Confidence >= routeTaskExecutionMinConfidence {
		return RouteDecision{}, false
	}
	return RouteDecision{
		Kind:        RouteClarify,
		Confidence:  decision.Confidence,
		ReasonCode:  "low_confidence_task_action",
		ClarifyCode: "ambiguous_intent",
		RouteSource: decision.RouteSource,
	}, true
}

// isTaskActionRoute 判断当前路由是否会启动、推进或取消任务状态。
func isTaskActionRoute(kind RouteKind) bool {
	switch kind {
	case RouteTaskStart, RouteTaskContinue, RouteTaskCancel:
		return true
	default:
		return false
	}
}

// ExtractedEntities holds entities extracted by the semantic router.
type ExtractedEntities struct {
	Scope     string   `json:"scope,omitempty"`
	DeptNames []string `json:"dept_names,omitempty"`
	UserName  string   `json:"user_name,omitempty"`
	Date      string   `json:"date,omitempty"`
	Section   int      `json:"section,omitempty"`
}

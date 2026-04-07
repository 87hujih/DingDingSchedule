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
}

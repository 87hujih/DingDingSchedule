package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

const semanticRouterTimeout = 5 * time.Second

type semanticRouter struct {
	client *LLMClient
}

// newSemanticRouter creates semantic router.
func newSemanticRouter(client *LLMClient) *semanticRouter {
	return &semanticRouter{client: client}
}

// Route routes the question through the semantic router model.
func (r *semanticRouter) Route(ctx context.Context, routeCtx RouteContext) RouteDecision {
	fallback := func(reason string) RouteDecision {
		return RouteDecision{
			Kind:        RouteClarify,
			ReasonCode:  reason,
			ClarifyCode: "ambiguous_intent",
			RouteSource: RouteSourceFallback,
		}
	}

	if r == nil || r.client == nil {
		return fallback("router_unavailable")
	}

	routerCtx, cancel := context.WithTimeout(ctx, semanticRouterTimeout)
	defer cancel()

	payload, err := json.Marshal(routeCtx)
	if err != nil {
		return fallback("route_context_encode_failed")
	}

	resp, err := r.client.Chat(routerCtx, []tools.Message{
		{
			Role: "system",
			Content: "你是一个语义路由器。你只能返回 JSON，对用户消息做单一路由判定。" +
				"输出字段：kind, confidence, reason_code, target_task_type, target_task_id, switch_task, soft_notice_code, clarify_code。" +
				"kind 只允许：off_topic_reject,social_refuse,clarify,task_start,task_continue,task_meta,task_cancel,rag_query,tool_query。" +
				"重要规则：当用户意图涉及 subscribe_attendance_push（开启/添加/开通考勤订阅、订阅考勤推送）时，必须路由到 tool_query，不要路由到 task_start。" +
				"同理，unsubscribe_attendance_push 和 query_subscription_status 也路由到 tool_query。",
		},
		{
			Role:    "user",
			Content: string(payload),
		},
	}, nil)
	if err != nil {
		return fallback("router_call_failed")
	}

	var decision RouteDecision
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &decision); err != nil {
		return fallback("router_parse_failed")
	}
	if !isValidRouteKind(decision.Kind) {
		return fallback("router_invalid_kind")
	}

	decision.RouteSource = RouteSourceSemanticRouter
	return decision
}

// isValidRouteKind reports whether it is valid route kind.
func isValidRouteKind(kind RouteKind) bool {
	switch kind {
	case RouteOffTopicReject,
		RouteSocialRefuse,
		RouteClarify,
		RouteTaskStart,
		RouteTaskContinue,
		RouteTaskMeta,
		RouteTaskCancel,
		RouteRAGQuery,
		RouteToolQuery:
		return true
	default:
		return false
	}
}

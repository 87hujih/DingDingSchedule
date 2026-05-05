package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

const semanticRouterTimeout = 5 * time.Second

const semanticRouterSystemPrompt = `你是一个语义路由器。你只能返回 JSON，对用户消息做单一路由判定。

输出字段：kind, confidence, reason_code, target_task_type, target_task_id, switch_task, soft_notice_code, clarify_code。

kind 只允许：off_topic_reject, social_refuse, clarify, task_start, task_continue, task_meta, task_cancel, rag_query, tool_query。

## 路由规则

### 有 active_task 时

当 active_task 存在且未过期时，用户消息属于以下三种之一：

**task_continue**（用户在提供缺失字段的值）：
- 用户直接回答了系统之前询问的内容
- 例如：missing_slots=["scope"]，用户说"全部人员"或"全部"→ task_continue
- 例如：missing_slots=["scope"]，用户说"指定部门"或"部分部门"→ task_continue
- 例如：missing_slots=["dept_names"]，用户说"一年级组"→ task_continue
- 例如：missing_slots=["user_name","date","section"]，用户说"昨天第五节"→ task_continue

**task_meta**（用户在询问与当前任务相关的信息，而非提供答案）：
- 用户在请求信息以帮助自己做决策
- 例如：missing_slots=["scope"]，用户说"都有哪些部门"/"有哪些可选"/"部门列表"→ task_meta
- 例如：missing_slots=["dept_names"]，用户说"缺什么信息"/"还差什么"→ task_meta
- 例如：用户说"补签有时间限制吗"/"这个功能怎么用"→ task_meta
- 判断标准：用户不是在提供答案，而是在提问。如果是疑问句或请求列举/说明，优先 task_meta

**task_cancel**（用户要取消）：
- 用户说"取消"/"不用了"/"算了"→ task_cancel

**关键区分原则**：如果不确定是 task_continue 还是 task_meta，优先选 task_meta。误判为 meta 的代价是多给一次信息，误判为 continue 的代价是吞掉用户问题。

### 无 active_task 时

判断是否为新任务请求或其他意图：

**task_start**（新任务）：
- 开启/添加/开通考勤订阅 → task_start, target_task_type=subscribe_attendance_push
- 取消/关闭考勤订阅 → task_start, target_task_type=unsubscribe_attendance_push
- 查询订阅状态 → task_start, target_task_type=query_subscription_status
- 补签/代签考勤 → task_start, target_task_type=sign_for_user

**tool_query**（需要调用工具查询但不是任务型操作）：
- 查课表、查考勤状态、查请假等实时数据查询

**rag_query**（规则/知识类问题）：
- 询问规则说明、制度解释等

**social_refuse**：闲聊、打招呼
**off_topic_reject**：与课表/考勤/请假完全无关
**clarify**：意图模糊，无法判断

## 实体提取

当 kind 为 task_continue 时，你必须同时提取用户提供的实体：

### 订阅任务 (target_task_type=subscribe_attendance_push)
- scope: "all"（用户说全部）或 "department"（用户指定部门）
- dept_names: 部门名称数组，从 active_task.candidate_hints 中匹配

### 补签任务 (target_task_type=sign_for_user)
- user_name: 用户姓名
- date: 日期（"今天"、"昨天"、"明天"或 YYYY-MM-DD）
- section: 节次（1-5）

### 匹配规则
1. 用户说"和"、"跟"、"与"、顿号、逗号分隔的多个值 → 数组
2. 用户说"两个都要"、"全部"、"所有" → scope="all"
3. 用户说"第一个"、"第二个" → 从 candidate_hints 中按索引取
4. 用户说的名称必须在 candidate_hints 中存在，否则留空`


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
			Role:    "system",
			Content: semanticRouterSystemPrompt,
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

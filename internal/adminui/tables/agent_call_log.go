package tables

import (
	"github.com/GoAdminGroup/go-admin/context"
	"github.com/GoAdminGroup/go-admin/modules/db"
	"github.com/GoAdminGroup/go-admin/plugins/admin/modules/table"
	"github.com/GoAdminGroup/go-admin/template/types"
	"github.com/GoAdminGroup/go-admin/template/types/form"
)

// GetAgentCallLogTable agent_call_logs 表的只读配置
func GetAgentCallLogTable(ctx *context.Context) (t table.Table) {
	t = table.NewDefaultTable(ctx, table.Config{
		Driver:     db.DriverMysql,
		CanAdd:     false,
		Editable:   false,
		Deletable:  false,
		Exportable: true,
		Connection: table.DefaultConnectionName,
		PrimaryKey: table.PrimaryKey{
			Type: db.Int,
			Name: table.DefaultPrimaryKeyName,
		},
	})

	info := t.GetInfo()
	info.AddField("ID", "id", db.Int).FieldSortable()
	info.AddField("租户", "tenant_id", db.Int).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return getTenantName(m.Value)
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(getTenantOptions())
	info.AddField("用户", "user_name", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("类型", "conv_type", db.Varchar).
		FieldDisplay(func(m types.FieldModel) interface{} {
			if m.Value == "2" {
				return "群聊"
			}
			return "单聊"
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "单聊", Value: "1"},
			{Text: "群聊", Value: "2"},
		})
	info.AddField("查询路径", "query_type", db.Varchar).
		FieldDisplay(func(m types.FieldModel) interface{} {
			switch m.Value {
			case "tool":
				return "工具查询"
			case "rag":
				return "规则检索"
			case "mixed":
				return "混合"
			default:
				return m.Value
			}
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "工具查询", Value: "tool"},
			{Text: "规则检索", Value: "rag"},
			{Text: "混合", Value: "mixed"},
		})
	info.AddField("领域判定", "domain_result", db.Varchar).
		FieldDisplay(func(m types.FieldModel) interface{} {
			switch m.Value {
			case "in_domain":
				return "站内"
			case "out_of_domain":
				return "站外"
			default:
				return m.Value
			}
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "站内", Value: "in_domain"},
			{Text: "站外", Value: "out_of_domain"},
		})
	info.AddField("回答模式", "answer_mode", db.Varchar).
		FieldDisplay(func(m types.FieldModel) interface{} {
			switch m.Value {
			case "knowledge-only":
				return "纯知识"
			case "tool-first":
				return "工具优先"
			case "mixed":
				return "混合"
			case "reject":
				return "拒答"
			default:
				return m.Value
			}
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "纯知识", Value: "knowledge-only"},
			{Text: "工具优先", Value: "tool-first"},
			{Text: "混合", Value: "mixed"},
			{Text: "拒答", Value: "reject"},
		})
	info.AddField("路由结果", "route_kind", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("路由来源", "route_source", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("执行器", "executor_name", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("澄清码", "clarify_code", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("轻提示码", "soft_notice_code", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("工具池", "tool_pool", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("路由耗时(ms)", "router_latency_ms", db.Int).FieldSortable()
	info.AddField("执行耗时(ms)", "executor_latency_ms", db.Int).FieldSortable()
	info.AddField("影子路由", "shadow_route_kind", db.Varchar)
	info.AddField("协议模式", "protocol_mode", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("编译状态", "compiler_status", db.Varchar).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "成功", Value: "ok"},
			{Text: "未识别", Value: "unknown"},
			{Text: "未调用", Value: "skipped"},
			{Text: "超时", Value: "timeout"},
			{Text: "传输异常", Value: "transport_error"},
			{Text: "输出异常", Value: "invalid_output"},
		})
	info.AddField("编译来源", "compiler_source", db.Varchar).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "确定性识别", Value: "deterministic"},
			{Text: "LLM", Value: "llm"},
			{Text: "降级", Value: "fallback"},
		})
	info.AddField("编译降级原因", "compiler_fallback_reason", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("协议动作", "protocol_act", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("协议域", "protocol_domain", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("协议操作", "protocol_operation", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("协议校验", "protocol_validation_code", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("阻断原因", "protocol_blocked_reason", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("失败层", "failure_layer", db.Varchar).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "入口失败", Value: "ingress_failed"},
			{Text: "意图失败", Value: "intent_failed"},
			{Text: "目录失败", Value: "catalog_failed"},
			{Text: "工作流失败", Value: "workflow_failed"},
			{Text: "实体歧义", Value: "entity_ambiguous"},
			{Text: "实体不存在", Value: "entity_not_found"},
			{Text: "前置策略拒绝", Value: "pre_policy_denied"},
			{Text: "资源策略拒绝", Value: "resource_policy_denied"},
			{Text: "写保护阻断", Value: "write_guard_blocked"},
			{Text: "执行失败", Value: "executor_failed"},
			{Text: "渲染失败", Value: "renderer_failed"},
			{Text: "持久化失败", Value: "persistence_failed"},
		})
	info.AddField("V2阻断原因", "blocked_reason", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("工作流决策", "workflow_decision", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("Legacy触发", "legacy_called", db.Tinyint).
		FieldDisplay(func(m types.FieldModel) interface{} {
			if m.Value == "1" || m.Value == "true" {
				return "是"
			}
			return "否"
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "否", Value: "0"},
			{Text: "是", Value: "1"},
		})
	info.AddField("Replay Case", "replay_case_id", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("已解析槽位", "protocol_resolved_slots", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return truncateAgentCallLogAdminText(m.Value, 80)
		})
	info.AddField("V2槽位JSON", "resolved_slots_json", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return truncateAgentCallLogAdminText(m.Value, 80)
		})
	info.AddField("意图草稿", "intent_draft_json", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return truncateAgentCallLogAdminText(m.Value, 80)
		})
	info.AddField("协议候选数", "protocol_candidate_count", db.Int).FieldSortable()
	info.AddField("实体解析状态", "entity_resolution_status", db.Varchar).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "已解析", Value: "resolved"},
			{Text: "有歧义", Value: "ambiguous"},
			{Text: "未找到", Value: "not_found"},
			{Text: "无需解析", Value: "not_required"},
		})
	info.AddField("前置 workflow", "workflow_id_before", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("后置 workflow", "workflow_id_after", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("前置状态", "workflow_state_before", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("后置状态", "workflow_state_after", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("回复类型", "response_kind", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("允许执行", "execution_allowed", db.Tinyint)
	info.AddField("提问", "question", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return truncateAgentCallLogAdminText(m.Value, 40)
		}).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("调用工具", "tools_called", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("工具次数", "tool_call_count", db.Int).FieldSortable()
	info.AddField("知识命中", "retrieval_hit_count", db.Int).FieldSortable()
	info.AddField("候选数", "retrieval_candidate_count", db.Int).FieldSortable()
	info.AddField("检索耗时(ms)", "retrieval_duration_ms", db.Int).FieldSortable()
	info.AddField("LLM耗时(ms)", "llm_duration_ms", db.Int).FieldSortable()
	info.AddField("来源引用", "source_refs", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return truncateAgentCallLogAdminText(m.Value, 50)
		})
	info.AddField("Top来源", "retrieval_top_refs", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return truncateAgentCallLogAdminText(m.Value, 50)
		})
	info.AddField("检索分数", "retrieval_scores", db.Text)
	info.AddField("过滤原因", "retrieval_filtered_reason", db.Varchar)
	info.AddField("文档类型", "knowledge_doc_types", db.Text)
	info.AddField("回复", "reply", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return truncateAgentCallLogAdminText(m.Value, 60)
		})
	info.AddField("轮数", "rounds", db.Int).FieldSortable()
	info.AddField("耗时(ms)", "duration_ms", db.Int).FieldSortable()
	info.AddField("状态", "status", db.Varchar).
		FieldDisplay(func(m types.FieldModel) interface{} {
			switch m.Value {
			case "success":
				return "成功"
			case "failed":
				return "失败"
			case "timeout":
				return "超时"
			default:
				return m.Value
			}
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "成功", Value: "success"},
			{Text: "失败", Value: "failed"},
			{Text: "超时", Value: "timeout"},
		})
	info.AddField("失败原因", "error_msg", db.Varchar).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return truncateAgentCallLogAdminText(m.Value, 50)
		})
	info.AddField("时间", "created_at", db.Timestamp).FieldSortable().
		FieldFilterable(types.FilterType{FormType: form.DatetimeRange})

	info.SetTable("agent_call_logs").SetTitle("AI 对话记录").SetDescription("Agent 每次对话的调用情况")

	return
}

func truncateAgentCallLogAdminText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}

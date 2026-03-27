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
	info.AddField("提问", "question", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			runes := []rune(m.Value)
			if len(runes) > 40 {
				return string(runes[:40]) + "..."
			}
			return m.Value
		}).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("调用工具", "tools_called", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("工具次数", "tool_call_count", db.Int).FieldSortable()
	info.AddField("知识命中", "retrieval_hit_count", db.Int).FieldSortable()
	info.AddField("检索耗时(ms)", "retrieval_duration_ms", db.Int).FieldSortable()
	info.AddField("LLM耗时(ms)", "llm_duration_ms", db.Int).FieldSortable()
	info.AddField("来源引用", "source_refs", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			runes := []rune(m.Value)
			if len(runes) > 50 {
				return string(runes[:50]) + "..."
			}
			return m.Value
		})
	info.AddField("回复", "reply", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			runes := []rune(m.Value)
			if len(runes) > 60 {
				return string(runes[:60]) + "..."
			}
			return m.Value
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
			runes := []rune(m.Value)
			if len(runes) > 50 {
				return string(runes[:50]) + "..."
			}
			return m.Value
		})
	info.AddField("时间", "created_at", db.Timestamp).FieldSortable().
		FieldFilterable(types.FilterType{FormType: form.DatetimeRange})

	info.SetTable("agent_call_logs").SetTitle("AI 对话记录").SetDescription("Agent 每次对话的调用情况")

	return
}

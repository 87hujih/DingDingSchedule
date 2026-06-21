package tables

import (
	"github.com/GoAdminGroup/go-admin/context"
	"github.com/GoAdminGroup/go-admin/modules/db"
	"github.com/GoAdminGroup/go-admin/plugins/admin/modules/table"
	"github.com/GoAdminGroup/go-admin/template/types"
	"github.com/GoAdminGroup/go-admin/template/types/form"
)

// GetAuditLogTable audit_logs 表的只读配置
func GetAuditLogTable(ctx *context.Context) (auditLogTable table.Table) {
	auditLogTable = table.NewDefaultTable(ctx, table.Config{
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

	info := auditLogTable.GetInfo()
	info.AddField("ID", "id", db.Int).FieldSortable()
	info.AddField("租户", "tenant_id", db.Int).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return getTenantName(m.Value)
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(getTenantOptions())
	info.AddField("用户ID", "user_id", db.Int).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorEqual})
	info.AddField("用户名", "user_name", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("角色", "user_role", db.Int).
		FieldDisplay(func(m types.FieldModel) interface{} {
			switch m.Value {
			case "0":
				return "普通用户"
			case "1":
				return "管理员"
			case "2":
				return "超级管理员"
			default:
				return m.Value
			}
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "普通用户", Value: "0"},
			{Text: "管理员", Value: "1"},
			{Text: "超级管理员", Value: "2"},
		})
	info.AddField("方法", "method", db.Varchar).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "POST", Value: "POST"},
			{Text: "PUT", Value: "PUT"},
			{Text: "DELETE", Value: "DELETE"},
			{Text: "PATCH", Value: "PATCH"},
		})
	info.AddField("路径", "path", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("状态码", "status_code", db.Int).FieldSortable()
	info.AddField("耗时(ms)", "duration", db.Int).FieldSortable()
	info.AddField("IP", "ip_address", db.Varchar)
	info.AddField("请求体", "request_body", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			if len(m.Value) > 100 {
				return m.Value[:100] + "..."
			}
			return m.Value
		})
	info.AddField("时间", "created_at", db.Timestamp).FieldSortable().
		FieldFilterable(types.FilterType{FormType: form.DatetimeRange})

	info.SetTable("audit_logs").SetTitle("操作审计日志").SetDescription("记录所有写操作的审计日志")

	return
}

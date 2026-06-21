package tables

import (
	"github.com/GoAdminGroup/go-admin/context"
	"github.com/GoAdminGroup/go-admin/modules/db"
	"github.com/GoAdminGroup/go-admin/plugins/admin/modules/table"
	"github.com/GoAdminGroup/go-admin/template/types"
	"github.com/GoAdminGroup/go-admin/template/types/form"
)

// GetGroupAttendanceSubscriptionTable group_attendance_subscriptions 表的后台配置。
func GetGroupAttendanceSubscriptionTable(ctx *context.Context) (subTable table.Table) {
	subTable = table.NewDefaultTable(ctx, table.Config{
		Driver:     db.DriverMysql,
		CanAdd:     false,
		Editable:   true,
		Deletable:  false,
		Exportable: true,
		Connection: table.DefaultConnectionName,
		PrimaryKey: table.PrimaryKey{
			Type: db.Int,
			Name: table.DefaultPrimaryKeyName,
		},
	})

	info := subTable.GetInfo().WhereRaw("deleted_at IS NULL")
	info.AddField("ID", "id", db.Int).FieldSortable()
	info.AddField("租户", "tenant_id", db.Int).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return getTenantName(m.Value)
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(getTenantOptions())
	info.AddField("群名称", "group_name", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("会话ID", "conversation_id", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("订阅部门", "dept_ids_json", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			if m.Value == "" {
				return "全部部门"
			}
			return m.Value
		})
	info.AddField("是否推送", "push_enabled", db.Tinyint).
		FieldDisplay(func(m types.FieldModel) interface{} {
			if m.Value == "1" || m.Value == "true" {
				return "已开启"
			}
			return "已暂停"
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "已开启", Value: "1"},
			{Text: "已暂停", Value: "0"},
		})
	info.AddField("开启人UID", "enabled_by_uid", db.Int)
	info.AddField("创建时间", "created_at", db.Timestamp).FieldSortable()
	info.AddField("删除时间", "deleted_at", db.Timestamp)
	info.SetTable("group_attendance_subscriptions").SetTitle("群考勤推送订阅").SetDescription("管理群聊考勤自动推送开关")

	formList := subTable.GetForm()
	formList.AddField("ID", "id", db.Int, form.Default).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("租户", "tenant_id", db.Int, form.Default).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("群名称", "group_name", db.Varchar, form.Text).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("会话ID", "conversation_id", db.Varchar, form.Text).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("订阅部门", "dept_ids_json", db.Text, form.TextArea).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("开启人UID", "enabled_by_uid", db.Int, form.Default).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("是否推送", "push_enabled", db.Tinyint, form.Switch).
		FieldOptions(types.FieldOptions{
			{Text: "开启", Value: "1"},
			{Text: "暂停", Value: "0"},
		}).
		FieldHelpMsg("关闭后保留订阅关系，但定时考勤不会向该群推送")
	formList.AddField("创建时间", "created_at", db.Timestamp, form.Datetime).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("删除时间", "deleted_at", db.Timestamp, form.Datetime).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.SetTable("group_attendance_subscriptions").SetTitle("群考勤推送订阅").SetDescription("仅允许切换自动推送开关")

	return
}

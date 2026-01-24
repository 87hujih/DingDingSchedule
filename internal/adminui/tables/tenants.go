package tables

import (
	"time"

	"github.com/GoAdminGroup/go-admin/context"
	"github.com/GoAdminGroup/go-admin/modules/db"
	form2 "github.com/GoAdminGroup/go-admin/plugins/admin/modules/form"
	"github.com/GoAdminGroup/go-admin/plugins/admin/modules/table"
	"github.com/GoAdminGroup/go-admin/template/types"
	"github.com/GoAdminGroup/go-admin/template/types/form"
)

// Generators go-admin table models 注册表
var Generators = table.GeneratorList{
	"tenants":           GetTenantTable,
	"semesters":         GetSemesterTable,
	"schedule_periods":  GetSchedulePeriodTable,
	"schedule_settings": GetScheduleSettingTable,
}

// GetTenantTable tenants 表的 CRUD 配置
func GetTenantTable(ctx *context.Context) (tenantTable table.Table) {
	tenantTable = table.NewDefaultTable(ctx, table.Config{
		Driver:     db.DriverMysql,
		CanAdd:     true,
		Editable:   true,
		Deletable:  false, // 默认不开放删除，避免误删导致业务数据无法关联
		Exportable: true,
		Connection: table.DefaultConnectionName,
		PrimaryKey: table.PrimaryKey{
			Type: db.Int,
			Name: table.DefaultPrimaryKeyName,
		},
	})

	// 列表页
	info := tenantTable.GetInfo()
	info.AddField("ID", "id", db.Int).FieldSortable()
	info.AddField("CorpID", "corp_id", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("Name", "name", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("Status", "status", db.Tinyint).FieldDisplay(func(m types.FieldModel) interface{} {
		if m.Value == "1" {
			return "启用"
		}
		return "禁用"
	}).FieldFilterable(types.FilterType{FormType: form.SelectSingle}).FieldFilterOptions(types.FieldOptions{
		{Text: "启用", Value: "1"},
		{Text: "禁用", Value: "0"},
	})
	info.AddField("CreatedAt", "created_at", db.Timestamp)
	info.AddField("UpdatedAt", "updated_at", db.Timestamp)
	info.SetTable("tenants").SetTitle("Tenants").SetDescription("企业租户")

	// 表单页
	formList := tenantTable.GetForm()
	formList.AddField("ID", "id", db.Int, form.Default).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("CorpID", "corp_id", db.Varchar, form.Text).FieldMust()
	formList.AddField("Name", "name", db.Varchar, form.Text)
	formList.AddField("AppKey", "app_key", db.Varchar, form.Text).FieldMust()
	formList.AddField("AppSecret", "app_secret", db.Varchar, form.Password).FieldMust()
	formList.AddField("AgentID", "agent_id", db.Varchar, form.Text).FieldMust()
	formList.AddField("Status", "status", db.Tinyint, form.Radio).FieldOptions(types.FieldOptions{
		{Text: "启用", Value: "1"},
		{Text: "禁用", Value: "0"},
	}).FieldDefault("1").FieldMust()

	// 注意：GoAdmin 写数据不会触发 GORM 的自动时间戳逻辑，因此这里通过 PreProcessFn 自动补齐。
	formList.SetPreProcessFn(func(values form2.Values) form2.Values {
		now := time.Now().Format("2006-01-02 15:04:05")
		if values.IsInsertPost() {
			values.Add("created_at", now)
			values.Add("updated_at", now)
			return values
		}
		if values.IsUpdatePost() {
			values.Add("updated_at", now)
			return values
		}
		return values
	})

	formList.AddField("CreatedAt", "created_at", db.Timestamp, form.Datetime).
		FieldDisableWhenCreate().
		FieldDisplayButCanNotEditWhenUpdate()
	formList.AddField("UpdatedAt", "updated_at", db.Timestamp, form.Datetime).
		FieldDisableWhenCreate().
		FieldDisplayButCanNotEditWhenUpdate()
	formList.SetTable("tenants").SetTitle("Tenants").SetDescription("企业租户")

	return
}

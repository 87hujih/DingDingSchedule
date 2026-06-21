package tables

import (
	"strconv"
	"time"

	"schedule_server/global"

	"github.com/GoAdminGroup/go-admin/context"
	"github.com/GoAdminGroup/go-admin/modules/db"
	form2 "github.com/GoAdminGroup/go-admin/plugins/admin/modules/form"
	"github.com/GoAdminGroup/go-admin/plugins/admin/modules/table"
	"github.com/GoAdminGroup/go-admin/template/types"
	"github.com/GoAdminGroup/go-admin/template/types/form"
)

// GetSchedulePeriodTable 作息时间配置表的 CRUD 配置
func GetSchedulePeriodTable(ctx *context.Context) (periodTable table.Table) {
	periodTable = table.NewDefaultTable(ctx, table.Config{
		Driver:     db.DriverMysql,
		CanAdd:     true,
		Editable:   true,
		Deletable:  true,
		Exportable: true,
		Connection: table.DefaultConnectionName,
		PrimaryKey: table.PrimaryKey{
			Type: db.Int,
			Name: table.DefaultPrimaryKeyName,
		},
	})

	// 列表页
	info := periodTable.GetInfo()
	info.AddField("ID", "id", db.Int).FieldSortable()
	info.AddField("企业名称", "tenant_id", db.Int).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return getTenantNameForSchedulePeriods(m.Value)
		})
	info.AddField("模式", "mode", db.Varchar).FieldDisplay(func(m types.FieldModel) interface{} {
		switch m.Value {
		case "school_summer":
			return "夏季上学"
		case "school_winter":
			return "冬季上学"
		case "holiday":
			return "假期"
		default:
			return m.Value
		}
	}).FieldFilterable(types.FilterType{FormType: form.SelectSingle}).FieldFilterOptions(types.FieldOptions{
		{Text: "夏季上学", Value: "school_summer"},
		{Text: "冬季上学", Value: "school_winter"},
		{Text: "假期", Value: "holiday"},
	})
	info.AddField("名称", "name", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("开始时间", "start_time", db.Varchar)
	info.AddField("结束时间", "end_time", db.Varchar)
	info.AddField("排序", "sort_order", db.Int).FieldSortable()
	info.AddField("状态", "is_active", db.Tinyint).FieldDisplay(func(m types.FieldModel) interface{} {
		if m.Value == "1" {
			return "启用"
		}
		return "禁用"
	}).FieldFilterable(types.FilterType{FormType: form.SelectSingle}).FieldFilterOptions(types.FieldOptions{
		{Text: "启用", Value: "1"},
		{Text: "禁用", Value: "0"},
	})
	info.AddField("创建时间", "created_at", db.Timestamp)
	info.SetTable("schedule_periods").SetTitle("作息时间配置").SetDescription("管理课程时段")

	// 表单页
	formList := periodTable.GetForm()
	formList.AddField("ID", "id", db.Int, form.Default).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("企业名称", "tenant_id", db.Int, form.SelectSingle).
		FieldOptions(getTenantOptionsForSchedulePeriods()).
		FieldMust().
		FieldDisplayButCanNotEditWhenUpdate()
	formList.AddField("模式", "mode", db.Varchar, form.SelectSingle).
		FieldOptions(types.FieldOptions{
			{Text: "夏季上学", Value: "school_summer"},
			{Text: "冬季上学", Value: "school_winter"},
			{Text: "假期", Value: "holiday"},
		}).
		FieldDefault("school_summer").
		FieldMust().
		FieldHelpMsg("选择配置所属的模式")
	formList.AddField("名称", "name", db.Varchar, form.Text).
		FieldMust().
		FieldHelpMsg("上学模式：第1-2节；假期模式：上午、下午、晚上")
	formList.AddField("开始时间", "start_time", db.Varchar, form.Text).
		FieldMust().
		FieldHelpMsg("格式：HH:MM:SS，例如：08:00:00")
	formList.AddField("结束时间", "end_time", db.Varchar, form.Text).
		FieldMust().
		FieldHelpMsg("格式：HH:MM:SS，例如：09:40:00")
	formList.AddField("排序", "sort_order", db.Int, form.Number).
		FieldDefault("0").
		FieldHelpMsg("数字越小越靠前")
	formList.AddField("状态", "is_active", db.Tinyint, form.Radio).
		FieldOptions(types.FieldOptions{
			{Text: "启用", Value: "1"},
			{Text: "禁用", Value: "0"},
		}).
		FieldDefault("1").
		FieldMust()

	// 自动填充时间戳
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

	formList.AddField("创建时间", "created_at", db.Timestamp, form.Datetime).
		FieldDisableWhenCreate().
		FieldDisplayButCanNotEditWhenUpdate()
	formList.AddField("更新时间", "updated_at", db.Timestamp, form.Datetime).
		FieldDisableWhenCreate().
		FieldDisplayButCanNotEditWhenUpdate()
	formList.SetTable("schedule_periods").SetTitle("作息时间配置").SetDescription("管理课程时段")

	return
}

// getTenantNameForSchedulePeriods 根据租户ID获取名称
func getTenantNameForSchedulePeriods(tenantID string) string {
	if tenantID == "" {
		return ""
	}
	var name string
	global.DB.Table("tenants").Select("name").Where("id = ?", tenantID).Scan(&name)
	return name
}

// getTenantOptionsForSchedulePeriods 从数据库加载租户选项
func getTenantOptionsForSchedulePeriods() types.FieldOptions {
	var tenants []struct {
		ID   uint
		Name string
	}
	if err := global.DB.Table("tenants").Select("id, name").Find(&tenants).Error; err != nil {
		return types.FieldOptions{}
	}
	opts := make(types.FieldOptions, 0, len(tenants))
	for _, t := range tenants {
		opts = append(opts, types.FieldOption{
			Text:  t.Name,
			Value: strconv.FormatUint(uint64(t.ID), 10),
		})
	}
	return opts
}

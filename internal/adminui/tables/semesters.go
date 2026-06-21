package tables

import (
	"errors"
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

// GetSemesterTable semesters 表的 CRUD 配置
func GetSemesterTable(ctx *context.Context) (semesterTable table.Table) {
	semesterTable = table.NewDefaultTable(ctx, table.Config{
		Driver:     db.DriverMysql,
		CanAdd:     true,
		Editable:   true,
		Deletable:  false,
		Exportable: true,
		Connection: table.DefaultConnectionName,
		PrimaryKey: table.PrimaryKey{
			Type: db.Int,
			Name: table.DefaultPrimaryKeyName,
		},
	})

	// 列表页
	info := semesterTable.GetInfo()
	info.AddField("ID", "id", db.Int).FieldSortable()
	info.AddField("租户", "tenant_id", db.Int).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return getTenantName(m.Value)
		}).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(getTenantOptions())
	info.AddField("学期名称", "name", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("开始日期", "start_date", db.Date)
	info.AddField("总周数", "total_weeks", db.Int)
	info.AddField("是否激活", "is_active", db.Tinyint).FieldDisplay(func(m types.FieldModel) interface{} {
		if m.Value == "1" {
			return "是"
		}
		return "否"
	}).FieldFilterable(types.FilterType{FormType: form.SelectSingle}).FieldFilterOptions(types.FieldOptions{
		{Text: "是", Value: "1"},
		{Text: "否", Value: "0"},
	})
	info.AddField("CreatedAt", "created_at", db.Timestamp)
	info.AddField("UpdatedAt", "updated_at", db.Timestamp)
	info.SetTable("semesters").SetTitle("学期管理").SetDescription("学期配置")

	// 表单页
	formList := semesterTable.GetForm()
	formList.AddField("ID", "id", db.Int, form.Default).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("租户", "tenant_id", db.Int, form.SelectSingle).
		FieldMust().
		FieldOptions(getTenantOptions())
	formList.AddField("学期名称", "name", db.Varchar, form.Text).FieldMust()
	formList.AddField("开始日期", "start_date", db.Date, form.Date).FieldMust().
		FieldHelpMsg("必须为周一")
	formList.AddField("总周数", "total_weeks", db.Int, form.Number).FieldMust().FieldDefault("20")
	formList.AddField("是否激活", "is_active", db.Tinyint, form.Radio).FieldOptions(types.FieldOptions{
		{Text: "是", Value: "1"},
		{Text: "否", Value: "0"},
	}).FieldDefault("0").FieldMust()

	// 校验开始日期必须为周一
	formList.SetPostValidator(func(values form2.Values) error {
		startDateStr := values.Get("start_date")
		if startDateStr == "" {
			return nil
		}
		startDate, err := time.ParseInLocation("2006-01-02", startDateStr, time.Local)
		if err != nil {
			return errors.New("开始日期格式错误")
		}
		if startDate.Weekday() != time.Monday {
			return errors.New("开始日期必须为周一")
		}
		return nil
	})

	// 自动补齐时间戳 + 激活时自动取消同租户其他学期
	formList.SetPostHook(func(values form2.Values) error {
		isActive := values.Get("is_active")
		if isActive != "1" {
			return nil
		}

		tenantIDStr := values.Get("tenant_id")
		tenantID, _ := strconv.Atoi(tenantIDStr)
		if tenantID == 0 {
			return nil
		}

		// 获取当前记录ID
		idStr := values.Get("id")
		currentID, _ := strconv.Atoi(idStr)

		// 取消同租户其他学期的激活状态 (使用 GORM)
		return global.DB.Table("semesters").
			Where("tenant_id = ? AND id != ?", tenantID, currentID).
			Update("is_active", 0).Error
	})

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
	formList.SetTable("semesters").SetTitle("学期管理").SetDescription("学期配置")

	return
}

// getTenantOptions 从数据库加载租户选项
func getTenantOptions() types.FieldOptions {
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

// getTenantName 根据租户ID获取名称
func getTenantName(tenantID string) string {
	if tenantID == "" {
		return ""
	}
	var name string
	global.DB.Table("tenants").Select("name").Where("id = ?", tenantID).Scan(&name)
	return name
}

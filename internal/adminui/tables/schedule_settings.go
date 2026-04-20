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

// GetScheduleSettingTable 作息设置表的 CRUD 配置
func GetScheduleSettingTable(ctx *context.Context) (settingTable table.Table) {
	settingTable = table.NewDefaultTable(ctx, table.Config{
		Driver:     db.DriverMysql,
		CanAdd:     true,
		Editable:   true,
		Deletable:  false,
		Exportable: false,
		Connection: table.DefaultConnectionName,
		PrimaryKey: table.PrimaryKey{
			Type: db.Int,
			Name: table.DefaultPrimaryKeyName,
		},
	})

	// 列表页
	info := settingTable.GetInfo()
	info.AddField("ID", "id", db.Int).FieldSortable()
	info.AddField("企业名称", "tenant_id", db.Int).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return getTenantNameForScheduleSettings(m.Value)
		})
	info.AddField("当前模式", "current_mode", db.Varchar).FieldDisplay(func(m types.FieldModel) interface{} {
		switch m.Value {
		case "school":
			return "上学模式"
		case "holiday":
			return "假期模式"
		default:
			return m.Value
		}
	})
	info.AddField("考勤开关", "attendance_enabled", db.Tinyint).FieldDisplay(func(m types.FieldModel) interface{} {
		if m.Value == "1" || m.Value == "true" {
			return "✅ 已开启"
		}
		return "❌ 已关闭"
	})
	info.AddField("课表变更通知", "schedule_change_notify_enabled", db.Tinyint).FieldDisplay(func(m types.FieldModel) interface{} {
		if m.Value == "1" || m.Value == "true" {
			return "✅ 已开启"
		}
		return "❌ 已关闭"
	})
	info.AddField("迟到提醒通知", "late_notify_enabled", db.Tinyint).FieldDisplay(func(m types.FieldModel) interface{} {
		if m.Value == "1" || m.Value == "true" {
			return "✅ 已开启"
		}
		return "❌ 已关闭"
	})
	info.AddField("休息日参与考勤", "rest_day_attendance_enabled", db.Tinyint).FieldDisplay(func(m types.FieldModel) interface{} {
		if m.Value == "1" || m.Value == "true" {
			return "✅ 已开启"
		}
		return "❌ 已关闭"
	})
	info.AddField("更新时间", "updated_at", db.Timestamp)
	info.SetTable("schedule_settings").SetTitle("作息与考勤设置").SetDescription("管理作息模式和考勤开关")

	// 表单页
	formList := settingTable.GetForm()
	formList.AddField("ID", "id", db.Int, form.Default).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("企业名称", "tenant_id", db.Int, form.SelectSingle).
		FieldOptions(getTenantOptionsForScheduleSettings()).
		FieldMust().
		FieldDisplayButCanNotEditWhenUpdate()
	formList.AddField("当前模式", "current_mode", db.Varchar, form.SelectSingle).
		FieldOptions(types.FieldOptions{
			{Text: "上学模式", Value: "school"},
			{Text: "假期模式", Value: "holiday"},
		}).
		FieldDefault("school").
		FieldMust().
		FieldHelpMsg("选择当前生效的作息模式")
	formList.AddField("考勤开关", "attendance_enabled", db.Tinyint, form.Switch).
		FieldOptions(types.FieldOptions{
			{Text: "开启", Value: "1"},
			{Text: "关闭", Value: "0"},
		}).
		FieldDefault("1").
		FieldHelpMsg("关闭后将停止自动考勤统计")
	formList.AddField("课表变更通知", "schedule_change_notify_enabled", db.Tinyint, form.Switch).
		FieldOptions(types.FieldOptions{
			{Text: "开启", Value: "1"},
			{Text: "关闭", Value: "0"},
		}).
		FieldDefault("1").
		FieldHelpMsg("关闭后导入/更新课表时不再发送钉钉通知")
	formList.AddField("迟到提醒通知", "late_notify_enabled", db.Tinyint, form.Switch).
		FieldOptions(types.FieldOptions{
			{Text: "开启", Value: "1"},
			{Text: "关闭", Value: "0"},
		}).
		FieldDefault("1").
		FieldHelpMsg("关闭后考勤统计时不再发送迟到提醒")
	formList.AddField("休息日参与考勤", "rest_day_attendance_enabled", db.Tinyint, form.Switch).
		FieldOptions(types.FieldOptions{
			{Text: "开启", Value: "1"},
			{Text: "关闭", Value: "0"},
		}).
		FieldDefault("1").
		FieldHelpMsg("关闭后保留个人休息日数据，但考勤统计时忽略休息日")

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

	formList.AddField("更新时间", "updated_at", db.Timestamp, form.Datetime).
		FieldDisableWhenCreate().
		FieldDisplayButCanNotEditWhenUpdate()
	formList.SetTable("schedule_settings").SetTitle("作息与考勤设置").SetDescription("管理作息模式和考勤开关")

	return
}

// getTenantOptionsForScheduleSettings 从数据库加载租户选项
func getTenantOptionsForScheduleSettings() types.FieldOptions {
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

// getTenantNameForScheduleSettings 根据租户ID获取名称
func getTenantNameForScheduleSettings(tenantID string) string {
	if tenantID == "" {
		return ""
	}
	var name string
	global.DB.Table("tenants").Select("name").Where("id = ?", tenantID).Scan(&name)
	return name
}

package tables

import (
	"encoding/json"
	"fmt"
	"strings"

	"schedule_server/global"

	"github.com/GoAdminGroup/go-admin/context"
	"github.com/GoAdminGroup/go-admin/modules/db"
	form2 "github.com/GoAdminGroup/go-admin/plugins/admin/modules/form"
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
			return formatGroupAttendanceSubscriptionDepartments(tenantIDFromFieldModel(m), m.Value)
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
	formList.SetPreProcessFn(preprocessGroupAttendanceSubscriptionFormValues)
	formList.AddField("ID", "id", db.Int, form.Default).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("租户", "tenant_id", db.Int, form.Default).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("群名称", "group_name", db.Varchar, form.Text).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("会话ID", "conversation_id", db.Varchar, form.Text).
		FieldDisplayButCanNotEditWhenUpdate().FieldDisableWhenCreate()
	formList.AddField("订阅部门", "dept_ids_json", db.Text, form.TextArea).
		FieldDisplay(func(m types.FieldModel) interface{} {
			return formatGroupAttendanceSubscriptionDepartments(tenantIDFromFieldModel(m), m.Value)
		}).
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

func preprocessGroupAttendanceSubscriptionFormValues(values form2.Values) form2.Values {
	if !values.IsUpdatePost() && !values.IsSingleUpdatePost() {
		return values
	}
	for _, key := range []string{
		"tenant_id",
		"group_name",
		"conversation_id",
		"dept_ids_json",
		"enabled_by_uid",
		"created_at",
		"deleted_at",
	} {
		values.Delete(key)
	}
	return values
}

func tenantIDFromFieldModel(m types.FieldModel) string {
	for _, key := range []string{"tenant_id", "group_attendance_subscriptions.tenant_id"} {
		if v, ok := m.Row[key]; ok {
			return fmt.Sprint(v)
		}
	}
	return ""
}

func formatGroupAttendanceSubscriptionDepartments(tenantID string, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || raw == "[]" {
		return "全部部门"
	}

	var deptIDs []int64
	if err := json.Unmarshal([]byte(raw), &deptIDs); err != nil {
		return raw
	}
	if len(deptIDs) == 0 {
		return "全部部门"
	}
	if global.DB == nil || strings.TrimSpace(tenantID) == "" {
		return formatUnknownDepartmentIDs(deptIDs)
	}

	var rows []struct {
		DeptID int64
		Name   string
	}
	if err := global.DB.Table("departments").
		Select("dept_id, name").
		Where("tenant_id = ? AND dept_id IN ?", tenantID, deptIDs).
		Find(&rows).Error; err != nil {
		return formatUnknownDepartmentIDs(deptIDs)
	}

	namesByID := make(map[int64]string, len(rows))
	for _, row := range rows {
		namesByID[row.DeptID] = row.Name
	}

	parts := make([]string, 0, len(deptIDs))
	for _, deptID := range deptIDs {
		if name := strings.TrimSpace(namesByID[deptID]); name != "" {
			parts = append(parts, fmt.Sprintf("%s（%d）", name, deptID))
			continue
		}
		parts = append(parts, fmt.Sprintf("未知部门（%d）", deptID))
	}
	return strings.Join(parts, "、")
}

func formatUnknownDepartmentIDs(deptIDs []int64) string {
	parts := make([]string, 0, len(deptIDs))
	for _, deptID := range deptIDs {
		parts = append(parts, fmt.Sprintf("未知部门（%d）", deptID))
	}
	return strings.Join(parts, "、")
}

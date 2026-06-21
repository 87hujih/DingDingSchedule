package tables

import (
	"github.com/GoAdminGroup/go-admin/context"
	"github.com/GoAdminGroup/go-admin/modules/db"
	"github.com/GoAdminGroup/go-admin/plugins/admin/modules/table"
	"github.com/GoAdminGroup/go-admin/template/types"
	"github.com/GoAdminGroup/go-admin/template/types/form"
)

// GetSystemLogTable system_logs 表的只读配置
func GetSystemLogTable(ctx *context.Context) (t table.Table) {
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
	info.AddField("级别", "level", db.Varchar).
		FieldFilterable(types.FilterType{FormType: form.SelectSingle}).
		FieldFilterOptions(types.FieldOptions{
			{Text: "error", Value: "error"},
			{Text: "dpanic", Value: "dpanic"},
			{Text: "panic", Value: "panic"},
			{Text: "fatal", Value: "fatal"},
		})
	info.AddField("调用位置", "caller", db.Varchar).
		FieldFilterable(types.FilterType{Operator: types.FilterOperatorLike})
	info.AddField("日志内容", "message", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			runes := []rune(m.Value)
			if len(runes) > 80 {
				return string(runes[:80]) + "..."
			}
			return m.Value
		})
	info.AddField("结构化字段", "fields", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			runes := []rune(m.Value)
			if len(runes) > 100 {
				return string(runes[:100]) + "..."
			}
			return m.Value
		})
	info.AddField("堆栈", "stack", db.Text).
		FieldDisplay(func(m types.FieldModel) interface{} {
			runes := []rune(m.Value)
			if len(runes) > 60 {
				return string(runes[:60]) + "..."
			}
			return m.Value
		})
	info.AddField("时间", "created_at", db.Timestamp).FieldSortable().
		FieldFilterable(types.FilterType{FormType: form.DatetimeRange})

	info.SetTable("system_logs").SetTitle("系统错误日志").SetDescription("Error 级别及以上的运行时日志")

	return
}

package tables

import (
	"testing"

	"schedule_server/global"
	"schedule_server/internal/model"

	admincontext "github.com/GoAdminGroup/go-admin/context"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAgentCallLogTableExposesCompilerAndEntityDiagnosticsAsFilters(t *testing.T) {
	previousDB := global.DB
	database, err := gorm.Open(sqlite.Open("file:admin-agent-call-log?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := database.AutoMigrate(&model.Tenant{}); err != nil {
		t.Fatalf("migrate tenants: %v", err)
	}
	global.DB = database
	t.Cleanup(func() { global.DB = previousDB })

	info := GetAgentCallLogTable(&admincontext.Context{}).GetInfo()
	for _, fieldName := range []string{
		"compiler_status",
		"compiler_source",
		"compiler_fallback_reason",
		"entity_resolution_status",
	} {
		field := info.FieldList.GetFieldByFieldName(fieldName)
		if field.Field != fieldName {
			t.Fatalf("field %q is not exposed in agent call log table", fieldName)
		}
		if !field.Filterable {
			t.Fatalf("field %q is not filterable", fieldName)
		}
	}
}

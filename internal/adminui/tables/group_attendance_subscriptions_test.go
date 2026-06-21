package tables

import (
	"os"
	"strings"
	"testing"

	"schedule_server/global"
	"schedule_server/internal/model"

	form2 "github.com/GoAdminGroup/go-admin/plugins/admin/modules/form"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGroupAttendanceSubscriptionTableRegistered(t *testing.T) {
	if _, ok := Generators["group_attendance_subscriptions"]; !ok {
		t.Fatalf("Generators missing group_attendance_subscriptions")
	}
}

func TestGroupAttendanceSubscriptionTableSourceKeepsOnlyPushSwitchEditable(t *testing.T) {
	source, err := os.ReadFile("group_attendance_subscriptions.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"CanAdd:     false",
		"Editable:   true",
		"Deletable:  false",
		`form.Switch`,
		`"push_enabled"`,
		`FieldDisplayButCanNotEditWhenUpdate()`,
		`WhereRaw("deleted_at IS NULL")`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("source missing %q", want)
		}
	}
}

func TestFormatGroupAttendanceSubscriptionDepartments(t *testing.T) {
	prevDB := global.DB
	db, err := gorm.Open(sqlite.Open("file:admin-group-sub-dept-display?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&model.Department{}); err != nil {
		t.Fatalf("migrate departments: %v", err)
	}
	if err := db.Create([]model.Department{
		{TenantID: 42, DeptID: 101, Name: "信工25级", Status: 1},
		{TenantID: 42, DeptID: 102, Name: "家族7期", Status: 1},
		{TenantID: 7, DeptID: 101, Name: "其他租户部门", Status: 1},
	}).Error; err != nil {
		t.Fatalf("seed departments: %v", err)
	}
	global.DB = db
	t.Cleanup(func() {
		global.DB = prevDB
	})

	tests := []struct {
		name     string
		tenantID string
		raw      string
		want     string
	}{
		{name: "empty means all departments", tenantID: "42", raw: "", want: "全部部门"},
		{name: "null means all departments", tenantID: "42", raw: "null", want: "全部部门"},
		{name: "empty array means all departments", tenantID: "42", raw: "[]", want: "全部部门"},
		{name: "maps ids to department names in order", tenantID: "42", raw: "[102,101]", want: "家族7期（102）、信工25级（101）"},
		{name: "unknown ids keep visible fallback", tenantID: "42", raw: "[999]", want: "未知部门（999）"},
		{name: "bad json keeps raw value visible", tenantID: "42", raw: "not-json", want: "not-json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatGroupAttendanceSubscriptionDepartments(tt.tenantID, tt.raw); got != tt.want {
				t.Fatalf("formatGroupAttendanceSubscriptionDepartments(%q, %q) = %q, want %q", tt.tenantID, tt.raw, got, tt.want)
			}
		})
	}
}

func TestPreprocessGroupAttendanceSubscriptionUpdateDropsReadonlyFields(t *testing.T) {
	values := form2.Values{
		form2.PostTypeKey: {"0"},
		"id":              {"10"},
		"tenant_id":       {"42"},
		"group_name":      {"不应更新的群名"},
		"conversation_id": {"cid"},
		"dept_ids_json":   {"[101]"},
		"enabled_by_uid":  {"7"},
		"created_at":      {""},
		"deleted_at":      {""},
		"push_enabled":    {"0"},
	}

	got := preprocessGroupAttendanceSubscriptionFormValues(values)
	if got.Get("push_enabled") != "0" {
		t.Fatalf("push_enabled = %q, want 0", got.Get("push_enabled"))
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
		if _, ok := got[key]; ok {
			t.Fatalf("readonly key %q still present in preprocessed values: %#v", key, got)
		}
	}
}

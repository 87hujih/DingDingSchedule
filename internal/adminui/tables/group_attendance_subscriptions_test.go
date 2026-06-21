package tables

import (
	"os"
	"strings"
	"testing"
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

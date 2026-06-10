package agent

import (
	"reflect"
	"testing"
)

func TestLookupOperationCatalogMetadata(t *testing.T) {
	t.Parallel()

	if _, ok := lookupOperation("manual_sign.create"); ok {
		t.Fatalf("manual_sign.create must not be in protocol_live operation catalog")
	}

	capability, ok := lookupOperation("manual_sign.describe_capability")
	if !ok {
		t.Fatalf("lookupOperation(manual_sign.describe_capability) = not found")
	}
	if capability.Domain != DomainManualSign {
		t.Fatalf("manual_sign.describe_capability domain = %q, want %q", capability.Domain, DomainManualSign)
	}
	if capability.IsWrite {
		t.Fatalf("manual_sign.describe_capability IsWrite = true, want false")
	}
	if !reflect.DeepEqual(capability.AllowedActs, []UserAct{ActCapabilityQuestion}) {
		t.Fatalf("manual_sign.describe_capability AllowedActs = %v, want [capability_question]", capability.AllowedActs)
	}

	attendance, ok := lookupOperation("attendance.query_status")
	if !ok {
		t.Fatalf("lookupOperation(attendance.query_status) = not found")
	}
	if attendance.Domain != DomainAttendance {
		t.Fatalf("attendance.query_status domain = %q, want %q", attendance.Domain, DomainAttendance)
	}
	if attendance.IsWrite {
		t.Fatalf("attendance.query_status IsWrite = true, want false")
	}
	if !reflect.DeepEqual(attendance.AllowedActs, []UserAct{ActReadQuery}) {
		t.Fatalf("attendance.query_status AllowedActs = %v, want [read_query]", attendance.AllowedActs)
	}
}

func TestOperationCatalogMatchesProtocolLiveWhitelist(t *testing.T) {
	t.Parallel()

	want := []string{
		"attendance.query_status",
		"subscription.start",
		"subscription.cancel",
		"subscription.query_status",
		"subscription.list_departments",
		"system.describe_capability",
		"attendance.describe_capability",
		"schedule.describe_capability",
		"subscription.describe_capability",
		"manual_sign.describe_capability",
		"attendance.rule_explain",
		"schedule.rule_explain",
		"subscription.rule_explain",
		"schedule.query_my_schedule",
		"schedule.query_user_schedule",
	}
	if got := operationNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("operationNames() = %v, want %v", got, want)
	}
}

func TestOperationCatalogAttendanceQueryShapes(t *testing.T) {
	t.Parallel()

	metadata, ok := lookupOperation("attendance.query_status")
	if !ok {
		t.Fatal("attendance.query_status missing")
	}
	assertQueryShape(t, metadata, "slot_status", []string{"date", "section"})
	assertQueryShape(t, metadata, "user_day_status", []string{"date", "user_id"})
}

func TestOperationCatalogSubscriptionWritesRequireAdminRole(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"subscription.start", "subscription.cancel"} {
		metadata, ok := lookupOperation(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if !metadata.IsWrite {
			t.Fatalf("%s IsWrite = false, want true", name)
		}
		if metadata.MinRole != 1 {
			t.Fatalf("%s MinRole = %d, want 1", name, metadata.MinRole)
		}
	}
}

func assertQueryShape(t *testing.T, metadata OperationMetadata, name string, requiredParams []string) {
	t.Helper()

	for _, shape := range metadata.QueryShapes {
		if shape.Name != name {
			continue
		}
		if !reflect.DeepEqual(shape.RequiredTrustedParams, requiredParams) {
			t.Fatalf("%s query shape %s RequiredTrustedParams = %v, want %v",
				metadata.Name, name, shape.RequiredTrustedParams, requiredParams)
		}
		return
	}
	t.Fatalf("%s query shape %s missing", metadata.Name, name)
}

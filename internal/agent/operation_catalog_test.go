package agent

import (
	"reflect"
	"testing"
)

func TestLookupOperationCatalogMetadata(t *testing.T) {
	t.Parallel()

	manualSign, ok := lookupOperation("manual_sign.create")
	if !ok {
		t.Fatalf("lookupOperation(manual_sign.create) = not found")
	}
	if manualSign.Domain != DomainManualSign {
		t.Fatalf("manual_sign.create domain = %q, want %q", manualSign.Domain, DomainManualSign)
	}
	if !manualSign.IsWrite {
		t.Fatalf("manual_sign.create IsWrite = false, want true")
	}
	if !reflect.DeepEqual(manualSign.AllowedActs, []UserAct{ActWriteRequest}) {
		t.Fatalf("manual_sign.create AllowedActs = %v, want [write_request]", manualSign.AllowedActs)
	}
	if !reflect.DeepEqual(manualSign.RequiredTrustedParams, []string{"user_id", "date", "section"}) {
		t.Fatalf("manual_sign.create RequiredTrustedParams = %v, want [user_id date section]", manualSign.RequiredTrustedParams)
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

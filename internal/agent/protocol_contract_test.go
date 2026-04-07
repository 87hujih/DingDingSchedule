package agent

import (
	"reflect"
	"testing"
)

func TestOperationCatalogWriteAllowlist(t *testing.T) {
	t.Parallel()

	got := writeOperations()
	want := []string{
		"subscription.start",
		"subscription.cancel",
		"manual_sign.create",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("writeOperations() = %v, want %v", got, want)
	}
}

func TestProtocolModesAllowlist(t *testing.T) {
	t.Parallel()

	modes := protocolModes()
	want := []ProtocolMode{
		ProtocolModeLegacy,
		ProtocolModeShadow,
		ProtocolModeLive,
	}
	if !reflect.DeepEqual(modes, want) {
		t.Fatalf("protocolModes() = %v, want %v", modes, want)
	}
}

func TestUserActsAllowlist(t *testing.T) {
	t.Parallel()

	got := userActs()
	want := []UserAct{
		ActCapabilityQuestion,
		ActRuleQuestion,
		ActReadQuery,
		ActWriteRequest,
		ActWorkflowContinue,
		ActWorkflowCancel,
		ActHelp,
		ActUnknown,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("userActs() = %v, want %v", got, want)
	}
}

func TestBusinessDomainsAllowlist(t *testing.T) {
	t.Parallel()

	got := businessDomains()
	want := []BusinessDomain{
		DomainAttendance,
		DomainSubscription,
		DomainManualSign,
		DomainSchedule,
		DomainLeave,
		DomainAnalytics,
		DomainUnknown,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("businessDomains() = %v, want %v", got, want)
	}
}

func TestNormalizeProtocolModeDefaultsInvalidValueToLegacy(t *testing.T) {
	t.Parallel()

	if got := normalizeProtocolMode("definitely-invalid"); got != ProtocolModeLegacy {
		t.Fatalf("normalizeProtocolMode() = %q, want %q", got, ProtocolModeLegacy)
	}
}

package agent

import (
	"reflect"
	"testing"
)

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

func TestNormalizeProtocolModeDefaultsInvalidValueToLegacy(t *testing.T) {
	t.Parallel()

	if got := normalizeProtocolMode("definitely-invalid"); got != ProtocolModeLegacy {
		t.Fatalf("normalizeProtocolMode() = %q, want %q", got, ProtocolModeLegacy)
	}
}

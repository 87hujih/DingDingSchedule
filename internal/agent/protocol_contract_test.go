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

func TestResponseKindsExcludeConfirm(t *testing.T) {
	t.Parallel()

	kinds := responseKinds()
	if containsResponseKind(kinds, ResponseKind("confirm")) {
		t.Fatalf("response kinds must not include confirm: %v", kinds)
	}
	want := []ResponseKind{
		ResponseAnswer,
		ResponseResult,
		ResponseClarify,
		ResponseSelectOptions,
		ResponseRefuse,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("responseKinds() = %v, want %v", kinds, want)
	}
}

func TestOperationRequestTrustedParamsAreTyped(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeOf(OperationRequest{}).FieldByName("TrustedParams")
	if !ok {
		t.Fatalf("OperationRequest.TrustedParams field missing")
	}
	if field.Type.Kind() != reflect.Map || field.Type.Key().Kind() != reflect.String {
		t.Fatalf("TrustedParams type = %v, want map[string]TrustedParam", field.Type)
	}
	if field.Type.Elem() != reflect.TypeOf(TrustedParam{}) {
		t.Fatalf("TrustedParams elem = %v, want TrustedParam", field.Type.Elem())
	}
}

func containsResponseKind(kinds []ResponseKind, target ResponseKind) bool {
	for _, kind := range kinds {
		if kind == target {
			return true
		}
	}
	return false
}

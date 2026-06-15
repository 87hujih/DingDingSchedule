package agent

import (
	"path/filepath"
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

func TestOperationCatalogLintPasses(t *testing.T) {
	t.Parallel()

	if errs := lintOperationCatalog(operationManifests()); len(errs) > 0 {
		t.Fatalf("lintOperationCatalog() errors = %v", errs)
	}
}

func TestOperationCatalogWriteManifestsDeclareSafetyBindings(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"subscription.start", "subscription.cancel"} {
		manifest, ok := lookupOperation(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if manifest.Risk != RiskWriteLow {
			t.Fatalf("%s Risk = %q, want %q", name, manifest.Risk, RiskWriteLow)
		}
		if manifest.Scope != ConversationScopeGroup {
			t.Fatalf("%s Scope = %q, want %q", name, manifest.Scope, ConversationScopeGroup)
		}
		if manifest.Workflow == nil {
			t.Fatalf("%s Workflow = nil, want declared workflow boundary", name)
		}
		if len(manifest.Policies) == 0 {
			t.Fatalf("%s Policies = empty, want policy binding", name)
		}
		if manifest.Executor.Name == "" {
			t.Fatalf("%s Executor.Name is empty", name)
		}
		if len(manifest.Idempotency.KeyFields) == 0 {
			t.Fatalf("%s Idempotency.KeyFields = empty", name)
		}
	}
}

func TestOperationCatalogEveryManifestHasRendererAndEvalBinding(t *testing.T) {
	t.Parallel()

	for _, manifest := range operationManifests() {
		if manifest.Renderer.Name == "" {
			t.Fatalf("%s Renderer.Name is empty", manifest.Name)
		}
		if len(manifest.Eval.CaseIDs) == 0 {
			t.Fatalf("%s Eval.CaseIDs is empty", manifest.Name)
		}
	}
}

func TestOperationCatalogEvalCaseIDsExistInFixture(t *testing.T) {
	t.Parallel()

	cases, err := LoadEvalCases(filepath.Join("testdata", "eval_cases.json"))
	if err != nil {
		t.Fatalf("LoadEvalCases() error = %v", err)
	}
	seen := make(map[string]struct{}, len(cases))
	for _, tc := range cases {
		seen[tc.Name] = struct{}{}
	}
	for _, manifest := range operationManifests() {
		for _, caseID := range manifest.Eval.CaseIDs {
			if _, ok := seen[caseID]; !ok {
				t.Fatalf("%s Eval.CaseIDs contains %q but fixture has no such case", manifest.Name, caseID)
			}
		}
	}
}

func TestOperationCatalogCapabilityOperationsCarryCapabilityBinding(t *testing.T) {
	t.Parallel()

	for _, domain := range []BusinessDomain{
		DomainSystem,
		DomainAttendance,
		DomainSchedule,
		DomainSubscription,
		DomainManualSign,
	} {
		name := capabilityOperationForDomain(domain)
		manifest, ok := lookupOperation(name)
		if !ok {
			t.Fatalf("capability operation for domain %q = %q not found", domain, name)
		}
		if manifest.Domain != domain {
			t.Fatalf("%s Domain = %q, want %q", name, manifest.Domain, domain)
		}
		if manifest.Capability == nil {
			t.Fatalf("%s Capability = nil, want catalog capability binding", name)
		}
		if manifest.Executor.Name == "" || manifest.Renderer.Name == "" {
			t.Fatalf("%s executor/renderer binding missing: %+v %+v", name, manifest.Executor, manifest.Renderer)
		}
	}
}

func assertQueryShape(t *testing.T, metadata OperationMetadata, name string, requiredParams []string) {
	t.Helper()

	for _, shape := range metadata.QueryShapes {
		if shape.Name != name {
			continue
		}
		got := paramNames(shape.RequiredTrustedParams)
		if !reflect.DeepEqual(got, requiredParams) {
			t.Fatalf("%s query shape %s RequiredTrustedParams = %v, want %v",
				metadata.Name, name, got, requiredParams)
		}
		return
	}
	t.Fatalf("%s query shape %s missing", metadata.Name, name)
}

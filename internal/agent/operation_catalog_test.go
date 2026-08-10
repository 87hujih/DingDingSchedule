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

	tests := []struct {
		name      string
		guarantee IdempotencyGuarantee
	}{
		{name: "subscription.start", guarantee: IdempotencyGuaranteeRepositoryUniqueUpsert},
		{name: "subscription.cancel", guarantee: IdempotencyGuaranteeRepositorySoftDelete},
	}
	for _, tt := range tests {
		name := tt.name
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
		if manifest.Idempotency.Guarantee != tt.guarantee {
			t.Fatalf("%s Idempotency.Guarantee = %q, want %q", name, manifest.Idempotency.Guarantee, tt.guarantee)
		}
	}
}

func TestOperationCatalogEveryManifestHasRendererAndEvalBinding(t *testing.T) {
	t.Parallel()

	for _, manifest := range operationManifests() {
		if manifest.Recognition.Description == "" {
			t.Fatalf("%s Recognition.Description is empty", manifest.Name)
		}
		if manifest.Renderer.Name == "" {
			t.Fatalf("%s Renderer.Name is empty", manifest.Name)
		}
		if len(manifest.Eval.CaseIDs) == 0 {
			t.Fatalf("%s Eval.CaseIDs is empty", manifest.Name)
		}
	}
}

func TestOperationCatalogEveryManifestDeclaresProtocolLiveRuntimeBindings(t *testing.T) {
	t.Parallel()

	for _, manifest := range operationManifests() {
		if manifest.Dispatch.Name == "" {
			t.Fatalf("%s Dispatch.Name is empty", manifest.Name)
		}
		if _, ok := lookupProtocolLiveDispatch(manifest.Dispatch.Name); !ok {
			t.Fatalf("%s Dispatch.Name %q has no protocol_live dispatch binding", manifest.Name, manifest.Dispatch.Name)
		}
		if manifest.Workflow == nil {
			t.Fatalf("%s Workflow is nil", manifest.Name)
		}
		if manifest.Workflow.Mode == "" {
			t.Fatalf("%s Workflow.Mode is empty", manifest.Name)
		}
		if manifest.Executor.Name == "" {
			t.Fatalf("%s Executor.Name is empty", manifest.Name)
		}
		if manifest.Renderer.Name == "" {
			t.Fatalf("%s Renderer.Name is empty", manifest.Name)
		}
		if manifest.IsWrite {
			if manifest.WriteGuard.Name != WriteGuardBindingDefault {
				t.Fatalf("%s WriteGuard.Name = %q, want %q", manifest.Name, manifest.WriteGuard.Name, WriteGuardBindingDefault)
			}
			continue
		}
		if manifest.WriteGuard.Name != WriteGuardBindingNotRequired {
			t.Fatalf("%s WriteGuard.Name = %q, want %q", manifest.Name, manifest.WriteGuard.Name, WriteGuardBindingNotRequired)
		}
	}
}

func TestOperationCatalogSubscriptionOperationsDeclareRecognition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		operation string
		aliases   []string
	}{
		{
			operation: "subscription.start",
			aliases:   []string{"开启考勤订阅", "打开本群考勤推送"},
		},
		{
			operation: "subscription.cancel",
			aliases:   []string{"取消考勤推送", "关闭考勤订阅"},
		},
		{
			operation: "subscription.list_departments",
			aliases:   []string{"当前都有哪些部门", "部门列表"},
		},
		{
			operation: "subscription.query_status",
			aliases:   []string{"查本群订阅状态", "当前群有没有订阅"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.operation, func(t *testing.T) {
			t.Parallel()

			manifest, ok := lookupOperation(tt.operation)
			if !ok {
				t.Fatalf("%s missing", tt.operation)
			}
			if len(manifest.Recognition.Aliases) == 0 {
				t.Fatalf("%s Recognition.Aliases is empty", tt.operation)
			}
			for _, alias := range tt.aliases {
				if !recognitionContainsAlias(manifest.Recognition, alias) {
					t.Fatalf("%s Recognition.Aliases = %v, want alias %q", tt.operation, manifest.Recognition.Aliases, alias)
				}
			}
		})
	}
}

func TestOperationCatalogScheduleOperationsDeclareLanguageContract(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"schedule.query_my_schedule", "schedule.query_user_schedule"} {
		manifest, ok := lookupOperation(operation)
		if !ok {
			t.Fatalf("%s missing", operation)
		}
		if manifest.Recognition.Description == "" {
			t.Fatalf("%s recognition description is empty", operation)
		}
		if len(manifest.Recognition.Examples) == 0 || len(manifest.Recognition.NegativeExamples) == 0 {
			t.Fatalf("%s examples=%v negative=%v, want both", operation, manifest.Recognition.Examples, manifest.Recognition.NegativeExamples)
		}
	}

	userSchedule, _ := lookupOperation("schedule.query_user_schedule")
	if !rawSlotMapsTo(userSchedule.Recognition.RawSlots, "user_name", "user_id", "user_resolver") {
		t.Fatalf("user schedule raw slots = %+v, want user_name -> user_id via user_resolver", userSchedule.Recognition.RawSlots)
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

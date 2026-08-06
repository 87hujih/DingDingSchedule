package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWorkflowSnapshotCodecRoundTripsTrustedState(t *testing.T) {
	t.Parallel()

	input := completeWorkflowSnapshotFixture()
	payload, err := MarshalWorkflowSnapshot(input)
	if err != nil {
		t.Fatalf("MarshalWorkflowSnapshot() error = %v", err)
	}
	decoded, err := UnmarshalWorkflowSnapshot(payload)
	if err != nil {
		t.Fatalf("UnmarshalWorkflowSnapshot() error = %v", err)
	}

	if decoded.ID != input.ID ||
		decoded.TenantID != input.TenantID ||
		decoded.ActorUserID != input.ActorUserID ||
		decoded.ConversationID != input.ConversationID ||
		decoded.Type != input.Type ||
		decoded.State != input.State ||
		decoded.Version != input.Version {
		t.Fatalf("decoded identity/state = %+v, want %+v", decoded, input)
	}
	if !reflect.DeepEqual(decoded.MissingFields, input.MissingFields) ||
		!reflect.DeepEqual(decoded.MissingSlots, input.MissingFields) {
		t.Fatalf("decoded missing fields = %v/%v, want %v", decoded.MissingFields, decoded.MissingSlots, input.MissingFields)
	}
	if !decoded.CreatedAt.Equal(input.CreatedAt) ||
		!decoded.UpdatedAt.Equal(input.UpdatedAt) ||
		!decoded.ExpiresAt.Equal(input.ExpiresAt) {
		t.Fatalf("decoded times = %v/%v/%v", decoded.CreatedAt, decoded.UpdatedAt, decoded.ExpiresAt)
	}

	wantTrusted := input.Trusted
	for key, param := range wantTrusted.TrustedParams {
		param.Source.Raw = ""
		wantTrusted.TrustedParams[key] = param
	}
	if !reflect.DeepEqual(decoded.Trusted, wantTrusted) {
		t.Fatalf("decoded trusted state = %#v, want %#v", decoded.Trusted, wantTrusted)
	}
	if !reflect.DeepEqual(decoded.TrustedEntities, input.TrustedEntities) {
		t.Fatalf("decoded trusted entities = %#v, want %#v", decoded.TrustedEntities, input.TrustedEntities)
	}
	if !reflect.DeepEqual(decoded.Candidates, input.Candidates) {
		t.Fatalf("decoded candidates = %#v, want %#v", decoded.Candidates, input.Candidates)
	}
	if decoded.LastUserMessage != "" {
		t.Fatalf("LastUserMessage = %q, want empty after persistence", decoded.LastUserMessage)
	}
}

func TestWorkflowSnapshotCodecPreservesAllowedDynamicTypes(t *testing.T) {
	t.Parallel()

	input := completeWorkflowSnapshotFixture()
	input.TrustedEntities = map[string]TrustedEntity{
		"string": {Label: "string", Value: "value", TenantID: input.TenantID},
		"int":    {Label: "int", Value: int(7), TenantID: input.TenantID},
		"int64":  {Label: "int64", Value: int64(8), TenantID: input.TenantID},
		"uint":   {Label: "uint", Value: uint(9), TenantID: input.TenantID},
		"slice":  {Label: "slice", Value: []int64{3, 1, 3}, TenantID: input.TenantID},
		"bool":   {Label: "bool", Value: true, TenantID: input.TenantID},
	}
	input.Candidates = map[string][]Candidate{
		"values": {
			{ID: "s", Label: "string", Value: "candidate", TenantID: input.TenantID},
			{ID: "i", Label: "int", Value: int(11), TenantID: input.TenantID},
			{ID: "i64", Label: "int64", Value: int64(12), TenantID: input.TenantID},
			{ID: "u", Label: "uint", Value: uint(13), TenantID: input.TenantID},
			{ID: "ids", Label: "slice", Value: []int64{14, 15}, TenantID: input.TenantID},
			{ID: "b", Label: "bool", Value: false, TenantID: input.TenantID},
		},
	}

	payload, err := MarshalWorkflowSnapshot(input)
	if err != nil {
		t.Fatalf("MarshalWorkflowSnapshot() error = %v", err)
	}
	decoded, err := UnmarshalWorkflowSnapshot(payload)
	if err != nil {
		t.Fatalf("UnmarshalWorkflowSnapshot() error = %v", err)
	}

	for key, want := range input.TrustedEntities {
		got := decoded.TrustedEntities[key].Value
		if reflect.TypeOf(got) != reflect.TypeOf(want.Value) || !reflect.DeepEqual(got, want.Value) {
			t.Fatalf("trusted entity %q value = %#v (%T), want %#v (%T)", key, got, got, want.Value, want.Value)
		}
	}
	for index, want := range input.Candidates["values"] {
		got := decoded.Candidates["values"][index].Value
		if reflect.TypeOf(got) != reflect.TypeOf(want.Value) || !reflect.DeepEqual(got, want.Value) {
			t.Fatalf("candidate %d value = %#v (%T), want %#v (%T)", index, got, got, want.Value, want.Value)
		}
	}
}

func TestWorkflowSnapshotCodecExcludesChatAndRawUserText(t *testing.T) {
	t.Parallel()

	const secret = "RAW-USER-TEXT-MUST-NOT-PERSIST"
	input := completeWorkflowSnapshotFixture()
	input.LastUserMessage = secret
	param := input.Trusted.TrustedParams["scope"]
	param.Source.Raw = secret
	input.Trusted.TrustedParams["scope"] = param

	payload, err := MarshalWorkflowSnapshot(input)
	if err != nil {
		t.Fatalf("MarshalWorkflowSnapshot() error = %v", err)
	}
	if strings.Contains(string(payload), secret) || strings.Contains(string(payload), "last_user_message") {
		t.Fatalf("payload persisted chat/raw user text: %s", payload)
	}
	decoded, err := UnmarshalWorkflowSnapshot(payload)
	if err != nil {
		t.Fatalf("UnmarshalWorkflowSnapshot() error = %v", err)
	}
	if decoded.LastUserMessage != "" || decoded.Trusted.TrustedParams["scope"].Source.Raw != "" {
		t.Fatalf("decoded privacy fields = message:%q raw:%q", decoded.LastUserMessage, decoded.Trusted.TrustedParams["scope"].Source.Raw)
	}
}

func TestWorkflowSnapshotCodecRejectsUnsupportedTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*WorkflowSnapshot)
	}{
		{
			name: "trusted param",
			mutate: func(snapshot *WorkflowSnapshot) {
				param := snapshot.Trusted.TrustedParams["scope"]
				param.Value = map[string]any{"scope": "all"}
				snapshot.Trusted.TrustedParams["scope"] = param
			},
		},
		{
			name: "trusted entity",
			mutate: func(snapshot *WorkflowSnapshot) {
				snapshot.TrustedEntities["scope"] = TrustedEntity{Label: "scope", Value: float64(1)}
			},
		},
		{
			name: "candidate",
			mutate: func(snapshot *WorkflowSnapshot) {
				snapshot.Candidates["dept_ids"][0].Value = []int{101}
			},
		},
		{
			name: "nil value",
			mutate: func(snapshot *WorkflowSnapshot) {
				snapshot.Candidates["dept_ids"][0].Value = nil
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := completeWorkflowSnapshotFixture()
			test.mutate(snapshot)
			if _, err := MarshalWorkflowSnapshot(snapshot); err == nil {
				t.Fatal("MarshalWorkflowSnapshot() error = nil, want unsupported type error")
			}
		})
	}
}

func TestWorkflowSnapshotCodecRejectsInvalidSchemaAndData(t *testing.T) {
	t.Parallel()

	valid, err := MarshalWorkflowSnapshot(completeWorkflowSnapshotFixture())
	if err != nil {
		t.Fatalf("MarshalWorkflowSnapshot() error = %v", err)
	}

	var unknownSchema map[string]any
	if err := json.Unmarshal(valid, &unknownSchema); err != nil {
		t.Fatal(err)
	}
	unknownSchema["schema_version"] = 2
	unknownSchemaPayload, _ := json.Marshal(unknownSchema)

	var unknownField map[string]any
	if err := json.Unmarshal(valid, &unknownField); err != nil {
		t.Fatal(err)
	}
	unknownField["unexpected"] = true
	unknownFieldPayload, _ := json.Marshal(unknownField)

	var badState map[string]any
	if err := json.Unmarshal(valid, &badState); err != nil {
		t.Fatal(err)
	}
	badState["state"] = "invented_state"
	badStatePayload, _ := json.Marshal(badState)

	tests := [][]byte{
		nil,
		[]byte(`{"schema_version":1`),
		append(valid, []byte(` {}`)...),
		unknownSchemaPayload,
		unknownFieldPayload,
		badStatePayload,
	}
	for index, payload := range tests {
		if _, err := UnmarshalWorkflowSnapshot(payload); err == nil {
			t.Fatalf("case %d UnmarshalWorkflowSnapshot() error = nil", index)
		}
	}

	invalid := completeWorkflowSnapshotFixture()
	invalid.Type = WorkflowType("invented")
	if _, err := MarshalWorkflowSnapshot(invalid); err == nil {
		t.Fatal("MarshalWorkflowSnapshot(invalid type) error = nil")
	}
}

func TestWorkflowSnapshotCodecEnforcesSizeAndCandidateBounds(t *testing.T) {
	t.Parallel()

	if _, err := UnmarshalWorkflowSnapshot(make([]byte, workflowSnapshotMaxBytes+1)); err == nil {
		t.Fatal("UnmarshalWorkflowSnapshot(oversized) error = nil")
	}

	tooMany := completeWorkflowSnapshotFixture()
	values := make([]Candidate, workflowSnapshotMaxCandidatesPerField+1)
	for index := range values {
		values[index] = Candidate{ID: "candidate", Label: "candidate", Value: int64(index + 1)}
	}
	tooMany.Candidates = map[string][]Candidate{"dept_ids": values}
	if _, err := MarshalWorkflowSnapshot(tooMany); err == nil {
		t.Fatal("MarshalWorkflowSnapshot(too many candidates) error = nil")
	}

	longLabel := completeWorkflowSnapshotFixture()
	longLabel.Candidates["dept_ids"][0].Label = strings.Repeat("界", workflowSnapshotMaxLabelRunes+1)
	if _, err := MarshalWorkflowSnapshot(longLabel); err == nil {
		t.Fatal("MarshalWorkflowSnapshot(long label) error = nil")
	}

	oversized := completeWorkflowSnapshotFixture()
	oversized.Candidates = make(map[string][]Candidate, 4)
	largeValue := strings.Repeat("x", workflowSnapshotMaxTypedStringRunes)
	for fieldIndex := 0; fieldIndex < 4; fieldIndex++ {
		field := "field_" + string(rune('a'+fieldIndex))
		fieldCandidates := make([]Candidate, workflowSnapshotMaxCandidatesPerField)
		for index := range fieldCandidates {
			fieldCandidates[index] = Candidate{
				ID:    "candidate",
				Label: "candidate",
				Value: largeValue,
			}
		}
		oversized.Candidates[field] = fieldCandidates
	}
	if _, err := MarshalWorkflowSnapshot(oversized); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("MarshalWorkflowSnapshot(oversized) error = %v, want size error", err)
	}
}

func TestWorkflowSnapshotHashIsCanonicalBusinessHash(t *testing.T) {
	t.Parallel()

	first := completeWorkflowSnapshotFixture()
	second := completeWorkflowSnapshotFixture()
	second.Version = first.Version + 99
	second.CreatedAt = first.CreatedAt.Add(10 * time.Hour)
	second.UpdatedAt = first.UpdatedAt.Add(10 * time.Hour)
	second.LastUserMessage = "different ignored chat"
	param := second.Trusted.TrustedParams["scope"]
	param.Source.Raw = "different ignored raw text"
	second.Trusted.TrustedParams = map[string]TrustedParam{
		"dept_ids": second.Trusted.TrustedParams["dept_ids"],
		"scope":    param,
		"enabled":  second.Trusted.TrustedParams["enabled"],
	}
	second.TrustedEntities = map[string]TrustedEntity{
		"department": second.TrustedEntities["department"],
		"scope":      second.TrustedEntities["scope"],
	}

	firstHash, err := WorkflowSnapshotHash(first)
	if err != nil {
		t.Fatalf("WorkflowSnapshotHash(first) error = %v", err)
	}
	secondHash, err := WorkflowSnapshotHash(second)
	if err != nil {
		t.Fatalf("WorkflowSnapshotHash(second) error = %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("canonical hashes differ: %s != %s", firstHash, secondHash)
	}

	second.State = WorkflowReady
	changedHash, err := WorkflowSnapshotHash(second)
	if err != nil {
		t.Fatalf("WorkflowSnapshotHash(changed) error = %v", err)
	}
	if changedHash == firstHash {
		t.Fatalf("business state change did not change hash: %s", changedHash)
	}
}

func TestWorkflowSnapshotCodecRejectsTamperedTypedValue(t *testing.T) {
	t.Parallel()

	payload, err := MarshalWorkflowSnapshot(completeWorkflowSnapshotFixture())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	trusted := document["trusted"].(map[string]any)
	params := trusted["trusted_params"].(map[string]any)
	scope := params["scope"].(map[string]any)
	value := scope["value"].(map[string]any)
	value["kind"] = "float64"
	tampered, _ := json.Marshal(document)
	if _, err := UnmarshalWorkflowSnapshot(tampered); err == nil {
		t.Fatal("UnmarshalWorkflowSnapshot(tampered typed value) error = nil")
	}
}

func TestWorkflowSnapshotCodecErrorsAreStableForNilSnapshot(t *testing.T) {
	t.Parallel()

	if _, err := MarshalWorkflowSnapshot(nil); err == nil {
		t.Fatal("MarshalWorkflowSnapshot(nil) error = nil")
	}
	if _, err := WorkflowSnapshotHash(nil); err == nil {
		t.Fatal("WorkflowSnapshotHash(nil) error = nil")
	}
	if _, err := UnmarshalWorkflowSnapshot(nil); err == nil {
		t.Fatal("UnmarshalWorkflowSnapshot(nil) error = nil")
	}
}

func TestReservedExecutionCodecRoundTripsAllowlistedAuthority(t *testing.T) {
	t.Parallel()

	input := reservedExecutionFixture()
	payload, err := MarshalReservedExecution(input)
	if err != nil {
		t.Fatalf("MarshalReservedExecution() error = %v", err)
	}
	decoded, err := UnmarshalReservedExecution(payload)
	if err != nil {
		t.Fatalf("UnmarshalReservedExecution() error = %v", err)
	}

	want := input
	want.StartedAt = input.StartedAt.UTC()
	want.LeaseExpiresAt = input.LeaseExpiresAt.UTC()
	want.TrustedParams = clonePersistedTrustedParams(input.TrustedParams)
	want.TrustedParams["dept_ids"] = []int64{101, 102}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded reservation = %#v, want %#v", decoded, want)
	}
	if got := decoded.TrustedParams[operationParamActorRole]; reflect.TypeOf(got).Kind() != reflect.Int {
		t.Fatalf("actor_role type = %T, want int", got)
	}
	if strings.Contains(string(payload), "raw_user_text") {
		t.Fatalf("payload persisted raw user text: %s", payload)
	}
}

func TestReservedExecutionCodecFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ReservedExecutionV1)
	}{
		{
			name: "unknown operation",
			mutate: func(value *ReservedExecutionV1) {
				value.Operation = "attendance.query"
			},
		},
		{
			name: "raw user text",
			mutate: func(value *ReservedExecutionV1) {
				value.TrustedParams["raw_user_text"] = "帮我订阅全校"
			},
		},
		{
			name: "api credential",
			mutate: func(value *ReservedExecutionV1) {
				value.TrustedParams["api_key"] = "secret"
			},
		},
		{
			name: "unsupported param type",
			mutate: func(value *ReservedExecutionV1) {
				value.TrustedParams["scope"] = float64(1)
			},
		},
		{
			name: "invalid scope",
			mutate: func(value *ReservedExecutionV1) {
				value.TrustedParams["scope"] = "tenant"
			},
		},
		{
			name: "invalid department id",
			mutate: func(value *ReservedExecutionV1) {
				value.TrustedParams["dept_ids"] = []int64{101, 0}
			},
		},
		{
			name: "lease before start",
			mutate: func(value *ReservedExecutionV1) {
				value.LeaseExpiresAt = value.StartedAt.Add(-time.Second)
			},
		},
		{
			name: "unnormalized token",
			mutate: func(value *ReservedExecutionV1) {
				value.ExecutionToken = " token-1 "
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := reservedExecutionFixture()
			tt.mutate(&input)
			if _, err := MarshalReservedExecution(input); err == nil {
				t.Fatal("MarshalReservedExecution() error = nil")
			}
		})
	}
}

func TestReservedExecutionCodecRejectsInvalidPayloads(t *testing.T) {
	t.Parallel()

	valid, err := MarshalReservedExecution(reservedExecutionFixture())
	if err != nil {
		t.Fatalf("MarshalReservedExecution() error = %v", err)
	}
	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "empty", payload: nil},
		{name: "oversized", payload: []byte(strings.Repeat("x", workflowSnapshotMaxBytes+1))},
		{name: "unknown schema", payload: bytesReplaceJSONField(t, valid, "schema_version", 2)},
		{name: "unknown field", payload: append(valid[:len(valid)-1], []byte(`,"raw":"user text"}`)...)},
		{name: "invalid time", payload: bytesReplaceJSONField(t, valid, "started_at", "tomorrow")},
		{name: "unsupported typed kind", payload: bytesReplaceTypedKind(t, valid, "scope", "float64")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := UnmarshalReservedExecution(tt.payload); err == nil {
				t.Fatal("UnmarshalReservedExecution() error = nil")
			}
		})
	}
}

func TestPersistedExecutionResultCodecRoundTripsAndFailsClosed(t *testing.T) {
	t.Parallel()

	effects := []WriteEffect{
		WriteEffectCreated,
		WriteEffectUpdated,
		WriteEffectNoOp,
		WriteEffectCancelled,
	}
	completedAt := time.Date(2026, 7, 30, 15, 0, 1, 234000000, time.FixedZone("fixture", 8*60*60))
	for _, effect := range effects {
		effect := effect
		t.Run(string(effect), func(t *testing.T) {
			t.Parallel()
			input := PersistedExecutionResultV1{
				BusinessKey: "subscription:42:cid-codec",
				WriteEffect: effect,
				CompletedAt: completedAt,
			}
			payload, err := MarshalPersistedExecutionResult(input)
			if err != nil {
				t.Fatalf("MarshalPersistedExecutionResult() error = %v", err)
			}
			decoded, err := UnmarshalPersistedExecutionResult(payload)
			if err != nil {
				t.Fatalf("UnmarshalPersistedExecutionResult() error = %v", err)
			}
			input.CompletedAt = input.CompletedAt.UTC()
			if !reflect.DeepEqual(decoded, input) {
				t.Fatalf("decoded result = %#v, want %#v", decoded, input)
			}
		})
	}

	invalid := PersistedExecutionResultV1{
		BusinessKey: "business-key",
		WriteEffect: WriteEffect("deleted"),
		CompletedAt: completedAt,
	}
	if _, err := MarshalPersistedExecutionResult(invalid); err == nil {
		t.Fatal("MarshalPersistedExecutionResult(invalid effect) error = nil")
	}
	valid, err := MarshalPersistedExecutionResult(PersistedExecutionResultV1{
		BusinessKey: "business-key",
		WriteEffect: WriteEffectCreated,
		CompletedAt: completedAt,
	})
	if err != nil {
		t.Fatalf("MarshalPersistedExecutionResult() error = %v", err)
	}
	for name, payload := range map[string][]byte{
		"unknown schema": bytesReplaceJSONField(t, valid, "schema_version", 9),
		"unknown field":  append(valid[:len(valid)-1], []byte(`,"raw":"user text"}`)...),
		"invalid time":   bytesReplaceJSONField(t, valid, "completed_at", "later"),
		"oversized":      []byte(strings.Repeat("x", workflowSnapshotMaxBytes+1)),
	} {
		if _, err := UnmarshalPersistedExecutionResult(payload); err == nil {
			t.Fatalf("UnmarshalPersistedExecutionResult(%s) error = nil", name)
		}
	}
}

func reservedExecutionFixture() ReservedExecutionV1 {
	startedAt := time.Date(2026, 7, 30, 14, 0, 1, 123000000, time.FixedZone("fixture", 8*60*60))
	return ReservedExecutionV1{
		Operation:   "subscription.start",
		BusinessKey: "subscription:42:cid-codec",
		TrustedParams: PersistedTrustedParamsV1{
			"conversation_id":               "cid-codec",
			"scope":                         "department",
			"dept_ids":                      []int64{102, 101, 102},
			operationParamActorRole:         2,
			operationParamConversationType:  "2",
			operationParamConversationTitle: "考勤群",
		},
		ExecutionToken:   "token-codec-1",
		AttemptRequestID: "request-codec-1",
		StartedAt:        startedAt,
		LeaseExpiresAt:   startedAt.Add(time.Minute),
	}
}

func bytesReplaceJSONField(t *testing.T, payload []byte, field string, value any) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	document[field] = value
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}

func bytesReplaceTypedKind(t *testing.T, payload []byte, field, kind string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	params := document["trusted_params"].(map[string]any)
	value := params[field].(map[string]any)
	value["kind"] = kind
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}

func completeWorkflowSnapshotFixture() *WorkflowSnapshot {
	now := time.Date(2026, 7, 30, 12, 34, 56, 789000000, time.FixedZone("fixture", 8*60*60))
	return &WorkflowSnapshot{
		ID:              "wf-codec-1",
		TenantID:        42,
		ActorUserID:     7,
		ConversationID:  "cid-codec",
		Type:            WorkflowSubscriptionStart,
		State:           WorkflowCollectDepartments,
		MissingFields:   []string{"dept_ids"},
		MissingSlots:    []string{"dept_ids"},
		LastUserMessage: "完整聊天原文不得持久化",
		CreatedAt:       now,
		UpdatedAt:       now.Add(time.Minute),
		ExpiresAt:       now.Add(30 * time.Minute),
		Version:         3,
		Trusted: trustedEntities{
			TenantID:       42,
			DepartmentID:   101,
			DeptIDs:        []int64{101, 102},
			UserID:         7,
			UserName:       "张三",
			Date:           "2026-07-30",
			Section:        2,
			Week:           9,
			ConversationID: "cid-codec",
			Scope:          "department",
			QueryShape:     "by_department",
			UserRole:       2,
			TrustedParams: map[string]TrustedParam{
				"scope": {
					Field: "scope",
					Value: "department",
					Source: TrustedParamSource{
						Kind:     TrustedParamSourceCandidate,
						Raw:      "指定部门",
						Resolver: "workflow_candidate",
					},
					TenantID: 42,
				},
				"dept_ids": {
					Field:    "dept_ids",
					Value:    []int64{101, 102},
					Source:   TrustedParamSource{Kind: TrustedParamSourceWorkflow, Resolver: "department_resolver"},
					TenantID: 42,
				},
				"enabled": {
					Field:    "enabled",
					Value:    true,
					Source:   TrustedParamSource{Kind: TrustedParamSourceDerived, Resolver: "fixture"},
					TenantID: 42,
				},
			},
		},
		TrustedEntities: map[string]TrustedEntity{
			"scope": {
				ID:       "scope-department",
				Type:     "scope",
				Label:    "指定部门",
				Value:    "department",
				TenantID: 42,
			},
			"department": {
				ID:       "101",
				Type:     "department",
				Label:    "信工24级",
				Value:    int64(101),
				TenantID: 42,
			},
		},
		Candidates: map[string][]Candidate{
			"dept_ids": {
				{ID: "101", Label: "信工24级", Value: int64(101), TenantID: 42},
				{ID: "102", Label: "信工25级", Value: int64(102), TenantID: 42},
			},
		},
	}
}

func TestExecutionCodecRoundTripsEmptyDeptIDs(t *testing.T) {
	t.Parallel()

	input := reservedExecutionFixture()
	input.TrustedParams["scope"] = "all"
	input.TrustedParams["dept_ids"] = []int64{}
	payload, err := MarshalReservedExecution(input)
	if err != nil {
		t.Fatalf("MarshalReservedExecution() error = %v", err)
	}
	decoded, err := UnmarshalReservedExecution(payload)
	if err != nil {
		t.Fatalf("UnmarshalReservedExecution() error = %v", err)
	}
	deptIDs, ok := decoded.TrustedParams["dept_ids"].([]int64)
	if !ok || deptIDs == nil || len(deptIDs) != 0 {
		t.Fatalf("decoded dept_ids = %#v, want non-nil empty []int64", decoded.TrustedParams["dept_ids"])
	}
}

func TestWorkflowSnapshotCodecRejectsCrossTenantAuthority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*WorkflowSnapshot)
	}{
		{"trusted", func(snapshot *WorkflowSnapshot) { snapshot.Trusted.TenantID++ }},
		{"param", func(snapshot *WorkflowSnapshot) {
			param := snapshot.Trusted.TrustedParams["scope"]
			param.TenantID++
			snapshot.Trusted.TrustedParams["scope"] = param
		}},
		{"entity", func(snapshot *WorkflowSnapshot) {
			entity := snapshot.TrustedEntities["scope"]
			entity.TenantID++
			snapshot.TrustedEntities["scope"] = entity
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := completeWorkflowSnapshotFixture()
			test.mutate(snapshot)
			if _, err := MarshalWorkflowSnapshot(snapshot); err == nil {
				t.Fatal("MarshalWorkflowSnapshot() error = nil, want tenant authority rejection")
			}
		})
	}
}

func TestWorkflowSnapshotCodecRejectsUnknownTrustedParamSource(t *testing.T) {
	t.Parallel()

	snapshot := completeWorkflowSnapshotFixture()
	param := snapshot.Trusted.TrustedParams["scope"]
	param.Source.Kind = "future_source"
	snapshot.Trusted.TrustedParams["scope"] = param
	if _, err := MarshalWorkflowSnapshot(snapshot); err == nil {
		t.Fatal("MarshalWorkflowSnapshot() error = nil, want unknown source rejection")
	}
}

func TestWorkflowAndExecutionCodecRespectDatabaseProjectionWidths(t *testing.T) {
	t.Parallel()

	snapshot := completeWorkflowSnapshotFixture()
	snapshot.ID = strings.Repeat("w", workflowSnapshotMaxWorkflowIDRunes+1)
	if _, err := MarshalWorkflowSnapshot(snapshot); err == nil {
		t.Fatal("MarshalWorkflowSnapshot() accepted workflow_id wider than database column")
	}

	reservation := reservedExecutionFixture()
	reservation.ExecutionToken = strings.Repeat("t", executionMaxProjectionRunes+1)
	if _, err := MarshalReservedExecution(reservation); err == nil {
		t.Fatal("MarshalReservedExecution() accepted token wider than database column")
	}
}

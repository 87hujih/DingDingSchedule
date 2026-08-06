package agent

import (
	"strings"
	"testing"
)

func TestSubscriptionBusinessKeyCanonicalizesDepartmentIDs(t *testing.T) {
	t.Parallel()

	first := subscriptionBusinessKeyRequest("subscription.start", "department", []int64{9, 2, 9})
	second := subscriptionBusinessKeyRequest("subscription.start", "department", []int64{2, 9})
	first.ActorUserID = 10
	second.ActorUserID = 99

	firstKey, err := subscriptionBusinessKeyForRequest(first)
	if err != nil {
		t.Fatalf("first key: %v", err)
	}
	secondKey, err := subscriptionBusinessKeyForRequest(second)
	if err != nil {
		t.Fatalf("second key: %v", err)
	}
	if firstKey != secondKey {
		t.Fatalf("canonical keys differ: %q != %q", firstKey, secondKey)
	}
	if len(firstKey) != 64 || firstKey != strings.ToLower(firstKey) {
		t.Fatalf("key must be 64 lowercase hex chars, got %q", firstKey)
	}
	const wantGolden = "14896efa215fe1d84a48e59ad55a8a6008e6fed558eeb9b136dbad6d2ef47487"
	if firstKey != wantGolden {
		t.Fatalf("business key = %q, want golden %q", firstKey, wantGolden)
	}
}

func TestSubscriptionBusinessKeySeparatesOperationAndScope(t *testing.T) {
	t.Parallel()

	startAll, err := subscriptionBusinessKeyForRequest(subscriptionBusinessKeyRequest("subscription.start", "all", nil))
	if err != nil {
		t.Fatal(err)
	}
	startDepartment, err := subscriptionBusinessKeyForRequest(subscriptionBusinessKeyRequest("subscription.start", "department", []int64{2}))
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := subscriptionBusinessKeyForRequest(subscriptionBusinessKeyRequest("subscription.cancel", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if startAll == startDepartment || startAll == cancel || startDepartment == cancel {
		t.Fatal("operation and scope must produce distinct business keys")
	}
}

func TestSubscriptionBusinessKeyRejectsInvalidScopeShape(t *testing.T) {
	t.Parallel()

	tests := []OperationRequest{
		subscriptionBusinessKeyRequest("subscription.start", "all", []int64{1}),
		subscriptionBusinessKeyRequest("subscription.start", "department", nil),
		subscriptionBusinessKeyRequest("subscription.start", "department", []int64{0}),
		subscriptionBusinessKeyRequest("other.write", "", nil),
	}
	for _, req := range tests {
		if _, err := subscriptionBusinessKeyForRequest(req); err == nil {
			t.Fatalf("expected invalid request to fail: %+v", req)
		}
	}
}

func subscriptionBusinessKeyRequest(operation, scope string, deptIDs []int64) OperationRequest {
	params := map[string]TrustedParam{}
	if scope != "" {
		params["scope"] = TrustedParam{Field: "scope", Value: scope, TenantID: 7}
	}
	if deptIDs != nil {
		params["dept_ids"] = TrustedParam{Field: "dept_ids", Value: deptIDs, TenantID: 7}
	}
	return OperationRequest{
		Operation:      operation,
		TenantID:       7,
		ActorUserID:    1,
		ConversationID: `cid-"escaped"`,
		TrustedParams:  params,
	}
}

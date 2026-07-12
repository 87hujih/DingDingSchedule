package agent

import (
	"strings"
	"testing"
)

func TestWriteGuardAllowsLowRiskSubscriptionWriteWithIdempotencyKey(t *testing.T) {
	t.Parallel()

	uctx := executorUserContext()
	workflow := &WorkflowSnapshot{ID: "wf-sub-1"}
	req := OperationRequest{
		Operation: "subscription.start",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": uctx.ConversationID,
			"scope":           "department",
			"dept_ids":        []int64{101},
		}),
	}

	first := newWriteGuard().Check(WriteGuardInput{
		User:     uctx,
		Request:  req,
		Workflow: workflow,
	})
	second := newWriteGuard().Check(WriteGuardInput{
		User:     uctx,
		Request:  req,
		Workflow: workflow,
	})

	if !first.Allow {
		t.Fatalf("Allow = false, want true: %+v", first)
	}
	if first.ResponseKind != ResponseResult {
		t.Fatalf("ResponseKind = %q, want %q", first.ResponseKind, ResponseResult)
	}
	if strings.TrimSpace(first.IdempotencyKey) == "" {
		t.Fatalf("IdempotencyKey is empty")
	}
	if first.IdempotencyKey != second.IdempotencyKey {
		t.Fatalf("IdempotencyKey changed for same request: %q vs %q", first.IdempotencyKey, second.IdempotencyKey)
	}
	if !strings.Contains(first.IdempotencyKey, "subscription.start") {
		t.Fatalf("IdempotencyKey = %q, want operation component", first.IdempotencyKey)
	}
}

func TestWriteGuardIdempotencyKeyChangesWithTrustedParams(t *testing.T) {
	t.Parallel()

	uctx := executorUserContext()
	all := OperationRequest{
		Operation: "subscription.start",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": uctx.ConversationID,
			"scope":           "all",
		}),
	}
	department := OperationRequest{
		Operation: "subscription.start",
		TrustedParams: executorTrustedParams(map[string]any{
			"conversation_id": uctx.ConversationID,
			"scope":           "department",
			"dept_ids":        []int64{101},
		}),
	}

	allResult := newWriteGuard().Check(WriteGuardInput{User: uctx, Request: all, Workflow: &WorkflowSnapshot{ID: "wf-sub-1"}})
	deptResult := newWriteGuard().Check(WriteGuardInput{User: uctx, Request: department, Workflow: &WorkflowSnapshot{ID: "wf-sub-1"}})

	if allResult.IdempotencyKey == deptResult.IdempotencyKey {
		t.Fatalf("IdempotencyKey should differ by trusted params: %q", allResult.IdempotencyKey)
	}
}

func TestSubscriptionStartBusinessKeyIgnoresActorAndWorkflowAndSortsDepartments(t *testing.T) {
	manifest, ok := lookupOperation("subscription.start")
	if !ok {
		t.Fatal("subscription.start manifest missing")
	}
	request := OperationRequest{
		TenantID: 1, ActorUserID: 10, ConversationID: "conv-1", Operation: "subscription.start",
		TrustedParams: map[string]TrustedParam{
			"scope": {Value: "department"}, "dept_ids": {Value: []int64{102, 101}},
		},
	}
	first := buildIdempotencyKey(manifest, WriteGuardInput{Request: request, Workflow: &WorkflowSnapshot{ID: "workflow-a"}})
	request.ActorUserID = 99
	request.TrustedParams["dept_ids"] = TrustedParam{Value: []int64{101, 102}}
	second := buildIdempotencyKey(manifest, WriteGuardInput{Request: request, Workflow: &WorkflowSnapshot{ID: "workflow-b"}})
	if first == "" || first != second {
		t.Fatalf("business key changed across actor/workflow/order: %q vs %q", first, second)
	}
}

func TestSubscriptionCancelBusinessKeyIgnoresActor(t *testing.T) {
	manifest, ok := lookupOperation("subscription.cancel")
	if !ok {
		t.Fatal("subscription.cancel manifest missing")
	}
	request := OperationRequest{TenantID: 1, ActorUserID: 10, ConversationID: "conv-1", Operation: "subscription.cancel"}
	first := buildIdempotencyKey(manifest, WriteGuardInput{Request: request})
	request.ActorUserID = 99
	second := buildIdempotencyKey(manifest, WriteGuardInput{Request: request})
	if first == "" || first != second {
		t.Fatalf("cancel business key changed across actor: %q vs %q", first, second)
	}
}

func TestWriteGuardRequiresConfirmForHighRiskManifest(t *testing.T) {
	t.Parallel()

	manifest := OperationManifest{
		Name:    "manual_sign.create",
		Domain:  DomainManualSign,
		IsWrite: true,
		Risk:    RiskWriteHigh,
		Idempotency: IdempotencySpec{
			KeyFields: []string{"tenant_id", "conversation_id", "actor_user_id", "operation", "user_id", "date", "section", "workflow_id"},
		},
		Renderer: RendererBinding{Name: "response_renderer", Kind: ResponseConfirm},
	}

	result := newWriteGuard().Check(WriteGuardInput{
		User:     executorUserContext(),
		Manifest: manifest,
		Request: OperationRequest{
			Operation: "manual_sign.create",
			TrustedParams: executorTrustedParams(map[string]any{
				"conversation_id": "conv-1",
				"user_id":         uint(99),
				"date":            "2026-06-15",
				"section":         2,
			}),
		},
		Workflow: &WorkflowSnapshot{ID: "wf-manual-1"},
	})

	if result.Allow {
		t.Fatalf("Allow = true, want false before confirmation")
	}
	if result.ResponseKind != ResponseConfirm {
		t.Fatalf("ResponseKind = %q, want %q", result.ResponseKind, ResponseConfirm)
	}
	if result.BlockedReason != "write_confirmation_required" {
		t.Fatalf("BlockedReason = %q, want write_confirmation_required", result.BlockedReason)
	}
	if _, ok := lookupOperation("manual_sign.create"); ok {
		t.Fatalf("manual_sign.create must stay outside active protocol catalog")
	}
}

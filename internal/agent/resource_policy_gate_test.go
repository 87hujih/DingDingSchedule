package agent

import (
	"context"
	"testing"

	"schedule_server/internal/agent/tools"
)

func TestResourcePolicyGateBindsSubscriptionToCurrentGroup(t *testing.T) {
	t.Parallel()

	uctx := executorUserContext()
	uctx.ConversationID = "conv-runtime"

	result := newResourcePolicyGate().Validate(context.Background(), ResourcePolicyGateInput{
		User: uctx,
		Request: OperationRequest{
			Operation: "subscription.cancel",
			TrustedParams: executorTrustedParams(map[string]any{
				"conversation_id": "conv-other",
			}),
		},
	})

	if result.Allow {
		t.Fatalf("Allow = true, want false")
	}
	if result.BlockedReason != "subscription_conversation_mismatch" {
		t.Fatalf("BlockedReason = %q, want subscription_conversation_mismatch", result.BlockedReason)
	}
}

func TestResourcePolicyGateAllowsCurrentGroupSubscription(t *testing.T) {
	t.Parallel()

	uctx := executorUserContext()
	uctx.ConversationID = "conv-runtime"

	result := newResourcePolicyGate().Validate(context.Background(), ResourcePolicyGateInput{
		User: uctx,
		Request: OperationRequest{
			Operation: "subscription.start",
			TrustedParams: executorTrustedParams(map[string]any{
				"conversation_id": "conv-runtime",
				"scope":           "all",
			}),
		},
	})

	if !result.Allow {
		t.Fatalf("Allow = false, want true: %+v", result)
	}
}

func TestResourcePolicyGateRejectsUserScheduleForOrdinaryUserViewingOtherUser(t *testing.T) {
	t.Parallel()

	uctx := executorUserContext()
	uctx.UserRole = 0
	uctx.UserID = 7

	result := newResourcePolicyGate().Validate(context.Background(), ResourcePolicyGateInput{
		User: uctx,
		Request: OperationRequest{
			Operation: "schedule.query_user_schedule",
			TrustedParams: executorTrustedParams(map[string]any{
				"user_id": uint(99),
				"week":    11,
			}),
		},
	})

	if result.Allow {
		t.Fatalf("Allow = true, want false")
	}
	if result.BlockedReason != "schedule_user_visibility_denied" {
		t.Fatalf("BlockedReason = %q, want schedule_user_visibility_denied", result.BlockedReason)
	}
}

func TestResourcePolicyGateAllowsOrdinaryUserViewingOwnSchedule(t *testing.T) {
	t.Parallel()

	uctx := executorUserContext()
	uctx.UserRole = 0
	uctx.UserID = 7

	result := newResourcePolicyGate().Validate(context.Background(), ResourcePolicyGateInput{
		User: uctx,
		Request: OperationRequest{
			Operation: "schedule.query_user_schedule",
			TrustedParams: executorTrustedParams(map[string]any{
				"user_id": uint(7),
				"week":    11,
			}),
		},
	})

	if !result.Allow {
		t.Fatalf("Allow = false, want true: %+v", result)
	}
}

func TestResourcePolicyGateRejectsDepartmentOutsideTenantScope(t *testing.T) {
	t.Parallel()

	uctx := executorUserContext()
	uctx.TenantID = 42
	dept := executorFakeDeptPort{depts: []tools.DeptItem{
		{TenantID: 42, DeptID: 101, Name: "信工24级"},
		{TenantID: 99, DeptID: 999, Name: "其他租户"},
	}}

	result := newResourcePolicyGate().Validate(context.Background(), ResourcePolicyGateInput{
		User: uctx,
		Dept: dept,
		Request: OperationRequest{
			Operation: "subscription.start",
			TrustedParams: executorTrustedParams(map[string]any{
				"conversation_id": uctx.ConversationID,
				"scope":           "department",
				"dept_ids":        []int64{999},
			}),
		},
	})

	if result.Allow {
		t.Fatalf("Allow = true, want false")
	}
	if result.BlockedReason != "department_scope_denied" {
		t.Fatalf("BlockedReason = %q, want department_scope_denied", result.BlockedReason)
	}
}

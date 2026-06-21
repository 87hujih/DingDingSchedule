package agent

import "testing"

func TestExecutionGateBlocksManualSignWithoutTrustedUserID(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "manual_sign.create",
	}, trustedEntities{
		Date:    "2026-04-07",
		Section: 2,
	})
	if !blocked {
		t.Fatalf("blocked = false, want true")
	}
}

func TestExecutionGateBuildsAttendanceReadRequestFromTrustedValues(t *testing.T) {
	t.Parallel()

	req, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActReadQuery,
		Operation: "attendance.query_status",
	}, trustedEntities{
		Date:    "2026-04-07",
		Section: 2,
	})
	if blocked {
		t.Fatalf("blocked = true, want false")
	}
	if req.Operation != "attendance.query_status" {
		t.Fatalf("Operation = %q, want attendance.query_status", req.Operation)
	}
	if trustedValue(req.TrustedParams, "date") != "2026-04-07" {
		t.Fatalf("date = %v, want 2026-04-07", req.TrustedParams["date"])
	}
	if trustedValue(req.TrustedParams, "section") != 2 {
		t.Fatalf("section = %v, want 2", req.TrustedParams["section"])
	}
}

func TestBuildOperationRequestSelectsUserDayAttendanceQueryShape(t *testing.T) {
	t.Parallel()

	req, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActReadQuery,
		Operation: "attendance.query_status",
	}, trustedEntities{
		Date:   "2026-04-07",
		UserID: 99,
	})
	if blocked {
		t.Fatalf("blocked = true, want false")
	}
	if trustedValue(req.TrustedParams, "query_shape") != "user_day_status" {
		t.Fatalf("query_shape = %v, want user_day_status", req.TrustedParams["query_shape"])
	}
	if trustedValue(req.TrustedParams, "user_id") != uint(99) {
		t.Fatalf("user_id = %v, want 99", req.TrustedParams["user_id"])
	}
}

func TestBuildOperationRequestBlocksAttendanceWithoutMatchingQueryShape(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActReadQuery,
		Operation: "attendance.query_status",
	}, trustedEntities{
		Date: "2026-04-07",
	})
	if !blocked {
		t.Fatalf("blocked = false, want true")
	}
}

func TestBuildOperationRequestRequiresScheduleWeek(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActReadQuery,
		Operation: "schedule.query_my_schedule",
	}, trustedEntities{})
	if !blocked {
		t.Fatalf("blocked = false, want true without trusted week")
	}

	req, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActReadQuery,
		Operation: "schedule.query_my_schedule",
	}, trustedEntities{
		Week: 10,
	})
	if blocked {
		t.Fatalf("blocked = true, want false with trusted week")
	}
	if trustedValue(req.TrustedParams, "week") != 10 {
		t.Fatalf("week = %v, want 10", req.TrustedParams["week"])
	}
}

func TestBuildOperationRequestRequiresUserScheduleUserIDAndWeek(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActReadQuery,
		Operation: "schedule.query_user_schedule",
	}, trustedEntities{
		Week: 10,
	})
	if !blocked {
		t.Fatalf("blocked = false, want true without trusted user_id")
	}

	req, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActReadQuery,
		Operation: "schedule.query_user_schedule",
	}, trustedEntities{
		UserID: 99,
		Week:   10,
	})
	if blocked {
		t.Fatalf("blocked = true, want false with trusted user_id and week")
	}
	if trustedValue(req.TrustedParams, "user_id") != uint(99) {
		t.Fatalf("user_id = %v, want 99", req.TrustedParams["user_id"])
	}
	if trustedValue(req.TrustedParams, "week") != 10 {
		t.Fatalf("week = %v, want 10", req.TrustedParams["week"])
	}
}

func TestBuildOperationRequestAllowsTrustedRuleTopic(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"attendance.rule_explain", "schedule.rule_explain", "subscription.rule_explain"} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			t.Parallel()

			req, blocked := buildOperationRequest(ProtocolDraft{
				Act:       ActRuleQuestion,
				Operation: operation,
			}, trustedEntities{
				TrustedParams: trustedParamsForGateTest(map[string]any{"rule_topic": "late_policy"}),
			})
			if blocked {
				t.Fatalf("blocked = true, want false for trusted rule_topic")
			}
			if trustedValue(req.TrustedParams, "rule_topic") != "late_policy" {
				t.Fatalf("rule_topic = %v, want late_policy", req.TrustedParams["rule_topic"])
			}
		})
	}
}

func TestBuildOperationRequestBlocksRuleExplainWithoutTrustedTopic(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActRuleQuestion,
		Operation: "attendance.rule_explain",
	}, trustedEntities{})
	if !blocked {
		t.Fatalf("blocked = false, want true without trusted rule_topic")
	}
}

func TestBuildOperationRequestRejectsRawStringTrustedParamsForTypedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		draft   ProtocolDraft
		trusted trustedEntities
	}{
		{
			name:  "section raw string",
			draft: ProtocolDraft{Act: ActReadQuery, Operation: "attendance.query_status"},
			trusted: trustedEntities{TrustedParams: trustedParamsForGateTest(map[string]any{
				"date":    "2026-04-07",
				"section": "第二节",
			})},
		},
		{
			name:  "user raw string",
			draft: ProtocolDraft{Act: ActReadQuery, Operation: "schedule.query_user_schedule"},
			trusted: trustedEntities{TrustedParams: trustedParamsForGateTest(map[string]any{
				"user_id": "张三",
				"week":    10,
			})},
		},
		{
			name:  "dept raw string for department scope subscription",
			draft: ProtocolDraft{Act: ActWriteRequest, Operation: "subscription.start"},
			trusted: trustedEntities{
				UserRole: 1,
				TrustedParams: trustedParamsForGateTest(map[string]any{
					"conversation_id": "conv-1",
					"scope":           "department",
					"dept_ids":        "信工24级",
				}),
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, blocked := buildOperationRequest(tt.draft, tt.trusted)
			if !blocked {
				t.Fatalf("blocked = false, want true for raw TrustedParams")
			}
		})
	}
}

func TestBuildOperationRequestRequiresDepartmentIDsForDepartmentScopeSubscription(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "subscription.start",
	}, trustedEntities{
		ConversationID: "conv-1",
		Scope:          "department",
		UserRole:       1,
	})
	if !blocked {
		t.Fatalf("blocked = false, want true for department scope without dept_ids")
	}
}

func TestBuildOperationRequestAcceptsDepartmentResolverOutput(t *testing.T) {
	t.Parallel()

	resolved := resolveDepartmentSlot("信工24级", []DeptItem{
		{DeptID: 101, Name: "信工24级"},
	})
	if resolved.Status != ResolveResolved {
		t.Fatalf("resolveDepartmentSlot() = %+v, want resolved", resolved)
	}

	req, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "subscription.start",
	}, trustedEntities{
		UserRole: 1,
		TrustedParams: trustedParamsForGateTest(map[string]any{
			"conversation_id": "conv-1",
			"scope":           "department",
			"dept_ids":        resolved.Values["dept_ids"],
		}),
	})
	if blocked {
		t.Fatalf("blocked = true, want false")
	}
	deptIDs, ok := trustedValue(req.TrustedParams, "dept_ids").([]int64)
	if !ok || len(deptIDs) != 1 || deptIDs[0] != 101 {
		t.Fatalf("dept_ids = %v, want [101]", req.TrustedParams["dept_ids"])
	}
}

func TestBuildOperationRequestRejectsCrossTenantTrustedParam(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "subscription.start",
	}, trustedEntities{
		TenantID: 42,
		UserRole: 1,
		TrustedParams: map[string]TrustedParam{
			"conversation_id": trustedParam("conversation_id", "conv-1", 42, TrustedParamSource{Kind: TrustedParamSourceRuntime, Resolver: "conversation_runtime"}),
			"scope":           trustedParam("scope", "department", 42, TrustedParamSource{Kind: TrustedParamSourceWorkflow, Resolver: "subscription_scope"}),
			"dept_ids":        trustedParam("dept_ids", []int64{101}, 99, TrustedParamSource{Kind: TrustedParamSourceRawSlot, Raw: "信工", Resolver: "department_resolver"}),
		},
	})
	if !blocked {
		t.Fatalf("blocked = false, want true for cross-tenant trusted param")
	}
}

func TestBuildOperationRequestRejectsTenantlessTrustedParamForTenantBoundRequest(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "subscription.start",
	}, trustedEntities{
		TenantID: 42,
		UserRole: 1,
		TrustedParams: map[string]TrustedParam{
			"conversation_id": trustedParam("conversation_id", "conv-1", 42, TrustedParamSource{Kind: TrustedParamSourceRuntime, Resolver: "conversation_runtime"}),
			"scope":           trustedParam("scope", "department", 42, TrustedParamSource{Kind: TrustedParamSourceWorkflow, Resolver: "subscription_scope"}),
			"dept_ids":        trustedParam("dept_ids", []int64{101}, 0, TrustedParamSource{Kind: TrustedParamSourceRawSlot, Raw: "信工", Resolver: "department_resolver"}),
		},
	})
	if !blocked {
		t.Fatalf("blocked = false, want true for tenantless trusted param in tenant-bound request")
	}
}

func TestExecutionGateEnforcesSubscriptionWritePermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		role      int
		blocked   bool
		deptIDs   []int64
		wantDepts bool
	}{
		{name: "ordinary user blocked", role: 0, blocked: true},
		{name: "admin allowed", role: 1, blocked: false, deptIDs: []int64{101}, wantDepts: true},
		{name: "super admin allowed", role: 2, blocked: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, blocked := buildOperationRequest(ProtocolDraft{
				Act:       ActWriteRequest,
				Operation: "subscription.start",
			}, trustedEntities{
				ConversationID: "conv-1",
				Scope:          subscriptionScopeForTest(tt.deptIDs),
				DeptIDs:        tt.deptIDs,
				UserRole:       tt.role,
			})
			if blocked != tt.blocked {
				t.Fatalf("blocked = %v, want %v", blocked, tt.blocked)
			}
			if tt.wantDepts && len(trustedValue(req.TrustedParams, "dept_ids").([]int64)) != 1 {
				t.Fatalf("dept_ids = %v, want one trusted dept id", req.TrustedParams["dept_ids"])
			}
		})
	}
}

func subscriptionScopeForTest(deptIDs []int64) string {
	if len(deptIDs) > 0 {
		return "department"
	}
	return "all"
}

func TestExecutionGateRequiresTrustedConversationForSubscriptionCancel(t *testing.T) {
	t.Parallel()

	_, blocked := buildOperationRequest(ProtocolDraft{
		Act:       ActWriteRequest,
		Operation: "subscription.cancel",
	}, trustedEntities{
		UserRole: 1,
	})
	if !blocked {
		t.Fatalf("blocked = false, want true without trusted conversation_id")
	}
}

func trustedParamsForGateTest(values map[string]any) map[string]TrustedParam {
	return trustedParamsFromValues(42, TrustedParamSource{
		Kind:     TrustedParamSourceWorkflow,
		Resolver: "execution_gate_test",
	}, values)
}

func trustedValue(params map[string]TrustedParam, field string) any {
	if params == nil {
		return nil
	}
	return params[field].Value
}

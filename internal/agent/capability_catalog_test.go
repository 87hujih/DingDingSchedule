package agent

import (
	"strings"
	"testing"

	"schedule_server/internal/agent/tools"
)

func TestCapabilityCatalogFiltersGroupSubscriptionInDM(t *testing.T) {
	t.Parallel()

	entries := capabilityEntriesFor(capabilityContext{
		UserRole:         0,
		ConversationType: "1",
	})
	if containsCapability(entries, "subscription.describe_capability") {
		t.Fatalf("single chat user should not see group subscription execution capability: %+v", entries)
	}
}

func TestCapabilityCatalogShowsSubscriptionStatusForOrdinaryGroupUser(t *testing.T) {
	t.Parallel()

	entries := capabilityEntriesFor(capabilityContext{
		UserRole:         0,
		ConversationType: "2",
	})
	if !containsCapability(entries, "subscription.describe_capability") {
		t.Fatalf("ordinary group user should see subscription status capability: %+v", entries)
	}

	reply := buildProtocolCapabilityReply(DomainSubscription, &tools.UserContext{
		UserRole:         0,
		ConversationType: "2",
	})
	if !strings.Contains(reply, "查询") {
		t.Fatalf("reply = %q, want query subscription status capability", reply)
	}
	if strings.Contains(reply, "只能") || strings.Contains(reply, "管理员能力") {
		t.Fatalf("reply = %q, should not say subscription query is admin-only", reply)
	}
}

func TestCapabilityCatalogShowsGroupSubscriptionForAdminGroup(t *testing.T) {
	t.Parallel()

	entries := capabilityEntriesFor(capabilityContext{
		UserRole:         1,
		ConversationType: "2",
	})
	if !containsCapability(entries, "subscription.describe_capability") {
		t.Fatalf("admin group should see subscription capability: %+v", entries)
	}
}

func TestManualSignCapabilityIsAnswerOnly(t *testing.T) {
	t.Parallel()

	entry, ok := lookupCapability("manual_sign.describe_capability")
	if !ok {
		t.Fatal("manual sign capability missing")
	}
	if entry.OperationScope != "manual_sign.describe_capability" {
		t.Fatalf("entry = %+v", entry)
	}
	if strings.Contains(entry.Description, "已执行") || strings.Contains(entry.Description, "已补签") {
		t.Fatalf("manual sign capability must describe, not execute: %q", entry.Description)
	}
}

func TestCapabilityCatalogEntriesAreDerivedFromOperationManifestBindings(t *testing.T) {
	t.Parallel()

	want := 0
	for _, manifest := range operationManifests() {
		if manifest.Capability != nil {
			want++
		}
	}

	entries := capabilityCatalogEntries()
	if len(entries) != want {
		t.Fatalf("capabilityCatalogEntries() len = %d, want %d derived manifest capabilities", len(entries), want)
	}
	for _, entry := range entries {
		manifest, ok := lookupOperation(entry.OperationScope)
		if !ok {
			t.Fatalf("capability %q has no operation manifest", entry.OperationScope)
		}
		if manifest.Capability == nil {
			t.Fatalf("capability %q is not backed by a manifest capability binding", entry.OperationScope)
		}
		if entry.Title != manifest.Capability.Title || entry.Description != manifest.Capability.Description {
			t.Fatalf("capability %q = %+v, want catalog binding %+v", entry.OperationScope, entry, manifest.Capability)
		}
		if entry.MinRole != manifest.MinRole || entry.ConversationScope != string(manifest.Scope) {
			t.Fatalf("capability %q role/scope = %d/%q, want %d/%q",
				entry.OperationScope, entry.MinRole, entry.ConversationScope, manifest.MinRole, manifest.Scope)
		}
	}
}

func TestCapabilitySnapshotUsesOnlyUserVisibleManifestCapabilities(t *testing.T) {
	t.Parallel()

	snapshot := capabilitySnapshot(capabilityContext{
		UserRole:         1,
		ConversationType: "2",
	})
	if len(snapshot) == 0 {
		t.Fatal("capabilitySnapshot() = empty, want user-visible catalog capabilities")
	}
	for _, entry := range snapshot {
		manifest, ok := lookupOperation(entry.Operation)
		if !ok {
			t.Fatalf("snapshot operation %q has no manifest", entry.Operation)
		}
		if manifest.Capability == nil {
			t.Fatalf("snapshot operation %q has no capability binding", entry.Operation)
		}
		if manifest.Availability != OperationAvailabilityActive &&
			manifest.Availability != OperationAvailabilityAnswerOnly {
			t.Fatalf("snapshot operation %q availability = %q, want user-visible", entry.Operation, manifest.Availability)
		}
		if entry.Availability != manifest.Availability ||
			entry.DirectlyUsable != manifest.Capability.DirectlyUsable {
			t.Fatalf("snapshot entry = %+v, want availability/direct usability from manifest %+v", entry, manifest)
		}
	}
}

func TestCapabilitySnapshotKeepsRoleAndConversationRestrictions(t *testing.T) {
	t.Parallel()

	dmUser := capabilitySnapshot(capabilityContext{UserRole: 0, ConversationType: "1"})
	if containsSnapshotCapability(dmUser, "subscription.describe_capability") {
		t.Fatalf("ordinary DM snapshot should hide group-only subscription: %+v", dmUser)
	}

	groupUser := capabilitySnapshot(capabilityContext{UserRole: 0, ConversationType: "2"})
	if !containsSnapshotCapability(groupUser, "subscription.describe_capability") {
		t.Fatalf("ordinary group snapshot should include subscription query: %+v", groupUser)
	}
}

func TestProtocolHelpDoesNotAdvertiseManualSignAsDirectExecution(t *testing.T) {
	t.Parallel()

	reply := buildHelpReply(&tools.UserContext{
		UserRole:         1,
		ConversationType: "2",
	})
	directMarker := "你当前在这个会话里可直接使用："
	directIndex := strings.Index(reply, directMarker)
	if directIndex < 0 {
		t.Fatalf("reply = %q, missing direct availability section", reply)
	}
	directSection := reply[directIndex:]
	if strings.Contains(directSection, "管理员补签") || strings.Contains(directSection, "代签") || strings.Contains(directSection, "补签") {
		t.Fatalf("reply = %q, should not advertise manual sign as direct protocol-live execution", reply)
	}
	if strings.Contains(reply, "人员交叉筛选") || strings.Contains(reply, "考勤统计分析") {
		t.Fatalf("reply = %q, should not advertise legacy-only direct actions as protocol-live direct abilities", reply)
	}
	if strings.Contains(reply, "周排行") || strings.Contains(reply, "周排名") {
		t.Fatalf("reply = %q, should not advertise legacy-only weekly rankings", reply)
	}
	if !strings.Contains(reply, "当前聊天路径不直接执行补签") {
		t.Fatalf("reply = %q, want explicit answer-only manual sign limitation", reply)
	}
}

func TestManualSignCapabilityReplyIsAnswerOnly(t *testing.T) {
	t.Parallel()

	reply := buildManualSignCapabilityReply(&tools.UserContext{UserRole: 1})
	if strings.Contains(reply, "可以为指定用户代签") || strings.Contains(reply, "我需要明确") {
		t.Fatalf("reply = %q, should describe capability without implying execution", reply)
	}
	if !strings.Contains(reply, "管理员能力") {
		t.Fatalf("reply = %q, want admin capability explanation", reply)
	}
}

func containsCapability(entries []CapabilityEntry, operation string) bool {
	for _, entry := range entries {
		if entry.OperationScope == operation {
			return true
		}
	}
	return false
}

func containsSnapshotCapability(entries []CapabilitySnapshotEntry, operation string) bool {
	for _, entry := range entries {
		if entry.Operation == operation {
			return true
		}
	}
	return false
}

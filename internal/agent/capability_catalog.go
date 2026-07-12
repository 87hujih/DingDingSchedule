package agent

// OperationCatalog-derived capability view; this is not an independent capability source.

type capabilityContext struct {
	UserRole         int
	ConversationType string
}

type CapabilitySnapshotEntry struct {
	Operation      string
	Title          string
	Description    string
	Availability   OperationAvailability
	DirectlyUsable bool
}

type CapabilityEntry struct {
	OperationScope    string
	Title             string
	Description       string
	MinRole           int
	ConversationScope string
	AnswerOnly        bool
}

func capabilitySnapshot(ctx capabilityContext) []CapabilitySnapshotEntry {
	manifests := userVisibleOperationManifests()
	entries := make([]CapabilitySnapshotEntry, 0, len(manifests))
	for _, manifest := range manifests {
		if ctx.UserRole < manifest.MinRole {
			continue
		}
		if !matchesConversationScope(string(manifest.Scope), ctx.ConversationType) {
			continue
		}
		entry := CapabilitySnapshotEntry{
			Operation:      manifest.Name,
			Availability:   manifest.Availability,
			DirectlyUsable: manifest.Availability == OperationAvailabilityActive,
		}
		if manifest.Capability != nil {
			entry.Title = manifest.Capability.Title
			entry.Description = manifest.Capability.Description
			entry.DirectlyUsable = manifest.Capability.DirectlyUsable
		}
		entry.Description = filteredCapabilityDescription(entry, ctx)
		entries = append(entries, entry)
	}
	return entries
}

func filteredCapabilityDescription(entry CapabilitySnapshotEntry, ctx capabilityContext) string {
	switch entry.Operation {
	case "system.describe_capability":
		return "根据当前会话和权限说明可用能力。"
	case "subscription.describe_capability":
		if ctx.UserRole >= 1 {
			return "在群聊里可以查询当前群考勤推送订阅状态；管理员还可以开启、取消或按部门管理订阅。"
		}
		return "在群聊里可以查询当前群考勤推送订阅状态。"
	default:
		return entry.Description
	}
}

func capabilityCatalogEntries() []CapabilityEntry {
	manifests := operationManifests()
	entries := make([]CapabilityEntry, 0, len(manifests))
	for _, manifest := range manifests {
		if manifest.Capability == nil {
			continue
		}
		entries = append(entries, CapabilityEntry{
			OperationScope:    manifest.Name,
			Title:             manifest.Capability.Title,
			Description:       manifest.Capability.Description,
			MinRole:           manifest.MinRole,
			ConversationScope: string(manifest.Scope),
			AnswerOnly:        manifest.Capability.AnswerOnly,
		})
	}
	return entries
}

func lookupCapability(operation string) (CapabilityEntry, bool) {
	for _, entry := range capabilityCatalogEntries() {
		if entry.OperationScope == operation {
			return entry, true
		}
	}
	return CapabilityEntry{}, false
}

func capabilityEntriesFor(ctx capabilityContext) []CapabilityEntry {
	entries := capabilityCatalogEntries()
	filtered := make([]CapabilityEntry, 0, len(entries))
	for _, entry := range entries {
		if ctx.UserRole < entry.MinRole {
			continue
		}
		if !matchesConversationScope(entry.ConversationScope, ctx.ConversationType) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

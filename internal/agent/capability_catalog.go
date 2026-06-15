package agent

type capabilityContext struct {
	UserRole         int
	ConversationType string
}

type CapabilityEntry struct {
	OperationScope    string
	Title             string
	Description       string
	MinRole           int
	ConversationScope string
	AnswerOnly        bool
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

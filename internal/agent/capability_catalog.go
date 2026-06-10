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
	return []CapabilityEntry{
		{
			OperationScope:    "system.describe_capability",
			Title:             "功能说明",
			Description:       "说明我当前可以处理的考勤、订阅、规则和课表能力。",
			ConversationScope: "both",
			AnswerOnly:        true,
		},
		{
			OperationScope:    "attendance.describe_capability",
			Title:             "考勤查询",
			Description:       "查询指定日期和节次的考勤状态。",
			ConversationScope: "both",
			AnswerOnly:        true,
		},
		{
			OperationScope:    "schedule.describe_capability",
			Title:             "课表查询",
			Description:       "查询自己的课表，也可以查询指定姓名用户的课表。",
			ConversationScope: "both",
			AnswerOnly:        true,
		},
		{
			OperationScope:    "subscription.describe_capability",
			Title:             "群考勤订阅",
			Description:       "在群聊里可以查询当前群考勤推送订阅状态；管理员还可以开启、取消或按部门管理订阅。",
			ConversationScope: "group",
			AnswerOnly:        true,
		},
		{
			OperationScope:    "manual_sign.describe_capability",
			Title:             "管理员补签",
			Description:       "说明代签/补签能力和所需信息；当前聊天路径不直接执行补签。",
			ConversationScope: "both",
			AnswerOnly:        true,
		},
	}
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

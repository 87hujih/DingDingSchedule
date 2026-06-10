package agent

type SlotDefault string

const (
	SlotDefaultNone        SlotDefault = ""
	SlotDefaultToday       SlotDefault = "today"
	SlotDefaultCurrentWeek SlotDefault = "current_week"
)

type QueryShapeMetadata struct {
	Name                  string
	RequiredTrustedParams []string
}

type OperationMetadata struct {
	Name                  string
	AllowedActs           []UserAct
	Domain                BusinessDomain
	IsWrite               bool
	MinRole               int
	RequiredTrustedParams []string
	OptionalTrustedParams []string
	QueryShapes           []QueryShapeMetadata
	Defaults              map[string]SlotDefault
}

var operationCatalogEntries = []OperationMetadata{
	{
		Name:                  "attendance.query_status",
		AllowedActs:           []UserAct{ActReadQuery},
		Domain:                DomainAttendance,
		MinRole:               0,
		RequiredTrustedParams: []string{"date", "section"},
		OptionalTrustedParams: []string{"user_id"},
		QueryShapes: []QueryShapeMetadata{
			{
				Name:                  "slot_status",
				RequiredTrustedParams: []string{"date", "section"},
			},
			{
				Name:                  "user_day_status",
				RequiredTrustedParams: []string{"date", "user_id"},
			},
		},
		Defaults: map[string]SlotDefault{
			"date": SlotDefaultToday,
		},
	},
	{
		Name:                  "subscription.start",
		AllowedActs:           []UserAct{ActWriteRequest},
		Domain:                DomainSubscription,
		IsWrite:               true,
		MinRole:               1,
		RequiredTrustedParams: []string{"conversation_id", "scope"},
		OptionalTrustedParams: []string{"dept_ids"},
	},
	{
		Name:                  "subscription.cancel",
		AllowedActs:           []UserAct{ActWriteRequest},
		Domain:                DomainSubscription,
		IsWrite:               true,
		MinRole:               1,
		RequiredTrustedParams: []string{"conversation_id"},
	},
	{
		Name:                  "subscription.query_status",
		AllowedActs:           []UserAct{ActReadQuery},
		Domain:                DomainSubscription,
		MinRole:               0,
		RequiredTrustedParams: []string{"conversation_id"},
	},
	{
		Name:        "subscription.list_departments",
		AllowedActs: []UserAct{ActReadQuery, ActWorkflowContinue},
		Domain:      DomainSubscription,
		MinRole:     0,
	},
	{
		Name:        "system.describe_capability",
		AllowedActs: []UserAct{ActHelp},
		Domain:      DomainSystem,
		MinRole:     0,
	},
	{
		Name:        "attendance.describe_capability",
		AllowedActs: []UserAct{ActCapabilityQuestion},
		Domain:      DomainAttendance,
		MinRole:     0,
	},
	{
		Name:        "schedule.describe_capability",
		AllowedActs: []UserAct{ActCapabilityQuestion},
		Domain:      DomainSchedule,
		MinRole:     0,
	},
	{
		Name:        "subscription.describe_capability",
		AllowedActs: []UserAct{ActCapabilityQuestion},
		Domain:      DomainSubscription,
		MinRole:     0,
	},
	{
		Name:        "manual_sign.describe_capability",
		AllowedActs: []UserAct{ActCapabilityQuestion},
		Domain:      DomainManualSign,
		MinRole:     0,
	},
	{
		Name:                  "attendance.rule_explain",
		AllowedActs:           []UserAct{ActRuleQuestion},
		Domain:                DomainAttendance,
		MinRole:               0,
		RequiredTrustedParams: []string{"rule_topic"},
	},
	{
		Name:                  "schedule.rule_explain",
		AllowedActs:           []UserAct{ActRuleQuestion},
		Domain:                DomainSchedule,
		MinRole:               0,
		RequiredTrustedParams: []string{"rule_topic"},
	},
	{
		Name:                  "subscription.rule_explain",
		AllowedActs:           []UserAct{ActRuleQuestion},
		Domain:                DomainSubscription,
		MinRole:               0,
		RequiredTrustedParams: []string{"rule_topic"},
	},
	{
		Name:                  "schedule.query_my_schedule",
		AllowedActs:           []UserAct{ActReadQuery},
		Domain:                DomainSchedule,
		MinRole:               0,
		RequiredTrustedParams: []string{"week"},
		Defaults: map[string]SlotDefault{
			"week": SlotDefaultCurrentWeek,
		},
	},
	{
		Name:                  "schedule.query_user_schedule",
		AllowedActs:           []UserAct{ActReadQuery},
		Domain:                DomainSchedule,
		MinRole:               0,
		RequiredTrustedParams: []string{"user_id", "week"},
		Defaults: map[string]SlotDefault{
			"week": SlotDefaultCurrentWeek,
		},
	},
}

// operationNames returns the catalog operation names in whitelist order.
func operationNames() []string {
	names := make([]string, 0, len(operationCatalogEntries))
	for _, metadata := range operationCatalogEntries {
		names = append(names, metadata.Name)
	}
	return names
}

// lookupOperation looks up operation.
func lookupOperation(name string) (OperationMetadata, bool) {
	for _, metadata := range operationCatalogEntries {
		if metadata.Name == name {
			return metadata, true
		}
	}
	return OperationMetadata{}, false
}

package agent

type OperationMetadata struct {
	Name                  string
	AllowedActs           []UserAct
	Domain                BusinessDomain
	IsWrite               bool
	RequiredTrustedParams []string
}

var operationCatalogEntries = []OperationMetadata{
	{
		Name:                  "attendance.query_status",
		AllowedActs:           []UserAct{ActReadQuery},
		Domain:                DomainAttendance,
		RequiredTrustedParams: []string{"date", "section"},
	},
	{
		Name:        "subscription.query_status",
		AllowedActs: []UserAct{ActReadQuery},
		Domain:      DomainSubscription,
	},
	{
		Name:                  "subscription.start",
		AllowedActs:           []UserAct{ActWriteRequest},
		Domain:                DomainSubscription,
		IsWrite:               true,
		RequiredTrustedParams: []string{"conversation_id", "scope"},
	},
	{
		Name:                  "subscription.cancel",
		AllowedActs:           []UserAct{ActWriteRequest},
		Domain:                DomainSubscription,
		IsWrite:               true,
		RequiredTrustedParams: []string{"conversation_id"},
	},
	{
		Name:        "manual_sign.describe_capability",
		AllowedActs: []UserAct{ActCapabilityQuestion},
		Domain:      DomainManualSign,
	},
	{
		Name:                  "manual_sign.create",
		AllowedActs:           []UserAct{ActWriteRequest},
		Domain:                DomainManualSign,
		IsWrite:               true,
		RequiredTrustedParams: []string{"user_id", "date", "section"},
	},
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

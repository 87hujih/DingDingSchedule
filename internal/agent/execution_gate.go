package agent

type trustedEntities struct {
	DepartmentID   int64
	UserID         uint
	UserName       string
	Date           string
	Section        int
	ConversationID string
	Scope          string
}

// buildOperationRequest builds an operation request from validated trusted entities.
func buildOperationRequest(draft ProtocolDraft, trusted trustedEntities) (OperationRequest, bool) {
	metadata, ok := lookupOperation(draft.Operation)
	if !ok {
		return OperationRequest{}, true
	}

	params := make(map[string]any, len(metadata.RequiredTrustedParams))
	for _, field := range metadata.RequiredTrustedParams {
		switch field {
		case "conversation_id":
			if trusted.ConversationID == "" {
				return OperationRequest{}, true
			}
			params[field] = trusted.ConversationID
		case "scope":
			if trusted.Scope == "" {
				return OperationRequest{}, true
			}
			params[field] = trusted.Scope
		case "user_id":
			if trusted.UserID == 0 {
				return OperationRequest{}, true
			}
			params[field] = trusted.UserID
		case "date":
			if trusted.Date == "" {
				return OperationRequest{}, true
			}
			params[field] = trusted.Date
		case "section":
			if trusted.Section == 0 {
				return OperationRequest{}, true
			}
			params[field] = trusted.Section
		}
	}

	return OperationRequest{
		Operation:     draft.Operation,
		TrustedParams: params,
	}, false
}

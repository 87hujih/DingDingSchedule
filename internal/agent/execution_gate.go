package agent

import "time"

type trustedEntities struct {
	DepartmentID   int64
	DeptIDs        []int64
	UserID         uint
	UserName       string
	Date           string
	Section        int
	Week           int
	ConversationID string
	Scope          string
	QueryShape     string
	UserRole       int
	TrustedParams  map[string]any
}

// buildOperationRequest builds an operation request from validated trusted entities.
func buildOperationRequest(draft ProtocolDraft, trusted trustedEntities) (OperationRequest, bool) {
	metadata, ok := lookupOperation(draft.Operation)
	if !ok {
		return OperationRequest{}, true
	}
	if metadata.IsWrite && trusted.UserRole < metadata.MinRole {
		return OperationRequest{}, true
	}

	requiredParams := metadata.RequiredTrustedParams
	params := make(map[string]any, len(requiredParams)+len(metadata.OptionalTrustedParams)+1)
	if len(metadata.QueryShapes) > 0 {
		shape, ok := selectQueryShape(metadata, trusted)
		if !ok {
			return OperationRequest{}, true
		}
		requiredParams = shape.RequiredTrustedParams
		params["query_shape"] = shape.Name
	}

	for _, field := range requiredParams {
		value, ok := trustedParamValue(trusted, field)
		if !ok {
			return OperationRequest{}, true
		}
		params[field] = value
	}
	for _, field := range metadata.OptionalTrustedParams {
		value, ok := trustedParamValue(trusted, field)
		if ok {
			params[field] = value
		}
	}
	if !subscriptionScopeValid(draft.Operation, params) {
		return OperationRequest{}, true
	}

	return OperationRequest{
		Operation:     draft.Operation,
		TrustedParams: params,
	}, false
}

func selectQueryShape(metadata OperationMetadata, trusted trustedEntities) (QueryShapeMetadata, bool) {
	if trusted.QueryShape != "" {
		for _, shape := range metadata.QueryShapes {
			if shape.Name == trusted.QueryShape {
				return shape, true
			}
		}
		return QueryShapeMetadata{}, false
	}
	for _, shape := range metadata.QueryShapes {
		if trustedHasRequiredParams(trusted, shape.RequiredTrustedParams) {
			return shape, true
		}
	}
	return QueryShapeMetadata{}, false
}

func trustedHasRequiredParams(trusted trustedEntities, fields []string) bool {
	for _, field := range fields {
		if _, ok := trustedParamValue(trusted, field); !ok {
			return false
		}
	}
	return true
}

func trustedParamValue(trusted trustedEntities, field string) (any, bool) {
	if trusted.TrustedParams != nil {
		if value, ok := trusted.TrustedParams[field]; ok {
			return canonicalTrustedParamValue(field, value)
		}
	}
	switch field {
	case "conversation_id":
		return trusted.ConversationID, trusted.ConversationID != ""
	case "scope":
		return canonicalTrustedParamValue(field, trusted.Scope)
	case "dept_ids":
		if len(trusted.DeptIDs) > 0 {
			return append([]int64(nil), trusted.DeptIDs...), true
		}
		if trusted.DepartmentID != 0 {
			return []int64{trusted.DepartmentID}, true
		}
	case "user_id":
		return trusted.UserID, trusted.UserID != 0
	case "date":
		return canonicalTrustedParamValue(field, trusted.Date)
	case "section":
		return trusted.Section, trusted.Section != 0
	case "week":
		return trusted.Week, trusted.Week != 0
	}
	return nil, false
}

func canonicalTrustedParamValue(field string, value any) (any, bool) {
	switch field {
	case "conversation_id":
		typed, ok := value.(string)
		return typed, ok && typed != ""
	case "scope":
		typed, ok := value.(string)
		return typed, ok && (typed == "all" || typed == "department")
	case "dept_ids":
		typed, ok := value.([]int64)
		if !ok || len(typed) == 0 {
			return nil, false
		}
		return append([]int64(nil), typed...), true
	case "user_id":
		typed, ok := value.(uint)
		return typed, ok && typed != 0
	case "date":
		typed, ok := value.(string)
		if !ok || typed == "" {
			return "", false
		}
		if _, err := time.Parse("2006-01-02", typed); err != nil {
			return "", false
		}
		return typed, true
	case "section":
		typed, ok := value.(int)
		return typed, ok && typed > 0
	case "week":
		typed, ok := value.(int)
		return typed, ok && typed > 0
	case "rule_topic":
		typed, ok := value.(string)
		return typed, ok && typed != ""
	default:
		return nil, false
	}
}

func subscriptionScopeValid(operation string, params map[string]any) bool {
	if operation != "subscription.start" {
		return true
	}
	scope, ok := params["scope"].(string)
	if !ok {
		return false
	}
	switch scope {
	case "all":
		return true
	case "department":
		deptIDs, ok := params["dept_ids"].([]int64)
		return ok && len(deptIDs) > 0
	default:
		return false
	}
}

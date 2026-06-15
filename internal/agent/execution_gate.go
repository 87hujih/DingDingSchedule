package agent

import "time"

type trustedEntities struct {
	TenantID       uint
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
	TrustedParams  map[string]TrustedParam
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

	requiredParams := paramNames(metadata.RequiredTrustedParams)
	optionalParams := paramNames(metadata.OptionalTrustedParams)
	params := make(map[string]TrustedParam, len(requiredParams)+len(optionalParams)+1)
	if len(metadata.QueryShapes) > 0 {
		shape, ok := selectQueryShape(metadata, trusted)
		if !ok {
			return OperationRequest{}, true
		}
		requiredParams = paramNames(shape.RequiredTrustedParams)
		params["query_shape"] = trustedParam("query_shape", shape.Name, trusted.TenantID, TrustedParamSource{
			Kind:     TrustedParamSourceDerived,
			Resolver: "query_shape_selector",
		})
	}

	for _, field := range requiredParams {
		value, ok := trustedParamValue(trusted, field)
		if !ok {
			return OperationRequest{}, true
		}
		params[field] = value
	}
	for _, field := range optionalParams {
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
		if trustedHasRequiredParams(trusted, paramNames(shape.RequiredTrustedParams)) {
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

func trustedParamValue(trusted trustedEntities, field string) (TrustedParam, bool) {
	if trusted.TrustedParams != nil {
		if param, ok := trusted.TrustedParams[field]; ok {
			if trusted.TenantID != 0 && param.TenantID != trusted.TenantID {
				return TrustedParam{}, false
			}
			value, ok := canonicalTrustedParamValue(field, param.Value)
			if !ok {
				return TrustedParam{}, false
			}
			param.Field = field
			param.Value = value
			if param.TenantID == 0 {
				param.TenantID = trusted.TenantID
			}
			if param.Source.Kind == "" {
				param.Source = TrustedParamSource{Kind: TrustedParamSourceWorkflow, Resolver: "trusted_params"}
			}
			return param, true
		}
	}
	switch field {
	case "conversation_id":
		return trustedParamFromTrustedEntity(trusted, field, trusted.ConversationID, TrustedParamSourceRuntime, "conversation_runtime")
	case "scope":
		return trustedParamFromTrustedEntity(trusted, field, trusted.Scope, TrustedParamSourceWorkflow, "subscription_scope")
	case "dept_ids":
		if len(trusted.DeptIDs) > 0 {
			return trustedParamFromTrustedEntity(trusted, field, append([]int64(nil), trusted.DeptIDs...), TrustedParamSourceWorkflow, "department_resolver")
		}
		if trusted.DepartmentID != 0 {
			return trustedParamFromTrustedEntity(trusted, field, []int64{trusted.DepartmentID}, TrustedParamSourceWorkflow, "department_resolver")
		}
	case "user_id":
		return trustedParamFromTrustedEntity(trusted, field, trusted.UserID, TrustedParamSourceWorkflow, "user_resolver")
	case "date":
		return trustedParamFromTrustedEntity(trusted, field, trusted.Date, TrustedParamSourceWorkflow, "date_slot")
	case "section":
		return trustedParamFromTrustedEntity(trusted, field, trusted.Section, TrustedParamSourceWorkflow, "section_slot")
	case "week":
		return trustedParamFromTrustedEntity(trusted, field, trusted.Week, TrustedParamSourceWorkflow, "week_slot")
	}
	return TrustedParam{}, false
}

func trustedParamFromTrustedEntity(trusted trustedEntities, field string, value any, source TrustedParamSourceKind, resolver string) (TrustedParam, bool) {
	canonical, ok := canonicalTrustedParamValue(field, value)
	if !ok {
		return TrustedParam{}, false
	}
	return trustedParam(field, canonical, trusted.TenantID, TrustedParamSource{
		Kind:     source,
		Resolver: resolver,
	}), true
}

func canonicalTrustedParamValue(field string, value any) (any, bool) {
	switch field {
	case "conversation_id":
		typed, ok := value.(string)
		return typed, ok && typed != ""
	case "scope":
		typed, ok := value.(string)
		return typed, ok && typed != ""
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
	case "query_shape":
		typed, ok := value.(string)
		return typed, ok && typed != ""
	default:
		return nil, false
	}
}

func subscriptionScopeValid(operation string, params map[string]TrustedParam) bool {
	if operation != "subscription.start" {
		return true
	}
	scopeParam, ok := params["scope"]
	if !ok {
		return false
	}
	scope, _ := scopeParam.Value.(string)
	switch scope {
	case "all":
		return true
	case "department":
		deptParam, ok := params["dept_ids"]
		if !ok {
			return false
		}
		deptIDs, ok := deptParam.Value.([]int64)
		return ok && len(deptIDs) > 0
	default:
		return false
	}
}

func trustedParamsFromValues(tenantID uint, source TrustedParamSource, values map[string]any) map[string]TrustedParam {
	if len(values) == 0 {
		return nil
	}
	params := make(map[string]TrustedParam, len(values))
	for field, value := range values {
		canonical, ok := canonicalTrustedParamValue(field, value)
		if !ok {
			params[field] = trustedParam(field, value, tenantID, source)
			continue
		}
		params[field] = trustedParam(field, canonical, tenantID, source)
	}
	return params
}

func trustedParamConcreteValue(params map[string]TrustedParam, field string) (any, bool) {
	if params == nil {
		return nil, false
	}
	param, ok := params[field]
	if !ok {
		return nil, false
	}
	value, ok := canonicalTrustedParamValue(field, param.Value)
	if !ok {
		return nil, false
	}
	return value, true
}

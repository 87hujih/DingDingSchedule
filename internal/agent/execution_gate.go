package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"schedule_server/internal/agent/tools"
)

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

type WriteGuard interface {
	Check(input WriteGuardInput) WriteGuardResult
}

type WriteGuardInput struct {
	User      *tools.UserContext
	Manifest  OperationManifest
	Request   OperationRequest
	Workflow  *WorkflowSnapshot
	Confirmed bool
}

type WriteGuardResult struct {
	Allow          bool
	ResponseKind   ResponseKind
	BlockedReason  string
	IdempotencyKey string
}

type writeGuard struct{}

func newWriteGuard() WriteGuard {
	return writeGuard{}
}

func (writeGuard) Check(input WriteGuardInput) WriteGuardResult {
	manifest, ok := writeGuardManifest(input)
	if !ok {
		return WriteGuardResult{ResponseKind: ResponseRefuse, BlockedReason: "operation_not_allowed"}
	}
	if !manifest.IsWrite {
		return WriteGuardResult{Allow: true, ResponseKind: manifestResponseKind(manifest)}
	}
	if writeRequiresConfirmation(manifest.Risk) && !input.Confirmed {
		return WriteGuardResult{
			ResponseKind:   ResponseConfirm,
			BlockedReason:  "write_confirmation_required",
			IdempotencyKey: buildIdempotencyKey(manifest, input),
		}
	}
	key := buildIdempotencyKey(manifest, input)
	if key == "" {
		return WriteGuardResult{ResponseKind: ResponseRefuse, BlockedReason: "idempotency_key_missing"}
	}
	return WriteGuardResult{
		Allow:          true,
		ResponseKind:   manifestResponseKind(manifest),
		IdempotencyKey: key,
	}
}

func writeGuardManifest(input WriteGuardInput) (OperationManifest, bool) {
	if input.Manifest.Name != "" {
		return input.Manifest, true
	}
	return lookupOperation(input.Request.Operation)
}

func writeRequiresConfirmation(risk RiskLevel) bool {
	return risk == RiskWriteMedium || risk == RiskWriteHigh
}

func manifestResponseKind(manifest OperationManifest) ResponseKind {
	if manifest.Renderer.Kind != "" {
		return manifest.Renderer.Kind
	}
	return ResponseResult
}

func buildIdempotencyKey(manifest OperationManifest, input WriteGuardInput) string {
	if len(manifest.Idempotency.KeyFields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(manifest.Idempotency.KeyFields))
	for _, field := range manifest.Idempotency.KeyFields {
		value, ok := idempotencyFieldValue(field, input)
		if !ok {
			return ""
		}
		parts = append(parts, field+"="+value)
	}
	raw := strings.Join(parts, "|")
	sum := sha256.Sum256([]byte(raw))
	encoded := hex.EncodeToString(sum[:])
	return fmt.Sprintf("%s:%s", manifest.Name, encoded[:16])
}

func idempotencyFieldValue(field string, input WriteGuardInput) (string, bool) { //nolint:gocyclo // Idempotency fields are enumerated explicitly to keep write keys reviewable.
	switch field {
	case "tenant_id":
		if input.Request.TenantID != 0 {
			return fmt.Sprint(input.Request.TenantID), true
		}
		if input.User != nil && input.User.TenantID != 0 {
			return fmt.Sprint(input.User.TenantID), true
		}
	case "actor_user_id":
		if input.Request.ActorUserID != 0 {
			return fmt.Sprint(input.Request.ActorUserID), true
		}
		if input.User != nil && input.User.UserID != 0 {
			return fmt.Sprint(input.User.UserID), true
		}
	case "conversation_id":
		if strings.TrimSpace(input.Request.ConversationID) != "" {
			return strings.TrimSpace(input.Request.ConversationID), true
		}
		if value, ok := extractParamString(input.Request.TrustedParams, "conversation_id"); ok {
			return value, true
		}
		if input.User != nil && strings.TrimSpace(input.User.ConversationID) != "" {
			return strings.TrimSpace(input.User.ConversationID), true
		}
	case "operation":
		if strings.TrimSpace(input.Request.Operation) != "" {
			return strings.TrimSpace(input.Request.Operation), true
		}
	case "workflow_id":
		if input.Workflow != nil && strings.TrimSpace(input.Workflow.ID) != "" {
			return strings.TrimSpace(input.Workflow.ID), true
		}
	case "dept_ids":
		value, ok := trustedParamConcreteValue(input.Request.TrustedParams, field)
		if !ok {
			return "none", true
		}
		return canonicalIdempotencyValue(value), true
	default:
		value, ok := trustedParamConcreteValue(input.Request.TrustedParams, field)
		if !ok {
			return "", false
		}
		return canonicalIdempotencyValue(value), true
	}
	return "", false
}

func canonicalIdempotencyValue(value any) string {
	switch typed := value.(type) {
	case []int64:
		values := append([]int64(nil), typed...)
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		parts := make([]string, 0, len(values))
		for _, item := range values {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ",")
	case []int:
		values := append([]int(nil), typed...)
		sort.Ints(values)
		parts := make([]string, 0, len(values))
		for _, item := range values {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(value)
	}
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

func trustedParamValue(trusted trustedEntities, field string) (TrustedParam, bool) { //nolint:gocyclo // Trusted protocol params are intentionally validated in one auditable switch.
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
	case operationParamActorRole:
		typed, ok := value.(int)
		return typed, ok && typed >= 0
	case operationParamConversationType:
		typed, ok := value.(string)
		return typed, ok && typed != ""
	case operationParamConversationTitle:
		typed, ok := value.(string)
		return typed, ok
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
	scope, ok := scopeParam.Value.(string)
	if !ok {
		return false
	}
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

package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	workflowSnapshotSchemaVersion = 1
	workflowSnapshotMaxBytes      = 64 * 1024
	executionSchemaVersion        = 1

	workflowSnapshotMaxCandidateFields    = 16
	workflowSnapshotMaxCandidatesPerField = 50
	workflowSnapshotMaxCandidatesTotal    = 200
	workflowSnapshotMaxTrustedEntities    = 32
	workflowSnapshotMaxTrustedParams      = 32
	workflowSnapshotMaxInt64SliceItems    = 256

	workflowSnapshotMaxKeyRunes          = 64
	workflowSnapshotMaxIDRunes           = 128
	workflowSnapshotMaxWorkflowIDRunes   = 64
	workflowSnapshotMaxLabelRunes        = 256
	workflowSnapshotMaxTypedStringRunes  = 4096
	workflowSnapshotMaxConversationRunes = 128
	executionMaxTrustedParams            = 6
	executionMaxProjectionRunes          = 64
)

type persistedWorkflowSnapshotV1 struct {
	SchemaVersion   int                                 `json:"schema_version"`
	ID              string                              `json:"id"`
	TenantID        uint                                `json:"tenant_id"`
	ActorUserID     uint                                `json:"actor_user_id"`
	ConversationID  string                              `json:"conversation_id"`
	Type            WorkflowType                        `json:"type"`
	State           WorkflowState                       `json:"state"`
	MissingFields   []string                            `json:"missing_fields,omitempty"`
	Trusted         persistedTrustedStateV1             `json:"trusted"`
	TrustedEntities map[string]persistedTrustedEntityV1 `json:"trusted_entities,omitempty"`
	Candidates      map[string][]persistedCandidateV1   `json:"candidates,omitempty"`
	CreatedAt       string                              `json:"created_at,omitempty"`
	UpdatedAt       string                              `json:"updated_at,omitempty"`
	ExpiresAt       string                              `json:"expires_at"`
	Version         uint64                              `json:"version"`
}

type persistedTrustedStateV1 struct {
	TenantID       uint                               `json:"tenant_id,omitempty"`
	DepartmentID   int64                              `json:"department_id,omitempty"`
	DeptIDs        []int64                            `json:"dept_ids,omitempty"`
	UserID         uint                               `json:"user_id,omitempty"`
	UserName       string                             `json:"user_name,omitempty"`
	Date           string                             `json:"date,omitempty"`
	Section        int                                `json:"section,omitempty"`
	Week           int                                `json:"week,omitempty"`
	ConversationID string                             `json:"conversation_id,omitempty"`
	Scope          string                             `json:"scope,omitempty"`
	QueryShape     string                             `json:"query_shape,omitempty"`
	UserRole       int                                `json:"user_role,omitempty"`
	TrustedParams  map[string]persistedTrustedParamV1 `json:"trusted_params,omitempty"`
}

type persistedTrustedEntityV1 struct {
	ID       string                `json:"id,omitempty"`
	Type     string                `json:"type,omitempty"`
	Label    string                `json:"label,omitempty"`
	Value    persistedTypedValueV1 `json:"value"`
	TenantID uint                  `json:"tenant_id,omitempty"`
}

type persistedCandidateV1 struct {
	ID       string                `json:"id,omitempty"`
	Label    string                `json:"label"`
	Value    persistedTypedValueV1 `json:"value"`
	TenantID uint                  `json:"tenant_id,omitempty"`
}

type persistedTrustedParamV1 struct {
	Field    string                 `json:"field"`
	Value    persistedTypedValueV1  `json:"value"`
	Source   persistedParamSourceV1 `json:"source"`
	TenantID uint                   `json:"tenant_id,omitempty"`
}

type persistedParamSourceV1 struct {
	Kind     TrustedParamSourceKind `json:"kind,omitempty"`
	Resolver string                 `json:"resolver,omitempty"`
}

type persistedTypedValueV1 struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

type persistedReservedExecutionV1 struct {
	SchemaVersion    int                              `json:"schema_version"`
	Operation        string                           `json:"operation"`
	BusinessKey      string                           `json:"business_key"`
	TrustedParams    map[string]persistedTypedValueV1 `json:"trusted_params,omitempty"`
	ExecutionToken   string                           `json:"execution_token"`
	AttemptRequestID string                           `json:"attempt_request_id"`
	StartedAt        string                           `json:"started_at"`
	LeaseExpiresAt   string                           `json:"lease_expires_at"`
}

type persistedExecutionResultV1 struct {
	SchemaVersion int         `json:"schema_version"`
	BusinessKey   string      `json:"business_key"`
	WriteEffect   WriteEffect `json:"write_effect"`
	CompletedAt   string      `json:"completed_at"`
}

// MarshalReservedExecution serializes the exact authority needed to retry a
// reserved subscription write. Arbitrary tool arguments and raw user text are
// rejected instead of being persisted.
func MarshalReservedExecution(reservation ReservedExecutionV1) ([]byte, error) {
	dto, err := reservedExecutionToV1(reservation)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("marshal reserved execution v1: %w", err)
	}
	if len(payload) > workflowSnapshotMaxBytes {
		return nil, fmt.Errorf("reserved execution exceeds %d bytes", workflowSnapshotMaxBytes)
	}
	return payload, nil
}

// UnmarshalReservedExecution accepts only the explicit V1 execution schema.
func UnmarshalReservedExecution(payload []byte) (ReservedExecutionV1, error) {
	if len(payload) == 0 {
		return ReservedExecutionV1{}, errors.New("reserved execution payload is empty")
	}
	if len(payload) > workflowSnapshotMaxBytes {
		return ReservedExecutionV1{}, fmt.Errorf("reserved execution exceeds %d bytes", workflowSnapshotMaxBytes)
	}
	var dto persistedReservedExecutionV1
	if err := decodeStrictWorkflowJSON(payload, &dto); err != nil {
		return ReservedExecutionV1{}, fmt.Errorf("decode reserved execution v1: %w", err)
	}
	if dto.SchemaVersion != executionSchemaVersion {
		return ReservedExecutionV1{}, fmt.Errorf("unsupported reserved execution schema version %d", dto.SchemaVersion)
	}
	return reservedExecutionFromV1(dto)
}

// MarshalPersistedExecutionResult serializes an authoritative write result.
func MarshalPersistedExecutionResult(result PersistedExecutionResultV1) ([]byte, error) {
	dto, err := persistedExecutionResultToV1(result)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("marshal persisted execution result v1: %w", err)
	}
	if len(payload) > workflowSnapshotMaxBytes {
		return nil, fmt.Errorf("persisted execution result exceeds %d bytes", workflowSnapshotMaxBytes)
	}
	return payload, nil
}

// UnmarshalPersistedExecutionResult accepts only the explicit V1 result schema.
func UnmarshalPersistedExecutionResult(payload []byte) (PersistedExecutionResultV1, error) {
	if len(payload) == 0 {
		return PersistedExecutionResultV1{}, errors.New("persisted execution result payload is empty")
	}
	if len(payload) > workflowSnapshotMaxBytes {
		return PersistedExecutionResultV1{}, fmt.Errorf("persisted execution result exceeds %d bytes", workflowSnapshotMaxBytes)
	}
	var dto persistedExecutionResultV1
	if err := decodeStrictWorkflowJSON(payload, &dto); err != nil {
		return PersistedExecutionResultV1{}, fmt.Errorf("decode persisted execution result v1: %w", err)
	}
	if dto.SchemaVersion != executionSchemaVersion {
		return PersistedExecutionResultV1{}, fmt.Errorf(
			"unsupported persisted execution result schema version %d",
			dto.SchemaVersion,
		)
	}
	return persistedExecutionResultFromV1(dto)
}

func reservedExecutionToV1(reservation ReservedExecutionV1) (persistedReservedExecutionV1, error) { //nolint:gocyclo // Explicit V1 validation intentionally rejects each malformed authority field.
	if err := validateReservedExecution(reservation); err != nil {
		return persistedReservedExecutionV1{}, err
	}
	if reservation.Operation != strings.TrimSpace(reservation.Operation) ||
		reservation.BusinessKey != strings.TrimSpace(reservation.BusinessKey) ||
		reservation.ExecutionToken != strings.TrimSpace(reservation.ExecutionToken) ||
		reservation.AttemptRequestID != strings.TrimSpace(reservation.AttemptRequestID) {
		return persistedReservedExecutionV1{}, errors.New("reserved execution identifiers must be normalized")
	}
	switch reservation.Operation {
	case "subscription.start", "subscription.cancel":
	default:
		return persistedReservedExecutionV1{}, fmt.Errorf(
			"unsupported reserved execution operation %q",
			reservation.Operation,
		)
	}
	if !runeLengthWithin(reservation.Operation, executionMaxProjectionRunes) ||
		!runeLengthWithin(reservation.BusinessKey, executionMaxProjectionRunes) ||
		!runeLengthWithin(reservation.ExecutionToken, executionMaxProjectionRunes) ||
		!runeLengthWithin(reservation.AttemptRequestID, executionMaxProjectionRunes) {
		return persistedReservedExecutionV1{}, errors.New("reserved execution identifier exceeds limit")
	}
	if len(reservation.TrustedParams) > executionMaxTrustedParams {
		return persistedReservedExecutionV1{}, errors.New("reserved execution trusted params exceed limit")
	}

	params := make(map[string]persistedTypedValueV1, len(reservation.TrustedParams))
	for key, value := range reservation.TrustedParams {
		normalized, err := normalizeExecutionTrustedParam(key, value)
		if err != nil {
			return persistedReservedExecutionV1{}, err
		}
		encoded, err := encodePersistedTypedValue(normalized)
		if err != nil {
			return persistedReservedExecutionV1{}, fmt.Errorf("reserved execution param %q: %w", key, err)
		}
		params[key] = encoded
	}
	if len(params) == 0 {
		params = nil
	}
	return persistedReservedExecutionV1{
		SchemaVersion:    executionSchemaVersion,
		Operation:        reservation.Operation,
		BusinessKey:      reservation.BusinessKey,
		TrustedParams:    params,
		ExecutionToken:   reservation.ExecutionToken,
		AttemptRequestID: reservation.AttemptRequestID,
		StartedAt:        formatWorkflowTime(normalizeWorkflowDatabaseTime(reservation.StartedAt)),
		LeaseExpiresAt:   formatWorkflowTime(normalizeWorkflowDatabaseTime(reservation.LeaseExpiresAt)),
	}, nil
}

func reservedExecutionFromV1(dto persistedReservedExecutionV1) (ReservedExecutionV1, error) {
	startedAt, err := parseExecutionTime("started_at", dto.StartedAt)
	if err != nil {
		return ReservedExecutionV1{}, err
	}
	leaseExpiresAt, err := parseExecutionTime("lease_expires_at", dto.LeaseExpiresAt)
	if err != nil {
		return ReservedExecutionV1{}, err
	}
	params := make(PersistedTrustedParamsV1, len(dto.TrustedParams))
	for key, encoded := range dto.TrustedParams {
		value, err := decodePersistedTypedValue(encoded)
		if err != nil {
			return ReservedExecutionV1{}, fmt.Errorf("reserved execution param %q: %w", key, err)
		}
		normalized, err := normalizeExecutionTrustedParam(key, value)
		if err != nil {
			return ReservedExecutionV1{}, err
		}
		params[key] = normalized
	}
	if len(params) == 0 {
		params = nil
	}
	reservation := ReservedExecutionV1{
		Operation:        dto.Operation,
		BusinessKey:      dto.BusinessKey,
		TrustedParams:    params,
		ExecutionToken:   dto.ExecutionToken,
		AttemptRequestID: dto.AttemptRequestID,
		StartedAt:        startedAt,
		LeaseExpiresAt:   leaseExpiresAt,
	}
	_, err = reservedExecutionToV1(reservation)
	return reservation, err
}

func normalizeExecutionTrustedParam(key string, value any) (any, error) { //nolint:gocyclo // The persisted execution allowlist validates each supported typed field explicitly.
	switch key {
	case "conversation_id":
		conversationID, ok := value.(string)
		if !ok || conversationID == "" || conversationID != strings.TrimSpace(conversationID) ||
			!runeLengthWithin(conversationID, workflowSnapshotMaxConversationRunes) {
			return nil, errors.New("reserved execution conversation_id is invalid")
		}
		return conversationID, nil
	case "scope":
		scope, ok := value.(string)
		if !ok || (scope != "all" && scope != "department") {
			return nil, errors.New("reserved execution scope is invalid")
		}
		return scope, nil
	case "dept_ids":
		deptIDs, ok := value.([]int64)
		if !ok || len(deptIDs) > workflowSnapshotMaxInt64SliceItems {
			return nil, errors.New("reserved execution dept_ids is invalid")
		}
		for _, departmentID := range deptIDs {
			if departmentID <= 0 {
				return nil, errors.New("reserved execution dept_ids contains invalid id")
			}
		}
		return sortedUniqueInt64s(deptIDs), nil
	case operationParamActorRole:
		role, ok := value.(int)
		if !ok || role < 0 {
			return nil, errors.New("reserved execution actor_role is invalid")
		}
		return role, nil
	case operationParamConversationType:
		conversationType, ok := value.(string)
		if !ok || conversationType != "2" {
			return nil, errors.New("reserved execution conversation_type is invalid")
		}
		return conversationType, nil
	case operationParamConversationTitle:
		title, ok := value.(string)
		if !ok || !runeLengthWithin(title, workflowSnapshotMaxLabelRunes) {
			return nil, errors.New("reserved execution conversation_title is invalid")
		}
		return title, nil
	default:
		return nil, fmt.Errorf("reserved execution trusted param %q is not allowlisted", key)
	}
}

func persistedExecutionResultToV1(result PersistedExecutionResultV1) (persistedExecutionResultV1, error) {
	if err := validatePersistedExecutionResult(result); err != nil {
		return persistedExecutionResultV1{}, err
	}
	if result.BusinessKey != strings.TrimSpace(result.BusinessKey) ||
		!runeLengthWithin(result.BusinessKey, executionMaxProjectionRunes) {
		return persistedExecutionResultV1{}, errors.New("persisted execution result business_key is invalid")
	}
	return persistedExecutionResultV1{
		SchemaVersion: executionSchemaVersion,
		BusinessKey:   result.BusinessKey,
		WriteEffect:   result.WriteEffect,
		CompletedAt:   formatWorkflowTime(result.CompletedAt),
	}, nil
}

func persistedExecutionResultFromV1(dto persistedExecutionResultV1) (PersistedExecutionResultV1, error) {
	completedAt, err := parseExecutionTime("completed_at", dto.CompletedAt)
	if err != nil {
		return PersistedExecutionResultV1{}, err
	}
	result := PersistedExecutionResultV1{
		BusinessKey: dto.BusinessKey,
		WriteEffect: dto.WriteEffect,
		CompletedAt: completedAt,
	}
	_, err = persistedExecutionResultToV1(result)
	return result, err
}

func parseExecutionTime(field, value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("reserved execution %s is required", field)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid reserved execution %s: %w", field, err)
	}
	return parsed.UTC(), nil
}

// MarshalWorkflowSnapshot serializes workflow business state using the explicit V1 schema.
// Full chat text and TrustedParam.Source.Raw are intentionally never persisted.
func MarshalWorkflowSnapshot(snapshot *WorkflowSnapshot) ([]byte, error) {
	dto, err := workflowSnapshotToV1(snapshot)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow snapshot v1: %w", err)
	}
	if len(payload) > workflowSnapshotMaxBytes {
		return nil, fmt.Errorf("workflow snapshot exceeds %d bytes", workflowSnapshotMaxBytes)
	}
	return payload, nil
}

// UnmarshalWorkflowSnapshot decodes only the explicit V1 schema and rejects
// unknown fields, unsupported values, and oversized input.
func UnmarshalWorkflowSnapshot(payload []byte) (*WorkflowSnapshot, error) {
	if len(payload) == 0 {
		return nil, errors.New("workflow snapshot payload is empty")
	}
	if len(payload) > workflowSnapshotMaxBytes {
		return nil, fmt.Errorf("workflow snapshot exceeds %d bytes", workflowSnapshotMaxBytes)
	}
	var dto persistedWorkflowSnapshotV1
	if err := decodeStrictWorkflowJSON(payload, &dto); err != nil {
		return nil, fmt.Errorf("decode workflow snapshot v1: %w", err)
	}
	if dto.SchemaVersion != workflowSnapshotSchemaVersion {
		return nil, fmt.Errorf("unsupported workflow snapshot schema version %d", dto.SchemaVersion)
	}
	return workflowSnapshotFromV1(dto)
}

// WorkflowSnapshotHash returns the stable business-snapshot hash used
// by shadow stores. Store-managed version and created/updated timestamps are
// compared separately and therefore excluded from this hash.
func WorkflowSnapshotHash(snapshot *WorkflowSnapshot) (string, error) {
	dto, err := workflowSnapshotToV1(snapshot)
	if err != nil {
		return "", err
	}
	dto.Version = 0
	dto.CreatedAt = ""
	dto.UpdatedAt = ""
	dto.ExpiresAt = ""
	payload, err := json.Marshal(dto)
	if err != nil {
		return "", fmt.Errorf("marshal canonical workflow snapshot: %w", err)
	}
	if len(payload) > workflowSnapshotMaxBytes {
		return "", fmt.Errorf("workflow snapshot exceeds %d bytes", workflowSnapshotMaxBytes)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func workflowSnapshotToV1(snapshot *WorkflowSnapshot) (persistedWorkflowSnapshotV1, error) {
	if snapshot == nil {
		return persistedWorkflowSnapshotV1{}, errors.New("workflow snapshot is nil")
	}
	if err := validateWorkflowSnapshotIdentity(snapshot); err != nil {
		return persistedWorkflowSnapshotV1{}, err
	}

	trusted, err := trustedStateToV1(snapshot.Trusted)
	if err != nil {
		return persistedWorkflowSnapshotV1{}, err
	}
	entities, err := trustedEntitiesToV1(snapshot.TrustedEntities)
	if err != nil {
		return persistedWorkflowSnapshotV1{}, err
	}
	candidates, err := candidatesToV1(snapshot.Candidates)
	if err != nil {
		return persistedWorkflowSnapshotV1{}, err
	}

	return persistedWorkflowSnapshotV1{
		SchemaVersion:   workflowSnapshotSchemaVersion,
		ID:              snapshot.ID,
		TenantID:        snapshot.TenantID,
		ActorUserID:     snapshot.ActorUserID,
		ConversationID:  snapshot.ConversationID,
		Type:            snapshot.Type,
		State:           snapshot.State,
		MissingFields:   cloneStringSlice(workflowMissingFields(snapshot)),
		Trusted:         trusted,
		TrustedEntities: entities,
		Candidates:      candidates,
		CreatedAt:       formatWorkflowTime(snapshot.CreatedAt),
		UpdatedAt:       formatWorkflowTime(snapshot.UpdatedAt),
		ExpiresAt:       formatWorkflowTime(snapshot.ExpiresAt),
		Version:         snapshot.Version,
	}, nil
}

func workflowSnapshotFromV1(dto persistedWorkflowSnapshotV1) (*WorkflowSnapshot, error) {
	snapshot := &WorkflowSnapshot{
		ID:             dto.ID,
		TenantID:       dto.TenantID,
		ActorUserID:    dto.ActorUserID,
		ConversationID: dto.ConversationID,
		Type:           dto.Type,
		State:          dto.State,
		MissingFields:  cloneStringSlice(dto.MissingFields),
		MissingSlots:   cloneStringSlice(dto.MissingFields),
		Version:        dto.Version,
	}
	createdAt, err := parseWorkflowTime("created_at", dto.CreatedAt, true)
	if err != nil {
		return nil, err
	}
	updatedAt, err := parseWorkflowTime("updated_at", dto.UpdatedAt, true)
	if err != nil {
		return nil, err
	}
	expiresAt, err := parseWorkflowTime("expires_at", dto.ExpiresAt, false)
	if err != nil {
		return nil, err
	}
	snapshot.CreatedAt = createdAt
	snapshot.UpdatedAt = updatedAt
	snapshot.ExpiresAt = expiresAt

	trusted, err := trustedStateFromV1(dto.Trusted)
	if err != nil {
		return nil, err
	}
	snapshot.Trusted = trusted
	snapshot.TrustedEntities, err = trustedEntitiesFromV1(dto.TrustedEntities)
	if err != nil {
		return nil, err
	}
	snapshot.Candidates, err = candidatesFromV1(dto.Candidates)
	if err != nil {
		return nil, err
	}
	if err := validateWorkflowSnapshotIdentity(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func validateWorkflowSnapshotIdentity(snapshot *WorkflowSnapshot) error {
	if strings.TrimSpace(snapshot.ID) == "" || !runeLengthWithin(snapshot.ID, workflowSnapshotMaxWorkflowIDRunes) {
		return errors.New("workflow snapshot id is missing or too long")
	}
	if snapshot.TenantID == 0 || snapshot.ActorUserID == 0 {
		return errors.New("workflow snapshot tenant_id and actor_user_id are required")
	}
	if strings.TrimSpace(snapshot.ConversationID) == "" ||
		!runeLengthWithin(snapshot.ConversationID, workflowSnapshotMaxConversationRunes) {
		return errors.New("workflow snapshot conversation_id is missing or too long")
	}
	switch snapshot.Type {
	case WorkflowSubscriptionStart, WorkflowSubscriptionCancel, WorkflowManualSignCreate:
	default:
		return fmt.Errorf("unsupported workflow type %q", snapshot.Type)
	}
	switch snapshot.State {
	case WorkflowCollectScope, WorkflowCollectDepartments, WorkflowCollectUser,
		WorkflowCollectDate, WorkflowCollectSection, WorkflowReady,
		WorkflowCompleted, WorkflowCancelled, WorkflowInterruptedState:
	default:
		return fmt.Errorf("unsupported workflow state %q", snapshot.State)
	}
	if snapshot.ExpiresAt.IsZero() {
		return errors.New("workflow snapshot expires_at is required")
	}
	for _, field := range workflowMissingFields(snapshot) {
		if strings.TrimSpace(field) == "" || !runeLengthWithin(field, workflowSnapshotMaxKeyRunes) {
			return errors.New("workflow snapshot has invalid missing field")
		}
	}
	return validateWorkflowSnapshotAuthority(snapshot)
}

func validateWorkflowSnapshotAuthority(snapshot *WorkflowSnapshot) error {
	if snapshot.Trusted.TenantID != 0 && snapshot.Trusted.TenantID != snapshot.TenantID {
		return errors.New("workflow trusted tenant_id does not match snapshot")
	}
	if snapshot.Trusted.ConversationID != "" &&
		snapshot.Trusted.ConversationID != snapshot.ConversationID {
		return errors.New("workflow trusted conversation_id does not match snapshot")
	}
	for field, param := range snapshot.Trusted.TrustedParams {
		if param.TenantID != snapshot.TenantID {
			return fmt.Errorf("trusted param %q tenant_id does not match snapshot", field)
		}
		if !knownTrustedParamSourceKind(param.Source.Kind) {
			return fmt.Errorf("trusted param %q has unsupported source kind %q", field, param.Source.Kind)
		}
	}
	for field, entity := range snapshot.TrustedEntities {
		if entity.TenantID != snapshot.TenantID {
			return fmt.Errorf("trusted entity %q tenant_id does not match snapshot", field)
		}
	}
	for field, candidates := range snapshot.Candidates {
		for index, candidate := range candidates {
			if candidate.TenantID != 0 && candidate.TenantID != snapshot.TenantID {
				return fmt.Errorf("candidate %q[%d] tenant_id does not match snapshot", field, index)
			}
		}
	}
	return nil
}

func knownTrustedParamSourceKind(kind TrustedParamSourceKind) bool {
	switch kind {
	case TrustedParamSourceRawSlot,
		TrustedParamSourceDefault,
		TrustedParamSourceRuntime,
		TrustedParamSourceWorkflow,
		TrustedParamSourceCandidate,
		TrustedParamSourceDerived:
		return true
	default:
		return false
	}
}

func trustedStateToV1(trusted trustedEntities) (persistedTrustedStateV1, error) {
	if len(trusted.DeptIDs) > workflowSnapshotMaxInt64SliceItems {
		return persistedTrustedStateV1{}, errors.New("workflow trusted dept_ids exceed limit")
	}
	if len(trusted.TrustedParams) > workflowSnapshotMaxTrustedParams {
		return persistedTrustedStateV1{}, errors.New("workflow trusted params exceed limit")
	}
	params := make(map[string]persistedTrustedParamV1, len(trusted.TrustedParams))
	for key, param := range trusted.TrustedParams {
		if err := validateWorkflowMapKey(key); err != nil {
			return persistedTrustedStateV1{}, fmt.Errorf("trusted param key: %w", err)
		}
		value, err := encodePersistedTypedValue(param.Value)
		if err != nil {
			return persistedTrustedStateV1{}, fmt.Errorf("trusted param %q: %w", key, err)
		}
		field := strings.TrimSpace(param.Field)
		if field == "" {
			field = key
		}
		if field != key {
			return persistedTrustedStateV1{}, fmt.Errorf("trusted param %q field mismatch %q", key, field)
		}
		if !runeLengthWithin(param.Source.Resolver, workflowSnapshotMaxIDRunes) {
			return persistedTrustedStateV1{}, fmt.Errorf("trusted param %q resolver too long", key)
		}
		params[key] = persistedTrustedParamV1{
			Field: field,
			Value: value,
			Source: persistedParamSourceV1{
				Kind:     param.Source.Kind,
				Resolver: param.Source.Resolver,
			},
			TenantID: param.TenantID,
		}
	}
	if len(params) == 0 {
		params = nil
	}
	if !runeLengthWithin(trusted.UserName, workflowSnapshotMaxLabelRunes) ||
		!runeLengthWithin(trusted.ConversationID, workflowSnapshotMaxConversationRunes) ||
		!runeLengthWithin(trusted.Date, workflowSnapshotMaxIDRunes) ||
		!runeLengthWithin(trusted.Scope, workflowSnapshotMaxKeyRunes) ||
		!runeLengthWithin(trusted.QueryShape, workflowSnapshotMaxKeyRunes) {
		return persistedTrustedStateV1{}, errors.New("workflow trusted string exceeds limit")
	}
	return persistedTrustedStateV1{
		TenantID:       trusted.TenantID,
		DepartmentID:   trusted.DepartmentID,
		DeptIDs:        append([]int64(nil), trusted.DeptIDs...),
		UserID:         trusted.UserID,
		UserName:       trusted.UserName,
		Date:           trusted.Date,
		Section:        trusted.Section,
		Week:           trusted.Week,
		ConversationID: trusted.ConversationID,
		Scope:          trusted.Scope,
		QueryShape:     trusted.QueryShape,
		UserRole:       trusted.UserRole,
		TrustedParams:  params,
	}, nil
}

func trustedStateFromV1(dto persistedTrustedStateV1) (trustedEntities, error) {
	if len(dto.DeptIDs) > workflowSnapshotMaxInt64SliceItems ||
		len(dto.TrustedParams) > workflowSnapshotMaxTrustedParams {
		return trustedEntities{}, errors.New("workflow trusted state exceeds limit")
	}
	params := make(map[string]TrustedParam, len(dto.TrustedParams))
	for key, param := range dto.TrustedParams {
		if err := validateWorkflowMapKey(key); err != nil {
			return trustedEntities{}, fmt.Errorf("trusted param key: %w", err)
		}
		if param.Field != key {
			return trustedEntities{}, fmt.Errorf("trusted param %q field mismatch %q", key, param.Field)
		}
		value, err := decodePersistedTypedValue(param.Value)
		if err != nil {
			return trustedEntities{}, fmt.Errorf("trusted param %q: %w", key, err)
		}
		params[key] = TrustedParam{
			Field: key,
			Value: value,
			Source: TrustedParamSource{
				Kind:     param.Source.Kind,
				Resolver: param.Source.Resolver,
			},
			TenantID: param.TenantID,
		}
	}
	if len(params) == 0 {
		params = nil
	}
	trusted := trustedEntities{
		TenantID:       dto.TenantID,
		DepartmentID:   dto.DepartmentID,
		DeptIDs:        append([]int64(nil), dto.DeptIDs...),
		UserID:         dto.UserID,
		UserName:       dto.UserName,
		Date:           dto.Date,
		Section:        dto.Section,
		Week:           dto.Week,
		ConversationID: dto.ConversationID,
		Scope:          dto.Scope,
		QueryShape:     dto.QueryShape,
		UserRole:       dto.UserRole,
		TrustedParams:  params,
	}
	_, err := trustedStateToV1(trusted)
	return trusted, err
}

func trustedEntitiesToV1(entities map[string]TrustedEntity) (map[string]persistedTrustedEntityV1, error) {
	if len(entities) > workflowSnapshotMaxTrustedEntities {
		return nil, errors.New("workflow trusted entities exceed limit")
	}
	result := make(map[string]persistedTrustedEntityV1, len(entities))
	for key, entity := range entities {
		if err := validateWorkflowMapKey(key); err != nil {
			return nil, fmt.Errorf("trusted entity key: %w", err)
		}
		if !runeLengthWithin(entity.ID, workflowSnapshotMaxIDRunes) ||
			!runeLengthWithin(entity.Type, workflowSnapshotMaxKeyRunes) ||
			!runeLengthWithin(entity.Label, workflowSnapshotMaxLabelRunes) {
			return nil, fmt.Errorf("trusted entity %q text exceeds limit", key)
		}
		value, err := encodePersistedTypedValue(entity.Value)
		if err != nil {
			return nil, fmt.Errorf("trusted entity %q: %w", key, err)
		}
		result[key] = persistedTrustedEntityV1{
			ID:       entity.ID,
			Type:     entity.Type,
			Label:    entity.Label,
			Value:    value,
			TenantID: entity.TenantID,
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func trustedEntitiesFromV1(entities map[string]persistedTrustedEntityV1) (map[string]TrustedEntity, error) {
	if len(entities) > workflowSnapshotMaxTrustedEntities {
		return nil, errors.New("workflow trusted entities exceed limit")
	}
	result := make(map[string]TrustedEntity, len(entities))
	for key, entity := range entities {
		if err := validateWorkflowMapKey(key); err != nil {
			return nil, fmt.Errorf("trusted entity key: %w", err)
		}
		value, err := decodePersistedTypedValue(entity.Value)
		if err != nil {
			return nil, fmt.Errorf("trusted entity %q: %w", key, err)
		}
		result[key] = TrustedEntity{
			ID:       entity.ID,
			Type:     entity.Type,
			Label:    entity.Label,
			Value:    value,
			TenantID: entity.TenantID,
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	_, err := trustedEntitiesToV1(result)
	return result, err
}

func candidatesToV1(candidates map[string][]Candidate) (map[string][]persistedCandidateV1, error) {
	if len(candidates) > workflowSnapshotMaxCandidateFields {
		return nil, errors.New("workflow candidate fields exceed limit")
	}
	total := 0
	result := make(map[string][]persistedCandidateV1, len(candidates))
	for field, values := range candidates {
		if err := validateWorkflowMapKey(field); err != nil {
			return nil, fmt.Errorf("candidate field: %w", err)
		}
		if len(values) > workflowSnapshotMaxCandidatesPerField {
			return nil, fmt.Errorf("candidate field %q exceeds limit", field)
		}
		total += len(values)
		if total > workflowSnapshotMaxCandidatesTotal {
			return nil, errors.New("workflow candidates exceed total limit")
		}
		persisted := make([]persistedCandidateV1, 0, len(values))
		for index, candidate := range values {
			if strings.TrimSpace(candidate.Label) == "" ||
				!runeLengthWithin(candidate.Label, workflowSnapshotMaxLabelRunes) ||
				!runeLengthWithin(candidate.ID, workflowSnapshotMaxIDRunes) {
				return nil, fmt.Errorf("candidate %q[%d] has invalid id or label", field, index)
			}
			value, err := encodePersistedTypedValue(candidate.Value)
			if err != nil {
				return nil, fmt.Errorf("candidate %q[%d]: %w", field, index, err)
			}
			persisted = append(persisted, persistedCandidateV1{
				ID:       candidate.ID,
				Label:    candidate.Label,
				Value:    value,
				TenantID: candidate.TenantID,
			})
		}
		if len(persisted) > 0 {
			result[field] = persisted
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func candidatesFromV1(candidates map[string][]persistedCandidateV1) (map[string][]Candidate, error) {
	if len(candidates) > workflowSnapshotMaxCandidateFields {
		return nil, errors.New("workflow candidate fields exceed limit")
	}
	total := 0
	result := make(map[string][]Candidate, len(candidates))
	for field, values := range candidates {
		if len(values) > workflowSnapshotMaxCandidatesPerField {
			return nil, fmt.Errorf("candidate field %q exceeds limit", field)
		}
		total += len(values)
		if total > workflowSnapshotMaxCandidatesTotal {
			return nil, errors.New("workflow candidates exceed total limit")
		}
		decoded := make([]Candidate, 0, len(values))
		for index, candidate := range values {
			value, err := decodePersistedTypedValue(candidate.Value)
			if err != nil {
				return nil, fmt.Errorf("candidate %q[%d]: %w", field, index, err)
			}
			decoded = append(decoded, Candidate{
				ID:       candidate.ID,
				Label:    candidate.Label,
				Value:    value,
				TenantID: candidate.TenantID,
			})
		}
		if len(decoded) > 0 {
			result[field] = decoded
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	_, err := candidatesToV1(result)
	return result, err
}

func encodePersistedTypedValue(value any) (persistedTypedValueV1, error) {
	var kind string
	var encoded any
	switch typed := value.(type) {
	case string:
		if !runeLengthWithin(typed, workflowSnapshotMaxTypedStringRunes) {
			return persistedTypedValueV1{}, errors.New("string value exceeds limit")
		}
		kind, encoded = "string", typed
	case int:
		kind, encoded = "int", int64(typed)
	case int64:
		kind, encoded = "int64", typed
	case uint:
		kind, encoded = "uint", uint64(typed)
	case []int64:
		if len(typed) > workflowSnapshotMaxInt64SliceItems {
			return persistedTypedValueV1{}, errors.New("int64 slice exceeds limit")
		}
		encodedSlice := make([]int64, len(typed))
		copy(encodedSlice, typed)
		kind, encoded = "int64_slice", encodedSlice
	case bool:
		kind, encoded = "bool", typed
	default:
		return persistedTypedValueV1{}, fmt.Errorf("unsupported persisted value type %T", value)
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return persistedTypedValueV1{}, fmt.Errorf("marshal typed value: %w", err)
	}
	return persistedTypedValueV1{Kind: kind, Value: raw}, nil
}

func decodePersistedTypedValue(value persistedTypedValueV1) (any, error) { //nolint:gocyclo // Explicit tagged-union decoding is intentionally exhaustive.
	if len(value.Value) == 0 || bytes.Equal(bytes.TrimSpace(value.Value), []byte("null")) {
		return nil, errors.New("typed value is empty or null")
	}
	switch value.Kind {
	case "string":
		var decoded string
		if err := json.Unmarshal(value.Value, &decoded); err != nil {
			return nil, err
		}
		if !runeLengthWithin(decoded, workflowSnapshotMaxTypedStringRunes) {
			return nil, errors.New("string value exceeds limit")
		}
		return decoded, nil
	case "int":
		var decoded int64
		if err := json.Unmarshal(value.Value, &decoded); err != nil {
			return nil, err
		}
		if strconv.IntSize == 32 && (decoded < int64(-1<<31) || decoded > int64(1<<31-1)) {
			return nil, errors.New("int value overflows platform int")
		}
		return int(decoded), nil
	case "int64":
		var decoded int64
		if err := json.Unmarshal(value.Value, &decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	case "uint":
		var decoded uint64
		if err := json.Unmarshal(value.Value, &decoded); err != nil {
			return nil, err
		}
		if strconv.IntSize == 32 && decoded > uint64(1<<32-1) {
			return nil, errors.New("uint value overflows platform uint")
		}
		return uint(decoded), nil
	case "int64_slice":
		var decoded []int64
		if err := json.Unmarshal(value.Value, &decoded); err != nil {
			return nil, err
		}
		if len(decoded) > workflowSnapshotMaxInt64SliceItems {
			return nil, errors.New("int64 slice exceeds limit")
		}
		return decoded, nil
	case "bool":
		var decoded bool
		if err := json.Unmarshal(value.Value, &decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported persisted value kind %q", value.Kind)
	}
}

func decodeStrictWorkflowJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateWorkflowMapKey(value string) error {
	if strings.TrimSpace(value) == "" || !runeLengthWithin(value, workflowSnapshotMaxKeyRunes) {
		return errors.New("map key is empty or too long")
	}
	return nil
}

func runeLengthWithin(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit
}

func formatWorkflowTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseWorkflowTime(field, value string, allowEmpty bool) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		if allowEmpty {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("workflow snapshot %s is required", field)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid workflow snapshot %s: %w", field, err)
	}
	return parsed.UTC(), nil
}

// sortedUniqueInt64s is shared by execution codecs added alongside the store
// contract; keeping it here guarantees canonical id lists across codecs.
func sortedUniqueInt64s(values []int64) []int64 {
	if len(values) == 0 {
		if values == nil {
			return nil
		}
		return []int64{}
	}
	result := append([]int64(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	write := 0
	for _, value := range result {
		if write > 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func normalizeWorkflowDatabaseTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Millisecond)
}

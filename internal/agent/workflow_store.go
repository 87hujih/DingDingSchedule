package agent

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"schedule_server/internal/agent/tools"
)

const defaultWorkflowTTL = 30 * time.Minute

var (
	errWorkflowKeyIncomplete = errors.New("workflow key requires tenant_id, conversation_id, and actor_user_id")
	ErrWorkflowConflict      = errors.New("workflow version conflict")
	ErrExecutionInProgress   = errors.New("workflow execution in progress")
)

type WorkflowKey struct {
	TenantID       uint
	ConversationID string
	ActorUserID    uint
}

type WorkflowExecutionStatus string

const (
	WorkflowExecutionExecuting        WorkflowExecutionStatus = "executing"
	WorkflowExecutionResultRecorded   WorkflowExecutionStatus = "result_recorded"
	WorkflowExecutionRecoveryRequired WorkflowExecutionStatus = "recovery_required"
)

type WriteEffect string

const (
	WriteEffectCreated   WriteEffect = "created"
	WriteEffectUpdated   WriteEffect = "updated"
	WriteEffectNoOp      WriteEffect = "no_op"
	WriteEffectCancelled WriteEffect = "cancelled"
)

type PersistedTrustedParamsV1 map[string]any

type ReservedExecutionV1 struct {
	Operation        string
	BusinessKey      string
	TrustedParams    PersistedTrustedParamsV1
	ExecutionToken   string
	AttemptRequestID string
	StartedAt        time.Time
	LeaseExpiresAt   time.Time
}

type PersistedExecutionResultV1 struct {
	BusinessKey string
	WriteEffect WriteEffect
	CompletedAt time.Time
}

type PersistedExecutionV1 struct {
	Status      WorkflowExecutionStatus
	Reservation ReservedExecutionV1
	Result      *PersistedExecutionResultV1
}

type VersionedWorkflow struct {
	Snapshot  *WorkflowSnapshot
	Version   uint64
	Execution *PersistedExecutionV1
}

type WorkflowStore interface {
	Load(ctx context.Context, key WorkflowKey) (*VersionedWorkflow, error)
	Create(ctx context.Context, key WorkflowKey, next *WorkflowSnapshot) (*VersionedWorkflow, error)
	CompareAndSwap(ctx context.Context, key WorkflowKey, expectedVersion uint64, next *WorkflowSnapshot) (*VersionedWorkflow, error)
	DeleteIfVersion(ctx context.Context, key WorkflowKey, expectedVersion uint64, reason string) error
	CreateReservedExecution(ctx context.Context, key WorkflowKey, next *WorkflowSnapshot, reservation ReservedExecutionV1) (*VersionedWorkflow, error)
	ReserveExecution(ctx context.Context, key WorkflowKey, expectedVersion uint64, next *WorkflowSnapshot, reservation ReservedExecutionV1) (*VersionedWorkflow, error)
	RecordExecutionResult(ctx context.Context, key WorkflowKey, expectedVersion uint64, executionToken string, result PersistedExecutionResultV1) (*VersionedWorkflow, error)
	FinalizeExecution(ctx context.Context, key WorkflowKey, expectedVersion uint64, executionToken string, next *WorkflowSnapshot) (*VersionedWorkflow, error)
	DeleteReservedExecution(ctx context.Context, key WorkflowKey, expectedVersion uint64, executionToken string, reason string) error
	TakeoverExpiredExecution(ctx context.Context, key WorkflowKey, expectedVersion uint64, previousToken string, next ReservedExecutionV1) (*VersionedWorkflow, error)
}

type RecoverableWorkflowExecution struct {
	Key      WorkflowKey
	Workflow *VersionedWorkflow
}

type WorkflowRecoveryStore interface {
	WorkflowStore
	ListRecoverableExecutions(ctx context.Context, now time.Time, limit int) ([]RecoverableWorkflowExecution, error)
	MarkExecutionRecoveryRequired(ctx context.Context, key WorkflowKey, expectedVersion uint64, executionToken string, retryAt time.Time) (*VersionedWorkflow, error)
}

type memoryWorkflowStore struct {
	mu        sync.Mutex
	workflows map[WorkflowKey]*memoryWorkflowRecord
	clock     func() time.Time
}

type memoryWorkflowRecord struct {
	snapshot  *WorkflowSnapshot
	version   uint64
	execution *PersistedExecutionV1
}

func newMemoryWorkflowStore(clock func() time.Time) *memoryWorkflowStore {
	if clock == nil {
		clock = time.Now
	}
	return &memoryWorkflowStore{
		workflows: make(map[WorkflowKey]*memoryWorkflowRecord),
		clock:     clock,
	}
}

// NewMemoryWorkflowStore creates the explicit development/test workflow store.
// Production wiring must not use this as an implicit fallback.
func NewMemoryWorkflowStore() WorkflowStore {
	return newMemoryWorkflowStore(nil)
}

func (s *memoryWorkflowStore) Load(ctx context.Context, key WorkflowKey) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record := s.activeRecordLocked(key)
	return cloneVersionedWorkflow(record), nil
}

func (s *memoryWorkflowStore) Create(ctx context.Context, key WorkflowKey, next *WorkflowSnapshot) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, errors.New("workflow snapshot is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeRecordLocked(key) != nil {
		return nil, ErrWorkflowConflict
	}
	record := &memoryWorkflowRecord{
		version:  1,
		snapshot: s.prepareSnapshotLocked(key, next, 1, nil),
	}
	s.workflows[key] = record
	return cloneVersionedWorkflow(record), nil
}

func (s *memoryWorkflowStore) CompareAndSwap(ctx context.Context, key WorkflowKey, expectedVersion uint64, next *WorkflowSnapshot) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, errors.New("workflow snapshot is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.recordForVersionedMutationLocked(key, expectedVersion)
	if err != nil {
		return nil, err
	}
	if record.execution != nil {
		return nil, ErrExecutionInProgress
	}
	record.version++
	record.snapshot = s.prepareSnapshotLocked(key, next, record.version, record.snapshot)
	return cloneVersionedWorkflow(record), nil
}

func (s *memoryWorkflowStore) DeleteIfVersion(ctx context.Context, key WorkflowKey, expectedVersion uint64, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateWorkflowKey(key); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.recordForVersionedMutationLocked(key, expectedVersion)
	if err != nil {
		return err
	}
	if record.execution != nil {
		return ErrExecutionInProgress
	}
	delete(s.workflows, key)
	return nil
}

func (s *memoryWorkflowStore) CreateReservedExecution(
	ctx context.Context,
	key WorkflowKey,
	next *WorkflowSnapshot,
	reservation ReservedExecutionV1,
) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, errors.New("workflow snapshot is required")
	}
	normalizedReservation, err := canonicalReservedExecution(reservation)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeRecordLocked(key) != nil {
		return nil, ErrWorkflowConflict
	}
	record := &memoryWorkflowRecord{
		version:  1,
		snapshot: s.prepareSnapshotLocked(key, next, 1, nil),
		execution: &PersistedExecutionV1{
			Status:      WorkflowExecutionExecuting,
			Reservation: normalizedReservation,
		},
	}
	s.workflows[key] = record
	return cloneVersionedWorkflow(record), nil
}

func (s *memoryWorkflowStore) ReserveExecution(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	next *WorkflowSnapshot,
	reservation ReservedExecutionV1,
) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, errors.New("workflow snapshot is required")
	}
	normalizedReservation, err := canonicalReservedExecution(reservation)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.recordForVersionedMutationLocked(key, expectedVersion)
	if err != nil {
		return nil, err
	}
	if record.execution != nil {
		return nil, ErrExecutionInProgress
	}
	record.version++
	record.snapshot = s.prepareSnapshotLocked(key, next, record.version, record.snapshot)
	record.execution = &PersistedExecutionV1{
		Status:      WorkflowExecutionExecuting,
		Reservation: normalizedReservation,
	}
	return cloneVersionedWorkflow(record), nil
}

func (s *memoryWorkflowStore) RecordExecutionResult(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	result PersistedExecutionResultV1,
) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	if err := validatePersistedExecutionResult(result); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.recordForExecutionMutationLocked(key, expectedVersion, executionToken)
	if err != nil {
		return nil, err
	}
	result.BusinessKey = strings.TrimSpace(result.BusinessKey)
	if record.execution.Reservation.BusinessKey != result.BusinessKey {
		return nil, fmt.Errorf("%w: execution business key mismatch", ErrWorkflowConflict)
	}
	record.version++
	record.execution.Status = WorkflowExecutionResultRecorded
	clonedResult := result
	record.execution.Result = &clonedResult
	setWorkflowSnapshotVersion(record.snapshot, record.version)
	return cloneVersionedWorkflow(record), nil
}

func (s *memoryWorkflowStore) FinalizeExecution(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	next *WorkflowSnapshot,
) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, errors.New("workflow snapshot is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.recordForExecutionMutationLocked(key, expectedVersion, executionToken)
	if err != nil {
		return nil, err
	}
	if record.execution.Status != WorkflowExecutionResultRecorded || record.execution.Result == nil {
		return nil, ErrExecutionInProgress
	}
	record.version++
	record.snapshot = s.prepareSnapshotLocked(key, next, record.version, record.snapshot)
	record.execution = nil
	return cloneVersionedWorkflow(record), nil
}

func (s *memoryWorkflowStore) DeleteReservedExecution(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	executionToken string,
	_ string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateWorkflowKey(key); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.recordForExecutionMutationLocked(key, expectedVersion, executionToken); err != nil {
		return err
	}
	if s.workflows[key].execution.Status != WorkflowExecutionResultRecorded ||
		s.workflows[key].execution.Result == nil {
		return ErrExecutionInProgress
	}
	delete(s.workflows, key)
	return nil
}

func (s *memoryWorkflowStore) TakeoverExpiredExecution(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	previousToken string,
	next ReservedExecutionV1,
) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	normalizedNext, err := canonicalReservedExecution(next)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(previousToken) == normalizedNext.ExecutionToken {
		return nil, errors.New("takeover requires a new execution token")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.recordForExecutionMutationLocked(key, expectedVersion, previousToken)
	if err != nil {
		return nil, err
	}
	if record.execution.Reservation.LeaseExpiresAt.After(s.now()) {
		return nil, ErrExecutionInProgress
	}
	if record.execution.Reservation.Operation != normalizedNext.Operation ||
		record.execution.Reservation.BusinessKey != normalizedNext.BusinessKey {
		return nil, fmt.Errorf("%w: takeover cannot change operation or business key", ErrWorkflowConflict)
	}
	result := clonePersistedExecutionResult(record.execution.Result)
	record.version++
	record.execution = &PersistedExecutionV1{
		Status:      WorkflowExecutionExecuting,
		Reservation: normalizedNext,
		Result:      result,
	}
	if result != nil {
		record.execution.Status = WorkflowExecutionResultRecorded
	}
	setWorkflowSnapshotVersion(record.snapshot, record.version)
	return cloneVersionedWorkflow(record), nil
}

func canonicalReservedExecution(reservation ReservedExecutionV1) (ReservedExecutionV1, error) {
	payload, err := MarshalReservedExecution(reservation)
	if err != nil {
		return ReservedExecutionV1{}, err
	}
	return UnmarshalReservedExecution(payload)
}

func (s *memoryWorkflowStore) activeRecordLocked(key WorkflowKey) *memoryWorkflowRecord {
	record := s.workflows[key]
	if record == nil {
		return nil
	}
	if record.execution == nil && workflowExpired(record.snapshot, s.now()) {
		delete(s.workflows, key)
		return nil
	}
	return record
}

func (s *memoryWorkflowStore) recordForVersionedMutationLocked(key WorkflowKey, expectedVersion uint64) (*memoryWorkflowRecord, error) {
	record := s.activeRecordLocked(key)
	if record == nil || record.version != expectedVersion {
		return nil, ErrWorkflowConflict
	}
	return record, nil
}

func (s *memoryWorkflowStore) recordForExecutionMutationLocked(
	key WorkflowKey,
	expectedVersion uint64,
	executionToken string,
) (*memoryWorkflowRecord, error) {
	record, err := s.recordForVersionedMutationLocked(key, expectedVersion)
	if err != nil {
		return nil, err
	}
	if record.execution == nil ||
		strings.TrimSpace(executionToken) == "" ||
		record.execution.Reservation.ExecutionToken != strings.TrimSpace(executionToken) {
		return nil, ErrWorkflowConflict
	}
	return record, nil
}

func (s *memoryWorkflowStore) prepareSnapshotLocked(
	key WorkflowKey,
	workflow *WorkflowSnapshot,
	version uint64,
	existing *WorkflowSnapshot,
) *WorkflowSnapshot {
	now := s.now()
	next := cloneWorkflowSnapshot(workflow)
	next.TenantID = key.TenantID
	next.ConversationID = key.ConversationID
	next.ActorUserID = key.ActorUserID
	if next.CreatedAt.IsZero() {
		if existing != nil && !existing.CreatedAt.IsZero() {
			next.CreatedAt = existing.CreatedAt
		} else {
			next.CreatedAt = now
		}
	}
	next.UpdatedAt = now
	if next.ExpiresAt.IsZero() {
		next.ExpiresAt = now.Add(defaultWorkflowTTL)
	}
	setWorkflowSnapshotVersion(next, version)
	syncWorkflowSnapshotFields(next)
	return next
}

func cloneVersionedWorkflow(record *memoryWorkflowRecord) *VersionedWorkflow {
	if record == nil {
		return nil
	}
	return &VersionedWorkflow{
		Snapshot:  cloneWorkflowSnapshot(record.snapshot),
		Version:   record.version,
		Execution: clonePersistedExecution(record.execution),
	}
}

func clonePersistedExecution(execution *PersistedExecutionV1) *PersistedExecutionV1 {
	if execution == nil {
		return nil
	}
	return &PersistedExecutionV1{
		Status:      execution.Status,
		Reservation: cloneReservedExecution(execution.Reservation),
		Result:      clonePersistedExecutionResult(execution.Result),
	}
}

func cloneReservedExecution(reservation ReservedExecutionV1) ReservedExecutionV1 {
	reservation.Operation = strings.TrimSpace(reservation.Operation)
	reservation.BusinessKey = strings.TrimSpace(reservation.BusinessKey)
	reservation.ExecutionToken = strings.TrimSpace(reservation.ExecutionToken)
	reservation.AttemptRequestID = strings.TrimSpace(reservation.AttemptRequestID)
	reservation.StartedAt = normalizeWorkflowDatabaseTime(reservation.StartedAt)
	reservation.LeaseExpiresAt = normalizeWorkflowDatabaseTime(reservation.LeaseExpiresAt)
	reservation.TrustedParams = clonePersistedTrustedParams(reservation.TrustedParams)
	return reservation
}

func clonePersistedExecutionResult(result *PersistedExecutionResultV1) *PersistedExecutionResultV1 {
	if result == nil {
		return nil
	}
	cloned := *result
	return &cloned
}

func clonePersistedTrustedParams(values PersistedTrustedParamsV1) PersistedTrustedParamsV1 {
	if len(values) == 0 {
		return nil
	}
	cloned := make(PersistedTrustedParamsV1, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case []int64:
			cloned[key] = append([]int64(nil), typed...)
		case []uint64:
			cloned[key] = append([]uint64(nil), typed...)
		case []int:
			cloned[key] = append([]int(nil), typed...)
		case []string:
			cloned[key] = append([]string(nil), typed...)
		default:
			cloned[key] = value
		}
	}
	return cloned
}

func validateReservedExecution(reservation ReservedExecutionV1) error {
	switch {
	case strings.TrimSpace(reservation.Operation) == "":
		return errors.New("execution operation is required")
	case strings.TrimSpace(reservation.BusinessKey) == "":
		return errors.New("execution business key is required")
	case strings.TrimSpace(reservation.ExecutionToken) == "":
		return errors.New("execution token is required")
	case strings.TrimSpace(reservation.AttemptRequestID) == "":
		return errors.New("execution request id is required")
	case reservation.StartedAt.IsZero():
		return errors.New("execution start time is required")
	case reservation.LeaseExpiresAt.IsZero():
		return errors.New("execution lease expiry is required")
	case !reservation.LeaseExpiresAt.After(reservation.StartedAt):
		return errors.New("execution lease expiry must be after start time")
	}
	if !runeLengthWithin(strings.TrimSpace(reservation.Operation), executionMaxProjectionRunes) ||
		!runeLengthWithin(strings.TrimSpace(reservation.BusinessKey), executionMaxProjectionRunes) ||
		!runeLengthWithin(strings.TrimSpace(reservation.ExecutionToken), executionMaxProjectionRunes) ||
		!runeLengthWithin(strings.TrimSpace(reservation.AttemptRequestID), executionMaxProjectionRunes) {
		return errors.New("execution identifier exceeds persistence limit")
	}
	for field, value := range reservation.TrustedParams {
		if strings.TrimSpace(field) == "" {
			return errors.New("execution trusted parameter name is required")
		}
		switch value.(type) {
		case string, int, int64, uint, []int64, bool:
		default:
			return fmt.Errorf("unsupported execution trusted parameter type for %q", field)
		}
	}
	return nil
}

func validatePersistedExecutionResult(result PersistedExecutionResultV1) error {
	if strings.TrimSpace(result.BusinessKey) == "" {
		return errors.New("execution result business key is required")
	}
	if !runeLengthWithin(strings.TrimSpace(result.BusinessKey), executionMaxProjectionRunes) {
		return errors.New("execution result business key exceeds persistence limit")
	}
	switch result.WriteEffect {
	case WriteEffectCreated, WriteEffectUpdated, WriteEffectNoOp, WriteEffectCancelled:
	default:
		return fmt.Errorf("unsupported write effect %q", result.WriteEffect)
	}
	if result.CompletedAt.IsZero() {
		return errors.New("execution result completion time is required")
	}
	return nil
}

// setWorkflowSnapshotVersion keeps the transitional snapshot projection
// compatible while WorkflowSnapshot.Version moves from int64 to uint64.
func setWorkflowSnapshotVersion(snapshot *WorkflowSnapshot, version uint64) {
	if snapshot == nil {
		return
	}
	value := reflect.ValueOf(&snapshot.Version).Elem()
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(int64(version))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(version)
	}
}

func (s *memoryWorkflowStore) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

type WorkflowArbiterInput struct {
	Draft          ProtocolDraft
	ActiveWorkflow *WorkflowSnapshot
}

type WorkflowArbiterResult struct {
	Decision       WorkflowDecision
	ActiveWorkflow *WorkflowSnapshot
	ClearWorkflow  bool
	Expired        bool
	Reason         string
}

type WorkflowArbiter struct {
	clock func() time.Time
}

func newWorkflowArbiter(clock func() time.Time) WorkflowArbiter {
	if clock == nil {
		clock = time.Now
	}
	return WorkflowArbiter{clock: clock}
}

func (a WorkflowArbiter) Decide(input WorkflowArbiterInput) WorkflowArbiterResult {
	active := cloneWorkflowSnapshot(input.ActiveWorkflow)
	if workflowExpired(active, a.now()) {
		return WorkflowArbiterResult{
			Decision:       workflowDecisionWithoutActiveWorkflow(input.Draft),
			ClearWorkflow:  true,
			Expired:        true,
			Reason:         "workflow_expired",
			ActiveWorkflow: nil,
		}
	}

	if active != nil {
		switch {
		case input.Draft.Act == ActWorkflowCancel:
			return WorkflowArbiterResult{Decision: WorkflowCanceled, ActiveWorkflow: active, ClearWorkflow: true, Reason: "workflow_cancel"}
		case input.Draft.Act == ActWorkflowContinue:
			return WorkflowArbiterResult{Decision: WorkflowContinueDecision, ActiveWorkflow: active}
		case policyExplicitNewRequest(input.Draft.Act):
			return WorkflowArbiterResult{Decision: WorkflowInterrupted, ActiveWorkflow: active, ClearWorkflow: true, Reason: "new_request"}
		default:
			return WorkflowArbiterResult{Decision: WorkflowContinueDecision, ActiveWorkflow: active}
		}
	}

	return WorkflowArbiterResult{
		Decision: workflowDecisionWithoutActiveWorkflow(input.Draft),
	}
}

func (a WorkflowArbiter) now() time.Time {
	if a.clock == nil {
		return time.Now()
	}
	return a.clock()
}

func workflowDecisionWithoutActiveWorkflow(draft ProtocolDraft) WorkflowDecision {
	if draft.Act == ActWriteRequest {
		if manifest, ok := lookupOperation(draft.Operation); ok && manifest.Workflow != nil && manifest.Workflow.Mode == WorkflowModeMultiTurn {
			return WorkflowStartNew
		}
	}
	return WorkflowSingleTurn
}

func workflowExpired(workflow *WorkflowSnapshot, now time.Time) bool {
	if workflow == nil || workflow.ExpiresAt.IsZero() {
		return false
	}
	return !workflow.ExpiresAt.After(now)
}

func workflowKeyFromSnapshot(workflow *WorkflowSnapshot) (WorkflowKey, error) {
	if workflow == nil {
		return WorkflowKey{}, errWorkflowKeyIncomplete
	}
	key := WorkflowKey{
		TenantID:       workflow.TenantID,
		ConversationID: strings.TrimSpace(workflow.ConversationID),
		ActorUserID:    workflow.ActorUserID,
	}
	if err := validateWorkflowKey(key); err != nil {
		return WorkflowKey{}, err
	}
	return key, nil
}

func validateWorkflowKey(key WorkflowKey) error {
	if key.TenantID == 0 || strings.TrimSpace(key.ConversationID) == "" || key.ActorUserID == 0 {
		return fmt.Errorf("%w: tenant_id=%d conversation_id=%q actor_user_id=%d", errWorkflowKeyIncomplete, key.TenantID, key.ConversationID, key.ActorUserID)
	}
	return nil
}

func workflowKeyFromUserContext(uctx *tools.UserContext) WorkflowKey {
	if uctx == nil {
		return WorkflowKey{}
	}
	conversationID := strings.TrimSpace(uctx.ConversationID)
	if conversationID == "" {
		conversationID = "direct:" + strings.TrimSpace(uctx.DingUserID)
	}
	return WorkflowKey{
		TenantID:       uctx.TenantID,
		ConversationID: conversationID,
		ActorUserID:    uctx.UserID,
	}
}

func workflowKeyFromSessionKey(sessionKey string, workflow *WorkflowSnapshot) WorkflowKey {
	if workflow != nil {
		if key, err := workflowKeyFromSnapshot(workflow); err == nil {
			return key
		}
	}
	parts := strings.Split(strings.TrimSpace(sessionKey), ":")
	if len(parts) == 0 {
		return WorkflowKey{}
	}
	var tenantID uint
	if tenant, err := strconv.ParseUint(parts[0], 10, 64); err == nil {
		tenantID = uint(tenant)
	}
	key := WorkflowKey{TenantID: tenantID}
	if key.TenantID == 0 {
		key.TenantID = workflowActorIDFromToken(parts[0])
	}
	switch {
	case len(parts) >= 3:
		key.ConversationID = strings.TrimSpace(parts[1])
		key.ActorUserID = workflowActorIDFromToken(strings.Join(parts[2:], ":"))
	case len(parts) == 2:
		key.ConversationID = "direct:" + strings.TrimSpace(parts[1])
		key.ActorUserID = workflowActorIDFromToken(parts[1])
	default:
		key.ConversationID = "legacy:" + strings.TrimSpace(sessionKey)
		key.ActorUserID = workflowActorIDFromToken(sessionKey)
	}
	return key
}

func workflowActorIDFromToken(token string) uint {
	value := strings.TrimSpace(token)
	if parsed, err := strconv.ParseUint(value, 10, 64); err == nil && parsed > 0 {
		return uint(parsed)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return uint(h.Sum32()) + 1
}

func syncWorkflowSnapshotFields(workflow *WorkflowSnapshot) {
	if workflow == nil {
		return
	}
	fields := workflowMissingFields(workflow)
	workflow.MissingFields = cloneStringSlice(fields)
	workflow.MissingSlots = cloneStringSlice(fields)
	if workflow.Candidates != nil {
		workflow.Candidates = cloneWorkflowCandidates(workflow.Candidates)
	}
	if workflow.TrustedEntities != nil {
		workflow.TrustedEntities = cloneTrustedEntities(workflow.TrustedEntities)
	}
}

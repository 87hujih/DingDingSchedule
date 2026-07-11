package agent

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"schedule_server/internal/agent/tools"
)

const defaultWorkflowTTL = 30 * time.Minute

var errWorkflowKeyIncomplete = errors.New("workflow key requires tenant_id, conversation_id, and actor_user_id")
var ErrWorkflowConflict = errors.New("workflow version conflict")

type WorkflowKey struct {
	TenantID       uint
	ConversationID string
	ActorUserID    uint
}

type WorkflowStore interface {
	Load(ctx context.Context, key WorkflowKey) (*VersionedWorkflow, error)
	Create(ctx context.Context, key WorkflowKey, next *WorkflowSnapshot) (*VersionedWorkflow, error)
	CompareAndSwap(ctx context.Context, key WorkflowKey, expectedVersion uint64, next *WorkflowSnapshot) (*VersionedWorkflow, error)
	DeleteIfVersion(ctx context.Context, key WorkflowKey, expectedVersion uint64, reason string) error
	ReserveExecution(ctx context.Context, key WorkflowKey, expectedVersion uint64, base *WorkflowSnapshot, lease WorkflowExecutionLease) (*VersionedWorkflow, error)
	FinalizeExecution(ctx context.Context, key WorkflowKey, expectedVersion uint64, executionToken string, next *WorkflowSnapshot) error
}

type memoryWorkflowStore struct {
	mu        sync.Mutex
	workflows map[WorkflowKey]*VersionedWorkflow
	locks     map[WorkflowKey]*workflowKeyLock
	clock     func() time.Time
}

type workflowKeyLock struct {
	mu   sync.Mutex
	refs int
}

func newMemoryWorkflowStore(clock func() time.Time) *memoryWorkflowStore {
	if clock == nil {
		clock = time.Now
	}
	return &memoryWorkflowStore{
		workflows: make(map[WorkflowKey]*VersionedWorkflow),
		locks:     make(map[WorkflowKey]*workflowKeyLock),
		clock:     clock,
	}
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

	current := s.workflows[key]
	if current == nil {
		return nil, nil
	}
	if workflowExpired(current.Snapshot, s.now()) {
		delete(s.workflows, key)
		return nil, nil
	}
	return cloneVersionedWorkflow(current), nil
}

func (s *memoryWorkflowStore) Create(ctx context.Context, key WorkflowKey, next *WorkflowSnapshot) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, nil
	}

	unlock := s.lockWorkflowKey(key)
	defer unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.workflows[key]; current != nil && !workflowExpired(current.Snapshot, s.now()) {
		return nil, ErrWorkflowConflict
	}
	delete(s.workflows, key)
	created := s.prepareVersionedLocked(key, next, 1)
	s.workflows[key] = created
	return cloneVersionedWorkflow(created), nil
}

func (s *memoryWorkflowStore) CompareAndSwap(ctx context.Context, key WorkflowKey, expectedVersion uint64, next *WorkflowSnapshot) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, nil
	}

	unlock := s.lockWorkflowKey(key)
	defer unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.workflows[key]
	if current == nil || workflowExpired(current.Snapshot, s.now()) || current.Version != expectedVersion {
		if current != nil && workflowExpired(current.Snapshot, s.now()) {
			delete(s.workflows, key)
		}
		return nil, ErrWorkflowConflict
	}
	updated := s.prepareVersionedLocked(key, next, expectedVersion+1)
	s.workflows[key] = updated
	return cloneVersionedWorkflow(updated), nil
}

func (s *memoryWorkflowStore) DeleteIfVersion(ctx context.Context, key WorkflowKey, expectedVersion uint64, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateWorkflowKey(key); err != nil {
		return err
	}

	unlock := s.lockWorkflowKey(key)
	defer unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.workflows[key]
	if current == nil {
		return nil
	}
	if workflowExpired(current.Snapshot, s.now()) {
		delete(s.workflows, key)
		return nil
	}
	if current.Version != expectedVersion {
		return ErrWorkflowConflict
	}
	delete(s.workflows, key)
	return nil
}

func (s *memoryWorkflowStore) ReserveExecution(ctx context.Context, key WorkflowKey, expectedVersion uint64, base *WorkflowSnapshot, lease WorkflowExecutionLease) (*VersionedWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}
	if base == nil {
		return nil, ErrWorkflowConflict
	}

	unlock := s.lockWorkflowKey(key)
	defer unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	lease.StartedAt = now
	lease.LeaseExpiresAt = now.Add(WorkflowExecutionLeaseDuration)
	current := s.workflows[key]
	if expectedVersion == 0 {
		if current != nil && !workflowExpired(current.Snapshot, now) {
			return nil, ErrWorkflowConflict
		}
		delete(s.workflows, key)
	} else if current == nil || workflowExpired(current.Snapshot, now) || current.Version != expectedVersion {
		if current != nil && workflowExpired(current.Snapshot, now) {
			delete(s.workflows, key)
		}
		return nil, ErrWorkflowConflict
	} else if executionLeaseActive(current.Snapshot.ExecutionLease, now) {
		return nil, ErrWorkflowConflict
	}

	next := cloneWorkflowSnapshot(base)
	next.State = WorkflowExecuting
	next.ExecutionLease = cloneWorkflowExecutionLease(&lease)
	if next.ExpiresAt.Before(lease.LeaseExpiresAt) {
		next.ExpiresAt = lease.LeaseExpiresAt
	}
	version := uint64(1)
	if current != nil {
		version = current.Version + 1
	}
	reserved := s.prepareVersionedLocked(key, next, version)
	s.workflows[key] = reserved
	return cloneVersionedWorkflow(reserved), nil
}

func (s *memoryWorkflowStore) FinalizeExecution(ctx context.Context, key WorkflowKey, expectedVersion uint64, executionToken string, next *WorkflowSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateWorkflowKey(key); err != nil {
		return err
	}

	unlock := s.lockWorkflowKey(key)
	defer unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.workflows[key]
	if current == nil || current.Version != expectedVersion || current.Snapshot == nil ||
		current.Snapshot.State != WorkflowExecuting || current.Snapshot.ExecutionLease == nil ||
		current.Snapshot.ExecutionLease.ExecutionToken != executionToken {
		return ErrWorkflowConflict
	}
	if next == nil {
		delete(s.workflows, key)
		return nil
	}
	finalized := cloneWorkflowSnapshot(next)
	finalized.ExecutionLease = nil
	s.workflows[key] = s.prepareVersionedLocked(key, finalized, expectedVersion+1)
	return nil
}

func executionLeaseActive(lease *WorkflowExecutionLease, now time.Time) bool {
	return lease != nil && lease.LeaseExpiresAt.After(now)
}

func cloneWorkflowExecutionLease(lease *WorkflowExecutionLease) *WorkflowExecutionLease {
	if lease == nil {
		return nil
	}
	cloned := *lease
	return &cloned
}

func (s *memoryWorkflowStore) lockWorkflowKey(key WorkflowKey) func() {
	s.mu.Lock()
	lock := s.locks[key]
	if lock == nil {
		lock = &workflowKeyLock{}
		s.locks[key] = lock
	}
	lock.refs++
	s.mu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.locks, key)
		}
		s.mu.Unlock()
	}
}

func (s *memoryWorkflowStore) prepareVersionedLocked(key WorkflowKey, workflow *WorkflowSnapshot, version uint64) *VersionedWorkflow {
	now := s.now()
	next := cloneWorkflowSnapshot(workflow)
	next.TenantID = key.TenantID
	next.ConversationID = key.ConversationID
	next.ActorUserID = key.ActorUserID
	if next.CreatedAt.IsZero() {
		if existing := s.workflows[key]; existing != nil && existing.Snapshot != nil && !existing.Snapshot.CreatedAt.IsZero() {
			next.CreatedAt = existing.Snapshot.CreatedAt
		} else {
			next.CreatedAt = now
		}
	}
	next.UpdatedAt = now
	if next.ExpiresAt.IsZero() {
		next.ExpiresAt = now.Add(defaultWorkflowTTL)
	}
	next.Version = int64(version)
	syncWorkflowSnapshotFields(next)
	return &VersionedWorkflow{Snapshot: next, Version: version}
}

func cloneVersionedWorkflow(workflow *VersionedWorkflow) *VersionedWorkflow {
	if workflow == nil {
		return nil
	}
	return &VersionedWorkflow{Snapshot: cloneWorkflowSnapshot(workflow.Snapshot), Version: workflow.Version}
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

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

type WorkflowKey struct {
	TenantID       uint
	ConversationID string
	ActorUserID    uint
}

type WorkflowStore interface {
	Load(ctx context.Context, key WorkflowKey) (*WorkflowSnapshot, error)
	Save(ctx context.Context, workflow *WorkflowSnapshot) error
	Clear(ctx context.Context, key WorkflowKey, reason string) error
	WithLock(ctx context.Context, key WorkflowKey, fn func(*WorkflowSnapshot) (*WorkflowSnapshot, error)) error
}

type memoryWorkflowStore struct {
	mu        sync.Mutex
	workflows map[WorkflowKey]*WorkflowSnapshot
	clock     func() time.Time
}

func newMemoryWorkflowStore(clock func() time.Time) *memoryWorkflowStore {
	if clock == nil {
		clock = time.Now
	}
	return &memoryWorkflowStore{
		workflows: make(map[WorkflowKey]*WorkflowSnapshot),
		clock:     clock,
	}
}

func (s *memoryWorkflowStore) Load(ctx context.Context, key WorkflowKey) (*WorkflowSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateWorkflowKey(key); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	workflow := s.workflows[key]
	if workflow == nil {
		return nil, nil
	}
	if workflowExpired(workflow, s.now()) {
		delete(s.workflows, key)
		return nil, nil
	}
	return cloneWorkflowSnapshot(workflow), nil
}

func (s *memoryWorkflowStore) Save(ctx context.Context, workflow *WorkflowSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if workflow == nil {
		return nil
	}
	key, err := workflowKeyFromSnapshot(workflow)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveLocked(key, workflow)
	return nil
}

func (s *memoryWorkflowStore) Clear(ctx context.Context, key WorkflowKey, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateWorkflowKey(key); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workflows, key)
	return nil
}

func (s *memoryWorkflowStore) WithLock(ctx context.Context, key WorkflowKey, fn func(*WorkflowSnapshot) (*WorkflowSnapshot, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateWorkflowKey(key); err != nil {
		return err
	}
	if fn == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.workflows[key]
	if current != nil && workflowExpired(current, s.now()) {
		delete(s.workflows, key)
		current = nil
	}

	next, err := fn(cloneWorkflowSnapshot(current))
	if err != nil {
		return err
	}
	if next == nil {
		delete(s.workflows, key)
		return nil
	}
	next.TenantID = key.TenantID
	next.ConversationID = key.ConversationID
	next.ActorUserID = key.ActorUserID
	s.saveLocked(key, next)
	return nil
}

func (s *memoryWorkflowStore) saveLocked(key WorkflowKey, workflow *WorkflowSnapshot) {
	now := s.now()
	next := cloneWorkflowSnapshot(workflow)
	next.TenantID = key.TenantID
	next.ConversationID = key.ConversationID
	next.ActorUserID = key.ActorUserID
	if next.CreatedAt.IsZero() {
		if existing := s.workflows[key]; existing != nil && !existing.CreatedAt.IsZero() {
			next.CreatedAt = existing.CreatedAt
		} else {
			next.CreatedAt = now
		}
	}
	next.UpdatedAt = now
	if next.ExpiresAt.IsZero() {
		next.ExpiresAt = now.Add(defaultWorkflowTTL)
	}
	if existing := s.workflows[key]; existing != nil {
		next.Version = existing.Version + 1
	} else {
		next.Version = 1
	}
	syncWorkflowSnapshotFields(next)
	s.workflows[key] = next
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
	tenant, _ := strconv.ParseUint(parts[0], 10, 64)
	key := WorkflowKey{TenantID: uint(tenant)}
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

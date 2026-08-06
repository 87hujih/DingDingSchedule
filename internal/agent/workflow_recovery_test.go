package agent

import (
	"context"
	"testing"
	"time"

	agenttools "schedule_server/internal/agent/tools"
)

func TestRecoverExpiredExecutionReplaysIdempotentlyAfterWriteBeforeRecord(t *testing.T) {
	t.Parallel()

	key := WorkflowKey{TenantID: 7, ConversationID: "conversation-recovery", ActorUserID: 9}
	base := NewMemoryWorkflowStore()
	request := preparedSubscriptionStartOutcome(key).PreparedWrite.Request
	businessKey, err := subscriptionBusinessKeyForRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = base.CreateReservedExecution(
		context.Background(),
		key,
		preparedSubscriptionStartOutcome(key).WorkflowAfter,
		ReservedExecutionV1{
			Operation:        request.Operation,
			BusinessKey:      businessKey,
			TrustedParams:    PersistedTrustedParamsV1{"conversation_id": key.ConversationID, "scope": "all"},
			ExecutionToken:   "expired-token",
			AttemptRequestID: "first-attempt",
			StartedAt:        now.Add(-2 * time.Minute),
			LeaseExpiresAt:   now.Add(-time.Minute),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &fixedRecoveryStore{WorkflowStore: base, key: key}
	port := &recoveryGroupSubPort{
		startResult: agenttools.GroupSubMutationResult{
			Effect:       agenttools.GroupSubWriteNoOp,
			Subscription: &agenttools.GroupSubInfo{Subscribed: true, PushEnabled: true},
		},
	}
	a := &Agent{workflowStore: store, deps: Deps{GroupSub: port}}

	completed, err := a.RecoverExpiredExecutions(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecoverExpiredExecutions() error = %v", err)
	}
	if completed != 1 || port.subscribeCalls != 1 {
		t.Fatalf("completed=%d subscribeCalls=%d, want one idempotent replay", completed, port.subscribeCalls)
	}
	loaded, err := base.Load(context.Background(), key)
	if err != nil || loaded != nil {
		t.Fatalf("Load() after recovery = %+v, %v, want deleted", loaded, err)
	}
}

func TestRecoverRecordedExecutionFinalizesWithoutReplay(t *testing.T) {
	t.Parallel()

	key := WorkflowKey{TenantID: 7, ConversationID: "conversation-recorded", ActorUserID: 9}
	base := NewMemoryWorkflowStore()
	request := preparedSubscriptionStartOutcome(key).PreparedWrite.Request
	businessKey, err := subscriptionBusinessKeyForRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	reserved, err := base.CreateReservedExecution(
		context.Background(),
		key,
		preparedSubscriptionStartOutcome(key).WorkflowAfter,
		ReservedExecutionV1{
			Operation:        request.Operation,
			BusinessKey:      businessKey,
			TrustedParams:    PersistedTrustedParamsV1{"conversation_id": key.ConversationID, "scope": "all"},
			ExecutionToken:   "recorded-token",
			AttemptRequestID: "recorded-attempt",
			StartedAt:        now.Add(-time.Minute),
			LeaseExpiresAt:   now.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = base.RecordExecutionResult(
		context.Background(),
		key,
		reserved.Version,
		"recorded-token",
		PersistedExecutionResultV1{
			BusinessKey: businessKey,
			WriteEffect: WriteEffectCreated,
			CompletedAt: now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &fixedRecoveryStore{WorkflowStore: base, key: key}
	port := &recoveryGroupSubPort{}
	a := &Agent{workflowStore: store, deps: Deps{GroupSub: port}}

	completed, err := a.RecoverExpiredExecutions(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecoverExpiredExecutions() error = %v", err)
	}
	if completed != 1 || port.subscribeCalls != 0 {
		t.Fatalf("completed=%d subscribeCalls=%d, want finalize without replay", completed, port.subscribeCalls)
	}
}

type fixedRecoveryStore struct {
	WorkflowStore
	key WorkflowKey
}

func (s *fixedRecoveryStore) ListRecoverableExecutions(
	ctx context.Context,
	_ time.Time,
	limit int,
) ([]RecoverableWorkflowExecution, error) {
	if limit <= 0 {
		return nil, nil
	}
	workflow, err := s.Load(ctx, s.key)
	if err != nil || workflow == nil {
		return nil, err
	}
	return []RecoverableWorkflowExecution{{Key: s.key, Workflow: workflow}}, nil
}

func (s *fixedRecoveryStore) MarkExecutionRecoveryRequired(
	ctx context.Context,
	key WorkflowKey,
	_ uint64,
	_ string,
	_ time.Time,
) (*VersionedWorkflow, error) {
	return s.Load(ctx, key)
}

type recoveryGroupSubPort struct {
	startResult    agenttools.GroupSubMutationResult
	subscribeCalls int
}

func (p *recoveryGroupSubPort) Subscribe(
	context.Context,
	uint,
	string,
	string,
	uint,
	[]int64,
	string,
) (agenttools.GroupSubMutationResult, error) {
	p.subscribeCalls++
	return p.startResult, nil
}

func (*recoveryGroupSubPort) Unsubscribe(
	context.Context,
	uint,
	string,
	string,
) (agenttools.GroupSubMutationResult, error) {
	return agenttools.GroupSubMutationResult{Effect: agenttools.GroupSubWriteNoOp}, nil
}

func (*recoveryGroupSubPort) GetSubscription(
	context.Context,
	uint,
	string,
) (*agenttools.GroupSubInfo, error) {
	return &agenttools.GroupSubInfo{Subscribed: true, PushEnabled: true}, nil
}

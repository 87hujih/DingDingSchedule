package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	agenttools "schedule_server/internal/agent/tools"
)

func TestCoordinatePreparedWriteReservesBeforeBusinessMutation(t *testing.T) {
	t.Parallel()

	store := NewMemoryWorkflowStore()
	key := WorkflowKey{TenantID: 7, ConversationID: "conversation-safe", ActorUserID: 9}
	port := &reservationAssertingGroupSubPort{
		store: store,
		key:   key,
	}
	a := &Agent{
		workflowStore: store,
		deps: Deps{
			GroupSub: port,
		},
	}
	outcome := preparedSubscriptionStartOutcome(key)

	got, persisted, writeStarted, err := a.coordinatePreparedWrite(context.Background(), key, nil, outcome)
	if err != nil {
		t.Fatalf("coordinatePreparedWrite() error = %v", err)
	}
	if !writeStarted || port.subscribeCalls != 1 || !port.observedReservation {
		t.Fatalf("writeStarted=%v subscribeCalls=%d observedReservation=%v", writeStarted, port.subscribeCalls, port.observedReservation)
	}
	if persisted != nil || !got.ClearWorkflow || got.PreparedWrite != nil {
		t.Fatalf("persisted=%+v outcome=%+v, want completed deletion", persisted, got)
	}
	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Fatalf("workflow remains after recorded finalization: %+v", loaded)
	}
}

func TestCoordinatePreparedWriteStoreFailureDoesNotMutateBusinessState(t *testing.T) {
	t.Parallel()

	key := WorkflowKey{TenantID: 7, ConversationID: "conversation-safe", ActorUserID: 9}
	port := &reservationAssertingGroupSubPort{}
	store := reservationFailingWorkflowStore{
		WorkflowStore: NewMemoryWorkflowStore(),
		err:           errors.New("reservation unavailable"),
	}
	a := &Agent{
		workflowStore: store,
		deps:          Deps{GroupSub: port},
	}

	_, _, writeStarted, err := a.coordinatePreparedWrite(
		context.Background(),
		key,
		nil,
		preparedSubscriptionStartOutcome(key),
	)
	if err == nil {
		t.Fatal("coordinatePreparedWrite() error = nil, want reservation failure")
	}
	if writeStarted || port.subscribeCalls != 0 {
		t.Fatalf("writeStarted=%v subscribeCalls=%d, want no business write", writeStarted, port.subscribeCalls)
	}
}

func TestCoordinatePreparedWriteKeepsReservationWhenResultRecordFails(t *testing.T) {
	t.Parallel()

	key := WorkflowKey{TenantID: 7, ConversationID: "conversation-record-fail", ActorUserID: 9}
	base := NewMemoryWorkflowStore()
	store := &faultWorkflowStore{
		WorkflowStore: base,
		key:           key,
		failRecord:    true,
	}
	port := &reservationAssertingGroupSubPort{store: base, key: key}
	a := &Agent{workflowStore: store, deps: Deps{GroupSub: port}}

	outcome, _, writeStarted, err := a.coordinatePreparedWrite(
		context.Background(),
		key,
		nil,
		preparedSubscriptionStartOutcome(key),
	)
	if err == nil || !writeStarted || port.subscribeCalls != 1 || outcome.BlockedReason != "write_result_pending" {
		t.Fatalf("err=%v writeStarted=%v calls=%d outcome=%+v", err, writeStarted, port.subscribeCalls, outcome)
	}
	loaded, loadErr := base.Load(context.Background(), key)
	if loadErr != nil || loaded == nil || loaded.Execution == nil ||
		loaded.Execution.Status != WorkflowExecutionExecuting {
		t.Fatalf("retained execution = %+v, err=%v", loaded, loadErr)
	}
}

func TestRecoveryAfterFinalizeFailureDoesNotReplayBusinessWrite(t *testing.T) {
	t.Parallel()

	key := WorkflowKey{TenantID: 7, ConversationID: "conversation-finalize-fail", ActorUserID: 9}
	base := NewMemoryWorkflowStore()
	store := &faultWorkflowStore{
		WorkflowStore:  base,
		key:            key,
		failDeleteOnce: true,
	}
	port := &reservationAssertingGroupSubPort{store: base, key: key}
	a := &Agent{workflowStore: store, deps: Deps{GroupSub: port}}

	_, _, writeStarted, err := a.coordinatePreparedWrite(
		context.Background(),
		key,
		nil,
		preparedSubscriptionStartOutcome(key),
	)
	if err == nil || !writeStarted || port.subscribeCalls != 1 {
		t.Fatalf("err=%v writeStarted=%v calls=%d", err, writeStarted, port.subscribeCalls)
	}
	loaded, loadErr := base.Load(context.Background(), key)
	if loadErr != nil || loaded == nil || loaded.Execution == nil ||
		loaded.Execution.Status != WorkflowExecutionResultRecorded {
		t.Fatalf("recorded execution = %+v, err=%v", loaded, loadErr)
	}

	completed, err := a.RecoverExpiredExecutions(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecoverExpiredExecutions() error = %v", err)
	}
	if completed != 1 || port.subscribeCalls != 1 {
		t.Fatalf("completed=%d subscribeCalls=%d, want finalize-only recovery", completed, port.subscribeCalls)
	}
}

func preparedSubscriptionStartOutcome(key WorkflowKey) protocolLiveOutcome {
	request := OperationRequest{
		Operation:      "subscription.start",
		TenantID:       key.TenantID,
		ActorUserID:    key.ActorUserID,
		ConversationID: key.ConversationID,
		TrustedParams: map[string]TrustedParam{
			"conversation_id": {
				Field:    "conversation_id",
				Value:    key.ConversationID,
				TenantID: key.TenantID,
			},
			"scope": {
				Field:    "scope",
				Value:    "all",
				TenantID: key.TenantID,
			},
		},
	}
	businessKey, err := subscriptionBusinessKeyForRequest(request)
	if err != nil {
		panic(err)
	}
	return protocolLiveOutcome{
		RequestID: "request-safe-write",
		WorkflowAfter: &WorkflowSnapshot{
			ID:    "workflow-safe-write",
			Type:  WorkflowSubscriptionStart,
			State: WorkflowReady,
		},
		PreparedWrite: &preparedWriteExecution{
			Request:                request,
			BusinessKey:            businessKey,
			ClearWorkflowOnSuccess: true,
		},
	}
}

type reservationAssertingGroupSubPort struct {
	store               WorkflowStore
	key                 WorkflowKey
	subscribeCalls      int
	observedReservation bool
}

func (p *reservationAssertingGroupSubPort) Subscribe(
	ctx context.Context,
	_ uint,
	_ string,
	_ string,
	_ uint,
	_ []int64,
	_ string,
) (agenttools.GroupSubMutationResult, error) {
	p.subscribeCalls++
	if p.store != nil {
		loaded, err := p.store.Load(ctx, p.key)
		p.observedReservation = err == nil && loaded != nil &&
			loaded.Execution != nil &&
			loaded.Execution.Status == WorkflowExecutionExecuting
	}
	return agenttools.GroupSubMutationResult{
		Effect:       agenttools.GroupSubWriteCreated,
		Subscription: &agenttools.GroupSubInfo{Subscribed: true, PushEnabled: true},
	}, nil
}

func (*reservationAssertingGroupSubPort) Unsubscribe(context.Context, uint, string, string) (agenttools.GroupSubMutationResult, error) {
	return agenttools.GroupSubMutationResult{Effect: agenttools.GroupSubWriteCancelled}, nil
}

func (*reservationAssertingGroupSubPort) GetSubscription(context.Context, uint, string) (*agenttools.GroupSubInfo, error) {
	return &agenttools.GroupSubInfo{Subscribed: false, PushEnabled: true}, nil
}

type reservationFailingWorkflowStore struct {
	WorkflowStore
	err error
}

func (s reservationFailingWorkflowStore) CreateReservedExecution(
	context.Context,
	WorkflowKey,
	*WorkflowSnapshot,
	ReservedExecutionV1,
) (*VersionedWorkflow, error) {
	return nil, s.err
}

type faultWorkflowStore struct {
	WorkflowStore
	key             WorkflowKey
	failRecord      bool
	failDeleteOnce  bool
	deleteCallCount int
}

func (s *faultWorkflowStore) RecordExecutionResult(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	token string,
	result PersistedExecutionResultV1,
) (*VersionedWorkflow, error) {
	if s.failRecord {
		return nil, errors.New("record result unavailable")
	}
	return s.WorkflowStore.RecordExecutionResult(ctx, key, expectedVersion, token, result)
}

func (s *faultWorkflowStore) DeleteReservedExecution(
	ctx context.Context,
	key WorkflowKey,
	expectedVersion uint64,
	token string,
	reason string,
) error {
	s.deleteCallCount++
	if s.failDeleteOnce && s.deleteCallCount == 1 {
		return errors.New("finalize unavailable")
	}
	return s.WorkflowStore.DeleteReservedExecution(ctx, key, expectedVersion, token, reason)
}

func (s *faultWorkflowStore) ListRecoverableExecutions(
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

func (s *faultWorkflowStore) MarkExecutionRecoveryRequired(
	ctx context.Context,
	key WorkflowKey,
	_ uint64,
	_ string,
	_ time.Time,
) (*VersionedWorkflow, error) {
	return s.Load(ctx, key)
}

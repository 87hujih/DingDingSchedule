package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingProtocolExecutor struct {
	calls atomic.Int32
	fn    func(context.Context, OperationRequest) OperationExecutionResult
}

type fakeOperationLedger struct {
	result *RecoveredOperationResult
	err    error
}

type postExecutionLedger struct {
	calls  atomic.Int32
	result *RecoveredOperationResult
}

func (l *postExecutionLedger) FindSucceeded(context.Context, uint, string) (*RecoveredOperationResult, error) {
	if l.calls.Add(1) == 1 {
		return nil, nil
	}
	return l.result, nil
}

type finalizeConflictStore struct{ WorkflowStore }

func (s finalizeConflictStore) FinalizeExecution(context.Context, WorkflowKey, uint64, string, *WorkflowSnapshot) error {
	return ErrWorkflowConflict
}

type finalizeErrorStore struct {
	WorkflowStore
	err error
}

func (s finalizeErrorStore) FinalizeExecution(context.Context, WorkflowKey, uint64, string, *WorkflowSnapshot) error {
	return s.err
}

func (f fakeOperationLedger) FindSucceeded(context.Context, uint, string) (*RecoveredOperationResult, error) {
	return f.result, f.err
}

func (e *countingProtocolExecutor) Execute(ctx context.Context, req OperationRequest) OperationExecutionResult {
	e.calls.Add(1)
	if e.fn != nil {
		return e.fn(ctx, req)
	}
	return OperationExecutionResult{Response: ResponseModel{Kind: ResponseResult, ResultText: "ok"}}
}

type rejectingExecutionStore struct {
	WorkflowStore
}

func (rejectingExecutionStore) ReserveExecution(context.Context, WorkflowKey, uint64, *WorkflowSnapshot, WorkflowExecutionLease) (*VersionedWorkflow, error) {
	return nil, ErrWorkflowConflict
}

func TestExecutionCoordinatorDoesNotCallExecutorWhenReservationFails(t *testing.T) {
	executor := &countingProtocolExecutor{}
	coordinator := newProtocolLiveExecutionCoordinator(rejectingExecutionStore{}, nil, time.Now)

	_, err := coordinator.Execute(context.Background(), WorkflowExecutionRequest{
		Key:             WorkflowKey{TenantID: 42, ConversationID: "cid", ActorUserID: 7},
		ExpectedVersion: 1,
		Workflow:        &WorkflowSnapshot{ID: "wf", Type: WorkflowSubscriptionStart, State: WorkflowReady},
		Operation:       OperationRequest{Operation: "subscription.start"},
		BusinessKey:     "business",
		RequestID:       "request",
	}, executor)

	if !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("Execute() error = %v, want workflow conflict", err)
	}
	if got := executor.calls.Load(); got != 0 {
		t.Fatalf("executor calls = %d, want 0", got)
	}
}

func TestProtocolLivePipelinePreparesBeforeReservationAndSkipsExecutorOnConflict(t *testing.T) {
	executor := &countingProtocolExecutor{}
	uctx := executorUserContext()
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{
		Executor:      executor,
		WorkflowStore: rejectingExecutionStore{},
	})
	outcome := protocolLiveOutcome{RequestID: "request"}
	result := pipeline.execute(context.Background(), uctx, OperationRequest{
		Operation:      "subscription.cancel",
		TenantID:       uctx.TenantID,
		ActorUserID:    uctx.UserID,
		ConversationID: uctx.ConversationID,
		TrustedParams: trustedParamsFromValues(uctx.TenantID, TrustedParamSource{
			Kind:     TrustedParamSourceRuntime,
			Resolver: "test",
		}, map[string]any{"conversation_id": uctx.ConversationID}),
	}, outcome)

	if got := executor.calls.Load(); got != 0 {
		t.Fatalf("executor calls = %d, want 0", got)
	}
	if !result.WorkflowStoreApplied {
		t.Fatal("WorkflowStoreApplied = false, want reservation conflict fenced from outer persistence")
	}
	if result.WriteGuardResult != "allow" || result.ResourcePolicyResult != "allow" {
		t.Fatalf("prepare results resource=%q write=%q, want both allow before reservation", result.ResourcePolicyResult, result.WriteGuardResult)
	}
	if result.BlockedReason != "workflow_processing" || result.FailureLayer != FailureExecutor {
		t.Fatalf("metadata blocked=%q layer=%q", result.BlockedReason, result.FailureLayer)
	}
}

func TestProtocolLivePipelineReportsLedgerLookupFailureMetadata(t *testing.T) {
	executor := &countingProtocolExecutor{}
	uctx := executorUserContext()
	pipeline := newProtocolLivePipeline(protocolLivePipelineDeps{Executor: executor, WorkflowStore: rejectingExecutionStore{}, OperationLedger: fakeOperationLedger{err: errors.New("db unavailable")}})
	result := pipeline.execute(context.Background(), uctx, OperationRequest{Operation: "subscription.cancel", TenantID: uctx.TenantID, ActorUserID: uctx.UserID, ConversationID: uctx.ConversationID, TrustedParams: trustedParamsFromValues(uctx.TenantID, TrustedParamSource{Kind: TrustedParamSourceRuntime, Resolver: "test"}, map[string]any{"conversation_id": uctx.ConversationID})}, protocolLiveOutcome{RequestID: "request"})
	if result.BlockedReason != "operation_ledger_lookup_failed" || result.FailureLayer != FailureExecutor || result.ExecutorStatus != "failed" {
		t.Fatalf("metadata blocked=%q layer=%q status=%q", result.BlockedReason, result.FailureLayer, result.ExecutorStatus)
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("executor calls=%d", executor.calls.Load())
	}
}

func TestExecutionCoordinatorCallsExecutorOutsideMemoryStoreMutex(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "cid", ActorUserID: 7}
	created, err := store.Create(context.Background(), key, &WorkflowSnapshot{ID: "wf", Type: WorkflowSubscriptionStart, State: WorkflowReady})
	if err != nil {
		t.Fatal(err)
	}
	executor := &countingProtocolExecutor{fn: func(ctx context.Context, _ OperationRequest) OperationExecutionResult {
		if _, err := store.Load(ctx, key); err != nil {
			t.Fatalf("Load() while executor runs: %v", err)
		}
		return OperationExecutionResult{Response: ResponseModel{Kind: ResponseResult, ResultText: "ok"}}
	}}

	coordinator := newProtocolLiveExecutionCoordinator(store, nil, func() time.Time { return now })
	_, err = coordinator.Execute(context.Background(), WorkflowExecutionRequest{
		Key:             key,
		ExpectedVersion: created.Version,
		Workflow:        created.Snapshot,
		Operation:       OperationRequest{Operation: "subscription.start"},
		BusinessKey:     "business",
		RequestID:       "request",
	}, executor)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMemoryWorkflowStoreExecutionLeaseFencingAndTakeover(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "cid", ActorUserID: 7}
	created, err := store.Create(context.Background(), key, &WorkflowSnapshot{ID: "wf", Type: WorkflowSubscriptionStart, State: WorkflowReady})
	if err != nil {
		t.Fatal(err)
	}
	firstLease := WorkflowExecutionLease{ExecutionToken: "first", LeaseExpiresAt: now.Add(2 * time.Minute)}
	reserved, err := store.ReserveExecution(context.Background(), key, created.Version, created.Snapshot, firstLease)
	if err != nil {
		t.Fatal(err)
	}
	if reserved.Snapshot.ExecutionLease == nil || reserved.Snapshot.ExecutionLease.LeaseExpiresAt.Sub(now) != 2*time.Minute {
		t.Fatalf("lease = %+v, want fixed two-minute lease", reserved.Snapshot.ExecutionLease)
	}
	if _, err := store.ReserveExecution(context.Background(), key, reserved.Version, reserved.Snapshot, WorkflowExecutionLease{ExecutionToken: "second", LeaseExpiresAt: now.Add(2 * time.Minute)}); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("active lease reserve error = %v, want conflict", err)
	}

	now = now.Add(2*time.Minute + time.Second)
	taken, err := store.ReserveExecution(context.Background(), key, reserved.Version, reserved.Snapshot, WorkflowExecutionLease{ExecutionToken: "second", LeaseExpiresAt: now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatalf("expired lease takeover: %v", err)
	}
	if err := store.FinalizeExecution(context.Background(), key, taken.Version, "first", nil); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("stale token finalize error = %v, want conflict", err)
	}
	if err := store.FinalizeExecution(context.Background(), key, taken.Version, "second", nil); err != nil {
		t.Fatalf("current token finalize: %v", err)
	}
}

func TestExecutionCoordinatorRecoversSucceededLedgerWithoutExecuting(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 7, ConversationID: "conv", ActorUserID: 9}
	base := &WorkflowSnapshot{ID: "wf", TenantID: 7, State: WorkflowReady, Version: 1}
	created, err := store.Create(context.Background(), key, base)
	if err != nil {
		t.Fatal(err)
	}
	executor := &countingProtocolExecutor{}
	coordinator := newProtocolLiveExecutionCoordinator(store, fakeOperationLedger{result: &RecoveredOperationResult{Operation: "subscription.start", WriteEffect: "created", PushEnabled: boolPtr(true)}}, func() time.Time { return now })
	got, err := coordinator.Execute(context.Background(), WorkflowExecutionRequest{Key: key, ExpectedVersion: created.Version, Workflow: created.Snapshot, Operation: OperationRequest{Operation: "subscription.start"}, BusinessKey: "business"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("executor calls=%d, want 0", executor.calls.Load())
	}
	payload, ok := got.OperationResult.Response.Payload.(OperationStatusPayload)
	if !ok || payload.Status != WriteStatusCreated {
		t.Fatalf("payload=%#v", got.OperationResult.Response.Payload)
	}
}

func TestExecutionCoordinatorExpiredLeaseWithoutLedgerRequiresRecovery(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 7, ConversationID: "conv", ActorUserID: 9}
	base := &WorkflowSnapshot{ID: "wf", TenantID: 7, State: WorkflowExecuting, ExecutionLease: &WorkflowExecutionLease{ExecutionToken: "old", BusinessKey: "business", LeaseExpiresAt: now.Add(-time.Second)}}
	created, err := store.Create(context.Background(), key, base)
	if err != nil {
		t.Fatal(err)
	}
	executor := &countingProtocolExecutor{}
	coordinator := newProtocolLiveExecutionCoordinator(store, fakeOperationLedger{}, func() time.Time { return now })
	got, err := coordinator.Execute(context.Background(), WorkflowExecutionRequest{Key: key, ExpectedVersion: created.Version, Workflow: created.Snapshot, Operation: OperationRequest{Operation: "subscription.start"}, BusinessKey: "business"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("executor calls=%d, want 0", executor.calls.Load())
	}
	if got.OperationResult.Response.RefusalReason != recoveryRequiredReply {
		t.Fatalf("reply=%q", got.OperationResult.Response.RefusalReason)
	}
	loaded, _ := store.Load(context.Background(), key)
	if loaded.Snapshot.State != WorkflowRecoveryRequired {
		t.Fatalf("state=%s", loaded.Snapshot.State)
	}
}

func TestExecutionCoordinatorReloadsLedgerAfterFinalizeConflict(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	inner := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 7, ConversationID: "conv", ActorUserID: 9}
	created, err := inner.Create(context.Background(), key, &WorkflowSnapshot{ID: "wf", TenantID: 7, State: WorkflowReady})
	if err != nil {
		t.Fatal(err)
	}
	ledger := &postExecutionLedger{result: &RecoveredOperationResult{Operation: "subscription.cancel", WriteEffect: "cancelled"}}
	executor := &countingProtocolExecutor{fn: func(context.Context, OperationRequest) OperationExecutionResult {
		return operationExecutionResult(ResponseModel{Kind: ResponseResult}, answerModeToolFirst)
	}}
	coordinator := newProtocolLiveExecutionCoordinator(finalizeConflictStore{inner}, ledger, func() time.Time { return now })
	got, err := coordinator.Execute(context.Background(), WorkflowExecutionRequest{Key: key, ExpectedVersion: created.Version, Workflow: created.Snapshot, Operation: OperationRequest{Operation: "subscription.cancel"}, BusinessKey: "business"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("executor calls=%d", executor.calls.Load())
	}
	payload := got.OperationResult.Response.Payload.(OperationStatusPayload)
	if payload.Status != WriteStatusCancelled {
		t.Fatalf("status=%s", payload.Status)
	}
	loaded, _ := inner.Load(context.Background(), key)
	if loaded == nil {
		t.Fatal("workflow was deleted after finalize conflict; a newer workflow must never be removed")
	}
}

func TestExecutionCoordinatorRecoveredExecutingWorkflowUsesOriginalFence(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 7, ConversationID: "conv", ActorUserID: 9}
	base := &WorkflowSnapshot{ID: "wf", TenantID: 7, State: WorkflowExecuting, ExecutionLease: &WorkflowExecutionLease{ExecutionToken: "original", BusinessKey: "business", LeaseExpiresAt: now.Add(time.Minute)}}
	created, err := store.Create(context.Background(), key, base)
	if err != nil {
		t.Fatal(err)
	}
	executor := &countingProtocolExecutor{}
	coordinator := newProtocolLiveExecutionCoordinator(store, fakeOperationLedger{result: &RecoveredOperationResult{Operation: "subscription.start", WriteEffect: "created"}}, func() time.Time { return now })
	_, err = coordinator.Execute(context.Background(), WorkflowExecutionRequest{Key: key, ExpectedVersion: created.Version, Workflow: created.Snapshot, Operation: OperationRequest{Operation: "subscription.start"}, BusinessKey: "business"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("executor calls=%d", executor.calls.Load())
	}
	loaded, _ := store.Load(context.Background(), key)
	if loaded != nil {
		t.Fatalf("original executing workflow not finalized: %+v", loaded)
	}
}

func TestExecutionCoordinatorRecoveredExecutingWorkflowPropagatesFinalizeInfrastructureError(t *testing.T) {
	sentinel := errors.New("database unavailable")
	workflow := &WorkflowSnapshot{ID: "wf", TenantID: 7, State: WorkflowExecuting, ExecutionLease: &WorkflowExecutionLease{ExecutionToken: "original", BusinessKey: "business"}}
	coordinator := newProtocolLiveExecutionCoordinator(finalizeErrorStore{WorkflowStore: newMemoryWorkflowStore(nil), err: sentinel}, fakeOperationLedger{result: &RecoveredOperationResult{Operation: "subscription.start", WriteEffect: "created"}}, time.Now)
	executor := &countingProtocolExecutor{}
	_, err := coordinator.Execute(context.Background(), WorkflowExecutionRequest{Key: WorkflowKey{TenantID: 7, ConversationID: "conv", ActorUserID: 9}, ExpectedVersion: 3, Workflow: workflow, Operation: OperationRequest{Operation: "subscription.start"}, BusinessKey: "business"}, executor)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v, want sentinel", err)
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("executor calls=%d", executor.calls.Load())
	}
}

func TestExecutionCoordinatorRecoveredExecutingWorkflowTreatsFenceConflictAsHistoricalSuccess(t *testing.T) {
	workflow := &WorkflowSnapshot{ID: "wf", TenantID: 7, State: WorkflowExecuting, ExecutionLease: &WorkflowExecutionLease{ExecutionToken: "original", BusinessKey: "business"}}
	coordinator := newProtocolLiveExecutionCoordinator(finalizeErrorStore{WorkflowStore: newMemoryWorkflowStore(nil), err: ErrWorkflowConflict}, fakeOperationLedger{result: &RecoveredOperationResult{Operation: "subscription.cancel", WriteEffect: "cancelled"}}, time.Now)
	executor := &countingProtocolExecutor{}
	got, err := coordinator.Execute(context.Background(), WorkflowExecutionRequest{Key: WorkflowKey{TenantID: 7, ConversationID: "conv", ActorUserID: 9}, ExpectedVersion: 3, Workflow: workflow, Operation: OperationRequest{Operation: "subscription.cancel"}, BusinessKey: "business"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("executor calls=%d", executor.calls.Load())
	}
	payload := got.OperationResult.Response.Payload.(OperationStatusPayload)
	if payload.Status != WriteStatusCancelled {
		t.Fatalf("status=%s", payload.Status)
	}
}

func TestMemoryWorkflowStoreReservationExtendsWorkflowTTLThroughActiveLease(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "cid-short-ttl", ActorUserID: 7}
	created, err := store.Create(context.Background(), key, &WorkflowSnapshot{
		ID: "wf", Type: WorkflowSubscriptionStart, State: WorkflowReady, ExpiresAt: now.Add(30 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	reserved, err := store.ReserveExecution(context.Background(), key, created.Version, created.Snapshot, WorkflowExecutionLease{
		ExecutionToken: "first", LeaseExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	leaseExpiresAt := now.Add(WorkflowExecutionLeaseDuration)
	if !reserved.Snapshot.ExecutionLease.LeaseExpiresAt.Equal(leaseExpiresAt) {
		t.Fatalf("lease expires at %v, want store-clock deadline %v", reserved.Snapshot.ExecutionLease.LeaseExpiresAt, leaseExpiresAt)
	}
	if reserved.Snapshot.ExpiresAt.Before(leaseExpiresAt) {
		t.Fatalf("workflow expires at %v before active lease %v", reserved.Snapshot.ExpiresAt, leaseExpiresAt)
	}

	now = now.Add(time.Minute)
	if loaded, err := store.Load(context.Background(), key); err != nil || loaded == nil {
		t.Fatalf("Load during active lease = %+v, %v; want workflow", loaded, err)
	}
	if _, err := store.ReserveExecution(context.Background(), key, reserved.Version, reserved.Snapshot, WorkflowExecutionLease{ExecutionToken: "second"}); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("reserve during active lease error = %v, want conflict", err)
	}
}

func TestMemoryWorkflowStoreOnlyOneConcurrentExecutionReservationWins(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "cid", ActorUserID: 7}
	created, err := store.Create(context.Background(), key, &WorkflowSnapshot{ID: "wf", Type: WorkflowSubscriptionStart, State: WorkflowReady})
	if err != nil {
		t.Fatal(err)
	}

	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reserveErr := store.ReserveExecution(context.Background(), key, created.Version, created.Snapshot, WorkflowExecutionLease{
				ExecutionToken: newExecutionToken(),
				LeaseExpiresAt: now.Add(2 * time.Minute),
			})
			if reserveErr == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := wins.Load(); got != 1 {
		t.Fatalf("reservation wins = %d, want 1", got)
	}
}

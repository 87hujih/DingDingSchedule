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
	coordinator := newProtocolLiveExecutionCoordinator(rejectingExecutionStore{}, time.Now)

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

	coordinator := newProtocolLiveExecutionCoordinator(store, func() time.Time { return now })
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

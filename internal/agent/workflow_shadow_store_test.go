package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWorkflowShadowStoreLoadKeepsPrimaryAuthorityAndReportsDiff(t *testing.T) {
	t.Parallel()

	primary := newMemoryWorkflowStore(nil)
	mirror := newMemoryWorkflowStore(nil)
	key := WorkflowKey{TenantID: 1, ConversationID: "conv-shadow", ActorUserID: 2}
	primarySnapshot := shadowTestSnapshot(key, WorkflowCollectScope)
	mirrorSnapshot := shadowTestSnapshot(key, WorkflowCollectDepartments)
	if _, err := primary.Create(context.Background(), key, primarySnapshot); err != nil {
		t.Fatalf("primary Create() error = %v", err)
	}
	if _, err := mirror.Create(context.Background(), key, mirrorSnapshot); err != nil {
		t.Fatalf("mirror Create() error = %v", err)
	}

	var events []WorkflowShadowEvent
	store, err := NewWorkflowShadowStore(primary, mirror, func(event WorkflowShadowEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("NewWorkflowShadowStore() error = %v", err)
	}

	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.Snapshot.State != WorkflowCollectScope {
		t.Fatalf("Load() = %+v, want primary collect_scope snapshot", loaded)
	}
	if len(events) != 1 || events[0].Code != "snapshot_diff" {
		t.Fatalf("events = %+v, want one snapshot_diff", events)
	}
}

func TestWorkflowShadowStoreMirrorFailureDoesNotFailPrimaryMutation(t *testing.T) {
	t.Parallel()

	primary := newMemoryWorkflowStore(nil)
	mirror := &workflowStoreCallRecorder{
		WorkflowStore: newMemoryWorkflowStore(nil),
		createErr:     errors.New("mirror unavailable"),
	}
	key := WorkflowKey{TenantID: 1, ConversationID: "conv-shadow-error", ActorUserID: 2}
	var events []WorkflowShadowEvent
	store, err := NewWorkflowShadowStore(primary, mirror, func(event WorkflowShadowEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("NewWorkflowShadowStore() error = %v", err)
	}

	created, err := store.Create(context.Background(), key, shadowTestSnapshot(key, WorkflowCollectScope))
	if err != nil {
		t.Fatalf("Create() error = %v, mirror failures must be observational", err)
	}
	if created == nil || created.Version != 1 {
		t.Fatalf("Create() = %+v, want primary version 1", created)
	}
	if mirror.createCallCount() != 1 {
		t.Fatalf("mirror Create calls = %d, want 1", mirror.createCallCount())
	}
	if len(events) != 1 || events[0].Code != "shadow_error" {
		t.Fatalf("events = %+v, want one shadow_error", events)
	}
}

func TestWorkflowShadowStorePrimaryConflictSkipsMirrorMutation(t *testing.T) {
	t.Parallel()

	primary := newMemoryWorkflowStore(nil)
	mirror := &workflowStoreCallRecorder{WorkflowStore: newMemoryWorkflowStore(nil)}
	key := WorkflowKey{TenantID: 1, ConversationID: "conv-primary-conflict", ActorUserID: 2}
	snapshot := shadowTestSnapshot(key, WorkflowCollectScope)
	if _, err := primary.Create(context.Background(), key, snapshot); err != nil {
		t.Fatalf("primary Create() error = %v", err)
	}
	store, err := NewWorkflowShadowStore(primary, mirror, nil)
	if err != nil {
		t.Fatalf("NewWorkflowShadowStore() error = %v", err)
	}

	if _, err := store.Create(context.Background(), key, snapshot); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("Create() error = %v, want ErrWorkflowConflict", err)
	}
	if mirror.createCallCount() != 0 {
		t.Fatalf("mirror Create calls = %d, want 0 after primary conflict", mirror.createCallCount())
	}
}

func TestCanonicalExecutionEqualIgnoresTimeRepresentationNoise(t *testing.T) {
	t.Parallel()

	now := time.Now()
	left := &PersistedExecutionV1{
		Status: WorkflowExecutionExecuting,
		Reservation: ReservedExecutionV1{
			Operation:        "subscription.start",
			BusinessKey:      "business-key",
			TrustedParams:    PersistedTrustedParamsV1{"scope": "all", "dept_ids": []int64{}},
			ExecutionToken:   "token",
			AttemptRequestID: "request",
			StartedAt:        now,
			LeaseExpiresAt:   now.Add(time.Minute),
		},
	}
	payload, err := MarshalReservedExecution(left.Reservation)
	if err != nil {
		t.Fatalf("MarshalReservedExecution() error = %v", err)
	}
	rightReservation, err := UnmarshalReservedExecution(payload)
	if err != nil {
		t.Fatalf("UnmarshalReservedExecution() error = %v", err)
	}
	equal, err := canonicalExecutionEqual(left, &PersistedExecutionV1{
		Status:      WorkflowExecutionExecuting,
		Reservation: rightReservation,
	})
	if err != nil {
		t.Fatalf("canonicalExecutionEqual() error = %v", err)
	}
	if !equal {
		t.Fatal("canonicalExecutionEqual() = false for equivalent wall-clock values")
	}
}

func TestWorkflowShadowStoreBoundsMirrorLatency(t *testing.T) {
	t.Parallel()

	primary := newMemoryWorkflowStore(nil)
	key := WorkflowKey{TenantID: 1, ConversationID: "conv-shadow-timeout", ActorUserID: 2}
	if _, err := primary.Create(context.Background(), key, shadowTestSnapshot(key, WorkflowCollectScope)); err != nil {
		t.Fatalf("primary Create() error = %v", err)
	}
	mirror := &blockingWorkflowStore{WorkflowStore: newMemoryWorkflowStore(nil)}
	var events []WorkflowShadowEvent
	store, err := NewWorkflowShadowStore(primary, mirror, func(event WorkflowShadowEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("NewWorkflowShadowStore() error = %v", err)
	}
	start := time.Now()
	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() = nil, want primary result")
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("Load() elapsed = %v, want bounded mirror latency", elapsed)
	}
	if len(events) != 1 || events[0].Code != "shadow_error" {
		t.Fatalf("events = %+v, want shadow_error", events)
	}
}

type workflowStoreCallRecorder struct {
	WorkflowStore
	mu          sync.Mutex
	createCalls int
	createErr   error
}

type blockingWorkflowStore struct {
	WorkflowStore
}

func (*blockingWorkflowStore) Load(ctx context.Context, _ WorkflowKey) (*VersionedWorkflow, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *workflowStoreCallRecorder) Create(
	ctx context.Context,
	key WorkflowKey,
	next *WorkflowSnapshot,
) (*VersionedWorkflow, error) {
	s.mu.Lock()
	s.createCalls++
	err := s.createErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.WorkflowStore.Create(ctx, key, next)
}

func (s *workflowStoreCallRecorder) createCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createCalls
}

func shadowTestSnapshot(key WorkflowKey, state WorkflowState) *WorkflowSnapshot {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &WorkflowSnapshot{
		ID:             "wf-shadow",
		TenantID:       key.TenantID,
		ActorUserID:    key.ActorUserID,
		ConversationID: key.ConversationID,
		Type:           WorkflowSubscriptionStart,
		State:          state,
		MissingFields:  []string{"scope"},
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(defaultWorkflowTTL),
	}
}

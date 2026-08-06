package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMemoryWorkflowStoreKeysByTenantConversationAndActor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}

	created, err := store.Create(context.Background(), key, testWorkflowSnapshot(key, now))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Version != 1 || created.Snapshot.Version != 1 {
		t.Fatalf("created versions = wrapper:%d snapshot:%d, want 1", created.Version, created.Snapshot.Version)
	}

	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() = nil, want workflow")
	}
	if loaded.Snapshot.TenantID != 42 ||
		loaded.Snapshot.ConversationID != "conv-a" ||
		loaded.Snapshot.ActorUserID != 7 {
		t.Fatalf(
			"loaded identity = tenant:%d conversation:%q actor:%d",
			loaded.Snapshot.TenantID,
			loaded.Snapshot.ConversationID,
			loaded.Snapshot.ActorUserID,
		)
	}
	loaded.Snapshot.MissingFields[0] = "mutated"
	reloaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() after mutation error = %v", err)
	}
	if reloaded.Snapshot.MissingFields[0] != "scope" {
		t.Fatalf("snapshot mutation leaked into store: %v", reloaded.Snapshot.MissingFields)
	}

	otherActor, err := store.Load(
		context.Background(),
		WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 8},
	)
	if err != nil {
		t.Fatalf("Load(other actor) error = %v", err)
	}
	if otherActor != nil {
		t.Fatalf("Load(other actor) = %+v, want nil", otherActor)
	}
}

func TestMemoryWorkflowStoreCreateCASDeleteUsesExpectedVersion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}

	created, err := store.Create(context.Background(), key, testWorkflowSnapshot(key, now))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.Create(
		context.Background(),
		key,
		testWorkflowSnapshot(key, now),
	); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("duplicate Create() error = %v, want ErrWorkflowConflict", err)
	}

	next := testWorkflowSnapshot(key, now)
	next.State = WorkflowCollectDepartments
	setWorkflowMissingFields(next, []string{"dept_names"})
	if _, err := store.CompareAndSwap(
		context.Background(),
		key,
		created.Version+1,
		next,
	); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("stale CompareAndSwap() error = %v, want ErrWorkflowConflict", err)
	}

	updated, err := store.CompareAndSwap(context.Background(), key, created.Version, next)
	if err != nil {
		t.Fatalf("CompareAndSwap() error = %v", err)
	}
	if updated.Version != 2 || updated.Snapshot.Version != 2 {
		t.Fatalf("updated versions = wrapper:%d snapshot:%d, want 2", updated.Version, updated.Snapshot.Version)
	}
	if updated.Snapshot.State != WorkflowCollectDepartments {
		t.Fatalf("updated state = %q, want %q", updated.Snapshot.State, WorkflowCollectDepartments)
	}

	if err := store.DeleteIfVersion(
		context.Background(),
		key,
		created.Version,
		"stale",
	); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("stale DeleteIfVersion() error = %v, want ErrWorkflowConflict", err)
	}
	if err := store.DeleteIfVersion(context.Background(), key, updated.Version, "done"); err != nil {
		t.Fatalf("DeleteIfVersion() error = %v", err)
	}
	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() after delete error = %v", err)
	}
	if loaded != nil {
		t.Fatalf("Load() after delete = %+v, want nil", loaded)
	}
}

func TestMemoryWorkflowStoreExpiresIdleButRetainsReservedExecution(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	idleKey := WorkflowKey{TenantID: 42, ConversationID: "idle", ActorUserID: 7}
	idle := testWorkflowSnapshot(idleKey, now)
	idle.ExpiresAt = now.Add(time.Minute)
	if _, err := store.Create(context.Background(), idleKey, idle); err != nil {
		t.Fatalf("Create(idle) error = %v", err)
	}

	reservedKey := WorkflowKey{TenantID: 42, ConversationID: "reserved", ActorUserID: 7}
	reserved := testWorkflowSnapshot(reservedKey, now)
	reserved.ExpiresAt = now.Add(time.Minute)
	if _, err := store.CreateReservedExecution(
		context.Background(),
		reservedKey,
		reserved,
		testReservedExecution("token-1", now, time.Minute),
	); err != nil {
		t.Fatalf("CreateReservedExecution() error = %v", err)
	}

	now = now.Add(2 * time.Minute)
	expired, err := store.Load(context.Background(), idleKey)
	if err != nil {
		t.Fatalf("Load(idle expired) error = %v", err)
	}
	if expired != nil {
		t.Fatalf("Load(idle expired) = %+v, want nil", expired)
	}

	retained, err := store.Load(context.Background(), reservedKey)
	if err != nil {
		t.Fatalf("Load(reserved expired) error = %v", err)
	}
	if retained == nil || retained.Execution == nil {
		t.Fatalf("Load(reserved expired) = %+v, want retained execution", retained)
	}
}

func TestMemoryWorkflowStoreOrdinaryMutationRejectsActiveExecution(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	created, err := store.Create(context.Background(), key, testWorkflowSnapshot(key, now))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reserved, err := store.ReserveExecution(
		context.Background(),
		key,
		created.Version,
		created.Snapshot,
		testReservedExecution("token-1", now, time.Minute),
	)
	if err != nil {
		t.Fatalf("ReserveExecution() error = %v", err)
	}

	if _, err := store.CompareAndSwap(
		context.Background(),
		key,
		reserved.Version,
		reserved.Snapshot,
	); !errors.Is(err, ErrExecutionInProgress) {
		t.Fatalf("CompareAndSwap(active execution) error = %v, want ErrExecutionInProgress", err)
	}
	if err := store.DeleteIfVersion(
		context.Background(),
		key,
		reserved.Version,
		"ordinary_delete",
	); !errors.Is(err, ErrExecutionInProgress) {
		t.Fatalf("DeleteIfVersion(active execution) error = %v, want ErrExecutionInProgress", err)
	}
}

func TestMemoryWorkflowStoreExecutionResultAndFinalizeRequireVersionAndToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	reserved, err := store.CreateReservedExecution(
		context.Background(),
		key,
		testWorkflowSnapshot(key, now),
		testReservedExecution("token-1", now, time.Minute),
	)
	if err != nil {
		t.Fatalf("CreateReservedExecution() error = %v", err)
	}
	if reserved.Version != 1 ||
		reserved.Execution == nil ||
		reserved.Execution.Status != WorkflowExecutionExecuting {
		t.Fatalf("reserved = %+v, want version 1 executing", reserved)
	}
	reserved.Execution.Reservation.TrustedParams["dept_ids"].([]int64)[0] = 999
	reloaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() after execution mutation error = %v", err)
	}
	if got := reloaded.Execution.Reservation.TrustedParams["dept_ids"].([]int64)[0]; got != 101 {
		t.Fatalf("execution mutation leaked into store: first dept ID = %d, want 101", got)
	}

	result := PersistedExecutionResultV1{
		BusinessKey: "business-key",
		WriteEffect: WriteEffectCreated,
		CompletedAt: now.Add(10 * time.Second),
	}
	if _, err := store.RecordExecutionResult(
		context.Background(),
		key,
		reserved.Version,
		"wrong-token",
		result,
	); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("RecordExecutionResult(wrong token) error = %v, want ErrWorkflowConflict", err)
	}
	recorded, err := store.RecordExecutionResult(
		context.Background(),
		key,
		reserved.Version,
		"token-1",
		result,
	)
	if err != nil {
		t.Fatalf("RecordExecutionResult() error = %v", err)
	}
	if recorded.Version != 2 || recorded.Execution.Status != WorkflowExecutionResultRecorded {
		t.Fatalf("recorded = %+v, want version 2 result_recorded", recorded)
	}

	if _, err := store.FinalizeExecution(
		context.Background(),
		key,
		reserved.Version,
		"token-1",
		recorded.Snapshot,
	); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("FinalizeExecution(stale version) error = %v, want ErrWorkflowConflict", err)
	}
	finalized, err := store.FinalizeExecution(
		context.Background(),
		key,
		recorded.Version,
		"token-1",
		recorded.Snapshot,
	)
	if err != nil {
		t.Fatalf("FinalizeExecution() error = %v", err)
	}
	if finalized.Version != 3 || finalized.Execution != nil {
		t.Fatalf("finalized = %+v, want version 3 idle", finalized)
	}
}

func TestMemoryWorkflowStoreRejectsUnsupportedExecutionParamType(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	reservation := testReservedExecution("token-1", now, time.Minute)
	reservation.TrustedParams["nested"] = map[string]any{"unsafe": true}

	if _, err := store.CreateReservedExecution(
		context.Background(),
		key,
		testWorkflowSnapshot(key, now),
		reservation,
	); err == nil {
		t.Fatal("CreateReservedExecution() error = nil, want unsupported trusted parameter error")
	}
}

func TestMemoryWorkflowStoreUsesExecutionCodecAllowlistAndCanonicalization(t *testing.T) {
	t.Parallel()

	store := newMemoryWorkflowStore(nil)
	key := WorkflowKey{TenantID: 18, ConversationID: "conversation-codec", ActorUserID: 28}
	snapshot := testWorkflowSnapshot(key, time.Now())
	snapshot.State = WorkflowReady
	reservation := testReservedExecution("token-codec", time.Now(), time.Minute)
	reservation.TrustedParams = PersistedTrustedParamsV1{
		"scope":    "department",
		"dept_ids": []int64{102, 101, 102},
	}
	created, err := store.CreateReservedExecution(context.Background(), key, snapshot, reservation)
	if err != nil {
		t.Fatalf("CreateReservedExecution() error = %v", err)
	}
	got := created.Execution.Reservation.TrustedParams["dept_ids"].([]int64)
	if len(got) != 2 || got[0] != 101 || got[1] != 102 {
		t.Fatalf("canonical dept_ids = %v, want [101 102]", got)
	}

	unsafeKey := WorkflowKey{TenantID: 18, ConversationID: "conversation-secret", ActorUserID: 28}
	unsafe := testReservedExecution("token-secret", time.Now(), time.Minute)
	unsafe.TrustedParams["api_key"] = "secret"
	if _, err := store.CreateReservedExecution(
		context.Background(),
		unsafeKey,
		testWorkflowSnapshot(unsafeKey, time.Now()),
		unsafe,
	); err == nil {
		t.Fatal("CreateReservedExecution() accepted disallowed api_key")
	}
}

func TestMemoryWorkflowStoreTakeoverRequiresExpiredLeaseAndFencesOldToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	reserved, err := store.CreateReservedExecution(
		context.Background(),
		key,
		testWorkflowSnapshot(key, now),
		testReservedExecution("token-old", now, time.Minute),
	)
	if err != nil {
		t.Fatalf("CreateReservedExecution() error = %v", err)
	}

	freshTakeover := testReservedExecution("token-new", now, 2*time.Minute)
	if _, err := store.TakeoverExpiredExecution(
		context.Background(),
		key,
		reserved.Version,
		"token-old",
		freshTakeover,
	); !errors.Is(err, ErrExecutionInProgress) {
		t.Fatalf("TakeoverExpiredExecution(fresh lease) error = %v, want ErrExecutionInProgress", err)
	}

	now = now.Add(2 * time.Minute)
	expiredTakeover := testReservedExecution("token-new", now, 2*time.Minute)
	takenOver, err := store.TakeoverExpiredExecution(
		context.Background(),
		key,
		reserved.Version,
		"token-old",
		expiredTakeover,
	)
	if err != nil {
		t.Fatalf("TakeoverExpiredExecution() error = %v", err)
	}
	if takenOver.Version != 2 ||
		takenOver.Execution.Reservation.ExecutionToken != "token-new" {
		t.Fatalf("takenOver = %+v, want version 2 token-new", takenOver)
	}

	result := PersistedExecutionResultV1{
		BusinessKey: "business-key",
		WriteEffect: WriteEffectNoOp,
		CompletedAt: now.Add(time.Second),
	}
	if _, err := store.RecordExecutionResult(
		context.Background(),
		key,
		takenOver.Version,
		"token-old",
		result,
	); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("RecordExecutionResult(old token) error = %v, want ErrWorkflowConflict", err)
	}
	if _, err := store.RecordExecutionResult(
		context.Background(),
		key,
		takenOver.Version,
		"token-new",
		result,
	); err != nil {
		t.Fatalf("RecordExecutionResult(new token) error = %v", err)
	}
}

func TestMemoryWorkflowStoreDeleteReservedExecutionRequiresCurrentToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	reserved, err := store.CreateReservedExecution(
		context.Background(),
		key,
		testWorkflowSnapshot(key, now),
		testReservedExecution("token-1", now, time.Minute),
	)
	if err != nil {
		t.Fatalf("CreateReservedExecution() error = %v", err)
	}

	if err := store.DeleteReservedExecution(
		context.Background(),
		key,
		reserved.Version,
		"wrong-token",
		"executor_failed_before_effect",
	); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("DeleteReservedExecution(wrong token) error = %v, want ErrWorkflowConflict", err)
	}
	if err := store.DeleteReservedExecution(
		context.Background(),
		key,
		reserved.Version,
		"token-1",
		"must_not_delete_executing",
	); !errors.Is(err, ErrExecutionInProgress) {
		t.Fatalf("DeleteReservedExecution(executing) error = %v, want ErrExecutionInProgress", err)
	}
	recorded, err := store.RecordExecutionResult(
		context.Background(),
		key,
		reserved.Version,
		"token-1",
		PersistedExecutionResultV1{
			BusinessKey: "business-key",
			WriteEffect: WriteEffectCreated,
			CompletedAt: now,
		},
	)
	if err != nil {
		t.Fatalf("RecordExecutionResult() error = %v", err)
	}
	if err := store.DeleteReservedExecution(
		context.Background(),
		key,
		recorded.Version,
		"token-1",
		"completed",
	); err != nil {
		t.Fatalf("DeleteReservedExecution(result recorded) error = %v", err)
	}
	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() after reserved delete error = %v", err)
	}
	if loaded != nil {
		t.Fatalf("Load() after reserved delete = %+v, want nil", loaded)
	}
}

func TestMemoryWorkflowStoreConcurrentCASAllowsSingleWinner(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	created, err := store.Create(context.Background(), key, testWorkflowSnapshot(key, now))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	const contenders = 2
	var wg sync.WaitGroup
	errs := make(chan error, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			next := testWorkflowSnapshot(key, now)
			next.LastUserMessage = fmt.Sprintf("candidate-%d", index)
			_, compareErr := store.CompareAndSwap(context.Background(), key, created.Version, next)
			errs <- compareErr
		}(i)
	}
	wg.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for compareErr := range errs {
		switch {
		case compareErr == nil:
			successes++
		case errors.Is(compareErr, ErrWorkflowConflict):
			conflicts++
		default:
			t.Fatalf("CompareAndSwap() unexpected error = %v", compareErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS outcomes = successes:%d conflicts:%d, want 1/1", successes, conflicts)
	}
}

func testWorkflowSnapshot(key WorkflowKey, now time.Time) *WorkflowSnapshot {
	return &WorkflowSnapshot{
		ID:             "wf-1",
		TenantID:       key.TenantID,
		ConversationID: key.ConversationID,
		ActorUserID:    key.ActorUserID,
		Type:           WorkflowSubscriptionStart,
		State:          WorkflowCollectScope,
		MissingFields:  []string{"scope"},
		MissingSlots:   []string{"scope"},
		ExpiresAt:      now.Add(defaultWorkflowTTL),
	}
}

func testReservedExecution(token string, now time.Time, lease time.Duration) ReservedExecutionV1 {
	return ReservedExecutionV1{
		Operation:   "subscription.start",
		BusinessKey: "business-key",
		TrustedParams: PersistedTrustedParamsV1{
			"scope":    "all",
			"dept_ids": []int64{101, 102},
		},
		ExecutionToken:   token,
		AttemptRequestID: "request-1",
		StartedAt:        now,
		LeaseExpiresAt:   now.Add(lease),
	}
}

func TestWorkflowArbiterDecisions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	arbiter := newWorkflowArbiter(func() time.Time { return now })
	active := &WorkflowSnapshot{
		ID:             "wf-1",
		Type:           WorkflowSubscriptionStart,
		State:          WorkflowCollectScope,
		MissingFields:  []string{"scope"},
		MissingSlots:   []string{"scope"},
		ExpiresAt:      now.Add(time.Minute),
		TenantID:       42,
		ConversationID: "conv-a",
		ActorUserID:    7,
	}

	tests := []struct {
		name     string
		active   *WorkflowSnapshot
		draft    ProtocolDraft
		want     WorkflowDecision
		expired  bool
		clearing bool
	}{
		{
			name:   "continue active workflow",
			active: active,
			draft:  ProtocolDraft{Act: ActWorkflowContinue, Domain: DomainSubscription, Operation: "subscription.start"},
			want:   WorkflowContinueDecision,
		},
		{
			name:     "cancel active workflow",
			active:   active,
			draft:    ProtocolDraft{Act: ActWorkflowCancel, Domain: DomainSubscription, Operation: "subscription.start"},
			want:     WorkflowCanceled,
			clearing: true,
		},
		{
			name:     "interrupt active workflow",
			active:   active,
			draft:    ProtocolDraft{Act: ActReadQuery, Domain: DomainAttendance, Operation: "attendance.query_status"},
			want:     WorkflowInterrupted,
			clearing: true,
		},
		{
			name:   "start new workflow",
			active: nil,
			draft: ProtocolDraft{
				Act:        ActWriteRequest,
				Domain:     DomainSubscription,
				Operation:  "subscription.start",
				Confidence: 0.96,
			},
			want: WorkflowStartNew,
		},
		{
			name:   "single turn without workflow",
			active: nil,
			draft:  ProtocolDraft{Act: ActReadQuery, Domain: DomainAttendance, Operation: "attendance.query_status"},
			want:   WorkflowSingleTurn,
		},
		{
			name: "expired workflow cannot continue",
			active: &WorkflowSnapshot{
				ID:             "wf-expired",
				Type:           WorkflowSubscriptionStart,
				State:          WorkflowCollectDepartments,
				MissingFields:  []string{"dept_names"},
				ExpiresAt:      now.Add(-time.Second),
				TenantID:       42,
				ConversationID: "conv-a",
				ActorUserID:    7,
			},
			draft:    ProtocolDraft{Act: ActWorkflowContinue, Domain: DomainSubscription, Operation: "subscription.start"},
			want:     WorkflowSingleTurn,
			expired:  true,
			clearing: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := arbiter.Decide(WorkflowArbiterInput{
				Draft:          tt.draft,
				ActiveWorkflow: tt.active,
			})
			if got.Decision != tt.want {
				t.Fatalf("Decision = %q, want %q", got.Decision, tt.want)
			}
			if got.Expired != tt.expired {
				t.Fatalf("Expired = %v, want %v", got.Expired, tt.expired)
			}
			if got.ClearWorkflow != tt.clearing {
				t.Fatalf("ClearWorkflow = %v, want %v", got.ClearWorkflow, tt.clearing)
			}
		})
	}
}

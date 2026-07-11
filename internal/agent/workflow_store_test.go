package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryWorkflowStoreCompareAndSwapRejectsStaleVersion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	snapshot := &WorkflowSnapshot{
		ID:             "wf-1",
		TenantID:       key.TenantID,
		ConversationID: key.ConversationID,
		ActorUserID:    key.ActorUserID,
		Type:           WorkflowSubscriptionStart,
		State:          WorkflowCollectScope,
		ExpiresAt:      now.Add(time.Minute),
	}

	created, err := store.Create(context.Background(), key, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CompareAndSwap(context.Background(), key, created.Version+1, snapshot)
	if !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryWorkflowStoreKeysByTenantConversationAndActor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}

	created, err := store.Create(context.Background(), key, &WorkflowSnapshot{
		ID:             "wf-1",
		TenantID:       key.TenantID,
		ConversationID: key.ConversationID,
		ActorUserID:    key.ActorUserID,
		Type:           WorkflowSubscriptionStart,
		State:          WorkflowCollectScope,
		MissingFields:  []string{"scope"},
		ExpiresAt:      now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("Version = %d, want 1 after create", created.Version)
	}

	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatalf("Load() = nil, want workflow")
	}
	if loaded.Snapshot.TenantID != 42 || loaded.Snapshot.ConversationID != "conv-a" || loaded.Snapshot.ActorUserID != 7 {
		t.Fatalf("loaded identity = tenant:%d conversation:%q actor:%d", loaded.Snapshot.TenantID, loaded.Snapshot.ConversationID, loaded.Snapshot.ActorUserID)
	}
	if loaded.Version != 1 {
		t.Fatalf("Version = %d, want 1 after create", loaded.Version)
	}

	otherActor, err := store.Load(context.Background(), WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 8})
	if err != nil {
		t.Fatalf("Load(other actor) error = %v", err)
	}
	if otherActor != nil {
		t.Fatalf("Load(other actor) = %+v, want nil", otherActor)
	}
}

func TestMemoryWorkflowStoreExpiresAndClonesSnapshots(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	_, err := store.Create(context.Background(), key, &WorkflowSnapshot{
		ID:             "wf-1",
		TenantID:       key.TenantID,
		ConversationID: key.ConversationID,
		ActorUserID:    key.ActorUserID,
		Type:           WorkflowSubscriptionStart,
		State:          WorkflowCollectDepartments,
		MissingFields:  []string{"dept_names"},
		MissingSlots:   []string{"dept_names"},
		Candidates: map[string][]Candidate{
			"dept_ids": {
				{ID: "101", Label: "信工24级", Value: int64(101)},
				{ID: "102", Label: "信工25级", Value: int64(102)},
			},
		},
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	loaded.Snapshot.Candidates["dept_ids"][0].Label = "mutated"
	loaded.Snapshot.MissingFields[0] = "mutated"

	reloaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() after mutation error = %v", err)
	}
	if reloaded.Snapshot.Candidates["dept_ids"][0].Label != "信工24级" {
		t.Fatalf("candidate clone leaked mutation: %+v", reloaded.Snapshot.Candidates["dept_ids"][0])
	}
	if reloaded.Snapshot.MissingFields[0] != "dept_names" {
		t.Fatalf("MissingFields clone leaked mutation: %v", reloaded.Snapshot.MissingFields)
	}

	now = now.Add(2 * time.Minute)
	expired, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load(expired) error = %v", err)
	}
	if expired != nil {
		t.Fatalf("Load(expired) = %+v, want nil", expired)
	}
}

func TestMemoryWorkflowStoreCompareAndSwapAllowsOnlyOneConcurrentUpdate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	created, err := store.Create(context.Background(), key, &WorkflowSnapshot{
		ID:        "wf-cas",
		Type:      WorkflowSubscriptionStart,
		State:     WorkflowCollectScope,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	const updates = 2
	var wg sync.WaitGroup
	errs := make(chan error, updates)
	for i := 0; i < updates; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := cloneWorkflowSnapshot(created.Snapshot)
			next.LastUserMessage = "updated"
			_, err := store.CompareAndSwap(context.Background(), key, created.Version, next)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	conflicts := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrWorkflowConflict):
			conflicts++
		default:
			t.Fatalf("CompareAndSwap() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
}

func TestMemoryWorkflowStoreDeleteIfVersionRejectsStaleVersion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	created, err := store.Create(context.Background(), key, &WorkflowSnapshot{
		ID:        "wf-delete",
		Type:      WorkflowSubscriptionStart,
		State:     WorkflowCollectScope,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteIfVersion(context.Background(), key, created.Version+1, "stale"); !errors.Is(err, ErrWorkflowConflict) {
		t.Fatalf("DeleteIfVersion() error = %v, want conflict", err)
	}
	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("stale delete removed workflow")
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
			draft:  ProtocolDraft{Act: ActWriteRequest, Domain: DomainSubscription, Operation: "subscription.start", Confidence: 0.96},
			want:   WorkflowStartNew,
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

package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryWorkflowStoreKeysByTenantConversationAndActor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}

	err := store.Save(context.Background(), &WorkflowSnapshot{
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
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatalf("Load() = nil, want workflow")
	}
	if loaded.TenantID != 42 || loaded.ConversationID != "conv-a" || loaded.ActorUserID != 7 {
		t.Fatalf("loaded identity = tenant:%d conversation:%q actor:%d", loaded.TenantID, loaded.ConversationID, loaded.ActorUserID)
	}
	if loaded.Version != 1 {
		t.Fatalf("Version = %d, want 1 after first save", loaded.Version)
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
	err := store.Save(context.Background(), &WorkflowSnapshot{
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
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	loaded.Candidates["dept_ids"][0].Label = "mutated"
	loaded.MissingFields[0] = "mutated"

	reloaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() after mutation error = %v", err)
	}
	if reloaded.Candidates["dept_ids"][0].Label != "信工24级" {
		t.Fatalf("candidate clone leaked mutation: %+v", reloaded.Candidates["dept_ids"][0])
	}
	if reloaded.MissingFields[0] != "dept_names" {
		t.Fatalf("MissingFields clone leaked mutation: %v", reloaded.MissingFields)
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

func TestMemoryWorkflowStoreWithLockSerializesConcurrentUpdates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	key := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}

	const updates = 25
	var wg sync.WaitGroup
	errs := make(chan error, updates)
	for i := 0; i < updates; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.WithLock(context.Background(), key, func(current *WorkflowSnapshot) (*WorkflowSnapshot, error) {
				if current == nil {
					current = &WorkflowSnapshot{
						ID:             "wf-locked",
						TenantID:       key.TenantID,
						ConversationID: key.ConversationID,
						ActorUserID:    key.ActorUserID,
						Type:           WorkflowSubscriptionStart,
						State:          WorkflowCollectScope,
						ExpiresAt:      now.Add(time.Minute),
					}
				}
				current.LastUserMessage += "x"
				return current, nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("WithLock() error = %v", err)
		}
	}

	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatalf("Load() = nil, want workflow")
	}
	if len(loaded.LastUserMessage) != updates {
		t.Fatalf("LastUserMessage length = %d, want %d", len(loaded.LastUserMessage), updates)
	}
	if loaded.Version != updates {
		t.Fatalf("Version = %d, want %d", loaded.Version, updates)
	}
}

func TestMemoryWorkflowStoreWithLockDoesNotBlockDifferentKeys(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := newMemoryWorkflowStore(func() time.Time { return now })
	keyA := WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	keyB := WorkflowKey{TenantID: 42, ConversationID: "conv-b", ActorUserID: 7}

	enteredA := make(chan struct{})
	releaseA := make(chan struct{})
	doneA := make(chan error, 1)
	go func() {
		doneA <- store.WithLock(context.Background(), keyA, func(current *WorkflowSnapshot) (*WorkflowSnapshot, error) {
			close(enteredA)
			<-releaseA
			if current == nil {
				current = &WorkflowSnapshot{ID: "wf-a", Type: WorkflowSubscriptionStart, State: WorkflowCollectScope, ExpiresAt: now.Add(time.Minute)}
			}
			current.LastUserMessage = "a"
			return current, nil
		})
	}()

	select {
	case <-enteredA:
	case <-time.After(time.Second):
		t.Fatal("first WithLock callback did not start")
	}

	enteredB := make(chan struct{})
	doneB := make(chan error, 1)
	go func() {
		doneB <- store.WithLock(context.Background(), keyB, func(current *WorkflowSnapshot) (*WorkflowSnapshot, error) {
			close(enteredB)
			if current == nil {
				current = &WorkflowSnapshot{ID: "wf-b", Type: WorkflowSubscriptionStart, State: WorkflowCollectScope, ExpiresAt: now.Add(time.Minute)}
			}
			current.LastUserMessage = "b"
			return current, nil
		})
	}()

	blockedDifferentKey := false
	select {
	case <-enteredB:
	case <-time.After(200 * time.Millisecond):
		blockedDifferentKey = true
	}

	close(releaseA)
	for name, done := range map[string]<-chan error{"keyA": doneA, "keyB": doneB} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s WithLock() error = %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s WithLock() did not finish", name)
		}
	}
	if blockedDifferentKey {
		t.Fatalf("WithLock for %v was blocked by an active lock for %v; locks must be isolated by workflow key", keyB, keyA)
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

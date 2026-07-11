package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"schedule_server/internal/agent"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAgentWorkflowStoreJSONVersionAndTTLRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 11, 10, 0, 0, 123456000, time.UTC)
	store := newAgentWorkflowStore(openAgentWorkflowStoreTestDB(t), func() time.Time { return now })
	key := agent.WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	snapshot := workflowStoreSnapshot(key, now.Add(45*time.Minute))

	created, err := store.Create(context.Background(), key, snapshot)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Version != 1 || created.Snapshot.Version != 1 {
		t.Fatalf("created versions = %d/%d", created.Version, created.Snapshot.Version)
	}

	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.Version != 1 || !loaded.Snapshot.ExpiresAt.Equal(snapshot.ExpiresAt) {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Snapshot.Candidates["dept_ids"][0].Label != "信工" || loaded.Snapshot.TrustedEntities["scope"].Value != "department" {
		t.Fatalf("JSON round trip lost fields: %+v", loaded.Snapshot)
	}

	loaded.Snapshot.Candidates["dept_ids"][0].Label = "mutated"
	reloaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Snapshot.Candidates["dept_ids"][0].Label != "信工" || reloaded.Version != 1 {
		t.Fatalf("load clone/version isolation failed: %+v", reloaded)
	}
}

func TestNewAgentWorkflowStoreReturnsNilWithoutRepository(t *testing.T) {
	if got := newAgentWorkflowStore(nil, nil); got != nil {
		t.Fatalf("newAgentWorkflowStore(nil) = %T, want nil for NewAgent memory fallback", got)
	}
}

func TestAgentWorkflowStoreCreateConflictExpiredRecreateAndCAS(t *testing.T) {
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	store := newAgentWorkflowStore(openAgentWorkflowStoreTestDB(t), func() time.Time { return now })
	key := agent.WorkflowKey{TenantID: 42, ConversationID: "conv-a", ActorUserID: 7}
	snapshot := workflowStoreSnapshot(key, now.Add(time.Minute))
	created, err := store.Create(context.Background(), key, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, snapshot); !errors.Is(err, agent.ErrWorkflowConflict) {
		t.Fatalf("Create(existing) error = %v", err)
	}

	next := workflowStoreSnapshot(key, now.Add(2*time.Minute))
	next.State = agent.WorkflowReady
	createdAt := created.Snapshot.CreatedAt
	now = now.Add(10 * time.Second)
	updated, err := store.CompareAndSwap(context.Background(), key, created.Version, next)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 || updated.Snapshot.Version != 2 || updated.Snapshot.State != agent.WorkflowReady {
		t.Fatalf("updated = %+v", updated)
	}
	if !updated.Snapshot.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %v, want preserved %v", updated.Snapshot.CreatedAt, createdAt)
	}
	if _, err := store.CompareAndSwap(context.Background(), key, created.Version, next); !errors.Is(err, agent.ErrWorkflowConflict) {
		t.Fatalf("stale CAS error = %v", err)
	}

	now = now.Add(3 * time.Minute)
	recreated, err := store.Create(context.Background(), key, workflowStoreSnapshot(key, now.Add(time.Minute)))
	if err != nil {
		t.Fatalf("Create(expired) error = %v", err)
	}
	if recreated.Version != 1 || recreated.Snapshot.Version != 1 {
		t.Fatalf("recreated versions = %d/%d", recreated.Version, recreated.Snapshot.Version)
	}
}

func workflowStoreSnapshot(key agent.WorkflowKey, expiresAt time.Time) *agent.WorkflowSnapshot {
	return &agent.WorkflowSnapshot{
		ID:             "wf-1",
		TenantID:       key.TenantID,
		ConversationID: key.ConversationID,
		ActorUserID:    key.ActorUserID,
		Type:           agent.WorkflowSubscriptionStart,
		State:          agent.WorkflowCollectDepartments,
		MissingFields:  []string{"dept_ids"},
		Candidates: map[string][]agent.Candidate{
			"dept_ids": {{ID: "101", Label: "信工", Value: float64(101), TenantID: key.TenantID}},
		},
		TrustedEntities: map[string]agent.TrustedEntity{
			"scope": {ID: "scope", Value: "department", TenantID: key.TenantID},
		},
		ExpiresAt: expiresAt,
	}
}

func openAgentWorkflowStoreTestDB(t *testing.T) repository.AgentWorkflowRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Use(repository.NewTenantScopePlugin()); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AgentWorkflow{}); err != nil {
		t.Fatal(err)
	}
	return repository.NewAgentWorkflowRepository(db)
}

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"schedule_server/internal/agent"
	"schedule_server/internal/model"
	"schedule_server/internal/tenantctx"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAgentWorkflowConflictErrorMapsKnownDuplicateKeys(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "gorm duplicate", err: gorm.ErrDuplicatedKey, want: agent.ErrWorkflowConflict},
		{name: "mysql 1062", err: &mysqlDriver.MySQLError{Number: 1062, Message: "duplicate"}, want: agent.ErrWorkflowConflict},
		{name: "other mysql", err: &mysqlDriver.MySQLError{Number: 1205, Message: "lock wait"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentWorkflowConflictError(tt.err)
			if tt.want != nil && !errors.Is(got, tt.want) {
				t.Fatalf("agentWorkflowConflictError() = %v, want %v", got, tt.want)
			}
			if tt.want == nil && !errors.Is(got, tt.err) {
				t.Fatalf("agentWorkflowConflictError() = %v, want original %v", got, tt.err)
			}
		})
	}
}

func TestAgentWorkflowRepositoryCreateLoadAndTenantIsolation(t *testing.T) {
	db := openAgentWorkflowRepoTestDB(t)
	repo := NewAgentWorkflowRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)

	first := agentWorkflowRow(1, "conv", 7, now.Add(time.Hour))
	if err := repo.Create(ctx, first, now); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second := agentWorkflowRow(2, "conv", 7, now.Add(time.Hour))
	second.WorkflowID = "wf-other-tenant"
	if err := repo.Create(ctx, second, now); err != nil {
		t.Fatalf("Create(other tenant) error = %v", err)
	}

	got, err := repo.Load(ctx, agent.WorkflowKey{TenantID: 1, ConversationID: "conv", ActorUserID: 7}, now)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got == nil || got.WorkflowID != "wf-1" || got.Version != 1 {
		t.Fatalf("Load() = %+v", got)
	}
}

func TestAgentWorkflowRepositoryCreateConflictsWithExistingAndRecreatesExpired(t *testing.T) {
	db := openAgentWorkflowRepoTestDB(t)
	repo := NewAgentWorkflowRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	key := agent.WorkflowKey{TenantID: 1, ConversationID: "conv", ActorUserID: 7}

	if err := repo.Create(ctx, agentWorkflowRow(1, "conv", 7, now.Add(time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, agentWorkflowRow(1, "conv", 7, now.Add(2*time.Hour)), now); !errors.Is(err, agent.ErrWorkflowConflict) {
		t.Fatalf("Create(existing) error = %v, want conflict", err)
	}

	if err := db.WithContext(tenantctx.WithSkipTenantScope(ctx)).Model(&model.AgentWorkflow{}).
		Where("tenant_id = ? AND conversation_id = ? AND actor_user_id = ?", 1, "conv", 7).
		Update("expires_at", now.Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	replacement := agentWorkflowRow(1, "conv", 7, now.Add(3*time.Hour))
	replacement.WorkflowID = "wf-recreated"
	if err := repo.Create(ctx, replacement, now); err != nil {
		t.Fatalf("Create(expired replacement) error = %v", err)
	}
	got, err := repo.Load(ctx, key, now)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.WorkflowID != "wf-recreated" || got.Version != 1 {
		t.Fatalf("recreated row = %+v", got)
	}
}

func TestAgentWorkflowRepositoryCompareAndSwapAndDelete(t *testing.T) {
	db := openAgentWorkflowRepoTestDB(t)
	repo := NewAgentWorkflowRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	key := agent.WorkflowKey{TenantID: 1, ConversationID: "conv", ActorUserID: 7}
	row := agentWorkflowRow(1, "conv", 7, now.Add(time.Hour))
	if err := repo.Create(ctx, row, now); err != nil {
		t.Fatal(err)
	}

	next := *row
	next.State = string(agent.WorkflowReady)
	next.SnapshotJSON = `{"state":"ready"}`
	next.ExpiresAt = now.Add(2 * time.Hour)
	if err := repo.CompareAndSwap(ctx, key, 1, &next, now); err != nil {
		t.Fatalf("CompareAndSwap() error = %v", err)
	}
	if err := repo.CompareAndSwap(ctx, key, 1, &next, now); !errors.Is(err, agent.ErrWorkflowConflict) {
		t.Fatalf("stale CompareAndSwap() error = %v, want conflict", err)
	}
	got, err := repo.Load(ctx, key, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.State != string(agent.WorkflowReady) || got.SnapshotJSON != `{"state":"ready"}` || !got.ExpiresAt.Equal(next.ExpiresAt) {
		t.Fatalf("CAS row = %+v", got)
	}

	if err := repo.DeleteIfVersion(ctx, key, 1); !errors.Is(err, agent.ErrWorkflowConflict) {
		t.Fatalf("stale DeleteIfVersion() error = %v", err)
	}
	if err := repo.DeleteIfVersion(ctx, key, 2); err != nil {
		t.Fatalf("DeleteIfVersion() error = %v", err)
	}
	got, err = repo.Load(ctx, key, now)
	if err != nil || got != nil {
		t.Fatalf("Load(after delete) = %+v, %v", got, err)
	}
}

func TestAgentWorkflowRepositoryCompareAndSwapRejectsExpiredWorkflow(t *testing.T) {
	db := openAgentWorkflowRepoTestDB(t)
	repo := NewAgentWorkflowRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	key := agent.WorkflowKey{TenantID: 1, ConversationID: "conv", ActorUserID: 7}
	row := agentWorkflowRow(1, "conv", 7, now.Add(-time.Second))
	if err := repo.Create(ctx, row, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	next := *row
	next.State = string(agent.WorkflowReady)
	next.ExpiresAt = now.Add(time.Hour)
	if err := repo.CompareAndSwap(ctx, key, 1, &next, now); !errors.Is(err, agent.ErrWorkflowConflict) {
		t.Fatalf("CompareAndSwap(expired) error = %v, want conflict", err)
	}

	var persisted model.AgentWorkflow
	if err := db.WithContext(tenantctx.WithSkipTenantScope(ctx)).
		Where("tenant_id = ? AND conversation_id = ? AND actor_user_id = ?", key.TenantID, key.ConversationID, key.ActorUserID).
		First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Version != 1 || persisted.State != string(agent.WorkflowCollectScope) || !persisted.ExpiresAt.Before(now) {
		t.Fatalf("expired workflow was revived: %+v", persisted)
	}
}

func agentWorkflowRow(tenantID uint, conversationID string, actorUserID uint, expiresAt time.Time) *model.AgentWorkflow {
	return &model.AgentWorkflow{
		TenantID:       tenantID,
		ConversationID: conversationID,
		ActorUserID:    actorUserID,
		WorkflowID:     "wf-1",
		WorkflowType:   string(agent.WorkflowSubscriptionStart),
		State:          string(agent.WorkflowCollectScope),
		Version:        1,
		SnapshotJSON:   `{"id":"wf-1"}`,
		ExpiresAt:      expiresAt,
	}
}

func openAgentWorkflowRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Use(NewTenantScopePlugin()); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AgentWorkflow{}); err != nil {
		t.Fatal(err)
	}
	return db
}

package repository

import (
	"context"
	"testing"

	"schedule_server/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAgentOperationExecutionRepositoryFindSucceededIsTenantScoped(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:agent-operation-ledger?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AgentOperationExecution{}); err != nil {
		t.Fatal(err)
	}
	row := model.AgentOperationExecution{TenantID: 1, BusinessKey: "key", ConversationID: "conv", Operation: "subscription.start", Status: model.AgentOperationStatusSucceeded, WriteEffect: model.AgentWriteEffectCreated, ResultJSON: `{}`}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewAgentOperationExecutionRepository(db)
	got, err := repo.FindSucceeded(context.Background(), 1, "key")
	if err != nil || got == nil || got.ID != row.ID {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	other, err := repo.FindSucceeded(context.Background(), 2, "key")
	if err != nil || other != nil {
		t.Fatalf("other=%+v err=%v", other, err)
	}
}

func TestAgentOperationExecutionRepositoryFindSucceededReturnsDatabaseError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:agent-operation-ledger-error?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := NewAgentOperationExecutionRepository(db).FindSucceeded(context.Background(), 1, "key")
	if err == nil || got != nil {
		t.Fatalf("got=%+v err=%v, want database error", got, err)
	}
}

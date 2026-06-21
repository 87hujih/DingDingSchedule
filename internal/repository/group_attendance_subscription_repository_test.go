package repository

import (
	"context"
	"testing"

	"schedule_server/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGroupAttendanceSubscriptionRepositoryListPushEnabledByTenantID(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()

	enabled := model.GroupAttendanceSubscription{TenantID: 1, ConversationID: "enabled", GroupName: "enabled"}
	disabled := model.GroupAttendanceSubscription{TenantID: 1, ConversationID: "disabled", GroupName: "disabled"}
	otherTenant := model.GroupAttendanceSubscription{TenantID: 2, ConversationID: "other", GroupName: "other"}
	deleted := model.GroupAttendanceSubscription{TenantID: 1, ConversationID: "deleted", GroupName: "deleted"}
	if err := db.Create(&[]model.GroupAttendanceSubscription{enabled, disabled, otherTenant, deleted}).Error; err != nil {
		t.Fatalf("create subscriptions: %v", err)
	}
	if err := db.Model(&model.GroupAttendanceSubscription{}).
		Where("tenant_id = ? AND conversation_id = ?", 1, "disabled").
		Update("push_enabled", false).Error; err != nil {
		t.Fatalf("disable subscription: %v", err)
	}
	if err := db.Where("conversation_id = ?", "deleted").Delete(&model.GroupAttendanceSubscription{}).Error; err != nil {
		t.Fatalf("soft delete subscription: %v", err)
	}

	subs, err := repo.ListPushEnabledByTenantID(ctx, 1)
	if err != nil {
		t.Fatalf("ListPushEnabledByTenantID() error = %v", err)
	}
	if len(subs) != 1 || subs[0].ConversationID != "enabled" {
		t.Fatalf("subscriptions = %+v, want only enabled", subs)
	}
}

func TestGroupAttendanceSubscriptionRepositoryUpsertPreservesDisabledPushSwitch(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()

	if err := repo.Upsert(ctx, &model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-1",
		GroupName:      "old",
		EnabledByUID:   10,
		DeptIDsJSON:    "[101]",
	}); err != nil {
		t.Fatalf("initial Upsert() error = %v", err)
	}
	if err := db.Model(&model.GroupAttendanceSubscription{}).
		Where("tenant_id = ? AND conversation_id = ?", 1, "conv-1").
		Update("push_enabled", false).Error; err != nil {
		t.Fatalf("disable push: %v", err)
	}

	if err := repo.Upsert(ctx, &model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-1",
		GroupName:      "new",
		EnabledByUID:   11,
		DeptIDsJSON:    "[102]",
	}); err != nil {
		t.Fatalf("second Upsert() error = %v", err)
	}

	var sub model.GroupAttendanceSubscription
	if err := db.Where("tenant_id = ? AND conversation_id = ?", 1, "conv-1").First(&sub).Error; err != nil {
		t.Fatalf("load subscription: %v", err)
	}
	if sub.PushEnabled {
		t.Fatalf("PushEnabled = true, want false")
	}
	if sub.GroupName != "new" || sub.EnabledByUID != 11 || sub.DeptIDsJSON != "[102]" {
		t.Fatalf("metadata was not updated: %+v", sub)
	}
}

func TestGroupAttendanceSubscriptionRepositoryUpsertResurrectsWithoutResettingDisabledPushSwitch(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()

	if err := repo.Upsert(ctx, &model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-1",
		GroupName:      "old",
		EnabledByUID:   10,
	}); err != nil {
		t.Fatalf("initial Upsert() error = %v", err)
	}
	if err := db.Model(&model.GroupAttendanceSubscription{}).
		Where("tenant_id = ? AND conversation_id = ?", 1, "conv-1").
		Update("push_enabled", false).Error; err != nil {
		t.Fatalf("disable push: %v", err)
	}
	if err := repo.SoftDelete(ctx, 1, "conv-1"); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}
	if err := repo.Upsert(ctx, &model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-1",
		GroupName:      "new",
		EnabledByUID:   11,
	}); err != nil {
		t.Fatalf("resubscribe Upsert() error = %v", err)
	}

	var sub model.GroupAttendanceSubscription
	if err := db.Unscoped().Where("tenant_id = ? AND conversation_id = ?", 1, "conv-1").First(&sub).Error; err != nil {
		t.Fatalf("load subscription: %v", err)
	}
	if sub.DeletedAt.Valid {
		t.Fatalf("DeletedAt is still valid after resubscribe")
	}
	if sub.PushEnabled {
		t.Fatalf("PushEnabled = true, want false")
	}
}

func openGroupSubRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:group-sub-repository-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.Migrator().DropTable(&model.GroupAttendanceSubscription{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if err := db.AutoMigrate(&model.GroupAttendanceSubscription{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

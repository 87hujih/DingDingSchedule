package repository

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"schedule_server/internal/model"
	"schedule_server/internal/tenantctx"

	mysqlDriver "gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGroupAttendanceSubscriptionRepositoryMySQLConcurrentStart(t *testing.T) {
	dsn := os.Getenv("AGENT_WORKFLOW_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set AGENT_WORKFLOW_MYSQL_DSN to run MySQL subscription concurrency integration")
	}
	dbOne, err := gorm.Open(mysqlDriver.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	dbTwo, err := gorm.Open(mysqlDriver.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := dbOne.AutoMigrate(&model.GroupAttendanceSubscription{}, &model.AgentWriteLedger{}); err != nil {
		t.Fatal(err)
	}
	tenantID := uint(time.Now().UnixNano()%1_000_000 + 100_000)
	conversationID := fmt.Sprintf("agent-p0-concurrent-%d", time.Now().UnixNano())
	ctx := tenantctx.WithTenantID(context.Background(), tenantID)
	key := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	defer func() {
		_ = dbOne.WithContext(ctx).Unscoped().
			Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID).
			Delete(&model.GroupAttendanceSubscription{}).Error
		_ = dbOne.WithContext(ctx).
			Where("tenant_id = ? AND business_key = ?", tenantID, key).
			Delete(&model.AgentWriteLedger{}).Error
	}()

	repos := []GroupAttendanceSubscriptionRepository{
		NewGroupAttendanceSubscriptionRepository(dbOne),
		NewGroupAttendanceSubscriptionRepository(dbTwo),
	}
	effects := make(chan GroupSubscriptionWriteEffect, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, repo := range repos {
		wg.Add(1)
		go func(candidate GroupAttendanceSubscriptionRepository) {
			defer wg.Done()
			result, err := candidate.ApplyStart(ctx, &model.GroupAttendanceSubscription{
				TenantID:       tenantID,
				ConversationID: conversationID,
				DeptIDsJSON:    "[9,2,9]",
			}, key)
			if err != nil {
				errs <- err
				return
			}
			effects <- result.Effect
		}(repo)
	}
	wg.Wait()
	close(errs)
	close(effects)
	for err := range errs {
		t.Errorf("ApplyStart() concurrent error = %v", err)
	}
	counts := map[GroupSubscriptionWriteEffect]int{}
	for effect := range effects {
		counts[effect]++
	}
	if counts[GroupSubscriptionCreated] != 1 || counts[GroupSubscriptionNoOp] != 1 {
		t.Fatalf("concurrent effects = %+v, want one created and one no_op", counts)
	}
}

func TestGroupAttendanceSubscriptionRepositoryApplyReturnsStableEffects(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	const (
		startBusinessKey  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		cancelBusinessKey = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	created, err := repo.ApplyStart(ctx, &model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-effect",
		GroupName:      "group",
		EnabledByUID:   10,
		DeptIDsJSON:    "[9,2,9]",
	}, startBusinessKey)
	if err != nil {
		t.Fatalf("ApplyStart(created) error = %v", err)
	}
	if created.Effect != GroupSubscriptionCreated || created.Subscription == nil ||
		created.Subscription.DeptIDsJSON != "[2,9]" {
		t.Fatalf("created = %+v", created)
	}
	if err := db.Model(&model.GroupAttendanceSubscription{}).
		Where("tenant_id = ? AND conversation_id = ?", 1, "conv-effect").
		Update("push_enabled", false).Error; err != nil {
		t.Fatal(err)
	}

	noOp, err := repo.ApplyStart(ctx, &model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-effect",
		GroupName:      "ignored metadata",
		EnabledByUID:   99,
		DeptIDsJSON:    "[2,9]",
	}, startBusinessKey)
	if err != nil {
		t.Fatalf("ApplyStart(no-op) error = %v", err)
	}
	if noOp.Effect != GroupSubscriptionNoOp || noOp.Subscription == nil || noOp.Subscription.PushEnabled {
		t.Fatalf("no-op = %+v, want disabled switch preserved", noOp)
	}

	updated, err := repo.ApplyStart(ctx, &model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-effect",
		GroupName:      "updated",
		EnabledByUID:   11,
		DeptIDsJSON:    "[3]",
	}, startBusinessKey)
	if err != nil {
		t.Fatalf("ApplyStart(updated) error = %v", err)
	}
	if updated.Effect != GroupSubscriptionUpdated || updated.Subscription == nil || updated.Subscription.PushEnabled {
		t.Fatalf("updated = %+v, want disabled switch preserved", updated)
	}

	cancelled, err := repo.ApplyCancel(ctx, 1, "conv-effect", cancelBusinessKey)
	if err != nil || cancelled.Effect != GroupSubscriptionCancelled {
		t.Fatalf("ApplyCancel(cancelled) = %+v, %v", cancelled, err)
	}
	cancelNoOp, err := repo.ApplyCancel(ctx, 1, "conv-effect", cancelBusinessKey)
	if err != nil || cancelNoOp.Effect != GroupSubscriptionNoOp {
		t.Fatalf("ApplyCancel(no-op) = %+v, %v", cancelNoOp, err)
	}

	resurrected, err := repo.ApplyStart(ctx, &model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-effect",
		GroupName:      "resurrected",
		EnabledByUID:   12,
		DeptIDsJSON:    "[3]",
	}, startBusinessKey)
	if err != nil {
		t.Fatalf("ApplyStart(resurrected) error = %v", err)
	}
	if resurrected.Effect != GroupSubscriptionCreated ||
		resurrected.Subscription == nil ||
		resurrected.Subscription.PushEnabled {
		t.Fatalf("resurrected = %+v, want created with disabled switch preserved", resurrected)
	}
	var ledgers []model.AgentWriteLedger
	if err := db.Order("business_key ASC").Find(&ledgers).Error; err != nil {
		t.Fatal(err)
	}
	if len(ledgers) != 2 ||
		ledgers[0].BusinessKey != startBusinessKey ||
		ledgers[0].WriteEffect != string(GroupSubscriptionCreated) ||
		ledgers[1].BusinessKey != cancelBusinessKey ||
		ledgers[1].WriteEffect != string(GroupSubscriptionNoOp) {
		t.Fatalf("ledgers = %+v, want transactionally updated start/cancel effects", ledgers)
	}
}

func TestGroupAttendanceSubscriptionRepositoryApplyFailsClosedOnCorruptStoredScope(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	if err := db.Create(&model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-corrupt",
		DeptIDsJSON:    "{bad-json",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ApplyStart(ctx, &model.GroupAttendanceSubscription{
		TenantID:       1,
		ConversationID: "conv-corrupt",
		DeptIDsJSON:    "[2]",
	}, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("ApplyStart() error = nil, want corrupt stored scope failure")
	}
}

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
	if err := db.AutoMigrate(&model.GroupAttendanceSubscription{}, &model.AgentWriteLedger{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

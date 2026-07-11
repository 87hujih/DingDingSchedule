package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"schedule_server/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestExecuteSubscriptionStartPersistsMutationAndLedgerAndReplaysHistoricalResult(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()
	req := SubscriptionStartExecution{TenantID: 1, BusinessKey: "start-key", ConversationID: "conv-1", GroupName: "group", EnabledByUID: 10, DeptIDsJSON: "[101]"}
	first, err := repo.ExecuteSubscriptionStart(ctx, req)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if first.WriteEffect != model.AgentWriteEffectCreated || first.Status != model.AgentOperationStatusSucceeded {
		t.Fatalf("first = %+v", first)
	}
	req.GroupName, req.EnabledByUID, req.DeptIDsJSON = "changed", 99, "[999]"
	second, err := repo.ExecuteSubscriptionStart(ctx, req)
	if err != nil {
		t.Fatalf("replay execute: %v", err)
	}
	if second.ID != first.ID || second.WriteEffect != first.WriteEffect || second.ResultJSON != first.ResultJSON {
		t.Fatalf("replay = %+v, want historical %+v", second, first)
	}
	var sub model.GroupAttendanceSubscription
	if err := db.Where("tenant_id = ? AND conversation_id = ?", 1, "conv-1").First(&sub).Error; err != nil {
		t.Fatal(err)
	}
	if sub.GroupName != "group" || sub.DeptIDsJSON != "[101]" {
		t.Fatalf("replay mutated subscription: %+v", sub)
	}
}

func TestExecuteSubscriptionWriteEffects(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()
	created, err := repo.ExecuteSubscriptionStart(ctx, SubscriptionStartExecution{TenantID: 1, BusinessKey: "s1", ConversationID: "conv", DeptIDsJSON: "[1]"})
	if err != nil || created.WriteEffect != model.AgentWriteEffectCreated {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	noOp, err := repo.ExecuteSubscriptionStart(ctx, SubscriptionStartExecution{TenantID: 1, BusinessKey: "s2", ConversationID: "conv", DeptIDsJSON: "[1]"})
	if err != nil || noOp.WriteEffect != model.AgentWriteEffectNoOp {
		t.Fatalf("noOp=%+v err=%v", noOp, err)
	}
	updated, err := repo.ExecuteSubscriptionStart(ctx, SubscriptionStartExecution{TenantID: 1, BusinessKey: "s3", ConversationID: "conv", DeptIDsJSON: "[2]"})
	if err != nil || updated.WriteEffect != model.AgentWriteEffectUpdated {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	cancelled, err := repo.ExecuteSubscriptionCancel(ctx, SubscriptionCancelExecution{TenantID: 1, BusinessKey: "c1", ConversationID: "conv"})
	if err != nil || cancelled.WriteEffect != model.AgentWriteEffectCancelled {
		t.Fatalf("cancelled=%+v err=%v", cancelled, err)
	}
	cancelNoOp, err := repo.ExecuteSubscriptionCancel(ctx, SubscriptionCancelExecution{TenantID: 1, BusinessKey: "c2", ConversationID: "conv"})
	if err != nil || cancelNoOp.WriteEffect != model.AgentWriteEffectNoOp {
		t.Fatalf("cancelNoOp=%+v err=%v", cancelNoOp, err)
	}
}

func TestExecuteSubscriptionStartConcurrentSameBusinessKeyHasOneLogicalChange(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()
	req := SubscriptionStartExecution{TenantID: 1, BusinessKey: "same-key", ConversationID: "conv", GroupName: "group"}
	results := make([]*model.AgentOperationExecution, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = repo.ExecuteSubscriptionStart(ctx, req) }(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
	}
	if results[0].ID == 0 || results[0].ID != results[1].ID {
		t.Fatalf("results = %+v / %+v", results[0], results[1])
	}
	var ledgerCount, subCount int64
	if err := db.Model(&model.AgentOperationExecution{}).Count(&ledgerCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.GroupAttendanceSubscription{}).Count(&subCount).Error; err != nil {
		t.Fatal(err)
	}
	if ledgerCount != 1 || subCount != 1 {
		t.Fatalf("ledger=%d subscriptions=%d", ledgerCount, subCount)
	}
}

func TestExecuteSubscriptionStartRollsBackMutationWhenLedgerInsertFails(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	if err := db.Exec(`CREATE TRIGGER reject_agent_ledger BEFORE UPDATE ON agent_operation_executions BEGIN SELECT RAISE(FAIL, 'reject ledger'); END`).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewGroupAttendanceSubscriptionRepository(db)
	_, err := repo.ExecuteSubscriptionStart(context.Background(), SubscriptionStartExecution{TenantID: 1, BusinessKey: "rejected", ConversationID: "conv"})
	if err == nil {
		t.Fatal("ExecuteSubscriptionStart() error = nil")
	}
	var count int64
	if err := db.Model(&model.GroupAttendanceSubscription{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("subscription count = %d, mutation was not rolled back", count)
	}
}

func TestSubscriptionLifecycleStartCancelStartReclaimsStableStartKey(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()
	start := SubscriptionStartExecution{TenantID: 1, BusinessKey: "stable-start", ConversationID: "conv", DeptIDsJSON: "[101]"}
	first, err := repo.ExecuteSubscriptionStart(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExecuteSubscriptionCancel(ctx, SubscriptionCancelExecution{TenantID: 1, BusinessKey: "stable-cancel", ConversationID: "conv"}); err != nil {
		t.Fatal(err)
	}
	third, err := repo.ExecuteSubscriptionStart(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID || third.WriteEffect == model.AgentWriteEffectNoOp {
		t.Fatalf("third=%+v must be a newly executed mutation after first=%+v", third, first)
	}
	var active int64
	if err := db.Model(&model.GroupAttendanceSubscription{}).Where("tenant_id = ? AND conversation_id = ?", 1, "conv").Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active subscriptions = %d, want 1", active)
	}
}

func TestSubscriptionLifecycleCancelStartCancelReclaimsStableCancelKey(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()
	cancel := SubscriptionCancelExecution{TenantID: 1, BusinessKey: "stable-cancel", ConversationID: "conv"}
	first, err := repo.ExecuteSubscriptionCancel(ctx, cancel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExecuteSubscriptionStart(ctx, SubscriptionStartExecution{TenantID: 1, BusinessKey: "stable-start", ConversationID: "conv"}); err != nil {
		t.Fatal(err)
	}
	third, err := repo.ExecuteSubscriptionCancel(ctx, cancel)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID || third.WriteEffect != model.AgentWriteEffectCancelled {
		t.Fatalf("third=%+v must cancel after first=%+v", third, first)
	}
	var active int64
	if err := db.Model(&model.GroupAttendanceSubscription{}).Where("tenant_id = ? AND conversation_id = ?", 1, "conv").Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("active subscriptions = %d, want 0", active)
	}
}

func TestSubscriptionCancelInvalidatesAllStartLedgersForConversation(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()
	for _, start := range []SubscriptionStartExecution{
		{TenantID: 1, BusinessKey: "start-a", ConversationID: "conv", DeptIDsJSON: "[101]"},
		{TenantID: 1, BusinessKey: "start-b", ConversationID: "conv", DeptIDsJSON: "[102]"},
	} {
		if _, err := repo.ExecuteSubscriptionStart(ctx, start); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.ExecuteSubscriptionCancel(ctx, SubscriptionCancelExecution{TenantID: 1, BusinessKey: "cancel", ConversationID: "conv"}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.AgentOperationExecution{}).Where("tenant_id = ? AND conversation_id = ? AND operation = ?", 1, "conv", "subscription.start").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("start ledger count = %d, want 0", count)
	}
}

func TestSubscriptionStartDifferentScopeInvalidatesOlderStartLedger(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	ctx := context.Background()
	startA := SubscriptionStartExecution{TenantID: 1, BusinessKey: "start-a", ConversationID: "conv", DeptIDsJSON: "[101]"}
	first, err := repo.ExecuteSubscriptionStart(ctx, startA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExecuteSubscriptionStart(ctx, SubscriptionStartExecution{TenantID: 1, BusinessKey: "start-b", ConversationID: "conv", DeptIDsJSON: "[102]"}); err != nil {
		t.Fatal(err)
	}
	third, err := repo.ExecuteSubscriptionStart(ctx, startA)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID || third.WriteEffect != model.AgentWriteEffectUpdated {
		t.Fatalf("third=%+v must reapply dept A after first=%+v", third, first)
	}
	var sub model.GroupAttendanceSubscription
	if err := db.Where("tenant_id = ? AND conversation_id = ?", 1, "conv").First(&sub).Error; err != nil {
		t.Fatal(err)
	}
	if sub.DeptIDsJSON != "[101]" {
		t.Fatalf("DeptIDsJSON = %q, want [101]", sub.DeptIDsJSON)
	}
	var currentCount int64
	if err := db.Model(&model.AgentOperationExecution{}).Where("tenant_id = ? AND business_key = ?", 1, "start-a").Count(&currentCount).Error; err != nil {
		t.Fatal(err)
	}
	if currentCount != 1 {
		t.Fatalf("current start ledger count = %d, want 1", currentCount)
	}
}

func TestConcurrentDifferentSubscriptionStartsLeaveLedgerMatchingFinalScope(t *testing.T) {
	db := openGroupSubRepoTestDB(t)
	repo := NewGroupAttendanceSubscriptionRepository(db)
	requests := []SubscriptionStartExecution{
		{TenantID: 1, BusinessKey: "start-a", ConversationID: "conv", DeptIDsJSON: "[101]"},
		{TenantID: 1, BusinessKey: "start-b", ConversationID: "conv", DeptIDsJSON: "[102]"},
	}
	errs := make([]error, len(requests))
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = repo.ExecuteSubscriptionStart(context.Background(), requests[i])
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	var sub model.GroupAttendanceSubscription
	if err := db.Where("tenant_id = ? AND conversation_id = ?", 1, "conv").First(&sub).Error; err != nil {
		t.Fatal(err)
	}
	var ledgers []model.AgentOperationExecution
	if err := db.Where("tenant_id = ? AND conversation_id = ? AND operation = ?", 1, "conv", "subscription.start").Find(&ledgers).Error; err != nil {
		t.Fatal(err)
	}
	if len(ledgers) != 1 {
		t.Fatalf("start ledgers = %+v, want exactly one", ledgers)
	}
	wantDept := map[string]string{"start-a": "[101]", "start-b": "[102]"}[ledgers[0].BusinessKey]
	if wantDept == "" || sub.DeptIDsJSON != wantDept {
		t.Fatalf("ledger=%q subscription scope=%q, want matching scope %q", ledgers[0].BusinessKey, sub.DeptIDsJSON, wantDept)
	}
}

func TestWaitSubscriptionRetryReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitSubscriptionRetry(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
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
	if err := db.Migrator().DropTable(&model.AgentOperationExecution{}, &model.GroupAttendanceSubscription{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if err := db.AutoMigrate(&model.GroupAttendanceSubscription{}, &model.AgentOperationExecution{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"schedule_server/internal/model"
	"schedule_server/internal/tenantctx"

	gormMysql "gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAgentWorkflowRepositoryCreateConflict(t *testing.T) {
	repo, _ := newAgentWorkflowSQLiteRepository(t)
	ctx := tenantctx.WithTenantID(context.Background(), 11)
	workflow := testAgentWorkflow(11, "conversation-create", 21)

	if err := repo.Create(ctx, workflow); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	duplicate := testAgentWorkflow(11, "conversation-create", 21)
	if err := repo.Create(ctx, duplicate); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("duplicate Create() error = %v, want conflict", err)
	}

	got, err := repo.Load(ctx, AgentWorkflowKey{
		TenantID:       11,
		ConversationID: "conversation-create",
		ActorUserID:    21,
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got == nil || got.Version != 1 || got.ExecutionStatus != AgentWorkflowExecutionIdle {
		t.Fatalf("Load() = %#v, want version 1 idle workflow", got)
	}
}

func TestAgentWorkflowRepositoryCompareAndSwap(t *testing.T) {
	repo, _ := newAgentWorkflowSQLiteRepository(t)
	ctx := tenantctx.WithTenantID(context.Background(), 12)
	workflow := testAgentWorkflow(12, "conversation-cas", 22)
	key := keyFromWorkflow(workflow)
	if err := repo.Create(ctx, workflow); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	next := testSnapshotUpdate("collect_departments", `{"state":"collect_departments"}`)
	if err := repo.CompareAndSwap(ctx, key, 2, next); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("stale CompareAndSwap() error = %v, want conflict", err)
	}
	if err := repo.CompareAndSwap(ctx, key, 1, next); err != nil {
		t.Fatalf("CompareAndSwap() error = %v", err)
	}

	got, err := repo.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Version != 2 || got.WorkflowState != "collect_departments" ||
		got.SnapshotJSON != `{"state":"collect_departments"}` {
		t.Fatalf("Load() = %#v, want committed CAS values", got)
	}
}

func TestAgentWorkflowRepositoryDeleteIfVersion(t *testing.T) {
	repo, _ := newAgentWorkflowSQLiteRepository(t)
	ctx := tenantctx.WithTenantID(context.Background(), 13)
	workflow := testAgentWorkflow(13, "conversation-delete", 23)
	key := keyFromWorkflow(workflow)
	if err := repo.Create(ctx, workflow); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := repo.DeleteIfVersion(ctx, key, 2); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("stale DeleteIfVersion() error = %v, want conflict", err)
	}
	if err := repo.DeleteIfVersion(ctx, key, 1); err != nil {
		t.Fatalf("DeleteIfVersion() error = %v", err)
	}
	got, err := repo.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != nil {
		t.Fatalf("Load() = %#v, want deleted row", got)
	}
}

func TestAgentWorkflowRepositoryExecutionLifecycle(t *testing.T) {
	repo, _ := newAgentWorkflowSQLiteRepository(t)
	ctx := tenantctx.WithTenantID(context.Background(), 14)
	now := time.Now().UTC().Truncate(time.Millisecond)
	workflow := testAgentWorkflow(14, "conversation-execution", 24)
	key := keyFromWorkflow(workflow)
	reservation := testReservation("token-1", now.Add(time.Minute))

	if err := repo.CreateReservedExecution(ctx, workflow, reservation); err != nil {
		t.Fatalf("CreateReservedExecution() error = %v", err)
	}
	if err := repo.CompareAndSwap(
		ctx,
		key,
		1,
		testSnapshotUpdate("blocked", `{"state":"blocked"}`),
	); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("CompareAndSwap() during execution error = %v, want conflict", err)
	}
	if err := repo.RecordExecutionResult(
		ctx,
		key,
		1,
		"wrong-token",
		testExecutionResult(),
	); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("RecordExecutionResult() wrong token error = %v, want conflict", err)
	}
	wrongBusinessKey := testExecutionResult()
	wrongBusinessKey.BusinessKey = "wrong-business-key"
	if err := repo.RecordExecutionResult(
		ctx,
		key,
		1,
		"token-1",
		wrongBusinessKey,
	); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("RecordExecutionResult() wrong business key error = %v, want conflict", err)
	}
	if err := repo.RecordExecutionResult(
		ctx,
		key,
		1,
		"token-1",
		testExecutionResult(),
	); err != nil {
		t.Fatalf("RecordExecutionResult() error = %v", err)
	}

	recorded, err := repo.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load() result-recorded error = %v", err)
	}
	if recorded.Version != 2 ||
		recorded.ExecutionStatus != AgentWorkflowExecutionResultRecorded ||
		recorded.ExecutionResultJSON == nil ||
		*recorded.ExecutionResultJSON != `{"effect":"created"}` {
		t.Fatalf("Load() = %#v, want recorded execution result", recorded)
	}

	final := testSnapshotUpdate("completed", `{"state":"completed"}`)
	if err := repo.FinalizeExecution(ctx, key, 1, "token-1", final); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("stale FinalizeExecution() error = %v, want conflict", err)
	}
	if err := repo.FinalizeExecution(ctx, key, 2, "token-1", final); err != nil {
		t.Fatalf("FinalizeExecution() error = %v", err)
	}

	finalized, err := repo.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load() finalized error = %v", err)
	}
	if finalized.Version != 3 ||
		finalized.WorkflowState != "completed" ||
		finalized.ExecutionStatus != AgentWorkflowExecutionIdle ||
		finalized.ExecutionToken != nil ||
		finalized.ExecutionRequestJSON != nil ||
		finalized.ExecutionResultJSON != nil ||
		finalized.LeaseExpiresAt != nil {
		t.Fatalf("Load() = %#v, want finalized idle workflow", finalized)
	}
}

func TestAgentWorkflowRepositoryReserveTakeoverAndDelete(t *testing.T) {
	repo, _ := newAgentWorkflowSQLiteRepository(t)
	ctx := tenantctx.WithTenantID(context.Background(), 15)
	now := time.Now().UTC().Truncate(time.Millisecond)
	workflow := testAgentWorkflow(15, "conversation-takeover", 25)
	key := keyFromWorkflow(workflow)
	if err := repo.Create(ctx, workflow); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := repo.ReserveExecution(
		ctx,
		key,
		1,
		testSnapshotUpdate("confirming", `{"state":"confirming"}`),
		testReservation("token-old", now.Add(time.Minute)),
	); err != nil {
		t.Fatalf("ReserveExecution() error = %v", err)
	}

	nextReservation := testReservation("token-new", now.Add(2*time.Minute))
	if err := repo.TakeoverExpiredExecution(
		ctx,
		key,
		2,
		"token-old",
		now,
		nextReservation,
	); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("fresh TakeoverExpiredExecution() error = %v, want conflict", err)
	}
	if err := repo.TakeoverExpiredExecution(
		ctx,
		key,
		2,
		"token-old",
		now.Add(time.Minute),
		nextReservation,
	); err != nil {
		t.Fatalf("expired TakeoverExpiredExecution() error = %v", err)
	}
	if err := repo.RecordExecutionResult(
		ctx,
		key,
		3,
		"token-old",
		testExecutionResult(),
	); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("old token RecordExecutionResult() error = %v, want conflict", err)
	}
	if err := repo.DeleteReservedExecution(ctx, key, 3, "token-old"); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("old token DeleteReservedExecution() error = %v, want conflict", err)
	}
	if err := repo.DeleteReservedExecution(ctx, key, 3, "token-new"); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("executing DeleteReservedExecution() error = %v, want conflict", err)
	}
	if err := repo.RecordExecutionResult(ctx, key, 3, "token-new", testExecutionResult()); err != nil {
		t.Fatalf("RecordExecutionResult() error = %v", err)
	}
	if err := repo.DeleteReservedExecution(ctx, key, 4, "token-new"); err != nil {
		t.Fatalf("DeleteReservedExecution() error = %v", err)
	}
	got, err := repo.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != nil {
		t.Fatalf("Load() = %#v, want deleted reserved workflow", got)
	}
}

func TestAgentWorkflowRepositoryTenantIsolation(t *testing.T) {
	repo, _ := newAgentWorkflowSQLiteRepository(t)
	ctxOne := tenantctx.WithTenantID(context.Background(), 16)
	ctxTwo := tenantctx.WithTenantID(context.Background(), 17)
	one := testAgentWorkflow(16, "same-conversation", 26)
	two := testAgentWorkflow(17, "same-conversation", 26)

	if err := repo.Create(ctxOne, one); err != nil {
		t.Fatalf("tenant one Create() error = %v", err)
	}
	if err := repo.Create(ctxTwo, two); err != nil {
		t.Fatalf("tenant two Create() error = %v", err)
	}
	if _, err := repo.Load(context.Background(), keyFromWorkflow(one)); !errors.Is(err, ErrTenantMissing) {
		t.Fatalf("missing-tenant Load() error = %v, want ErrTenantMissing", err)
	}
	if _, err := repo.Load(ctxTwo, keyFromWorkflow(one)); !errors.Is(err, ErrAgentWorkflowTenantMismatch) {
		t.Fatalf("cross-tenant Load() error = %v, want tenant mismatch", err)
	}

	gotOne, err := repo.Load(ctxOne, keyFromWorkflow(one))
	if err != nil {
		t.Fatalf("tenant one Load() error = %v", err)
	}
	gotTwo, err := repo.Load(ctxTwo, keyFromWorkflow(two))
	if err != nil {
		t.Fatalf("tenant two Load() error = %v", err)
	}
	if gotOne == nil || gotTwo == nil || gotOne.ID == gotTwo.ID {
		t.Fatalf("tenant rows = %#v / %#v, want separate records", gotOne, gotTwo)
	}
}

func TestAgentWorkflowRepositoryListsExpiredAndRecordedExecutionsAcrossTenants(t *testing.T) {
	repo, _ := newAgentWorkflowSQLiteRepository(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	expired := testAgentWorkflow(31, "conversation-expired", 41)
	if err := repo.CreateReservedExecution(
		tenantctx.WithTenantID(context.Background(), 31),
		expired,
		testReservation("token-expired", now.Add(-time.Minute)),
	); err != nil {
		t.Fatal(err)
	}
	fresh := testAgentWorkflow(32, "conversation-fresh", 42)
	if err := repo.CreateReservedExecution(
		tenantctx.WithTenantID(context.Background(), 32),
		fresh,
		testReservation("token-fresh", now.Add(time.Minute)),
	); err != nil {
		t.Fatal(err)
	}
	recorded := testAgentWorkflow(33, "conversation-recorded", 43)
	recordedCtx := tenantctx.WithTenantID(context.Background(), 33)
	if err := repo.CreateReservedExecution(
		recordedCtx,
		recorded,
		testReservation("token-recorded", now.Add(time.Minute)),
	); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordExecutionResult(
		recordedCtx,
		keyFromWorkflow(recorded),
		1,
		"token-recorded",
		testExecutionResult(),
	); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListRecoverableExecutions(context.Background(), now, 10)
	if err != nil {
		t.Fatalf("ListRecoverableExecutions() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("recoverable rows = %+v, want expired and recorded only", got)
	}
	seen := map[string]bool{}
	for _, row := range got {
		seen[row.ConversationID] = true
	}
	if !seen["conversation-expired"] || !seen["conversation-recorded"] || seen["conversation-fresh"] {
		t.Fatalf("recoverable conversations = %+v", seen)
	}
}

func TestAgentWorkflowRepositoryMarksRecoveryRequiredWithFencing(t *testing.T) {
	repo, _ := newAgentWorkflowSQLiteRepository(t)
	ctx := tenantctx.WithTenantID(context.Background(), 34)
	now := time.Now().UTC().Truncate(time.Millisecond)
	workflow := testAgentWorkflow(34, "conversation-recovery-required", 44)
	key := keyFromWorkflow(workflow)
	if err := repo.CreateReservedExecution(
		ctx,
		workflow,
		testReservation("token-recovery", now.Add(-time.Minute)),
	); err != nil {
		t.Fatal(err)
	}
	retryAt := now.Add(2 * time.Minute)
	if err := repo.MarkExecutionRecoveryRequired(
		ctx,
		key,
		1,
		"wrong-token",
		retryAt,
	); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("wrong token mark error = %v, want conflict", err)
	}
	if err := repo.MarkExecutionRecoveryRequired(
		ctx,
		key,
		1,
		"token-recovery",
		retryAt,
	); err != nil {
		t.Fatalf("MarkExecutionRecoveryRequired() error = %v", err)
	}
	got, err := repo.Load(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil ||
		got.Version != 2 ||
		got.ExecutionStatus != AgentWorkflowExecutionRecoveryRequired ||
		got.LeaseExpiresAt == nil ||
		!got.LeaseExpiresAt.Equal(retryAt) {
		t.Fatalf("recovery-required row = %+v", got)
	}
}

// TestAgentWorkflowRepositoryMySQLIntegration is opt-in because local and
// default CI environments do not necessarily provide MySQL 8. Set
// AGENT_WORKFLOW_MYSQL_DSN to a disposable integration schema to run it.
func TestAgentWorkflowRepositoryMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("AGENT_WORKFLOW_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENT_WORKFLOW_MYSQL_DSN is not set")
	}
	open := func() *gorm.DB {
		db, err := gorm.Open(gormMysql.Open(dsn), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		if err != nil {
			t.Fatalf("open MySQL: %v", err)
		}
		return db
	}
	dbOne := open()
	if err := dbOne.AutoMigrate(&model.AgentWorkflow{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := dbOne.Use(NewTenantScopePlugin()); err != nil {
		t.Fatalf("register tenant plugin on first connection: %v", err)
	}
	dbTwo := open()
	if err := dbTwo.Use(NewTenantScopePlugin()); err != nil {
		t.Fatalf("register tenant plugin on second connection: %v", err)
	}

	tenantID := uint(time.Now().UnixNano()%900_000_000) + 100_000_000
	conversationID := fmt.Sprintf("mysql-agent-workflow-%d", time.Now().UnixNano())
	ctx := tenantctx.WithTenantID(context.Background(), tenantID)
	workflow := testAgentWorkflow(tenantID, conversationID, 91)
	key := keyFromWorkflow(workflow)
	repoOne := NewAgentWorkflowRepository(dbOne)
	repoTwo := NewAgentWorkflowRepository(dbTwo)
	t.Cleanup(func() {
		_ = dbOne.WithContext(ctx).
			Where(workflowKeyWhere(), key.TenantID, key.ConversationID, key.ActorUserID).
			Delete(&model.AgentWorkflow{}).Error
	})

	if err := repoOne.Create(ctx, workflow); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := repoTwo.Create(ctx, testAgentWorkflow(tenantID, conversationID, 91)); !errors.Is(err, ErrAgentWorkflowConflict) {
		t.Fatalf("duplicate Create() error = %v, want conflict", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, repo := range []AgentWorkflowRepository{repoOne, repoTwo} {
		wg.Add(1)
		go func(index int, candidate AgentWorkflowRepository) {
			defer wg.Done()
			<-start
			errs <- candidate.CompareAndSwap(
				ctx,
				key,
				1,
				testSnapshotUpdate(
					fmt.Sprintf("winner-%d", index),
					fmt.Sprintf(`{"winner":%d}`, index),
				),
			)
		}(i, repo)
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAgentWorkflowConflict):
			conflicts++
		default:
			t.Fatalf("concurrent CompareAndSwap() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS outcomes success=%d conflict=%d, want 1/1", successes, conflicts)
	}

	got, err := repoOne.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got == nil || got.Version != 2 {
		t.Fatalf("Load() = %#v, want version 2", got)
	}
}

func newAgentWorkflowSQLiteRepository(t *testing.T) (AgentWorkflowRepository, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:agent-workflow-%s?mode=memory&cache=shared&_busy_timeout=5000",
		t.Name(),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	if err := db.AutoMigrate(&model.AgentWorkflow{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := db.Use(NewTenantScopePlugin()); err != nil {
		t.Fatalf("register tenant plugin: %v", err)
	}
	return NewAgentWorkflowRepository(db), db
}

func testAgentWorkflow(tenantID uint, conversationID string, actorUserID uint) *model.AgentWorkflow {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &model.AgentWorkflow{
		TenantID:              tenantID,
		ConversationID:        conversationID,
		ActorUserID:           actorUserID,
		WorkflowID:            "workflow-1",
		WorkflowType:          "subscription",
		WorkflowState:         "collect_scope",
		SnapshotSchemaVersion: 1,
		SnapshotJSON:          `{"state":"collect_scope"}`,
		ExpiresAt:             now.Add(30 * time.Minute),
	}
}

func testSnapshotUpdate(state, snapshotJSON string) AgentWorkflowSnapshotUpdate {
	return AgentWorkflowSnapshotUpdate{
		WorkflowID:            "workflow-1",
		WorkflowType:          "subscription",
		WorkflowState:         state,
		SnapshotSchemaVersion: 1,
		SnapshotJSON:          snapshotJSON,
		ExpiresAt:             time.Now().UTC().Add(30 * time.Minute).Truncate(time.Millisecond),
	}
}

func testReservation(token string, leaseExpiresAt time.Time) AgentWorkflowReservation {
	return AgentWorkflowReservation{
		ExecutionToken:       token,
		ExecutionOperation:   "subscription.start",
		BusinessKey:          "6d29bb5222dced784c700ce89c49d9e786c70fc9c2c86b63c01dce94864ad147",
		RequestID:            "request-1",
		RequestSchemaVersion: 1,
		RequestJSON:          `{"scope":"all"}`,
		LeaseExpiresAt:       leaseExpiresAt,
	}
}

func testExecutionResult() AgentWorkflowExecutionResult {
	return AgentWorkflowExecutionResult{
		ResultSchemaVersion: 1,
		ResultJSON:          `{"effect":"created"}`,
		BusinessKey:         "6d29bb5222dced784c700ce89c49d9e786c70fc9c2c86b63c01dce94864ad147",
		WriteEffect:         "created",
		CompletedAt:         time.Now().UTC(),
	}
}

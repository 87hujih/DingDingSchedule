package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"schedule_server/internal/agent"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/tenantctx"

	gormMysql "gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAgentWorkflowStoreCreateCASDeleteRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestAgentWorkflowStore(t)
	key := agent.WorkflowKey{TenantID: 71, ConversationID: "conv-adapter", ActorUserID: 81}
	ctx := tenantctx.WithTenantID(context.Background(), key.TenantID)
	snapshot := appWorkflowSnapshot(key, agent.WorkflowCollectScope)

	created, err := store.Create(ctx, key, snapshot)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Version != 1 || created.Snapshot.Version != 1 {
		t.Fatalf("created = %+v, want wrapper/snapshot version 1", created)
	}

	next := appWorkflowSnapshot(key, agent.WorkflowCollectDepartments)
	swapped, err := store.CompareAndSwap(ctx, key, created.Version, next)
	if err != nil {
		t.Fatalf("CompareAndSwap() error = %v", err)
	}
	if swapped.Version != 2 || swapped.Snapshot.State != agent.WorkflowCollectDepartments {
		t.Fatalf("swapped = %+v, want version 2 collect_departments", swapped)
	}
	if _, err := store.CompareAndSwap(ctx, key, 1, next); !errors.Is(err, agent.ErrWorkflowConflict) {
		t.Fatalf("stale CompareAndSwap() error = %v, want ErrWorkflowConflict", err)
	}
	if err := store.DeleteIfVersion(ctx, key, swapped.Version, "test"); err != nil {
		t.Fatalf("DeleteIfVersion() error = %v", err)
	}
	loaded, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != nil {
		t.Fatalf("Load() = %+v, want nil after delete", loaded)
	}
}

func TestAgentWorkflowStoreRejectsMissingTenantContext(t *testing.T) {
	t.Parallel()

	store := newTestAgentWorkflowStore(t)
	key := agent.WorkflowKey{TenantID: 72, ConversationID: "conv-tenant", ActorUserID: 82}
	if _, err := store.Create(context.Background(), key, appWorkflowSnapshot(key, agent.WorkflowCollectScope)); err == nil {
		t.Fatal("Create() error = nil, want missing tenant context rejection")
	}
}

func TestAgentWorkflowStoreNormalCASRejectsActiveExecution(t *testing.T) {
	t.Parallel()

	store := newTestAgentWorkflowStore(t)
	key := agent.WorkflowKey{TenantID: 73, ConversationID: "conv-executing", ActorUserID: 83}
	ctx := tenantctx.WithTenantID(context.Background(), key.TenantID)
	now := time.Now().UTC().Truncate(time.Millisecond)
	reservation := agent.ReservedExecutionV1{
		Operation:        "subscription.start",
		BusinessKey:      "business-key",
		TrustedParams:    agent.PersistedTrustedParamsV1{"scope": "all", "dept_ids": []int64{1, 2}},
		ExecutionToken:   "token-1",
		AttemptRequestID: "request-1",
		StartedAt:        now,
		LeaseExpiresAt:   now.Add(time.Minute),
	}
	created, err := store.CreateReservedExecution(
		ctx,
		key,
		appWorkflowSnapshot(key, agent.WorkflowReady),
		reservation,
	)
	if err != nil {
		t.Fatalf("CreateReservedExecution() error = %v", err)
	}
	if _, err := store.CompareAndSwap(
		ctx,
		key,
		created.Version,
		appWorkflowSnapshot(key, agent.WorkflowCollectScope),
	); !errors.Is(err, agent.ErrExecutionInProgress) {
		t.Fatalf("CompareAndSwap() error = %v, want ErrExecutionInProgress", err)
	}
}

func TestBuildWorkflowStoreAllowsShadowAndDatabasePrimary(t *testing.T) {
	t.Parallel()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	repos := &repository.Repository{AgentWorkflowRepo: repository.NewAgentWorkflowRepository(db)}
	if _, err := buildWorkflowStore("shadow", repos); err != nil {
		t.Fatalf("buildWorkflowStore(shadow) error = %v", err)
	}
	databaseStore, err := buildWorkflowStore("database", repos)
	if err != nil {
		t.Fatalf("buildWorkflowStore(database) error = %v", err)
	}
	if _, ok := databaseStore.(agent.WorkflowRecoveryStore); !ok {
		t.Fatalf("database store type = %T, want WorkflowRecoveryStore", databaseStore)
	}
}

func TestAgentWorkflowRecoverySkipsCorruptRowWithoutBlockingValidRows(t *testing.T) {
	t.Parallel()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&model.AgentWorkflow{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	store, err := newAgentWorkflowStore(repository.NewAgentWorkflowRepository(db))
	if err != nil {
		t.Fatalf("newAgentWorkflowStore() error = %v", err)
	}
	databaseStore := store.(*agentWorkflowStore)
	var decodeErrors atomic.Int32
	databaseStore.recoveryDecodeObserver = func(model.AgentWorkflow, error) {
		decodeErrors.Add(1)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	validKey := agent.WorkflowKey{TenantID: 751, ConversationID: "valid-recovery", ActorUserID: 851}
	validCtx := tenantctx.WithTenantID(context.Background(), validKey.TenantID)
	validReservation := agent.ReservedExecutionV1{
		Operation:        "subscription.start",
		BusinessKey:      "valid-business-key",
		TrustedParams:    agent.PersistedTrustedParamsV1{"scope": "all"},
		ExecutionToken:   "valid-token",
		AttemptRequestID: "valid-request",
		StartedAt:        now,
		LeaseExpiresAt:   now.Add(time.Minute),
	}
	valid, err := store.CreateReservedExecution(
		validCtx,
		validKey,
		appWorkflowSnapshot(validKey, agent.WorkflowReady),
		validReservation,
	)
	if err != nil {
		t.Fatalf("CreateReservedExecution(valid) error = %v", err)
	}
	if _, err := store.RecordExecutionResult(
		validCtx,
		validKey,
		valid.Version,
		validReservation.ExecutionToken,
		agent.PersistedExecutionResultV1{
			BusinessKey: validReservation.BusinessKey,
			WriteEffect: agent.WriteEffectCreated,
			CompletedAt: now.Add(time.Second),
		},
	); err != nil {
		t.Fatalf("RecordExecutionResult(valid) error = %v", err)
	}

	corruptKey := agent.WorkflowKey{TenantID: 752, ConversationID: "corrupt-recovery", ActorUserID: 852}
	corruptCtx := tenantctx.WithTenantID(context.Background(), corruptKey.TenantID)
	corruptReservation := agent.ReservedExecutionV1{
		Operation:        "subscription.start",
		BusinessKey:      "corrupt-business-key",
		TrustedParams:    agent.PersistedTrustedParamsV1{"scope": "all"},
		ExecutionToken:   "corrupt-token",
		AttemptRequestID: "corrupt-request",
		StartedAt:        now.Add(-2 * time.Hour),
		LeaseExpiresAt:   now.Add(-time.Hour),
	}
	if _, err := store.CreateReservedExecution(
		corruptCtx,
		corruptKey,
		appWorkflowSnapshot(corruptKey, agent.WorkflowReady),
		corruptReservation,
	); err != nil {
		t.Fatalf("CreateReservedExecution(corrupt) error = %v", err)
	}
	if err := db.Model(&model.AgentWorkflow{}).
		Where("tenant_id = ? AND conversation_id = ? AND actor_user_id = ?", corruptKey.TenantID, corruptKey.ConversationID, corruptKey.ActorUserID).
		Update("snapshot_json", "{").Error; err != nil {
		t.Fatalf("corrupt snapshot update error = %v", err)
	}

	recoverable, err := databaseStore.ListRecoverableExecutions(context.Background(), now, 20)
	if err != nil {
		t.Fatalf("ListRecoverableExecutions() error = %v", err)
	}
	if len(recoverable) != 1 || recoverable[0].Key != validKey {
		t.Fatalf("recoverable = %+v, want only valid row", recoverable)
	}
	if decodeErrors.Load() != 1 {
		t.Fatalf("decode observer calls = %d, want 1", decodeErrors.Load())
	}
}

func TestAgentWorkflowStoreNormalizesReservationAndDefaultsTTL(t *testing.T) {
	t.Parallel()

	store := newTestAgentWorkflowStore(t)
	key := agent.WorkflowKey{TenantID: 74, ConversationID: "conv-normalize", ActorUserID: 84}
	ctx := tenantctx.WithTenantID(context.Background(), key.TenantID)
	baseTime := time.Now().UTC().Truncate(time.Millisecond)
	store.(*agentWorkflowStore).clock = func() time.Time { return baseTime }
	snapshot := appWorkflowSnapshot(key, agent.WorkflowReady)
	snapshot.CreatedAt = time.Time{}
	snapshot.UpdatedAt = time.Time{}
	snapshot.ExpiresAt = time.Time{}
	created, err := store.Create(ctx, key, snapshot)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Snapshot.ExpiresAt.IsZero() {
		t.Fatal("Create() did not default workflow expiry")
	}

	deptIDs := []int64{2, 1, 2}
	now := baseTime.Add(123456 * time.Nanosecond)
	reservation := agent.ReservedExecutionV1{
		Operation:        "subscription.start",
		BusinessKey:      "business-key",
		TrustedParams:    agent.PersistedTrustedParamsV1{"scope": "department", "dept_ids": deptIDs},
		ExecutionToken:   "token-normalize",
		AttemptRequestID: "request-normalize",
		StartedAt:        now,
		LeaseExpiresAt:   now.Add(time.Minute),
	}
	reserved, err := store.ReserveExecution(ctx, key, created.Version, created.Snapshot, reservation)
	if err != nil {
		t.Fatalf("ReserveExecution() error = %v", err)
	}
	deptIDs[0] = 999
	gotIDs := reserved.Execution.Reservation.TrustedParams["dept_ids"].([]int64)
	if len(gotIDs) != 2 || gotIDs[0] != 1 || gotIDs[1] != 2 {
		t.Fatalf("reserved dept_ids = %v, want canonical [1 2] without aliasing", gotIDs)
	}
	if reserved.Execution.Reservation.LeaseExpiresAt.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("lease = %v, want millisecond precision", reserved.Execution.Reservation.LeaseExpiresAt)
	}
	loaded, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load() active reservation error = %v", err)
	}
	if loaded == nil || loaded.Execution == nil {
		t.Fatalf("Load() = %+v, want active reservation", loaded)
	}
	if _, err := store.FinalizeExecution(
		ctx,
		key,
		loaded.Version,
		"token-normalize",
		loaded.Snapshot,
	); !errors.Is(err, agent.ErrExecutionInProgress) {
		t.Fatalf("FinalizeExecution() before result error = %v, want ErrExecutionInProgress", err)
	}
	takeover := reservation
	takeover.ExecutionToken = "token-takeover"
	takeover.AttemptRequestID = "request-takeover"
	if _, err := store.TakeoverExpiredExecution(
		ctx,
		key,
		loaded.Version,
		"token-normalize",
		takeover,
	); !errors.Is(err, agent.ErrExecutionInProgress) {
		t.Fatalf("TakeoverExpiredExecution() fresh lease error = %v, want ErrExecutionInProgress", err)
	}
}

func TestDecodeExecutionRejectsCorruptAuthority(t *testing.T) {
	t.Parallel()

	value := "unexpected"
	now := time.Now().UTC().Truncate(time.Millisecond)
	reservation := agent.ReservedExecutionV1{
		Operation:        "subscription.start",
		BusinessKey:      "business-key",
		TrustedParams:    agent.PersistedTrustedParamsV1{"scope": "all", "dept_ids": []int64{}},
		ExecutionToken:   "token-corrupt",
		AttemptRequestID: "request-corrupt",
		StartedAt:        now,
		LeaseExpiresAt:   now.Add(time.Minute),
	}
	requestJSON, err := agent.MarshalReservedExecution(reservation)
	if err != nil {
		t.Fatalf("MarshalReservedExecution() error = %v", err)
	}
	schemaVersion := workflowCodecVersion
	operation := reservation.Operation
	businessKey := reservation.BusinessKey
	requestID := reservation.AttemptRequestID
	token := reservation.ExecutionToken
	lease := reservation.LeaseExpiresAt
	tests := []model.AgentWorkflow{
		{ExecutionStatus: "future"},
		{ExecutionStatus: repository.AgentWorkflowExecutionIdle, ExecutionOperation: &value},
		{
			ExecutionStatus:               repository.AgentWorkflowExecutionExecuting,
			ExecutionToken:                &token,
			ExecutionOperation:            &operation,
			BusinessKey:                   &businessKey,
			RequestID:                     &requestID,
			ExecutionRequestSchemaVersion: &schemaVersion,
			ExecutionRequestJSON:          stringTestPointer(string(requestJSON)),
			ExecutionResultJSON:           &value,
			LeaseExpiresAt:                &lease,
		},
	}
	for _, row := range tests {
		row := row
		if _, err := decodeExecution(&row); err == nil {
			t.Fatalf("decodeExecution(%+v) error = nil, want corruption rejection", row)
		}
	}
}

func stringTestPointer(value string) *string {
	return &value
}

func TestAgentWorkflowStoreMySQLReservationRoundTrip(t *testing.T) {
	dsn := os.Getenv("AGENT_WORKFLOW_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENT_WORKFLOW_MYSQL_DSN is not set")
	}
	db, err := gorm.Open(gormMysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open(mysql) error = %v", err)
	}
	if err := db.AutoMigrate(&model.AgentWorkflow{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := db.Use(repository.NewTenantScopePlugin()); err != nil {
		t.Fatalf("Use(TenantScopePlugin) error = %v", err)
	}
	store, err := newAgentWorkflowStore(repository.NewAgentWorkflowRepository(db))
	if err != nil {
		t.Fatalf("newAgentWorkflowStore() error = %v", err)
	}
	key := agent.WorkflowKey{
		TenantID:       97531,
		ConversationID: fmt.Sprintf("mysql-reservation-%d", time.Now().UnixNano()),
		ActorUserID:    86420,
	}
	ctx := tenantctx.WithTenantID(context.Background(), key.TenantID)
	now := time.Now().UTC()
	reservation := agent.ReservedExecutionV1{
		Operation:        "subscription.start",
		BusinessKey:      "mysql-business-key",
		TrustedParams:    agent.PersistedTrustedParamsV1{"scope": "all", "dept_ids": []int64{}},
		ExecutionToken:   "mysql-token",
		AttemptRequestID: "mysql-request",
		StartedAt:        now,
		LeaseExpiresAt:   now.Add(time.Minute),
	}
	created, err := store.CreateReservedExecution(ctx, key, appWorkflowSnapshot(key, agent.WorkflowReady), reservation)
	if err != nil {
		t.Fatalf("CreateReservedExecution() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.DeleteReservedExecution(ctx, key, created.Version, "mysql-token", "test_cleanup")
	})
	loaded, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.Execution == nil ||
		loaded.Execution.Reservation.LeaseExpiresAt.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("Load() = %+v, want millisecond-precision active reservation", loaded)
	}
}

func newTestAgentWorkflowStore(t *testing.T) agent.WorkflowStore {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&model.AgentWorkflow{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := db.Use(repository.NewTenantScopePlugin()); err != nil {
		t.Fatalf("Use(TenantScopePlugin) error = %v", err)
	}
	store, err := newAgentWorkflowStore(repository.NewAgentWorkflowRepository(db))
	if err != nil {
		t.Fatalf("newAgentWorkflowStore() error = %v", err)
	}
	return store
}

func appWorkflowSnapshot(key agent.WorkflowKey, state agent.WorkflowState) *agent.WorkflowSnapshot {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &agent.WorkflowSnapshot{
		ID:             "wf-app-adapter",
		TenantID:       key.TenantID,
		ActorUserID:    key.ActorUserID,
		ConversationID: key.ConversationID,
		Type:           agent.WorkflowSubscriptionStart,
		State:          state,
		MissingFields:  []string{"scope"},
		TrustedEntities: map[string]agent.TrustedEntity{
			"scope": {ID: "all", Type: "scope", Label: "全部人员", Value: "all", TenantID: key.TenantID},
		},
		ExpiresAt: now.Add(30 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
}
